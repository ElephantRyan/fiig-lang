package runtime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"fiig-lang/compiler/logging"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// ADR 0114, ADR 0117 §2: the Session primitive. A package that provides
// fiig.session.SessionProtocol calls Session.call(settings, message), and
// the runtime runs that package's protocol over one connection per
// distinct settings map: one goroutine per session owns the connection,
// reads everything that arrives, and calls the protocol's operations one at
// a time, so its state is never shared.
//
// ADR 0117 §2, as amended 2026-11-21: the settings may name "alternates",
// "host:port,host:port", tried in order when host and port can't be
// reached; an IPv6 host is written in brackets, "[::1]:9092".

const (
	// SessionProtocolInterface is the interface a protocol package provides.
	SessionProtocolInterface = "fiig.session.SessionProtocol"

	// sessionWriteTimeout bounds one write; a peer that takes no bytes for
	// this long has broken the connection.
	sessionWriteTimeout = 10 * time.Second
	// sessionOpTimeout bounds one protocol operation, which reaches nothing.
	sessionOpTimeout = 10 * time.Second
	// sessionGrace is how long Close lets a closing write drain (§2).
	sessionGrace = time.Second
	// sessionMoves is how many MoveTos one connect may follow, so two
	// brokers naming each other don't loop.
	sessionMoves = 5
)

// SessionProvider is the `Session` primitive capability.
type SessionProvider struct {
	// Backend dials; nil uses the bound Socket's backend, else the network.
	Backend SocketBackend

	in        *Interp
	mu        sync.Mutex
	protocols map[*types.Universe]*sessionProtocol
	sessions  map[string]*session
	closed    bool
}

// NewSessionProvider is the real Session.
func NewSessionProvider() *SessionProvider { return &SessionProvider{} }

func (p *SessionProvider) bindInterp(in *Interp) { p.in = in }

// sessionProtocol is one package's SessionProtocol group.
type sessionProtocol struct {
	path  string
	group *syntax.CapabilityDecl
	sc    scope
}

// Call is Session.call(settings, message, deadline).
func (p *SessionProvider) Call(ctx context.Context, method string, args []Value) (Value, *Fault) {
	if method != "call" {
		return nil, Faultf("Session has no operation %q", method)
	}
	if len(args) != 3 {
		return nil, Faultf("Session.call takes settings, a message, and a deadline")
	}
	m, okSettings := args[0].(Map)
	message, okMessage := args[1].(Bytes)
	if !okSettings || !okMessage {
		return nil, Faultf("Session.call takes a Map<string, string> and bytes")
	}
	settings := map[string]string{}
	var bad *Fault
	m.Each(func(k, v Value) bool {
		ks, okK := k.(Str)
		vs, okV := v.(Str)
		if !okK || !okV {
			bad = Faultf("Session.call's settings are a Map<string, string>")
			return false
		}
		settings[string(ks)] = string(vs)
		return true
	})
	if bad != nil {
		return nil, bad
	}
	var until time.Time
	switch d := args[2].(type) {
	case Int:
		until = time.UnixMilli(int64(d))
	case None:
	default:
		return nil, Faultf("Session.call's deadline is an int or None")
	}
	if ctx.Value(sessionOpKey{}) != nil {
		// The operation runs on the session's own loop, which would wait
		// on itself (§2).
		return nil, Faultf("Session.call inside a protocol operation: an operation returns what to write instead (ADR 0117 §2)")
	}
	proto, f := p.protocolFor(ctx)
	if f != nil {
		return nil, f
	}
	s, f := p.session(proto, settings)
	if f != nil {
		return nil, f
	}
	parts, failure := s.call(ctx, message.B, until)
	if failure != nil {
		ev := EnumValue{TypeName: "SessionError", Variant: failure.variant, Decl: types.SessionErrorDecl}
		if failure.variant == "Broken" {
			ev.Fields = []Value{Str(failure.reason)}
		}
		return Result{Ok: false, Value: ev}, nil
	}
	out := make([]Value, len(parts))
	for i, b := range parts {
		out[i] = Bytes{B: b}
	}
	return Result{Ok: true, Value: List{Elems: out}}, nil
}

