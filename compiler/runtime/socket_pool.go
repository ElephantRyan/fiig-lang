package runtime

import (
	"context"
	"errors"
	"math"
	"net"
	"strconv"
	"sync"
	"time"

	"fiig-lang/compiler/types"
)

// ADR 0109: connections kept between calls. Socket.keep hands a clean
// connection to the provider's SocketPool; Socket.reuse takes one back for a
// call of the same package, to the same place, set up the same way.

// IdleLimits bounds the connections a SocketPool keeps (ADR 0109 §5).
// MaxLifetime is measured from when a connection was opened, not from when
// it was kept: one older is neither handed out nor kept (§5a). 0 is no
// limit.
type IdleLimits struct {
	PerKey      int
	Total       int
	Timeout     time.Duration
	MaxLifetime time.Duration
}

// DefaultIdleLimits is §5's defaults: requests' pool_maxsize per key, and
// Go's MaxIdleConns and IdleConnTimeout; and §5a's ten minutes, so a busy
// connection, which the idle timeout never closes, still follows a DNS
// change or a load balancer's new members within that.
var DefaultIdleLimits = IdleLimits{PerKey: 10, Total: 100, Timeout: 90 * time.Second, MaxLifetime: 10 * time.Minute}

// keptKey is what a reused connection must match (§2). pkg is the root
// Universe of the package whose gateway called keep.
type keptKey struct {
	pkg     *types.Universe
	host    string
	port    int
	tls     bool
	session string
}

// keptConn is one idle connection the pool owns.
type keptConn struct {
	key  keptKey
	t    Transport
	addr string
	// opened is when the connection was opened, for MaxLifetime (§5a).
	opened time.Time
	timer  *time.Timer
	// watch is the read that notices the peer closing or sending while
	// the connection is idle; nil when the transport reports that itself
	// (staleReporter).
	watch chan error
	// taken is set, under the pool's lock, once reuse, expiry, or the
	// watch has claimed the connection, so only one of them acts on it.
	taken bool
}

// staleReporter is a transport that can tell without reading whether its
// peer closed it or sent bytes nobody read: the scripted socket, whose
// reads must follow its script.
type staleReporter interface{ staleWhileIdle() bool }

// SocketPool is the idle connections of one Socket binding (§2: a
// per-package binding has its own).
type SocketPool struct {
	limits IdleLimits
	// now is the clock MaxLifetime is measured by; time.Now unless a test
	// sets it.
	now func() time.Time

	mu     sync.Mutex
	idle   []*keptConn // oldest first
	closed bool
}

// NewSocketPool is a pool keeping at most limits.
func NewSocketPool(limits IdleLimits) *SocketPool {
	return &SocketPool{limits: limits, now: time.Now}
}

// openedAt is the time a connection opened now is stamped with: the
// pool's clock, or the real one without a pool.
func (p SocketProvider) openedAt() time.Time {
	if p.Pool != nil {
		return p.Pool.now()
	}
	return time.Now()
}

// lifeLeft is how long a connection opened at opened may still be kept,
// or handed out (§5a); math.MaxInt64 with no MaxLifetime.
func (pl *SocketPool) lifeLeft(opened time.Time) time.Duration {
	if pl.limits.MaxLifetime <= 0 {
		return time.Duration(math.MaxInt64)
	}
	return pl.limits.MaxLifetime - pl.now().Sub(opened)
}

// callingPackage is the package whose code is running: the root of its
// file scopes (ADR 0054), so every file of a package shares one key.
func callingPackage(ctx context.Context) *types.Universe {
	var u *types.Universe
	if c, ok := ctx.Value(callKey{}).(*callInfo); ok {
		u = c.sc.universe
	}
	for u != nil && u.Parent != nil {
		u = u.Parent
	}
	return u
}

// keep gives h to the pool (§1, §3). Misuse faults; a connection unfit to
// keep is closed.
func (p SocketProvider) keep(ctx context.Context, h *connHandle, session string) (Value, *Fault) {
	if h.owner != nil && h.owner.driver {
		return nil, Faultf("Socket.keep: this connection belongs to a database driver's pool (ADR 0084 §4), which keeps it")
	}
	h.readMu.Lock()
	defer h.readMu.Unlock()
	h.writeMu.Lock()
	defer h.writeMu.Unlock()
	t, f := h.usable("keep")
	if f != nil {
		return nil, f
	}
	if h.owner != ownerOf(ctx) {
		return nil, Faultf("Socket.keep: this connection belongs to another call")
	}
	h.stateMu.Lock()
	h.kept = true
	failed := h.failed
	h.stateMu.Unlock()
	// Kept only when its TLS, if any, was verified for the host it was
	// opened to: reuse can only ask for that (§2).
	if failed || p.Pool == nil || (h.tlsName != "" && h.tlsName != h.host) {
		_ = t.Close()
		return None{}, nil
	}
	key := keptKey{pkg: callingPackage(ctx), host: h.host, port: h.port, tls: h.tlsName != "", session: session}
	p.Pool.put(&keptConn{key: key, t: t, addr: h.addr, opened: h.opened})
	return None{}, nil
}

