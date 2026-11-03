package runtime

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"sync"
	"syscall"
	"time"
)

// ADR 0060: the Socket primitive. SocketProvider implements its six
// operations over a SocketBackend; NetBackend is the real one (net and
// crypto/tls), ScriptedSocket (socket_script.go) the test double.

// maxRead is the most one Socket.read returns (ADR 0060 §3).
const maxRead = 64 * 1024

// Transport is one open byte stream a SocketBackend hands out.
type Transport interface {
	io.ReadWriteCloser
	// SetDeadline unblocks a pending Read or Write once t passes; the
	// provider uses it to honor the call's context.
	SetDeadline(t time.Time) error
}

// SocketBackend opens transports. Errors that should reach fiig code as a
// particular SocketError variant are a *SocketFailure, or a net/syscall
// error the provider classifies (socketFailureOf); any other error is a
// fault.
type SocketBackend interface {
	Dial(ctx context.Context, host string, port int) (Transport, error)
	StartTLS(ctx context.Context, t Transport, serverName string) (Transport, error)
}

// SocketFailure is a SocketError variant a backend reports directly.
// Reason is used only by the Tls variant.
type SocketFailure struct {
	Variant string
	Reason  string
}

func (f *SocketFailure) Error() string {
	if f.Reason != "" {
		return f.Variant + ": " + f.Reason
	}
	return f.Variant
}

// Connection is the runtime value of the prelude's opaque `Connection` type
// (ADR 0060 §2). Copies of one Connection share the same handle.
type Connection struct{ h *connHandle }

func (Connection) isValue() {}
func (c Connection) String() string {
	if c.h == nil {
		return "<connection>"
	}
	return "<connection " + c.h.addr + ">"
}

// connHandle is one open connection. readMu and writeMu serialize each
// direction (ADR 0060 open question 3): one reader and one writer may run
// at once, two readers may not interleave. startTls holds both. stateMu
// guards closed and superseded, which close may change mid-read.
type connHandle struct {
	addr       string
	owner      *resourceOwner
	readMu     sync.Mutex
	writeMu    sync.Mutex
	stateMu    sync.Mutex
	t          Transport
	closed     bool
	superseded bool
}

// usable returns the handle's transport, or the fault for using it after
// the program closed it, after startTls replaced it, or after its owning
// call ended (ADR 0060 §4).
func (h *connHandle) usable(op string) (Transport, *Fault) {
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	switch {
	case h.superseded:
		return nil, Faultf("Socket.%s: this connection was upgraded by startTls; use the connection startTls returned", op)
	case h.closed && h.owner.isEnded():
		return nil, Faultf("Socket.%s: the call that opened this connection has ended, so it was closed", op)
	case h.closed:
		return nil, Faultf("Socket.%s: connection already closed by this program", op)
	}
	return h.t, nil
}

func (h *connHandle) closeTransport() {
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	if h.closed || h.superseded {
		return
	}
	h.closed = true
	_ = h.t.Close()
}

// resourceOwner is the top-level call that owns every connection opened
// beneath it (ADR 0060 §5, Erlang's port-owner model). Interp.Call creates
// one and ends it on the way out, however the call ends.
type resourceOwner struct {
	mu      sync.Mutex
	handles []*connHandle
	ended   bool
}

type resourceOwnerKey struct{}

func ownerOf(ctx context.Context) *resourceOwner {
	o, _ := ctx.Value(resourceOwnerKey{}).(*resourceOwner)
	return o
}

// withResourceOwner gives ctx an owner when it has none yet. end is a no-op
// unless this call created the owner.
func withResourceOwner(ctx context.Context) (context.Context, func()) {
	if ownerOf(ctx) != nil {
		return ctx, func() {}
	}
	o := &resourceOwner{}
	return context.WithValue(ctx, resourceOwnerKey{}, o), o.end
}

func (o *resourceOwner) track(h *connHandle) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.ended {
		return false
	}
	o.handles = append(o.handles, h)
	return true
}

