package runprogram

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"fiig-lang/compiler/loader"
	"fiig-lang/compiler/runtime"
	"fiig-lang/compiler/types"
	"fiig-lang/internal/fixture"
)

// ADR 0113: fiig.kafka against a broker in memory that answers
// ApiVersions v0, Metadata v12, and Produce v9 (the flexible versions, as
// amended 2026-11-21), and checks every record batch's CRC-32C with Go's
// hash/crc32, independently of crypto.crc32c.

type fakeKafka struct {
	ln       net.Listener
	accepted atomic.Int32

	mu        sync.Mutex
	records   map[int]int // partition -> records written
	bad       []string    // what was wrong with a request
	metadatas int         // Metadata requests answered
	refuse    int16       // the error code the next produce answers, once
}

func startFakeKafka(t *testing.T) *fakeKafka {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	k := &fakeKafka{ln: ln, records: map[int]int{}}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			k.accepted.Add(1)
			go k.serve(c)
		}
	}()
	return k
}

func (k *fakeKafka) problem(format string, args ...any) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.bad = append(k.bad, fmt.Sprintf(format, args...))
}

func (k *fakeKafka) serve(c net.Conn) {
	defer c.Close()
	for {
		var size int32
		if err := binary.Read(c, binary.BigEndian, &size); err != nil {
			return
		}
		req := make([]byte, size)
		if _, err := io.ReadFull(c, req); err != nil {
			return
		}
		key := int16(binary.BigEndian.Uint16(req[0:]))
		version := int16(binary.BigEndian.Uint16(req[2:]))
		corr := binary.BigEndian.Uint32(req[4:])
		idLen := int(binary.BigEndian.Uint16(req[8:]))
		body := req[10+idLen:]
		var reply []byte
		switch {
		case key == 18 && version == 0:
			reply = k.apiVersions()
		case key == 3 && version == 12:
			reply = append([]byte{0}, k.metadata(flexBody(body))...)
		case key == 0 && version == 9:
			reply = append([]byte{0}, k.produce(flexBody(body))...)
		default:
			k.problem("unexpected API %d v%d", key, version)
			return
		}
		out := binary.BigEndian.AppendUint32(nil, uint32(4+len(reply)))
		out = binary.BigEndian.AppendUint32(out, corr)
		if _, err := c.Write(append(out, reply...)); err != nil {
			return
		}
	}
}

// flexBody is a flexible request's body, after request header v2's
// tagged fields (none, from fiig).
func flexBody(b []byte) []byte { return b[1:] }

func i16(b []byte, v int) []byte { return binary.BigEndian.AppendUint16(b, uint16(int16(v))) }
func i32(b []byte, v int) []byte { return binary.BigEndian.AppendUint32(b, uint32(int32(v))) }
func i64(b []byte, v int) []byte { return binary.BigEndian.AppendUint64(b, uint64(int64(v))) }
func kstr(b []byte, s string) []byte {
	return append(i16(b, len(s)), s...)
}

// cstrk is a COMPACT_STRING; ucount a compact array's length.
func cstrk(b []byte, s string) []byte { return append(binary.AppendUvarint(b, uint64(len(s)+1)), s...) }
func ucount(b []byte, n int) []byte   { return binary.AppendUvarint(b, uint64(n+1)) }

// readCompact reads a compact string or bytes at the front of r.
func readCompact(r *bytes.Reader) []byte {
	n, _ := binary.ReadUvarint(r)
	if n == 0 {
		return nil
	}
	out := make([]byte, n-1)
	io.ReadFull(r, out)
	return out
}

func (k *fakeKafka) apiVersions() []byte {
	b := i16(nil, 0)
	b = i32(b, 2)
	b = i16(i16(i16(b, 0), 3), 12) // Produce 3-12
	b = i16(i16(i16(b, 3), 0), 13) // Metadata 0-13
	return b
}

// metadata says this broker leads all three partitions of any topic but
// "missing", which is unknown.
func (k *fakeKafka) metadata(body []byte) []byte {
	k.mu.Lock()
	k.metadatas++
	k.mu.Unlock()
	r := bytes.NewReader(body)
	binary.ReadUvarint(r)
	io.ReadFull(r, make([]byte, 16)) // no topic id
	name := string(readCompact(r))
	host, portText, _ := net.SplitHostPort(k.ln.Addr().String())
	port, _ := strconv.Atoi(portText)
	b := i32(nil, 0)                      // throttle
	b = ucount(b, 1)                      // one broker
	b = i32(cstrk(i32(b, 1), host), port) // id 1, host, port
	b = append(b, 0, 0)                   // no rack, no tags
	b = i32(cstrk(b, "cluster"), 1)       // cluster id, controller
	code := 0
	if name == "missing" {
		code = 3
	}
	b = ucount(b, 1)                                           // one topic
	b = append(cstrk(i16(b, code), name), make([]byte, 17)...) // no id, not internal
	if code != 0 {
		return append(i32(ucount(b, 0), 0), 0, 0)
	}
	b = ucount(b, 3)
	for p := 2; p >= 0; p-- { // out of order, as a broker may send them
		b = i32(i32(i32(i16(b, 0), p), 1), 0) // index, leader 1, epoch
		b = i32(ucount(b, 1), 1)              // replicas
		b = i32(ucount(b, 1), 1)              // in sync
		b = append(ucount(b, 0), 0)           // none offline, no tags
	}
	return append(i32(b, 0), 0, 0)
}