// protocolFor is the protocol of the package whose gateway is calling: a
// session runs its caller's own protocol (§2).
func (p *SessionProvider) protocolFor(ctx context.Context) (*sessionProtocol, *Fault) {
	if p.in == nil {
		return nil, Faultf("Session has no interpreter")
	}
	u := p.in.scope(ctx).universe
	p.mu.Lock()
	defer p.mu.Unlock()
	if proto, ok := p.protocols[u]; ok {
		return proto, nil
	}
	var pkg *types.Package
	for _, candidate := range p.in.Packages {
		if candidate.Universe == u {
			pkg = candidate
			break
		}
	}
	if pkg == nil {
		return nil, Faultf("Session.call: only a package that provides %s can call it (ADR 0117 §2)", SessionProtocolInterface)
	}
	name := pkg.Provides[SessionProtocolInterface]
	d, found := pkg.Universe.Lookup(name)
	group, isGroup := d.(*syntax.CapabilityDecl)
	if name == "" || !found || !isGroup {
		return nil, Faultf("Session.call: package %s doesn't provide %s; its manifest names no capability group for it (ADR 0117 §2)", pkg.Path, SessionProtocolInterface)
	}
	proto := &sessionProtocol{path: pkg.Path, group: group, sc: packageScope(pkg)}
	if p.protocols == nil {
		p.protocols = map[*types.Universe]*sessionProtocol{}
	}
	p.protocols[u] = proto
	return proto, nil
}

