package runprogram

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeRedis is a Redis server in memory speaking RESP2, with the commands
// fiig.redis's client and stream source send (ADRs 0110, 0111): AUTH,
// SELECT, INCR, GET, the consumer-group stream commands, and XRANGE and
// XLEN. Pipelined commands are answered in order, as Redis does.
type fakeRedis struct {
	ln       net.Listener
	password string
	accepted atomic.Int32
	// lastCount is the COUNT the last XREADGROUP asked for.
	lastCount atomic.Int32
	// lastBlock is the BLOCK it waited up to, in milliseconds.
	lastBlock atomic.Int32

	mu      sync.Mutex
	kv      map[string]int
	strings map[string]string
	streams map[string]*fakeStream
}

type fakeEntry struct {
	id     string
	fields []string
}

type fakePending struct {
	consumer string
	at       time.Time
	count    int
}

type fakeGroup struct {
	next    int
	pending map[string]*fakePending
}

type fakeStream struct {
	entries []fakeEntry
	groups  map[string]*fakeGroup
}

func startFakeRedis(t *testing.T, password string) *fakeRedis {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	r := &fakeRedis{ln: ln, password: password, kv: map[string]int{}, strings: map[string]string{}, streams: map[string]*fakeStream{}}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			r.accepted.Add(1)
			go r.serve(c)
		}
	}()
	return r
}

func (r *fakeRedis) url() string {
	return fmt.Sprintf("redis://:%s@%s", r.password, r.ln.Addr())
}

func (r *fakeRedis) serve(c net.Conn) {
	defer c.Close()
	in := bufio.NewReader(c)
	authed := r.password == ""
	for {
		args, err := readCommand(in)
		if err != nil {
			return
		}
		name := strings.ToUpper(args[0])
		var reply string
		switch {
		case name == "AUTH":
			if args[len(args)-1] == r.password {
				authed = true
				reply = "+OK\r\n"
			} else {
				reply = "-WRONGPASS invalid username-password pair\r\n"
			}
		case !authed:
			reply = "-NOAUTH Authentication required.\r\n"
		default:
			reply = r.command(name, args[1:])
		}
		if _, err := io.WriteString(c, reply); err != nil {
			return
		}
	}
}

func readCommand(in *bufio.Reader) ([]string, error) {
	line, err := in.ReadString('\n')
	if err != nil {
		return nil, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "*")))
	if err != nil {
		return nil, err
	}
	args := make([]string, n)
	for i := range args {
		head, err := in.ReadString('\n')
		if err != nil {
			return nil, err
		}
		size, _ := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(head, "$")))
		buf := make([]byte, size+2)
		if _, err := io.ReadFull(in, buf); err != nil {
			return nil, err
		}
		args[i] = string(buf[:size])
	}
	return args, nil
}

func bulk(s string) string { return fmt.Sprintf("$%d\r\n%s\r\n", len(s), s) }
func array(items []string) string {
	return fmt.Sprintf("*%d\r\n%s", len(items), strings.Join(items, ""))
}

func (e fakeEntry) reply() string {
	fields := make([]string, len(e.fields))
	for i, f := range e.fields {
		fields[i] = bulk(f)
	}
	return array([]string{bulk(e.id), array(fields)})
}

// add appends an entry to key, as XADD key * fields... does.
func (r *fakeRedis) add(key string, fields ...string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.addLocked(key, fields)
}

func (r *fakeRedis) addLocked(key string, fields []string) string {
	s := r.stream(key)
	id := fmt.Sprintf("%d-0", len(s.entries)+1)
	s.entries = append(s.entries, fakeEntry{id: id, fields: fields})
	return id
}

func (r *fakeRedis) stream(key string) *fakeStream {
	s, ok := r.streams[key]
	if !ok {
		s = &fakeStream{groups: map[string]*fakeGroup{}}
		r.streams[key] = s
	}
	return s
}

// payloads is the payload field of every entry in key.
func (r *fakeRedis) payloads(key string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	if s, ok := r.streams[key]; ok {
		for _, e := range s.entries {
			out = append(out, e.fields[1])
		}
	}
	return out
}

func (r *fakeRedis) pending(key, group string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if s, ok := r.streams[key]; ok && s.groups[group] != nil {
		return len(s.groups[group].pending)
	}
	return 0
}

func (r *fakeRedis) counter(key string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.kv[key]
}

