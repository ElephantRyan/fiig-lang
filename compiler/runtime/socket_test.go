package runtime

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// ADR 0060: the Socket primitive, against a scripted peer and against real
// loopback servers.

func sslRequest() []byte { return []byte{0, 0, 0, 8, 0x04, 0xD2, 0x16, 0x2F} }

func startupBytes(user, database string) []byte {
	params := "user\x00" + user + "\x00database\x00" + database + "\x00\x00"
	return append([]byte{0, 0, 0, byte(8 + len(params)), 0, 3, 0, 0}, params...)
}

// postgresInterp loads the postgres-wire corpus with script as its Socket.
func postgresInterp(t *testing.T, script *ScriptedSocket) *Interp {
	t.Helper()
	u := loadUniverse(t, "postgres-wire/models", "postgres-wire/conversions", "postgres-wire/gateways", "postgres-wire/activities")
	caps := NewCapabilityRegistry()
	caps.Register("Socket", SocketProvider{Backend: script})
	return newTestInterp(u, caps)
}

func authenticateArgs() map[string]Value {
	return map[string]Value{"host": Str("db"), "port": Int(5432), "user": Str("postgres"), "database": Str("app")}
}

// TestCorpusPostgresAuthenticate runs the whole opening: SSLRequest, 'S',
// the TLS upgrade, the startup message, and an MD5 request that arrives in
// two reads, so readMessage's while loop goes round more than once.
func TestCorpusPostgresAuthenticate(t *testing.T) {
	peer := &SocketPeer{Host: "db", Port: 5432, Steps: []SocketStep{
		{Expect: sslRequest()},
		{Send: []byte("S")},
		{StartTLS: true},
		{Expect: startupBytes("postgres", "app")},
		{Send: md5Request[:5]},
		{Send: md5Request[5:]},
	}}
	script := &ScriptedSocket{Peers: []*SocketPeer{peer}}
	v, fault := postgresInterp(t, script).Call(context.Background(), "authenticate", authenticateArgs())
	if fault != nil {
		t.Fatalf("authenticate: %v", fault)
	}
	r, ok := v.(Result)
	if !ok || !r.Ok {
		t.Fatalf("authenticate = %s, want Ok", v)
	}
	auth, ok := r.Value.(EnumValue)
	if !ok || auth.Variant != "Md5Password" || !Equal(auth.Fields[0], b(0xDE, 0xAD, 0xBE, 0xEF)) {
		t.Fatalf("authentication = %s, want Md5Password with the salt", r.Value)
	}
	if err := script.Done(); err != nil {
		t.Fatal(err)
	}
	// §5: authenticate leaves the connection open; the call closes it.
	if !peer.Closed() {
		t.Fatal("connection still open after the owning call ended")
	}
}

func socketErrVariant(t *testing.T, v Value, fault *Fault) string {
	t.Helper()
	if fault != nil {
		t.Fatalf("unexpected fault: %v", fault)
	}
	r, ok := v.(Result)
	if !ok || r.Ok {
		t.Fatalf("got %s, want Err(SocketError...)", v)
	}
	ev, ok := r.Value.(EnumValue)
	if !ok || ev.TypeName != "SocketError" {
		t.Fatalf("got %s, want a SocketError", r.Value)
	}
	return ev.Variant
}

func TestPostgresServerDecliningTlsIsRefused(t *testing.T) {
	peer := &SocketPeer{Host: "db", Port: 5432, Steps: []SocketStep{{Expect: sslRequest()}, {Send: []byte("N")}}}
	v, fault := postgresInterp(t, &ScriptedSocket{Peers: []*SocketPeer{peer}}).Call(context.Background(), "authenticate", authenticateArgs())
	if got := socketErrVariant(t, v, fault); got != "Refused" {
		t.Fatalf("got %s, want Refused", got)
	}
	if !peer.Closed() {
		t.Fatal("openSecure did not close the declined connection")
	}
}

func TestDialFailureIsASocketError(t *testing.T) {
	peer := &SocketPeer{Host: "db", Port: 5432, DialError: &SocketFailure{Variant: "HostNotFound"}}
	v, fault := postgresInterp(t, &ScriptedSocket{Peers: []*SocketPeer{peer}}).Call(context.Background(), "authenticate", authenticateArgs())
	if got := socketErrVariant(t, v, fault); got != "HostNotFound" {
		t.Fatalf("got %s, want HostNotFound", got)
	}
}