// session is the one session of proto for settings, started on first use.
func (p *SessionProvider) session(proto *sessionProtocol, settings map[string]string) (*session, *Fault) {
	keys := make([]string, 0, len(settings))
	for k := range settings {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var key strings.Builder
	key.WriteString(proto.path)
	for _, k := range keys {
		fmt.Fprintf(&key, "\x00%s\x00%s", k, settings[k])
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil, Faultf("Session.call: the process is stopping")
	}
	if s, ok := p.sessions[key.String()]; ok {
		return s, nil
	}
	if _, err := strconv.Atoi(settings["port"]); settings["host"] == "" || err != nil {
		return nil, Faultf(`Session.call's settings name no "host" and "port" to connect to (ADR 0117 §2)`)
	}
	if _, err := alternatesOf(settings["alternates"]); err != nil {
		return nil, Faultf(`Session.call's "alternates" setting: %v (ADR 0117 §2)`, err)
	}
	id := make([]byte, 16)
	_, _ = rand.Read(id)
	own := map[string]string{"sessionId": hex.EncodeToString(id)}
	for k, v := range settings {
		own[k] = v
	}
	s := &session{
		p: p, proto: proto, settings: own,
		asks: make(chan *sessionAsk), stop: make(chan chan struct{}), quit: make(chan struct{}),
		events: make(chan sessionEvent),
	}
	if p.sessions == nil {
		p.sessions = map[string]*session{}
	}
	p.sessions[key.String()] = s
	go s.loop()
	return s, nil
}

// Close runs every session's closing and closes its connection (§2).
func (p *SessionProvider) Close() {
	p.mu.Lock()
	p.closed = true
	sessions := make([]*session, 0, len(p.sessions))
	for _, s := range p.sessions {
		sessions = append(sessions, s)
	}
	p.mu.Unlock()
	var wg sync.WaitGroup
	for _, s := range sessions {
		wg.Add(1)
		go func() {
			defer wg.Done()
			done := make(chan struct{})
			select {
			case s.stop <- done:
				<-done
			case <-s.quit:
			}
		}()
	}
	wg.Wait()
}

func (p *SessionProvider) backend() SocketBackend {
	if p.Backend != nil {
		return p.Backend
	}
	if p.in != nil {
		if sp, ok := p.in.Capabilities.Lookup("Socket"); ok {
			if socket, ok := sp.(SocketProvider); ok && socket.Backend != nil {
				return socket.Backend
			}
		}
	}
	return NetBackend{}
}

// sessionFailure is a SessionError: "Broken" with a reason, or "TimedOut".
type sessionFailure struct{ variant, reason string }

// sessionOpKey marks a protocol operation's context.
type sessionOpKey struct{}

type sessionResult struct {
	parts   [][]byte
	failure *sessionFailure
}

// sessionAsk is one waiting Session.call.
type sessionAsk struct {
	message []byte
	parts   [][]byte
	done    chan sessionResult // buffered, so the loop never waits on a caller
	gone    chan struct{}      // closed when the caller stops waiting
}

func (a *sessionAsk) finish(r sessionResult) {
	select {
	case a.done <- r:
	default:
	}
}

func (a *sessionAsk) isGone() bool {
	select {
	case <-a.gone:
		return true
	default:
		return false
	}
}

// sessionEvent is what a connection's reader saw: data, or the error that
// ended it. gen is the connection it came from.
type sessionEvent struct {
	gen  int
	data []byte
	err  error
}

// session is one connection's owner. Everything below the channels is
// the loop goroutine's alone.
type session struct {
	p        *SessionProvider
	proto    *sessionProtocol
	settings map[string]string
	asks     chan *sessionAsk
	stop     chan chan struct{}
	quit     chan struct{}
	events   chan sessionEvent

	conn    Transport
	gen     int
	state   []byte
	waiting map[int64]*sessionAsk
	order   []int64 // waiting's keys in the order they were accepted
	held    []*sessionAsk
	timer   *time.Timer
	moves   int
}

// call asks the session and waits for the reply's parts, until until.
func (s *session) call(ctx context.Context, message []byte, until time.Time) ([][]byte, *sessionFailure) {
	a := &sessionAsk{message: message, done: make(chan sessionResult, 1), gone: make(chan struct{})}
	defer close(a.gone)
	var expired <-chan time.Time
	if !until.IsZero() {
		t := time.NewTimer(time.Until(until))
		defer t.Stop()
		expired = t.C
	}
	timedOut := &sessionFailure{variant: "TimedOut"}
	select {
	case s.asks <- a:
	case <-expired:
		return nil, timedOut
	case <-ctx.Done():
		return nil, timedOut
	case <-s.quit:
		return nil, &sessionFailure{variant: "Broken", reason: "the process is stopping"}
	}
	select {
	case r := <-a.done:
		return r.parts, r.failure
	case <-expired:
		return nil, timedOut
	case <-ctx.Done():
		return nil, timedOut
	}
}

func (s *session) loop() {
	defer close(s.quit)
	s.timer = time.NewTimer(time.Hour)
	s.timer.Stop()
	s.waiting = map[int64]*sessionAsk{}
	for {
		select {
		case a := <-s.asks:
			if s.conn == nil {
				s.moves = 0
				if reason, ok := s.open(); !ok {
					a.finish(sessionResult{failure: &sessionFailure{variant: "Broken", reason: reason}})
					continue
				}
			}
			s.ask(a)
		case ev := <-s.events:
			if ev.gen != s.gen || s.conn == nil {
				continue
			}
			if ev.err != nil {
				s.broken("the connection broke: " + ev.err.Error())
				continue
			}
			s.run("received", Bytes{B: s.state}, Bytes{B: ev.data}, s.now())
		case <-s.timer.C:
			if s.conn != nil {
				s.run("tick", Bytes{B: s.state}, s.now())
			}
		case done := <-s.stop:
			s.closing()
			close(done)
			return
		}
		s.askHeld()
	}
}

func (s *session) now() Value { return Int(time.Now().UnixMilli()) }

// open connects to the settings' host and port, else to each of their
// alternates in order, and runs opened on the first it reaches. When it
// reaches none, or opened closes the connection, it says why.
func (s *session) open() (string, bool) {
	targets := []sessionTarget{{s.settings["host"], s.settings["port"]}}
	alternates, _ := alternatesOf(s.settings["alternates"]) // checked when the session started
	targets = append(targets, alternates...)
	var failures []string
	for _, target := range targets {
		port, _ := strconv.Atoi(target.port)
		t, err := s.dial(target.host, port)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", addrOf(target.host, port), err))
			continue
		}
		if s.opened(target.host, target.port, t) {
			return "", true
		}
		return "the connection to " + addrOf(target.host, port) + " closed as it opened", false
	}
	reason := "can't connect to " + strings.Join(failures, "; nor to ")
	s.broken(reason)
	return reason, false
}

