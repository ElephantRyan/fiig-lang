package adapter

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"fiig-lang/compiler/runtime"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// ADR 0124 §2: a startup adapter's checks. Before anything listens, every
// route's workflow runs at once, each with a deadline, and the process
// starts only when none says halt, faults, or runs out of time.

// StartupDeadline is how long one startup route may run (ADR 0124 §2).
const StartupDeadline = 30 * time.Second

// OnceHold is how long a `once` route's claim is held (ADR 0124 §6): a
// replica that starts within it, as the replicas of one rolling deploy do,
// finds the route claimed and skips it.
const OnceHold = 10 * time.Minute

// StartupRunner runs one startup route.
type StartupRunner struct {
	name  string // `Preflight run -> verifyStorage`
	route *syntax.Route
	in    *runtime.Interp
	enter func(context.Context) context.Context
	lock  runtime.ScheduleLock // set for a `once` route
	// holder names this process in a `once` claim.
	holder string
	// Deadline is how long the route may run: its `within`, else
	// StartupDeadline, unless a test shortens it.
	Deadline time.Duration
	// IgnoreOnce runs a `once` route without claiming it, as `fiig serve
	// -check` does: a check run is one process asking whether it could start.
	IgnoreOnce bool
	// Logf reports a failed check the mapping lets the process start after,
	// and a `once` route another replica has claimed.
	Logf func(format string, args ...any)
}

// Name is the route's name, as the report of a failure gives it.
func (r *StartupRunner) Name() string { return r.name }

// Lock is the route's claim lock, nil unless the route says `once`, so the
// caller can read the config a lock written in fiig uses.
func (r *StartupRunner) Lock() runtime.ScheduleLock { return r.lock }

// StartupRunners plans every route of the startup adapters in adapters. A
// `once` route is claimed with the ScheduleLock bound for its package, and
// fails here, before anything runs, when none is bound. holder names this
// process in its claims.
func StartupRunners(adapters []Served, u *types.Universe, in *runtime.Interp, holder string) ([]*StartupRunner, error) {
	var out []*StartupRunner
	for _, a := range adapters {
		d := a.Decl
		if d.Kind != syntax.AdapterStartup {
			return nil, fmt.Errorf("adapter %q is not a startup adapter", a.Name())
		}
		scope, enter, pkgPath := u, func(ctx context.Context) context.Context { return ctx }, ""
		if pkg := a.Package; pkg != nil {
			scope, pkgPath = pkg.Universe, pkg.Path
			enter = func(ctx context.Context) context.Context { return in.EnterPackage(ctx, pkg) }
		}
		for _, route := range d.Routes {
			if _, err := routeWorkflow(route, scope); err != nil {
				return nil, err
			}
			deadline := StartupDeadline
			if route.Within != "" {
				w, err := time.ParseDuration(route.Within)
				if err != nil || w <= 0 {
					return nil, fmt.Errorf("adapter %s: `within %q` is not a positive duration (ADR 0124 §6)", a.Name(), route.Within)
				}
				deadline = w
			}
			var lock runtime.ScheduleLock
			if route.Once {
				bound, ok := in.Capabilities.LookupFor(pkgPath, runtime.ScheduleLockCapability)
				l, isLock := bound.(runtime.ScheduleLock)
				if !ok || !isLock {
					return nil, fmt.Errorf(`adapter %s has a startup route that says once, but the capabilities file binds no ScheduleLock to claim it with; add {"ScheduleLock": {"type": "lock", "lock": "fiig.redis"}}, or {"ScheduleLock": {"type": "local"}} when one process runs it (ADR 0124 §6)`, a.Name())
				}
				if p, isFiig := l.(*runtime.LockProvider); isFiig {
					if _, _, f := p.Resolve(); f != nil {
						return nil, fmt.Errorf("adapter %s: %s", a.Name(), f.Message)
					}
				}
				lock = l
			}
			out = append(out, &StartupRunner{
				name:     fmt.Sprintf("%s run -> %s", a.Name(), workflowName(route)),
				route:    route,
				in:       in,
				enter:    enter,
				lock:     lock,
				holder:   holder,
				Deadline: deadline,
				Logf:     func(string, ...any) {},
			})
		}
	}
	return out, nil
}

// claim reports whether this replica runs the route. A replica that finds it
// claimed skips it and starts without waiting: a claim says who runs the
// route, not when it has finished (ADR 0124 §6).
func (r *StartupRunner) claim(ctx context.Context) (bool, error) {
	if r.lock == nil || r.IgnoreOnce {
		return true, nil
	}
	lctx, cancel := context.WithTimeout(r.enter(ctx), lockTimeout)
	defer cancel()
	claimed, failure, fault := r.lock.Claim(lctx, r.name, 0, r.holder, OnceHold.Milliseconds())
	if fault != nil {
		return false, fmt.Errorf("%s: claiming it faulted: %s", r.name, fault.Message)
	}
	if failure != nil {
		// Whether the route has run is unknown, and starting on that is what
		// a check exists to prevent.
		return false, fmt.Errorf("%s: it could not be claimed (%v)", r.name, failure)
	}
	if !claimed {
		r.Logf("startup %s: another replica has claimed it, so this one starts without running it", r.name)
	}
	return claimed, nil
}

// Check runs the route once. It returns nil when the process may start
// (the mapping says proceed), and the reason it may not otherwise.
func (r *StartupRunner) Check(ctx context.Context) error {
	if mine, err := r.claim(ctx); err != nil || !mine {
		return err
	}
	wctx, cancel := context.WithTimeout(r.enter(ctx), r.Deadline)
	defer cancel()
	result, fault := r.in.Call(wctx, workflowName(r.route), map[string]runtime.Value{})
	if fault != nil {
		// A fault halts, and so does running out of time (ADR 0124 §2): a
		// check that crashed or hung has not said the thing is fine.
		if wctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("%s: it did not finish within %s", r.name, r.Deadline)
		}
		return fmt.Errorf("%s: it faulted: %s", r.name, fault.Message)
	}
	for _, arm := range r.route.Mapping {
		if !runtime.MatchPattern(arm.Pattern, result, runtime.NewEnv()) {
			continue
		}
		if d, ok := arm.Value.(*syntax.DispositionResponse); ok && d.Kind == "proceed" {
			if res, isResult := result.(runtime.Result); isResult && !res.Ok {
				r.Logf("startup %s failed with %s, and its mapping lets the process start", r.name, res.Value)
			}
			return nil
		}
		return fmt.Errorf("%s: its mapping says halt, for %s", r.name, result)
	}
	return fmt.Errorf("%s: no arm of its mapping matched %s", r.name, result)
}

// RunStartup runs every route at once and waits for all of them, so one
// report names every failure (ADR 0124 §2). It returns nil when every route
// may proceed.
func RunStartup(ctx context.Context, runners []*StartupRunner) error {
	failures := make([]string, 0, len(runners))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, r := range runners {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := r.Check(ctx); err != nil {
				mu.Lock()
				failures = append(failures, err.Error())
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(failures) == 0 {
		return nil
	}
	sort.Strings(failures)
	return fmt.Errorf("refusing to start, since %d startup check(s) failed:\n  %s", len(failures), strings.Join(failures, "\n  "))
}
