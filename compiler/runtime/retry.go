package runtime

import (
	"context"
	"time"

	"fiig-lang/compiler/syntax"
)

// evalRetry implements `retry(attempts: N): body` (ADR 0014) — the checker
// already guarantees body wraps a call to a gateway declared `idempotent`
// before this ever runs, so the only remaining job here is the actual
// retry loop: attempt body up to N times, returning as soon as it produces
// Ok, backing off between failed attempts, and returning the last Err once
// attempts are exhausted.
func (in *Interp) evalRetry(ctx context.Context, e *syntax.RetryExpr, env *Env) (Value, bool, *Fault) {
	attemptsVal, earlyReturn, fault := in.evalExpr(ctx, e.Attempts, env)
	if fault != nil || earlyReturn {
		return attemptsVal, earlyReturn, fault
	}
	n, ok := attemptsVal.(Int)
	if !ok {
		return nil, false, Faultf("retry attempts must be an int (got %s)", attemptsVal)
	}
	if n < 1 {
		return nil, false, Faultf("retry attempts must be at least 1 (got %d)", n)
	}

	var last Value
	for attempt := int64(0); attempt < int64(n); attempt++ {
		if f := checkCancelled(ctx); f != nil {
			return nil, false, f
		}
		val, earlyReturn, fault := in.runProducingBody(ctx, e.Body, env)
		if fault != nil {
			return nil, false, fault
		}
		if earlyReturn {
			return val, true, nil
		}
		rv, ok := val.(Result)
		if !ok {
			// A non-Result retry body isn't attested anywhere in the corpus
			// (ADR 0014 only ever wraps a gateway call) — pass it through as a
			// one-shot success rather than guess a retry policy for it.
			return val, false, nil
		}
		if rv.Ok {
			return rv, false, nil
		}
		last = rv
		if attempt < int64(n)-1 {
			in.backoff(attempt)
		}
	}
	return last, false, nil
}

// backoff waits an exponentially increasing delay plus random jitter before
// the next attempt, through in.Clock so tests using a ManualClock (see
// NewDeterministic, clock.go) never actually wait.
func (in *Interp) backoff(attempt int64) {
	const base = 50 * time.Millisecond
	const maxAttempt = 10 // caps 1<<attempt from overflowing for any plausible `attempts` value
	shift := attempt
	if shift > maxAttempt {
		shift = maxAttempt
	}
	delay := base * time.Duration(int64(1)<<shift)
	jitter := time.Duration(in.jitter(int64(delay)/2 + 1))
	in.Clock.Sleep(delay + jitter)
}

// jitter returns a random int64 in [0, n) — Rand access is serialized,
// since math/rand.Rand isn't safe for concurrent use and retry can run
// inside a concurrent block's worker goroutines (concurrent.go).
func (in *Interp) jitter(n int64) int64 {
	if n <= 0 {
		return 0
	}
	in.randMu.Lock()
	defer in.randMu.Unlock()
	return in.Rand.Int63n(n)
}