func (r *fakeRedis) command(name string, args []string) string {
	if name == "XREADGROUP" {
		return r.readGroup(args)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	switch name {
	case "SELECT":
		// Redis has 16 databases by default.
		if n, err := strconv.Atoi(args[0]); err != nil || n < 0 || n > 15 {
			return "-ERR DB index is out of range\r\n"
		}
		return "+OK\r\n"
	case "SET":
		// SET key value NX PX ms, the form a schedule lock sends (ADR
		// 0112 §4); expiry isn't modelled, since a test's firings differ.
		if _, taken := r.strings[args[0]]; taken && slices.Contains(args, "NX") {
			return "$-1\r\n"
		}
		r.strings[args[0]] = args[1]
		return "+OK\r\n"
	case "EXISTS":
		// A lock's done key (ADR 0124 §6).
		if _, ok := r.strings[args[0]]; ok {
			return ":1\r\n"
		}
		return ":0\r\n"
	case "INCR":
		r.kv[args[0]]++
		return fmt.Sprintf(":%d\r\n", r.kv[args[0]])
	case "GET":
		if v, ok := r.kv[args[0]]; ok {
			return bulk(strconv.Itoa(v))
		}
		return "$-1\r\n"
	case "XADD":
		return bulk(r.addLocked(args[0], args[2:]))
	case "XLEN":
		return fmt.Sprintf(":%d\r\n", len(r.stream(args[0]).entries))
	case "XRANGE":
		// XRANGE key start end, as a replay's fetch sends (ADR 0111 open
		// question 1, as amended 2026-11-21).
		var items []string
		for _, e := range r.stream(args[0]).entries {
			if inRange(e.id, args[1], args[2]) {
				items = append(items, e.reply())
			}
		}
		return array(items)
	case "XGROUP":
		s := r.stream(args[1])
		if s.groups[args[2]] != nil {
			return "-BUSYGROUP Consumer Group name already exists\r\n"
		}
		g := &fakeGroup{pending: map[string]*fakePending{}}
		// $ starts after the last entry, 0 at the first.
		if args[3] == "$" {
			g.next = len(s.entries)
		}
		s.groups[args[2]] = g
		return "+OK\r\n"
	case "XPENDING":
		g := r.group(args[0], args[1])
		if g == nil {
			return "-NOGROUP No such key or consumer group\r\n"
		}
		idle, _ := strconv.Atoi(args[3])
		count, _ := strconv.Atoi(args[6])
		var items []string
		for _, e := range r.streams[args[0]].entries {
			p := g.pending[e.id]
			if p == nil || !inRange(e.id, args[4], args[5]) || time.Since(p.at) < time.Duration(idle)*time.Millisecond || len(items) == count {
				continue
			}
			items = append(items, array([]string{bulk(e.id), bulk(p.consumer), fmt.Sprintf(":%d\r\n", time.Since(p.at).Milliseconds()), fmt.Sprintf(":%d\r\n", p.count)}))
		}
		return array(items)
	case "XCLAIM":
		g := r.group(args[0], args[1])
		idle, _ := strconv.Atoi(args[3])
		var items []string
		for _, id := range args[4:] {
			p := g.pending[id]
			if p == nil || time.Since(p.at) < time.Duration(idle)*time.Millisecond {
				continue
			}
			p.consumer, p.at = args[2], time.Now()
			p.count++
			for _, e := range r.streams[args[0]].entries {
				if e.id == id {
					items = append(items, e.reply())
				}
			}
		}
		return array(items)
	case "XACK":
		g := r.group(args[0], args[1])
		if g == nil || g.pending[args[2]] == nil {
			return ":0\r\n"
		}
		delete(g.pending, args[2])
		return ":1\r\n"
	}
	return fmt.Sprintf("-ERR unknown command '%s'\r\n", name)
}

// inRange is whether a stream id is within start and end, as XRANGE and
// XPENDING read them: - and + for either end, and an id without its
// sequence number covering the whole millisecond.
func inRange(id, start, end string) bool {
	at := func(s string, upper bool) [2]uint64 {
		switch s {
		case "-":
			return [2]uint64{0, 0}
		case "+":
			return [2]uint64{^uint64(0), ^uint64(0)}
		}
		ms, seq, whole := strings.Cut(s, "-")
		m, _ := strconv.ParseUint(ms, 10, 64)
		if !whole {
			if upper {
				return [2]uint64{m, ^uint64(0)}
			}
			return [2]uint64{m, 0}
		}
		n, _ := strconv.ParseUint(seq, 10, 64)
		return [2]uint64{m, n}
	}
	less := func(a, b [2]uint64) bool { return a[0] < b[0] || a[0] == b[0] && a[1] < b[1] }
	v := at(id, false)
	return !less(v, at(start, false)) && !less(at(end, true), v)
}

func (r *fakeRedis) group(key, name string) *fakeGroup {
	if s, ok := r.streams[key]; ok {
		return s.groups[name]
	}
	return nil
}

// readGroup is XREADGROUP GROUP g c COUNT n BLOCK ms STREAMS key >,
// waiting up to BLOCK for an entry to arrive.
func (r *fakeRedis) readGroup(args []string) string {
	group, consumer, key := args[1], args[2], args[8]
	count, _ := strconv.Atoi(args[4])
	r.lastCount.Store(int32(count))
	block, _ := strconv.Atoi(args[6])
	r.lastBlock.Store(int32(block))
	until := time.Now().Add(time.Duration(block) * time.Millisecond)
	for {
		r.mu.Lock()
		g := r.group(key, group)
		if g == nil {
			r.mu.Unlock()
			return "-NOGROUP No such key or consumer group\r\n"
		}
		s := r.streams[key]
		var items []string
		for g.next < len(s.entries) && len(items) < count {
			e := s.entries[g.next]
			g.next++
			g.pending[e.id] = &fakePending{consumer: consumer, at: time.Now(), count: 1}
			items = append(items, e.reply())
		}
		r.mu.Unlock()
		if len(items) > 0 {
			return array([]string{array([]string{bulk(key), array(items)})})
		}
		if time.Now().After(until) {
			return "*-1\r\n"
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// ack drops id from group's pending entries, as an operator who has dealt
// with it would.
func (r *fakeRedis) ack(key, group, id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.group(key, group).pending, id)
}