func TestPeerHangingUpMidFrameIsClosed(t *testing.T) {
	peer := &SocketPeer{Host: "db", Port: 5432, Steps: []SocketStep{
		{Expect: sslRequest()},
		{Send: []byte("S")},
		{StartTLS: true},
		{Expect: startupBytes("postgres", "app")},
		{Send: md5Request[:5]},
		{Hangup: true},
	}}
	v, fault := postgresInterp(t, &ScriptedSocket{Peers: []*SocketPeer{peer}}).Call(context.Background(), "authenticate", authenticateArgs())
	if got := socketErrVariant(t, v, fault); got != "Closed" {
		t.Fatalf("got %s, want Closed", got)
	}
}

func TestTlsHandshakeFailureCarriesItsReason(t *testing.T) {
	peer := &SocketPeer{Host: "db", Port: 5432, Steps: []SocketStep{
		{Expect: sslRequest()},
		{Send: []byte("S")},
		{StartTLS: true, TLSError: "certificate signed by unknown authority"},
	}}
	v, fault := postgresInterp(t, &ScriptedSocket{Peers: []*SocketPeer{peer}}).Call(context.Background(), "authenticate", authenticateArgs())
	if got := socketErrVariant(t, v, fault); got != "Tls" {
		t.Fatalf("got %s, want Tls", got)
	}
	if reason := v.(Result).Value.(EnumValue).Fields[0]; reason != Str("certificate signed by unknown authority") {
		t.Fatalf("reason = %s", reason)
	}
}

// TestWritingWhatTheScriptDoesNotExpectFaults: the fake fails a protocol
// test on the first wrong byte, naming both sides.
func TestWritingWhatTheScriptDoesNotExpectFaults(t *testing.T) {
	peer := &SocketPeer{Host: "db", Port: 5432, Steps: []SocketStep{{Expect: []byte{0, 0, 0, 8, 0, 0, 0, 0}}}}
	_, fault := postgresInterp(t, &ScriptedSocket{Peers: []*SocketPeer{peer}}).Call(context.Background(), "authenticate", authenticateArgs())
	if fault == nil || !strings.Contains(fault.Error(), "socket script expected a write") {
		t.Fatalf("got fault %v, want a script mismatch", fault)
	}
}

// TestStalledReadStopsAtDeadline is §4: no SocketError for a timeout; the
// call's deadline ends the read with the cancellation fault.
func TestStalledReadStopsAtDeadline(t *testing.T) {
	peer := &SocketPeer{Host: "db", Port: 5432, Steps: []SocketStep{{Expect: sslRequest()}, {Stall: true}}}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, fault := postgresInterp(t, &ScriptedSocket{Peers: []*SocketPeer{peer}}).Call(ctx, "authenticate", authenticateArgs())
	if fault == nil || !strings.Contains(fault.Error(), "cancelled") {
		t.Fatalf("got fault %v, want a cancellation fault", fault)
	}
}

const misuseSource = `gateway readAfterClose(host: string) -> Result<bytes, SocketError>
    requires Socket
{
    connection = Socket.connect(host, 1)?;
    Socket.close(connection);
    Socket.close(connection);
    return Socket.read(connection);
}
`

const supersededSource = `gateway plaintextAfterUpgrade(host: string) -> Result<None, SocketError>
    requires Socket
{
    connection = Socket.connect(host, 1)?;
    secure = Socket.startTls(connection, host)?;
    return Socket.write(connection, <<1: u8>>);
}
`

// TestMisusingAConnectionFaults covers §4's two program errors; closing
// twice is not one of them.
func TestMisusingAConnectionFaults(t *testing.T) {
	cases := []struct {
		src, name, want string
		steps           []SocketStep
	}{
		{misuseSource, "readAfterClose", "already closed by this program", nil},
		{supersededSource, "plaintextAfterUpgrade", "upgraded by startTls", []SocketStep{{StartTLS: true}}},
	}
	for _, tc := range cases {
		caps := NewCapabilityRegistry()
		caps.Register("Socket", SocketProvider{Backend: &ScriptedSocket{Peers: []*SocketPeer{{Host: "h", Port: 1, Steps: tc.steps}}}})
		_, fault := newTestInterp(universeFromSource(t, tc.src), caps).Call(context.Background(), tc.name, map[string]Value{"host": Str("h")})
		if fault == nil || !strings.Contains(fault.Error(), tc.want) {
			t.Errorf("%s: got fault %v, want one containing %q", tc.name, fault, tc.want)
		}
	}
}