// reuse takes an idle connection matching the key for this call, or None.
func (p SocketProvider) reuse(ctx context.Context, host string, port int, secure bool, session string) (Value, *Fault) {
	owner := ownerOf(ctx)
	if owner == nil {
		return nil, Faultf("Socket.reuse outside a call: the runtime has no owner for the connection")
	}
	if p.Pool == nil {
		return None{}, nil
	}
	key := keptKey{pkg: callingPackage(ctx), host: host, port: port, tls: secure, session: session}
	for {
		kc := p.Pool.take(key)
		if kc == nil {
			return None{}, nil
		}
		// Past its lifetime is checked first: its timer may not have
		// fired yet, and the clock may be a test's (§5a).
		if p.Pool.lifeLeft(kc.opened) <= 0 || !kc.stillClean() {
			_ = kc.t.Close()
			continue
		}
		h := &connHandle{addr: kc.addr, host: host, port: port, owner: owner, t: kc.t, opened: kc.opened}
		if secure {
			h.tlsName = host
		}
		if !owner.track(h) {
			_ = kc.t.Close()
			return nil, Faultf("Socket.reuse: the owning call has already ended")
		}
		return Connection{h: h}, nil
	}
}

// put keeps kc, unless the pool is closed, its key is full, or it is past
// its lifetime; past the total, the connection idle longest is closed
// (§5). It is closed at the idle timeout or at the end of its lifetime,
// whichever comes first (§5a).
func (pl *SocketPool) put(kc *keptConn) {
	pl.mu.Lock()
	defer pl.mu.Unlock()
	same := 0
	for _, other := range pl.idle {
		if other.key == kc.key {
			same++
		}
	}
	left := pl.lifeLeft(kc.opened)
	if pl.closed || same >= pl.limits.PerKey || left <= 0 {
		_ = kc.t.Close()
		return
	}
	if len(pl.idle) >= pl.limits.Total {
		oldest := pl.idle[0]
		pl.idle = pl.idle[1:]
		pl.claimLocked(oldest)
		_ = oldest.t.Close()
	}
	pl.idle = append(pl.idle, kc)
	kc.timer = time.AfterFunc(min(pl.limits.Timeout, left), func() { pl.discard(kc) })
	if _, reports := kc.t.(staleReporter); !reports {
		kc.watch = make(chan error, 1)
		go pl.watch(kc)
	}
}

// watch reads while kc is idle. A read that returns before reuse stops it
// means the peer closed or sent bytes nobody asked for, so the connection
// is dropped then rather than handed out later.
func (pl *SocketPool) watch(kc *keptConn) {
	var b [1]byte
	n, err := kc.t.Read(b[:])
	if n > 0 {
		err = errUnreadBytes
	}
	pl.discard(kc)
	kc.watch <- err
}

var errUnreadBytes = errors.New("bytes arrived on an idle connection")

// discard closes kc unless reuse has already taken it.
func (pl *SocketPool) discard(kc *keptConn) {
	pl.mu.Lock()
	if kc.taken {
		pl.mu.Unlock()
		return
	}
	pl.removeLocked(kc)
	pl.claimLocked(kc)
	pl.mu.Unlock()
	_ = kc.t.Close()
}

// take removes and returns the newest idle connection for key, or nil.
// Newest first, as Go's Transport does, so the connections left idle are
// the ones the timeout closes.
func (pl *SocketPool) take(key keptKey) *keptConn {
	pl.mu.Lock()
	defer pl.mu.Unlock()
	for i := len(pl.idle) - 1; i >= 0; i-- {
		if kc := pl.idle[i]; kc.key == key {
			pl.idle = append(pl.idle[:i], pl.idle[i+1:]...)
			pl.claimLocked(kc)
			return kc
		}
	}
	return nil
}

func (pl *SocketPool) claimLocked(kc *keptConn) {
	kc.taken = true
	if kc.timer != nil {
		kc.timer.Stop()
	}
}

func (pl *SocketPool) removeLocked(kc *keptConn) {
	for i, other := range pl.idle {
		if other == kc {
			pl.idle = append(pl.idle[:i], pl.idle[i+1:]...)
			return
		}
	}
}

// stillClean reports whether a connection just taken from the pool can be
// handed out (§4): its peer hasn't closed it or sent anything while idle.
func (kc *keptConn) stillClean() bool {
	if r, reports := kc.t.(staleReporter); reports {
		return !r.staleWhileIdle()
	}
	// Stop the watching read. It ends with a timeout if nothing came;
	// anything else is the peer.
	_ = kc.t.SetDeadline(time.Unix(1, 0))
	err := <-kc.watch
	_ = kc.t.SetDeadline(time.Time{})
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// Idle is how many connections the pool holds, for tests.
func (pl *SocketPool) Idle() int {
	pl.mu.Lock()
	defer pl.mu.Unlock()
	return len(pl.idle)
}

// Close closes every idle connection and keeps none from now on, when the
// program stops (CapabilityRegistry.Close).
func (pl *SocketPool) Close() {
	pl.mu.Lock()
	idle := pl.idle
	pl.idle = nil
	pl.closed = true
	for _, kc := range idle {
		pl.claimLocked(kc)
	}
	pl.mu.Unlock()
	for _, kc := range idle {
		_ = kc.t.Close()
	}
}

// Close stops the provider's pool.
func (p SocketProvider) Close() {
	if p.Pool != nil {
		p.Pool.Close()
	}
}

func reuseArgs(args []Value) (string, int, bool, string, *Fault) {
	if len(args) != 4 {
		return "", 0, false, "", Faultf("Socket.reuse takes a host, a port, tls, and a session")
	}
	host, port, f := hostPort("reuse", args[:2])
	if f != nil {
		return "", 0, false, "", f
	}
	secure, okTLS := args[2].(Bool)
	session, okSession := args[3].(Str)
	if !okTLS || !okSession {
		return "", 0, false, "", Faultf("Socket.reuse takes a bool tls and a string session")
	}
	return host, port, bool(secure), string(session), nil
}

func addrOf(host string, port int) string { return net.JoinHostPort(host, strconv.Itoa(port)) }