// sessionTarget is one place a session may connect.
type sessionTarget struct{ host, port string }

// alternatesOf reads an "alternates" setting: host:port entries, comma
// separated, each IPv6 host in brackets.
func alternatesOf(text string) ([]sessionTarget, error) {
	var out []sessionTarget
	for _, entry := range strings.Split(text, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		host, port, err := net.SplitHostPort(entry)
		if _, bad := strconv.Atoi(port); err != nil || bad != nil || host == "" {
			return nil, fmt.Errorf("%q isn't a host:port, such as broker-2:9092 or [::1]:9092", entry)
		}
		out = append(out, sessionTarget{host, port})
	}
	return out, nil
}

// dial opens a connection to host and port, upgraded to TLS when the
// settings say so.
func (s *session) dial(host string, port int) (Transport, error) {
	ctx, cancel := context.WithTimeout(context.Background(), sessionWriteTimeout)
	defer cancel()
	b := s.p.backend()
	t, err := b.Dial(ctx, host, port)
	if err == nil && s.settings["tls"] == "true" {
		var tt Transport
		if tt, err = b.StartTLS(ctx, t, host); err != nil {
			_ = t.Close()
		} else {
			t = tt
		}
	}
	return t, err
}

// connect opens a connection to host and port, and runs opened on it.
func (s *session) connect(host, portText string) bool {
	port, _ := strconv.Atoi(portText)
	t, err := s.dial(host, port)
	if err != nil {
		s.broken(fmt.Sprintf("can't connect to %s: %v", addrOf(host, port), err))
		return false
	}
	return s.opened(host, portText, t)
}

// opened makes t the session's connection and runs opened on it, with
// the host and port it reached in place of the settings' own.
func (s *session) opened(host, portText string, t Transport) bool {
	s.gen++
	s.conn = t
	go s.read(s.gen, t)
	settings := Map{}
	for k, v := range s.settings {
		if k == "host" {
			v = host
		} else if k == "port" {
			v = portText
		}
		settings = settings.Put(Str(k), Str(v))
	}
	s.run("opened", settings, s.now())
	return s.conn != nil
}

// read hands everything t delivers to the loop until it fails.
func (s *session) read(gen int, t Transport) {
	buf := make([]byte, maxRead)
	for {
		n, err := t.Read(buf)
		if n > 0 {
			select {
			case s.events <- sessionEvent{gen: gen, data: append([]byte(nil), buf[:n]...)}:
			case <-s.quit:
				return
			}
		}
		if err != nil {
			select {
			case s.events <- sessionEvent{gen: gen, err: err}:
			case <-s.quit:
			}
			return
		}
	}
}

// ask hands a's message to the protocol, or holds it when the connection
// can take no more now.
func (s *session) ask(a *sessionAsk) {
	if a.isGone() {
		return
	}
	st, ok := s.op("asked", Bytes{B: s.state}, Bytes{B: a.message}, s.now())
	if !ok {
		a.finish(sessionResult{failure: &sessionFailure{variant: "Broken", reason: "the session's protocol failed"}})
		return
	}
	if st.accepted != nil {
		key := *st.accepted
		s.waiting[key] = a
		s.order = append(s.order, key)
	} else {
		s.held = append(s.held, a)
	}
	s.apply(st)
}

