package runtime

import (
	"context"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"fiig-lang/compiler/types"
)

// ADR 0109: connections kept between calls, against real loopback servers,
// so the read that watches an idle connection runs for real.

const keepSource = `gateway exchange(host: string, port: int, message: string, session: string) -> Result<bytes, SocketError>
    requires Socket
{
    reused: Connection | None = Socket.reuse(host, port, tls: false, session: session);
    opened: Result<Connection, SocketError> = Err(SocketError.Closed);
    match (reused) {
        None -> {
            opened = Socket.connect(host, port);
        },
        kept -> {
            opened = Ok(kept);
        },
    }
    connection = opened?;
    Socket.write(connection, message.utf8())?;
    reply = Socket.read(connection)?;
    Socket.keep(connection, session: session);
    return Ok(reply);
}
`

const keepAfterFailureSource = `gateway keepAfterFailure(host: string, port: int) -> Result<bytes, SocketError>
    requires Socket
{
    connection = Socket.connect(host, port)?;
    reply = Socket.read(connection);
    Socket.keep(connection, session: "");
    return reply;
}
`

const readAfterKeepSource = `gateway readAfterKeep(host: string, port: int) -> Result<bytes, SocketError>
    requires Socket
{
    connection = Socket.connect(host, port)?;
    Socket.keep(connection, session: "");
    return Socket.read(connection);
}
`

const keepTwoSource = `gateway keepTwo(host: string, port: int, first: string, second: string) -> Result<None, SocketError>
    requires Socket
{
    a = Socket.connect(host, port)?;
    b = Socket.connect(host, port)?;
    Socket.keep(a, session: first);
    Socket.keep(b, session: second);
    return Ok(None);
}
`

// server is a loopback listener that counts the connections it accepts.
type server struct {
	ln       net.Listener
	accepted atomic.Int32
}

func startServer(t *testing.T, serve func(c net.Conn)) *server {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &server{ln: ln}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			s.accepted.Add(1)
			go func() {
				defer c.Close()
				serve(c)
			}()
		}
	}()
	return s
}

// echoEach answers every read with the same bytes, until the client closes.
func echoEach(c net.Conn) {
	buf := make([]byte, 64)
	for {
		n, err := c.Read(buf)
		if err != nil {
			return
		}
		if _, err := c.Write(buf[:n]); err != nil {
			return
		}
	}
}

func keepInterp(t *testing.T, p SocketProvider, src ...string) *Interp {
	t.Helper()
	if len(src) == 0 {
		src = []string{keepSource}
	}
	caps := NewCapabilityRegistry()
	caps.Register("Socket", p)
	t.Cleanup(p.Close)
	return newTestInterp(universeFromSource(t, src[0]), caps)
}

func exchange(t *testing.T, in *Interp, s *server, message, session string) string {
	t.Helper()
	v, fault := in.Call(context.Background(), "exchange", map[string]Value{
		"host": Str("127.0.0.1"), "port": localPort(t, s.ln.Addr()), "message": Str(message), "session": Str(session),
	})
	if fault != nil {
		t.Fatalf("exchange: %v", fault)
	}
	r, ok := v.(Result)
	if !ok || !r.Ok {
		t.Fatalf("exchange = %s", v)
	}
	return string(r.Value.(Bytes).B)
}

// §1, §2: a kept connection serves the next call with the same key, and
// only that one.
func TestKeptConnectionIsReused(t *testing.T) {
	s := startServer(t, echoEach)
	p := NewSocketProvider()
	in := keepInterp(t, p)
	for _, m := range []string{"one", "two", "three"} {
		if got := exchange(t, in, s, m, "user=a"); got != m {
			t.Fatalf("reply %q, want %q", got, m)
		}
	}
	if n := s.accepted.Load(); n != 1 {
		t.Errorf("three exchanges opened %d connections, want 1", n)
	}
	exchange(t, in, s, "four", "user=b")
	if n := s.accepted.Load(); n != 2 {
		t.Errorf("another session opened %d connections in all, want 2", n)
	}
	if n := p.Pool.Idle(); n != 2 {
		t.Errorf("pool holds %d, want one per session", n)
	}
}