func (k *fakeKafka) produce(body []byte) []byte {
	r := bytes.NewReader(body)
	var acks int16
	var timeout int32
	txn := readCompact(r)
	binary.Read(r, binary.BigEndian, &acks)
	binary.Read(r, binary.BigEndian, &timeout)
	topics, _ := binary.ReadUvarint(r)
	if txn != nil || acks != -1 || topics != 2 {
		k.problem("transactional id %q, acks %d, %d topics; want none, -1, and 1", txn, acks, topics-1)
	}
	name := readCompact(r)
	partitions, _ := binary.ReadUvarint(r)
	out := ucount(cstrk(ucount(nil, 1), string(name)), int(partitions-1))
	k.mu.Lock()
	refuse := k.refuse
	k.refuse = 0
	k.mu.Unlock()
	for range partitions - 1 {
		var index int32
		binary.Read(r, binary.BigEndian, &index)
		batch := readCompact(r)
		r.ReadByte() // the partition's tags
		// The CRC covers the attributes on, from byte 21.
		if want := crc32.Checksum(batch[21:], crc32.MakeTable(crc32.Castagnoli)); binary.BigEndian.Uint32(batch[17:]) != want || batch[16] != 2 {
			k.problem("partition %d: magic %d, crc %08x, want 2 and %08x", index, batch[16], binary.BigEndian.Uint32(batch[17:]), want)
		}
		count := int(binary.BigEndian.Uint32(batch[57:]))
		if refuse != 0 {
			out = i16(i32(out, int(index)), int(refuse))
			out = i64(i64(i64(out, -1), -1), 0)
			out = append(ucount(out, 0), 0, 0) // no record errors, no message, no tags
			continue
		}
		k.mu.Lock()
		base := k.records[int(index)]
		k.records[int(index)] += count
		k.mu.Unlock()
		out = i16(i32(out, int(index)), 0)
		out = i64(i64(i64(out, base), -1), 0)
		out = append(ucount(out, 0), 0, 0)
	}
	out = append(out, 0) // the topic's tags
	return append(i32(out, 0), 0)
}

var kafkaProgram = map[string]string{
	"fiig.pkg.json": `{
    "path": "kafkatest.app",
    "kind": "application",
    "version": "1.0.0",
    "dependencies": { "fiig.kafka": "1.0.0" },
    "capabilities": ["Socket", "Session"]
}`,
	"src/orders/Publish.fiig": `use fiig.kafka
use fiig.kafka.Kafka

gateway publish(topic: string) -> Result<List<kafka.Sent>, kafka.KafkaError>
    requires Kafka
{
    return Kafka.send(
        topic,
        [
            kafka.KafkaRecord(key: "order-1".utf8(), value: "a".utf8(), headers: None),
            kafka.KafkaRecord(key: "order-2".utf8(), value: "b".utf8(), headers: [kafka.KafkaHeader(name: "trace", value: "t".utf8())]),
            kafka.KafkaRecord(key: "order-3".utf8(), value: "c".utf8(), headers: None),
        ],
    )
}`,
}

func kafkaPublish(t *testing.T, bootstrap, topic string, times int) []string {
	t.Helper()
	root := t.TempDir()
	for name, src := range fixture.Module(kafkaProgram) {
		os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0o755)
		if err := os.WriteFile(filepath.Join(root, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	loaded, errs := loader.Load(root, nil)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	files, _, err := LoadFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	if diags := Check(root, files, loaded.Packages); len(diags) > 0 {
		t.Fatal(diags)
	}
	u, _ := types.BuildUniverse(files)
	caps := runtime.NewCapabilityRegistry()
	RegisterDefaults(caps)
	t.Cleanup(caps.Close)
	in := runtime.New(u, caps)
	in.Imports = BuildImports(files, loaded.Packages)
	in.Groups = BuildGroups(files, loaded.Packages)
	in.Packages = loaded.Packages
	pkg := loaded.Packages["kafkatest.app.orders"]
	env := map[string]string{"KAFKA_BOOTSTRAP": bootstrap}
	lookup := func(name string) (string, bool) { v, ok := env[name]; return v, ok }
	program := Program{Universe: u, Imports: in.Imports, Groups: in.Groups}
	if problems := in.LoadConfig(ReachableConfigFrom(program, []Entry{{Package: pkg, Name: "publish"}}), lookup); len(problems) > 0 {
		t.Fatal(problems)
	}
	var out []string
	for range times {
		v, fault := in.Call(in.EnterPackage(context.Background(), pkg), "publish", map[string]runtime.Value{"topic": runtime.Str(topic)})
		if fault != nil {
			t.Fatal(fault)
		}
		out = append(out, v.String())
	}
	return out
}

func TestKafkaProducerAgainstABrokerInMemory(t *testing.T) {
	k := startFakeKafka(t)
	got := kafkaPublish(t, k.ln.Addr().String(), "orders", 2)
	// order-1 is partition 1, order-2 and order-3 partition 0, where the
	// Java client puts them; the second call's offsets follow the first's.
	want := []string{
		"Ok([Sent{offset: 0, partition: 1}, Sent{offset: 0, partition: 0}, Sent{offset: 1, partition: 0}])",
		"Ok([Sent{offset: 1, partition: 1}, Sent{offset: 2, partition: 0}, Sent{offset: 3, partition: 0}])",
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("send %d = %s\nwant %s", i+1, got[i], want[i])
		}
	}
	if len(k.bad) > 0 {
		t.Errorf("the broker found: %s", strings.Join(k.bad, "; "))
	}
	// ADR 0109: metadata and produce, twice, over one kept connection.
	if n := k.accepted.Load(); n != 1 {
		t.Errorf("the broker saw %d connections, want 1", n)
	}
	if got := kafkaPublish(t, k.ln.Addr().String(), "missing", 1); !strings.Contains(got[0], "UnknownTopic(missing)") {
		t.Errorf("an unknown topic: %s", got[0])
	}
}