// askHeld asks again each held ask, after a step that may have made room.
func (s *session) askHeld() {
	if s.conn == nil || len(s.held) == 0 {
		return
	}
	held := s.held
	s.held = nil
	for i, a := range held {
		if s.conn == nil {
			s.held = append(s.held, held[i:]...)
			return
		}
		s.ask(a)
	}
}

// run calls one protocol operation and applies its step.
func (s *session) run(name string, args ...Value) {
	if st, ok := s.op(name, args...); ok {
		s.apply(st)
	}
}

// sessionStep is fiig.session.Step.
type sessionStep struct {
	state, write []byte
	accepted     *int64
	replies      []sessionReply
	nextTickAt   *int64
	close        *EnumValue
}

type sessionReply struct {
	key     int64
	message []byte
	last    bool
}

// op calls one protocol operation. A fault, or a step that isn't one, is
// a defect in the protocol package: logged, and the connection broken.
func (s *session) op(name string, args ...Value) (sessionStep, bool) {
	gw := s.proto.group.Gateway(name)
	if gw == nil || len(gw.Params) != len(args) {
		s.defect(name, fmt.Sprintf("%s has no gateway %s taking %d parameters", s.proto.group.Name, name, len(args)))
		return sessionStep{}, false
	}
	named := make(map[string]Value, len(args))
	for i, prm := range gw.Params {
		named[prm.Name] = args[i]
	}
	ctx, cancel := context.WithTimeout(context.Background(), sessionOpTimeout)
	defer cancel()
	ctx, end := withResourceOwner(context.WithValue(ctx, sessionOpKey{}, true))
	defer end()
	v, f := s.p.in.callDecl(ctx, s.proto.sc, s.proto.group.Name+"."+name, gw, named)
	if f != nil {
		s.defect(name, f.Message)
		return sessionStep{}, false
	}
	st, err := stepOf(v)
	if err != nil {
		s.defect(name, err.Error())
		return sessionStep{}, false
	}
	return st, true
}

func (s *session) defect(op, why string) {
	Log.Logf(logging.Error, logging.EventRuntime, nil, "fiig: Session: %s's %s.%s failed, a defect in %s: %s", s.proto.path, s.proto.group.Name, op, s.proto.path, why)
	s.broken(fmt.Sprintf("%s.%s failed: %s", s.proto.group.Name, op, why))
}

func stepOf(v Value) (sessionStep, error) {
	rec, ok := v.(Record)
	if !ok {
		return sessionStep{}, fmt.Errorf("it returned %s, not a Step", v)
	}
	var st sessionStep
	state, okState := rec.Fields["state"].(Bytes)
	write, okWrite := rec.Fields["write"].(Bytes)
	replies, okReplies := rec.Fields["replies"].(List)
	if !okState || !okWrite || !okReplies {
		return sessionStep{}, fmt.Errorf("it returned %s, not a Step", v)
	}
	st.state, st.write = state.B, write.B
	if n, ok := rec.Fields["accepted"].(Int); ok {
		k := int64(n)
		st.accepted = &k
	}
	if n, ok := rec.Fields["nextTickAt"].(Int); ok {
		at := int64(n)
		st.nextTickAt = &at
	}
	if c, ok := rec.Fields["close"].(EnumValue); ok {
		st.close = &c
	}
	for _, e := range replies.Elems {
		r, ok := e.(Record)
		key, okKey := r.Fields["key"].(Int)
		message, okMessage := r.Fields["message"].(Bytes)
		last, okLast := r.Fields["last"].(Bool)
		if !ok || !okKey || !okMessage || !okLast {
			return sessionStep{}, fmt.Errorf("its replies hold %s, not a SessionReply", e)
		}
		st.replies = append(st.replies, sessionReply{key: int64(key), message: message.B, last: bool(last)})
	}
	return st, nil
}

