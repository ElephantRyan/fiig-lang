package adapter

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sync/atomic"
	"time"
)

// Limits bound what clients can make a served http adapter hold (ADR
// 0076): every one is on by default and set by the deployment.
type Limits struct {
	MaxBodyBytes   int64         // §1: MAX_BODY_BYTES
	MaxRequests    int           // §2: MAX_REQUESTS running, as many more waiting
	RequestTimeout time.Duration // §3: REQUEST_TIMEOUT
}

const (
	DefaultMaxBodyBytes   = 2 << 20 // 2 MiB, axum's and Spring's figure
	DefaultMaxRequests    = 256
	DefaultRequestTimeout = 30 * time.Second

	// SlotWait is how long a request waits for a slot before a 503 (§2).
	SlotWait = 5 * time.Second
	// MaxHeaderBytes bounds the request line and headers (§1), which a
	// waiting request holds.
	MaxHeaderBytes = 64 << 10
	// writeGrace is the time past the deadline the server allows to write
	// the answer, so a request stopped by §3 still gets its 503.
	writeGrace = 5 * time.Second
)

func DefaultLimits() Limits {
	return Limits{MaxBodyBytes: DefaultMaxBodyBytes, MaxRequests: DefaultMaxRequests, RequestTimeout: DefaultRequestTimeout}
}

// ServerTimeouts are the read and write timeouts an http.Server serving
// under l needs (§3): a request's whole life is its wait for a slot and
// its deadline, and the write gets writeGrace more.
func (l Limits) ServerTimeouts() (read, write time.Duration) {
	return SlotWait + l.RequestTimeout, SlotWait + l.RequestTimeout + writeGrace
}

// limiter is one adapter's limits and its slots, shared by every route.
type limiter struct {
	Limits
	slots   chan struct{}
	waiting atomic.Int64
	wait    time.Duration
}

// slotWait is SlotWait, shorter in tests.
var slotWait = SlotWait

func newLimiter(l Limits) *limiter {
	return &limiter{Limits: l, slots: make(chan struct{}, l.MaxRequests), wait: slotWait}
}

// wrap runs next under the limits: a declared body over §1's limit is
// refused before waiting, a request waits for a slot (§2), and it runs
// under the deadline (§3) with its body read through a limited reader.
func (l *limiter) wrap(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength > l.MaxBodyBytes {
			l.tooLarge(w)
			return
		}
		if !l.acquire(r.Context()) {
			w.Header().Set("Retry-After", "1")
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": fmt.Sprintf("the server is running its limit of %d requests at once (MAX_REQUESTS); retry", l.MaxRequests)})
			return
		}
		defer func() { <-l.slots }()
		deadline := time.Now().Add(l.RequestTimeout)
		ctx, cancel := context.WithDeadline(r.Context(), deadline)
		defer cancel()
		r = r.WithContext(ctx)
		// The connection gets the deadline too, so a body read stalls no
		// longer than the workflow may run; ServerTimeouts is only the
		// backstop. A writer without a connection (a test's) can't.
		rc := http.NewResponseController(w)
		_ = rc.SetReadDeadline(deadline)
		_ = rc.SetWriteDeadline(deadline.Add(writeGrace))
		r.Body = http.MaxBytesReader(w, r.Body, l.MaxBodyBytes)
		next(w, r)
	}
}

// acquire takes a slot at once, or waits up to l.wait for one when
// fewer than MaxRequests are already waiting.
func (l *limiter) acquire(ctx context.Context) bool {
	select {
	case l.slots <- struct{}{}:
		return true
	default:
	}
	if l.waiting.Add(1) > int64(l.MaxRequests) {
		l.waiting.Add(-1)
		return false
	}
	defer l.waiting.Add(-1)
	t := time.NewTimer(l.wait)
	defer t.Stop()
	select {
	case l.slots <- struct{}{}:
		return true
	case <-t.C:
		return false
	case <-ctx.Done():
		return false
	}
}

func (l *limiter) tooLarge(w http.ResponseWriter) {
	writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{"error": fmt.Sprintf("the request body is over the limit of %d bytes (MAX_BODY_BYTES)", l.MaxBodyBytes)})
}

func (l *limiter) timedOut(w http.ResponseWriter) {
	writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": fmt.Sprintf("the request ran past the limit of %s (REQUEST_TIMEOUT)", l.RequestTimeout)})
}

// bodyError answers a failed body read: over §1's limit is 413, past the
// deadline (the request's, or the server's read timeout) is §3's 503, and
// anything else is the client's 400.
func (l *limiter) bodyError(w http.ResponseWriter, r *http.Request, err error) {
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &tooLarge):
		l.tooLarge(w)
	case errors.Is(err, os.ErrDeadlineExceeded) || r.Context().Err() == context.DeadlineExceeded:
		l.timedOut(w)
	default:
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": fmt.Sprintf("invalid request body: %v", err)})
	}
}
