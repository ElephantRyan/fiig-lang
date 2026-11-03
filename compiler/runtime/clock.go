package runtime

import (
	"context"
	"math/rand"
	"sync"
	"time"

	"fiig-lang/compiler/types"
)

// Clock is the interpreter's only source of wall-clock time and the only
// way it ever waits — retry.go's backoff goes through it instead of calling
// time.Sleep directly, so a test can swap in ManualClock and get a
// deterministic, instant run of code that would otherwise really sleep.
// This is deliberately scoped to retry backoff, not a full cooperative
// task scheduler that also controls `concurrent` block interleaving —
// concurrent.go's worker pool still runs on real goroutines.
type Clock interface {
	Now() time.Time
	Sleep(d time.Duration)
}

// RealClock is the default: an ordinary wall clock.
type RealClock struct{}

func (RealClock) Now() time.Time        { return time.Now() }
func (RealClock) Sleep(d time.Duration) { time.Sleep(d) }

// ManualClock never actually blocks — Sleep just advances Now() and records
// the requested duration, so a retry loop's backoff schedule can be
// asserted on directly instead of a test having to wait through it in real
// time.
type ManualClock struct {
	mu     sync.Mutex
	now    time.Time
	sleeps []time.Duration
}

func NewManualClock(start time.Time) *ManualClock {
	return &ManualClock{now: start}
}

func (c *ManualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *ManualClock) Sleep(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	c.sleeps = append(c.sleeps, d)
}

// Sleeps returns every duration requested so far, in order.
func (c *ManualClock) Sleeps() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Duration(nil), c.sleeps...)
}

// NewDeterministic builds an Interp whose Clock never really waits and
// whose Rand is seeded — the "deterministic test scheduler and
// controllable clock" deliverable, scoped to what this interpreter
// actually needs determinism for: retry backoff/jitter. Concurrent-block
// completion order still depends on real goroutine scheduling and whatever
// CapabilityProvider a test registers; tests that need a specific
// completion order should use `preserveOrder` (ADR 0024) or a provider that
// itself blocks on a controlled signal, not this.
func NewDeterministic(universe *types.Universe, caps *CapabilityRegistry, seed int64) *Interp {
	return &Interp{
		Universe:                universe,
		Capabilities:            caps,
		Clock:                   NewManualClock(time.Unix(0, 0)),
		Rand:                    rand.New(rand.NewSource(seed)),
		DefaultConcurrencyLimit: 16,
	}
}

// ClockProvider exposes a Clock as the `Clock` primitive capability (ADR
// 0042): `Clock.now()` returns the current time as Unix milliseconds and
// `Clock.sleep(ms)` waits that long, both through the same Clock the
// interpreter's retry backoff uses, so a ManualClock controls fiig code and
// the runtime together. Milliseconds as a plain Int is deliberate for now —
// a richer instant type belongs to a time library written in fiig on top of
// this, not to the floor.
type ClockProvider struct{ Clock Clock }

func (p ClockProvider) Call(_ context.Context, method string, args []Value) (Value, *Fault) {
	switch method {
	case "now":
		return Int(p.Clock.Now().UnixMilli()), nil
	case "sleep":
		if len(args) != 1 {
			return nil, Faultf("Clock.sleep takes one argument (milliseconds)")
		}
		ms, ok := args[0].(Int)
		if !ok {
			return nil, Faultf("Clock.sleep takes an int number of milliseconds, got %T", args[0])
		}
		p.Clock.Sleep(time.Duration(ms) * time.Millisecond)
		return None{}, nil
	default:
		return nil, Faultf("Clock has no method %q", method)
	}
}