// apply does what a step says: write, deliver replies, set the tick, close.
func (s *session) apply(st sessionStep) {
	s.state = st.state
	if len(st.write) > 0 && s.conn != nil {
		_ = s.conn.SetDeadline(time.Now().Add(sessionWriteTimeout))
		_, err := s.conn.Write(st.write)
		_ = s.conn.SetDeadline(time.Time{})
		if err != nil {
			s.broken("can't write to the connection: " + err.Error())
			return
		}
	}
	for _, r := range st.replies {
		a, ok := s.waiting[r.key]
		if !ok {
			continue
		}
		a.parts = append(a.parts, r.message)
		if r.last {
			s.forget(r.key)
			a.finish(sessionResult{parts: a.parts})
		}
	}
	if st.nextTickAt != nil {
		s.timer.Reset(time.Until(time.UnixMilli(*st.nextTickAt)))
	} else {
		s.timer.Stop()
	}
	if st.close == nil {
		return
	}
	reason := ""
	if len(st.close.Fields) > 0 {
		if r, ok := st.close.Fields[0].(Str); ok {
			reason = string(r)
		}
	}
	switch st.close.Variant {
	case "MoveTo":
		host, _ := st.close.Fields[0].(Str)
		port, _ := st.close.Fields[1].(Int)
		s.moveTo(string(host), int(port))
	case "Failed":
		s.broken(s.proto.group.Name + " closed the connection: " + reason)
	default:
		s.broken("the connection is done: " + reason)
	}
}

func (s *session) forget(key int64) {
	delete(s.waiting, key)
	for i, k := range s.order {
		if k == key {
			s.order = append(s.order[:i], s.order[i+1:]...)
			return
		}
	}
}

// moveTo reconnects at host and port and asks it every waiting message
// again, in the order they were first accepted (§2).
func (s *session) moveTo(host string, port int) {
	var again []*sessionAsk
	for _, k := range s.order {
		if a := s.waiting[k]; a != nil && !a.isGone() {
			a.parts = nil
			again = append(again, a)
		}
	}
	again = append(again, s.held...)
	s.waiting, s.order, s.held = map[int64]*sessionAsk{}, nil, nil
	s.drop()
	s.moves++
	if s.moves > sessionMoves {
		s.fail(again, fmt.Sprintf("moved more than %d times without settling on a server", sessionMoves))
		return
	}
	if !s.connect(host, strconv.Itoa(port)) {
		s.fail(again, "can't move to "+addrOf(host, port))
		return
	}
	for _, a := range again {
		if s.conn == nil {
			s.held = append(s.held, a)
			continue
		}
		s.ask(a)
	}
}

// drop closes the connection without failing anyone.
func (s *session) drop() {
	if s.conn != nil {
		_ = s.conn.Close()
		s.conn = nil
	}
	s.gen++
	s.state = nil
	s.timer.Stop()
}

// broken closes the connection and fails every waiting call; the next
// call opens another.
func (s *session) broken(reason string) {
	waiting := make([]*sessionAsk, 0, len(s.order)+len(s.held))
	for _, k := range s.order {
		waiting = append(waiting, s.waiting[k])
	}
	waiting = append(waiting, s.held...)
	s.waiting, s.order, s.held = map[int64]*sessionAsk{}, nil, nil
	s.drop()
	s.fail(waiting, reason)
}

func (s *session) fail(asks []*sessionAsk, reason string) {
	for _, a := range asks {
		a.finish(sessionResult{failure: &sessionFailure{variant: "Broken", reason: reason}})
	}
}

// closing runs the protocol's closing, lets its write drain, and closes.
func (s *session) closing() {
	if s.conn != nil {
		if st, ok := s.op("closing", Bytes{B: s.state}, s.now()); ok && len(st.write) > 0 && s.conn != nil {
			_ = s.conn.SetDeadline(time.Now().Add(sessionGrace))
			if _, err := s.conn.Write(st.write); err == nil {
				// Wait for the peer's answer, or the grace, before closing.
				timeout := time.After(sessionGrace)
			drain:
				for {
					select {
					case ev := <-s.events:
						if ev.gen == s.gen {
							break drain
						}
					case <-timeout:
						break drain
					}
				}
			}
		}
	}
	s.broken("the process is stopping")
}