// §4: a server that closes an idle connection, or sends on it, is noticed
// before the connection is handed out again.
func TestStaleIdleConnectionIsNotReused(t *testing.T) {
	cases := map[string]func(c net.Conn){
		"closed by the server": func(c net.Conn) {
			buf := make([]byte, 64)
			n, _ := c.Read(buf)
			_, _ = c.Write(buf[:n])
		},
		"bytes nobody asked for": func(c net.Conn) {
			buf := make([]byte, 64)
			n, _ := c.Read(buf)
			_, _ = c.Write(buf[:n])
			time.Sleep(20 * time.Millisecond)
			_, _ = c.Write([]byte("junk"))
			echoEach(c)
		},
	}
	for name, serve := range cases {
		t.Run(name, func(t *testing.T) {
			s := startServer(t, serve)
			in := keepInterp(t, NewSocketProvider())
			exchange(t, in, s, "first", "")
			time.Sleep(100 * time.Millisecond)
			if got := exchange(t, in, s, "second", ""); got != "second" {
				t.Errorf("second reply %q, want second", got)
			}
			if n := s.accepted.Load(); n != 2 {
				t.Errorf("opened %d connections, want 2", n)
			}
		})
	}
}

// §3: a connection whose read failed is closed, not kept.
func TestKeepAfterAFailureCloses(t *testing.T) {
	s := startServer(t, func(net.Conn) {})
	p := NewSocketProvider()
	in := keepInterp(t, p, keepAfterFailureSource)
	v, fault := in.Call(context.Background(), "keepAfterFailure", map[string]Value{
		"host": Str("127.0.0.1"), "port": localPort(t, s.ln.Addr()),
	})
	if fault != nil {
		t.Fatal(fault)
	}
	if r, ok := v.(Result); !ok || r.Ok {
		t.Fatalf("got %s, want Err(Closed)", v)
	}
	if n := p.Pool.Idle(); n != 0 {
		t.Errorf("pool holds %d, want 0", n)
	}
}

// §1: a handle given to keep belongs to the pool.
func TestUsingAConnectionAfterKeepFaults(t *testing.T) {
	s := startServer(t, echoEach)
	in := keepInterp(t, NewSocketProvider(), readAfterKeepSource)
	_, fault := in.Call(context.Background(), "readAfterKeep", map[string]Value{
		"host": Str("127.0.0.1"), "port": localPort(t, s.ln.Addr()),
	})
	if fault == nil || !strings.Contains(fault.Error(), "given to Socket.keep") {
		t.Fatalf("got fault %v", fault)
	}
}

// §5: the limits per key, in all, and in time.
func TestIdleLimits(t *testing.T) {
	s := startServer(t, echoEach)
	run := func(limits IdleLimits, first, second string) *SocketPool {
		t.Helper()
		p := SocketProvider{Backend: NetBackend{}, Pool: NewSocketPool(limits)}
		in := keepInterp(t, p, keepTwoSource)
		if _, fault := in.Call(context.Background(), "keepTwo", map[string]Value{
			"host": Str("127.0.0.1"), "port": localPort(t, s.ln.Addr()), "first": Str(first), "second": Str(second),
		}); fault != nil {
			t.Fatal(fault)
		}
		return p.Pool
	}
	if n := run(IdleLimits{PerKey: 1, Total: 10, Timeout: time.Minute}, "a", "a").Idle(); n != 1 {
		t.Errorf("perKey 1 kept %d", n)
	}
	if n := run(IdleLimits{PerKey: 10, Total: 1, Timeout: time.Minute}, "a", "b").Idle(); n != 1 {
		t.Errorf("total 1 kept %d", n)
	}
	pool := run(IdleLimits{PerKey: 10, Total: 10, Timeout: 30 * time.Millisecond}, "a", "b")
	deadline := time.Now().Add(2 * time.Second)
	for pool.Idle() > 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n := pool.Idle(); n != 0 {
		t.Errorf("after the idle timeout the pool holds %d", n)
	}
}

