package runtime

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"
)

// ADR 0074 §5: a deadline the program chose is Err(SocketError.TimedOut),
// a value; the call's own deadline is still a fault.

const deadlineSource = `gateway readBy(host: string, port: int, until: int) -> Result<bytes, SocketError>
    requires Socket
{
    connection = Socket.connect(host, port, deadline: until)?
    return Socket.read(connection, until)
}
`

func timedOut(t *testing.T, v Value, fault *Fault) {
	t.Helper()
	if fault != nil {
		t.Fatalf("fault %v, want Err(TimedOut)", fault)
	}
	r, ok := v.(Result)
	if !ok || r.Ok {
		t.Fatalf("got %s, want Err(TimedOut)", v)
	}
	if ev, ok := r.Value.(EnumValue); !ok || ev.Variant != "TimedOut" {
		t.Fatalf("got %s, want Err(TimedOut)", v)
	}
}

func TestStalledReadStopsAtTheProgramsDeadline(t *testing.T) {
	peer := &SocketPeer{Host: "api", Port: 80, Steps: []SocketStep{{Stall: true}}}
	caps := NewCapabilityRegistry()
	caps.Register("Socket", SocketProvider{Backend: &ScriptedSocket{Peers: []*SocketPeer{peer}}})
	in := newTestInterp(universeFromSource(t, deadlineSource), caps)
	until := Int(time.Now().Add(30 * time.Millisecond).UnixMilli())
	v, fault := in.Call(context.Background(), "readBy", map[string]Value{"host": Str("api"), "port": Int(80), "until": until})
	timedOut(t, v, fault)
}

func TestADeadlineAlreadyPassedTimesOutAtOnce(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	until := Int(time.Now().Add(-time.Second).UnixMilli())
	v, fault := realSocketInterp(t, deadlineSource).Call(context.Background(), "readBy", map[string]Value{
		"host": Str("127.0.0.1"), "port": localPort(t, ln.Addr()), "until": until,
	})
	timedOut(t, v, fault)
}

// A real server that accepts and never answers: the read stops at the
// program's deadline.
func TestRealSilentServerTimesOut(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err == nil {
			defer c.Close()
			time.Sleep(2 * time.Second)
		}
	}()
	start := time.Now()
	until := Int(start.Add(50 * time.Millisecond).UnixMilli())
	v, fault := realSocketInterp(t, deadlineSource).Call(context.Background(), "readBy", map[string]Value{
		"host": Str("127.0.0.1"), "port": localPort(t, ln.Addr()), "until": until,
	})
	timedOut(t, v, fault)
	if waited := time.Since(start); waited > time.Second {
		t.Errorf("waited %s for a 50ms deadline", waited)
	}
}

// Left out or None, an operation waits as before, until the call's
// deadline, which is still a fault.
func TestNoDeadlineStillStopsAtTheCallsDeadline(t *testing.T) {
	src := `gateway readPlain(host: string) -> Result<bytes, SocketError>
    requires Socket
{
    connection = Socket.connect(host, 80, deadline: None)?
    return Socket.read(connection)
}
`
	peer := &SocketPeer{Host: "api", Port: 80, Steps: []SocketStep{{Stall: true}}}
	caps := NewCapabilityRegistry()
	caps.Register("Socket", SocketProvider{Backend: &ScriptedSocket{Peers: []*SocketPeer{peer}}})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, fault := newTestInterp(universeFromSource(t, src), caps).Call(ctx, "readPlain", map[string]Value{"host": Str("api")})
	if fault == nil || !strings.Contains(fault.Error(), "cancelled") {
		t.Fatalf("got fault %v, want a cancellation fault", fault)
	}
}