func (o *resourceOwner) isEnded() bool {
	if o == nil {
		return false
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.ended
}

func (o *resourceOwner) end() {
	o.mu.Lock()
	o.ended = true
	handles := o.handles
	o.handles = nil
	o.mu.Unlock()
	for _, h := range handles {
		h.closeTransport()
	}
}

// SocketProvider is the `Socket` primitive capability.
type SocketProvider struct {
	Backend SocketBackend
}

// NewSocketProvider is the real Socket: TCP via net, TLS via crypto/tls
// against the host's trust store.
func NewSocketProvider() SocketProvider {
	return SocketProvider{Backend: NetBackend{}}
}

func (p SocketProvider) Call(ctx context.Context, method string, args []Value) (Value, *Fault) {
	switch method {
	case "connect", "connectTls":
		host, port, f := hostPort(method, args)
		if f != nil {
			return nil, f
		}
		return p.connect(ctx, host, port, method == "connectTls")
	case "startTls":
		h, f := connArg(method, args, 2)
		if f != nil {
			return nil, f
		}
		name, ok := args[1].(Str)
		if !ok {
			return nil, Faultf("Socket.startTls: serverName must be a string")
		}
		return p.startTls(ctx, h, string(name))
	case "read":
		h, f := connArg(method, args, 1)
		if f != nil {
			return nil, f
		}
		return read(ctx, h)
	case "write":
		h, f := connArg(method, args, 2)
		if f != nil {
			return nil, f
		}
		data, ok := args[1].(Bytes)
		if !ok {
			return nil, Faultf("Socket.write: data must be bytes")
		}
		return write(ctx, h, data.B)
	case "close":
		h, f := connArg(method, args, 1)
		if f != nil {
			return nil, f
		}
		h.closeTransport()
		return None{}, nil
	}
	return nil, Faultf("Socket has no operation %q", method)
}

func hostPort(op string, args []Value) (string, int, *Fault) {
	if len(args) != 2 {
		return "", 0, Faultf("Socket.%s takes a host and a port", op)
	}
	host, ok := args[0].(Str)
	port, okPort := args[1].(Int)
	if !ok || !okPort {
		return "", 0, Faultf("Socket.%s takes a string host and an int port", op)
	}
	if port < 1 || port > 65535 {
		return "", 0, Faultf("Socket.%s: port %d is out of range", op, port)
	}
	return string(host), int(port), nil
}

func connArg(op string, args []Value, n int) (*connHandle, *Fault) {
	if len(args) != n {
		return nil, Faultf("Socket.%s takes %d argument(s)", op, n)
	}
	c, ok := args[0].(Connection)
	if !ok || c.h == nil {
		return nil, Faultf("Socket.%s: first argument must be a Connection", op)
	}
	return c.h, nil
}

func (p SocketProvider) connect(ctx context.Context, host string, port int, secure bool) (Value, *Fault) {
	owner := ownerOf(ctx)
	if owner == nil {
		return nil, Faultf("Socket.connect outside a call: the runtime has no owner for the connection")
	}
	t, err := p.Backend.Dial(ctx, host, port)
	if err != nil {
		return socketErr(ctx, "connect", err)
	}
	if secure {
		tt, err := p.Backend.StartTLS(ctx, t, host)
		if err != nil {
			_ = t.Close()
			return socketErr(ctx, "connectTls", err)
		}
		t = tt
	}
	h := &connHandle{addr: net.JoinHostPort(host, strconv.Itoa(port)), owner: owner, t: t}
	if !owner.track(h) {
		_ = t.Close()
		return nil, Faultf("Socket.connect: the owning call has already ended")
	}
	return Result{Ok: true, Value: Connection{h: h}}, nil
}

func (p SocketProvider) startTls(ctx context.Context, h *connHandle, serverName string) (Value, *Fault) {
	h.readMu.Lock()
	defer h.readMu.Unlock()
	h.writeMu.Lock()
	defer h.writeMu.Unlock()
	t, f := h.usable("startTls")
	if f != nil {
		return nil, f
	}
	tt, err := p.Backend.StartTLS(ctx, t, serverName)
	if err != nil {
		return socketErr(ctx, "startTls", err)
	}
	h.stateMu.Lock()
	h.superseded = true
	h.stateMu.Unlock()
	next := &connHandle{addr: h.addr, owner: h.owner, t: tt}
	if !h.owner.track(next) {
		_ = tt.Close()
		return nil, Faultf("Socket.startTls: the owning call has already ended")
	}
	return Result{Ok: true, Value: Connection{h: next}}, nil
}

func read(ctx context.Context, h *connHandle) (Value, *Fault) {
	h.readMu.Lock()
	defer h.readMu.Unlock()
	t, f := h.usable("read")
	if f != nil {
		return nil, f
	}
	stop := unblockOnDone(ctx, t)
	defer stop()
	buf := make([]byte, maxRead)
	for {
		n, err := t.Read(buf)
		if n > 0 {
			return Result{Ok: true, Value: Bytes{B: buf[:n:n]}}, nil
		}
		if err != nil {
			return socketErr(ctx, "read", err)
		}
	}
}

func write(ctx context.Context, h *connHandle, data []byte) (Value, *Fault) {
	h.writeMu.Lock()
	defer h.writeMu.Unlock()
	t, f := h.usable("write")
	if f != nil {
		return nil, f
	}
	stop := unblockOnDone(ctx, t)
	defer stop()
	for len(data) > 0 {
		n, err := t.Write(data)
		data = data[n:]
		if err != nil {
			return socketErr(ctx, "write", err)
		}
	}
	return Result{Ok: true, Value: None{}}, nil
}

// unblockOnDone makes a blocked Read or Write return once ctx is done, so a
// read that never completes stops at the call's deadline (ADR 0060 §4).
func unblockOnDone(ctx context.Context, t Transport) (stop func()) {
	cancel := context.AfterFunc(ctx, func() { _ = t.SetDeadline(time.Unix(1, 0)) })
	return func() { cancel() }
}

// socketErr turns a backend error into Err(SocketError.…), or into the
// cancellation fault when the call's context is what stopped it.
func socketErr(ctx context.Context, op string, err error) (Value, *Fault) {
	if ctx.Err() != nil {
		return nil, Faultf("cancelled: %v", ctx.Err())
	}
	failure := socketFailureOf(err)
	if failure == nil {
		return nil, Faultf("Socket.%s: %v", op, err)
	}
	ev := EnumValue{TypeName: "SocketError", Variant: failure.Variant}
	if failure.Variant == "Tls" {
		ev.Fields = []Value{Str(failure.Reason)}
	}
	return Result{Ok: false, Value: ev}, nil
}

// socketFailureOf classifies err as a SocketError variant, or nil when it is
// none of them.
func socketFailureOf(err error) *SocketFailure {
	var sf *SocketFailure
	if errors.As(err, &sf) {
		return sf
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return &SocketFailure{Variant: "HostNotFound"}
	}
	var certErr *tls.CertificateVerificationError
	var recordErr tls.RecordHeaderError
	var alert tls.AlertError
	if errors.As(err, &certErr) || errors.As(err, &recordErr) || errors.As(err, &alert) {
		return &SocketFailure{Variant: "Tls", Reason: err.Error()}
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		if variant, ok := platformErrnoVariants[errno]; ok {
			return &SocketFailure{Variant: variant}
		}
	}
	switch {
	case errors.Is(err, io.EOF), errors.Is(err, net.ErrClosed), errors.Is(err, io.ErrClosedPipe):
		return &SocketFailure{Variant: "Closed"}
	case errors.Is(err, syscall.ECONNREFUSED):
		return &SocketFailure{Variant: "Refused"}
	case errors.Is(err, syscall.ECONNRESET), errors.Is(err, syscall.ECONNABORTED), errors.Is(err, syscall.EPIPE):
		return &SocketFailure{Variant: "Reset"}
	case errors.Is(err, syscall.ENETUNREACH), errors.Is(err, syscall.EHOSTUNREACH), errors.Is(err, os.ErrDeadlineExceeded):
		return &SocketFailure{Variant: "Unreachable"}
	}
	return nil
}

// NetBackend is the real backend: net for TCP, crypto/tls for TLS, verified
// against the host's trust store.
type NetBackend struct{}

func (NetBackend) Dial(ctx context.Context, host string, port int) (Transport, error) {
	var d net.Dialer
	c, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return nil, err
	}
	return c, nil
}

func (NetBackend) StartTLS(ctx context.Context, t Transport, serverName string) (Transport, error) {
	nc, ok := t.(net.Conn)
	if !ok {
		return nil, fmt.Errorf("startTls: transport %T is not a net.Conn", t)
	}
	tc := tls.Client(nc, &tls.Config{ServerName: serverName, MinVersion: tls.VersionTLS12})
	if err := tc.HandshakeContext(ctx); err != nil {
		var sf *SocketFailure
		if errors.As(err, &sf) || ctx.Err() != nil {
			return nil, err
		}
		return nil, &SocketFailure{Variant: "Tls", Reason: err.Error()}
	}
	return tc, nil
}