// §5a: a connection older than MaxLifetime, counted from when it was
// opened however often it was reused, is not handed out, not kept, and is
// closed when its lifetime ends while idle.
func TestMaxLifetime(t *testing.T) {
	s := startServer(t, echoEach)
	var clock atomic.Int64
	clock.Store(time.Unix(1700000000, 0).UnixNano())
	advance := func(d time.Duration) { clock.Add(int64(d)) }
	withClock := func(limits IdleLimits) SocketProvider {
		p := SocketProvider{Backend: NetBackend{}, Pool: NewSocketPool(limits)}
		p.Pool.now = func() time.Time { return time.Unix(0, clock.Load()) }
		return p
	}
	limits := IdleLimits{PerKey: 10, Total: 100, Timeout: time.Hour, MaxLifetime: 10 * time.Minute}

	p := withClock(limits)
	in := keepInterp(t, p)
	exchange(t, in, s, "one", "")
	advance(9 * time.Minute)
	exchange(t, in, s, "two", "")
	if n := s.accepted.Load(); n != 1 {
		t.Fatalf("at 9 minutes old the connection wasn't reused: %d opened", n)
	}
	// Reuse didn't reset its age: at 11 minutes it is closed and a new
	// one opened.
	advance(2 * time.Minute)
	if got := exchange(t, in, s, "three", ""); got != "three" {
		t.Fatalf("reply %q", got)
	}
	if n := s.accepted.Load(); n != 2 {
		t.Errorf("past its lifetime: %d connections opened in all, want 2", n)
	}
	if n := p.Pool.Idle(); n != 1 {
		t.Errorf("pool holds %d, want only the new connection", n)
	}

	// Past its lifetime by the time it is kept: closed, not kept.
	p = withClock(limits)
	t.Cleanup(p.Close)
	ctx, end := withResourceOwner(context.Background())
	opened, f := p.Call(ctx, "connect", []Value{Str("127.0.0.1"), localPort(t, s.ln.Addr())})
	if f != nil {
		t.Fatal(f)
	}
	advance(10 * time.Minute)
	if _, f := p.Call(ctx, "keep", []Value{opened.(Result).Value, Str("")}); f != nil {
		t.Fatal(f)
	}
	end()
	if n := p.Pool.Idle(); n != 0 {
		t.Errorf("kept a connection past its lifetime: pool holds %d", n)
	}

	// Swept: an idle connection is closed when its lifetime ends, before
	// the idle timeout.
	pool := SocketProvider{Backend: NetBackend{}, Pool: NewSocketPool(IdleLimits{PerKey: 10, Total: 10, Timeout: time.Hour, MaxLifetime: 50 * time.Millisecond})}
	exchange(t, keepInterp(t, pool), s, "four", "")
	deadline := time.Now().Add(2 * time.Second)
	for pool.Pool.Idle() > 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n := pool.Pool.Idle(); n != 0 {
		t.Errorf("after its lifetime the pool holds %d", n)
	}

	// 0 is no limit.
	p = withClock(IdleLimits{PerKey: 10, Total: 100, Timeout: time.Hour})
	in = keepInterp(t, p)
	before := s.accepted.Load()
	exchange(t, in, s, "five", "")
	advance(24 * time.Hour)
	exchange(t, in, s, "six", "")
	if n := s.accepted.Load() - before; n != 1 {
		t.Errorf("with no MaxLifetime: %d connections opened, want 1", n)
	}
}

// §2, §3: a connection is reused only by the package that kept it, and a
// database driver's connection can't be kept.
func TestKeepIsPerPackageAndNotForDrivers(t *testing.T) {
	s := startServer(t, echoEach)
	port := localPort(t, s.ln.Addr())
	p := NewSocketProvider()
	t.Cleanup(p.Close)
	inPackage := func(u *types.Universe) (context.Context, func()) {
		ctx, end := withResourceOwner(context.Background())
		return withScope(ctx, scope{universe: u}), end
	}
	mine, other := &types.Universe{}, &types.Universe{}

	ctx, end := inPackage(mine)
	opened, f := p.Call(ctx, "connect", []Value{Str("127.0.0.1"), port})
	if f != nil {
		t.Fatal(f)
	}
	if _, f := p.Call(ctx, "keep", []Value{opened.(Result).Value, Str("")}); f != nil {
		t.Fatal(f)
	}
	end()

	reuse := func(u *types.Universe) Value {
		ctx, end := inPackage(u)
		defer end()
		v, f := p.Call(ctx, "reuse", []Value{Str("127.0.0.1"), port, Bool(false), Str("")})
		if f != nil {
			t.Fatal(f)
		}
		return v
	}
	if v := reuse(other); v != (None{}) {
		t.Errorf("another package reused %s", v)
	}
	if _, ok := reuse(mine).(Connection); !ok {
		t.Error("the package that kept it got nothing")
	}

	driverCtx := context.WithValue(context.Background(), resourceOwnerKey{}, &resourceOwner{driver: true})
	opened, f = p.Call(driverCtx, "connect", []Value{Str("127.0.0.1"), port})
	if f != nil {
		t.Fatal(f)
	}
	if _, f := p.Call(driverCtx, "keep", []Value{opened.(Result).Value, Str("")}); f == nil || !strings.Contains(f.Error(), "driver's pool") {
		t.Errorf("keep on a driver's connection: %v", f)
	}
}