const echoSource = `gateway echo(host: string, port: int, message: string) -> Result<bytes, SocketError>
    requires Socket
{
    connection = Socket.connect(host, port)?;
    Socket.write(connection, message.utf8())?;
    reply = Socket.read(connection)?;
    Socket.close(connection);
    return Ok(reply);
}
`

func realSocketInterp(t *testing.T, src string) *Interp {
	t.Helper()
	caps := NewCapabilityRegistry()
	caps.Register("Socket", NewSocketProvider())
	return newTestInterp(universeFromSource(t, src), caps)
}

func localPort(t *testing.T, addr net.Addr) Int {
	t.Helper()
	_, port, err := net.SplitHostPort(addr.String())
	if err != nil {
		t.Fatal(err)
	}
	n, _ := strconv.Atoi(port)
	return Int(n)
}

// TestRealSocketEchoesOverLoopback is the real NetBackend end to end.
func TestRealSocketEchoesOverLoopback(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		buf := make([]byte, 64)
		n, _ := c.Read(buf)
		_, _ = c.Write(buf[:n])
	}()
	v, fault := realSocketInterp(t, echoSource).Call(context.Background(), "echo", map[string]Value{
		"host": Str("127.0.0.1"), "port": localPort(t, ln.Addr()), "message": Str("ping"),
	})
	if fault != nil {
		t.Fatalf("echo: %v", fault)
	}
	if r, ok := v.(Result); !ok || !r.Ok || !Equal(r.Value, b('p', 'i', 'n', 'g')) {
		t.Fatalf("echo = %s, want Ok(ping)", v)
	}
}

func TestRealSocketRefusedConnection(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := localPort(t, ln.Addr())
	ln.Close()
	v, fault := realSocketInterp(t, echoSource).Call(context.Background(), "echo", map[string]Value{
		"host": Str("127.0.0.1"), "port": port, "message": Str("ping"),
	})
	if got := socketErrVariant(t, v, fault); got != "Refused" {
		t.Fatalf("got %s, want Refused", got)
	}
}

// TestRealPeerClosingIsClosed: a clean hangup reads as Err(Closed), never
// as empty bytes (§3).
func TestRealPeerClosingIsClosed(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		_, _ = io.ReadFull(c, make([]byte, 4))
		c.Close()
	}()
	v, fault := realSocketInterp(t, echoSource).Call(context.Background(), "echo", map[string]Value{
		"host": Str("127.0.0.1"), "port": localPort(t, ln.Addr()), "message": Str("ping"),
	})
	if got := socketErrVariant(t, v, fault); got != "Closed" {
		t.Fatalf("got %s, want Closed", got)
	}
}

const tlsSource = `gateway hello(host: string, port: int) -> Result<None, SocketError>
    requires Socket
{
    connection = Socket.connectTls(host, port)?;
    Socket.close(connection);
    return Ok(None);
}
`

// TestRealTlsRejectsAnUntrustedCertificate: httptest's certificate is
// self-signed, so verification against the host's trust store fails, and
// that reaches fiig as SocketError.Tls, not a fault.
func TestRealTlsRejectsAnUntrustedCertificate(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	srv.Config.ErrorLog = nil
	v, fault := realSocketInterp(t, tlsSource).Call(context.Background(), "hello", map[string]Value{
		"host": Str("127.0.0.1"), "port": localPort(t, srv.Listener.Addr()),
	})
	if got := socketErrVariant(t, v, fault); got != "Tls" {
		t.Fatalf("got %s, want Tls", got)
	}
}

// TestRealConnectionLeftOpenIsClosedByItsCall is §5 against a real socket:
// the server sees EOF once the call that leaked the connection returns.
func TestRealConnectionLeftOpenIsClosedByItsCall(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	sawEOF := make(chan bool, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			sawEOF <- false
			return
		}
		defer c.Close()
		_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
		_, err = c.Read(make([]byte, 1))
		sawEOF <- err == io.EOF
	}()
	src := `gateway leak(host: string, port: int) -> Result<None, SocketError>
    requires Socket
{
    connection = Socket.connect(host, port)?;
    return Ok(None);
}
`
	if _, fault := realSocketInterp(t, src).Call(context.Background(), "leak", map[string]Value{
		"host": Str("127.0.0.1"), "port": localPort(t, ln.Addr()),
	}); fault != nil {
		t.Fatalf("leak: %v", fault)
	}
	if !<-sawEOF {
		t.Fatal("server did not see the leaked connection closed")
	}
}
