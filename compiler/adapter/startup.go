package adapter

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"fiig-lang/compiler/logging"
	"fiig-lang/compiler/runtime"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// ADR 0124 §2: a startup adapter's checks. Before anything listens, every
// route's workflow runs at once, each with a deadline, and the process
// starts only when none says halt, faults, or runs out of time.

// StartupDeadline is how long one startup route may run (ADR 0124 §2).
const StartupDeadline = 30 * time.Second

const (
	// OnceSlack is added to a `once` route's deadline for how long its claim
	// is held (ADR 0124 §6): past it, a claimant that died without finishing
	// is presumed gone, and a waiting replica claims the route next.
	OnceSlack = 30 * time.Second
	// OnceRetain is how long a finished `once` route is remembered for its
	// deploy. A later replica of the same deploy finds it done.
	OnceRetain = 7 * 24 * time.Hour
	// OncePoll is how often a replica that lost the claim asks whether the
	// winner has finished.
	OncePoll = time.Second
)

// StartupRunner runs one startup route.
type StartupRunner struct {
	name  string // `Preflight run -> verifyStorage`
	route *syntax.Route
	// pkgPath and key identify the route to a config field that reads it
	// (ADR 0127): the package ("" for the application) and `Adapter.workflow`.
	pkgPath, key string
	degraded     bool
	in           *runtime.Interp
	enter        func(context.Context) context.Context
	lock         runtime.ScheduleLock // set for a `once` route
	// holder names this process in a `once` claim, and deploy names the
	// deploy the claim is for: a route runs once per deploy, so a new deploy
	// runs it again (ADR 0124 §6).
	holder string
	deploy int64
	// Deadline is how long the route may run: its `within`, else
	// StartupDeadline, unless a test shortens it.
	Deadline time.Duration
	// IgnoreOnce runs a `once` route without claiming it, as `fiig serve
	// -check` does: a check run is one process asking whether it could start.
	IgnoreOnce bool
	// Slack and Poll are OnceSlack and OncePoll unless a test shortens them;
	// Wait is how a replica waits between polls, false when ctx ends first.
	Slack time.Duration
	Poll  time.Duration
	Wait  func(ctx context.Context, d time.Duration) bool
	// Logf reports a failed check the mapping lets the process start after,
	// and a `once` route another replica ran or is running.
	Logf func(format string, args ...any)
	// Log, when set, is used instead of Logf (ADR 0150).
	Log *logging.Logger
}

// Name is the route's name, as the report of a failure gives it.
func (r *StartupRunner) Name() string { return r.name }

// Degraded reports whether the route ran and its mapping said `degrade`: the
// process starts, and a config field that reads the route is true (ADR 0127).
// It is read after RunStartup has returned.
func (r *StartupRunner) Degraded() bool { return r.degraded }

// Route is the route as a config field names it, `Adapter.workflow`, with the
// path of its package ("" for the application).
func (r *StartupRunner) Route() (pkgPath, key string) { return r.pkgPath, r.key }

// Lock is the route's claim lock, nil unless the route says `once`, so the
// caller can read the config a lock written in fiig uses.
func (r *StartupRunner) Lock() runtime.ScheduleLock { return r.lock }

// StartupRunners plans every route of the startup adapters in adapters. A
// `once` route is claimed with the ScheduleLock bound for its package, and
// fails here, before anything runs, when none is bound. holder names this
// process in its claims, and deploy names the deploy they are for.
func StartupRunners(adapters []Served, u *types.Universe, in *runtime.Interp, holder string, deploy int64) ([]*StartupRunner, error) {
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
					return nil, fmt.Errorf(`adapter %s has a startup route that says once, but the capabilities file binds no ScheduleLock to claim it with; add {"ScheduleLock": {"type": "lock", "lock": "com.degeneratedevelopment.redis"}}, or {"ScheduleLock": {"type": "local"}} when one process runs it (ADR 0124 §6)`, a.Name())
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
				pkgPath:  pkgPath,
				key:      d.Name + "." + workflowName(route),
				in:       in,
				enter:    enter,
				lock:     lock,
				holder:   holder,
				deploy:   deploy,
				Deadline: deadline,
				Slack:    OnceSlack,
				Poll:     OncePoll,
				Wait:     realWait,
				Logf:     func(string, ...any) {},
			})
		}
	}
	return out, nil
}

// once reports whether the route is claimed, and so run only once per deploy.
func (r *StartupRunner) once() bool { return r.lock != nil && !r.IgnoreOnce }

// lockCall runs one call of the route's lock under the lock's own timeout,
// turning a fault or an Unavailable failure into the error that refuses the
// start: whether the route has run is unknown, and starting on that is what
// a check exists to prevent.
func (r *StartupRunner) lockCall(ctx context.Context, what string, call func(context.Context) (bool, error, *runtime.Fault)) (bool, error) {
	lctx, cancel := context.WithTimeout(r.enter(ctx), lockTimeout)
	defer cancel()
	ok, failure, fault := call(lctx)
	if fault != nil {
		return false, fmt.Errorf("%s: %s faulted: %s", r.name, what, fault.Message)
	}
	if failure != nil {
		return false, fmt.Errorf("%s: it could not be %s (%v)", r.name, what, failure)
	}
	return ok, nil
}

// claim reports whether this replica runs the route. A replica that loses
// the claim waits for the winner to finish it, and claims it again if the
// winner's claim lapses without that: it died, and somebody must run it.
// The wait is as long as a winner needs, its deadline and the claim's slack,
// twice over, so a lapsed claim's successor has its own turn (ADR 0124 §6).
func (r *StartupRunner) claim(ctx context.Context) (bool, error) {
	hold := r.Deadline + r.Slack
	wctx, cancel := context.WithTimeout(ctx, hold+hold)
	defer cancel()
	for {
		mine, err := r.lockCall(wctx, "claimed", func(c context.Context) (bool, error, *runtime.Fault) {
			return r.lock.Claim(c, r.name, r.deploy, r.holder, hold.Milliseconds())
		})
		if err != nil || mine {
			return mine, err
		}
		done, err := r.lockCall(wctx, "asked about", func(c context.Context) (bool, error, *runtime.Fault) {
			return r.lock.Completed(c, r.name, r.deploy)
		})
		if err != nil {
			return false, err
		}
		if done {
			pickLogger(r.Log, r.Logf).Logf(logging.Info, logging.EventStartup, []logging.Field{logging.F("route", r.name)}, "startup %s: another replica has run it for this deploy, so this one starts without running it", r.name)
			return false, nil
		}
		if !r.Wait(wctx, r.Poll) {
			return false, fmt.Errorf("%s: another replica claimed it, and it was not finished within %s", r.name, hold+hold)
		}
	}
}

// complete records that this replica ran the route and its mapping said to
// proceed, so the replicas waiting on it may start. A failure to record it
// is logged and not fatal: this replica's check passed, and the others wait
// out their time and then run it themselves.
func (r *StartupRunner) complete(ctx context.Context) {
	lctx, cancel := context.WithTimeout(r.enter(ctx), lockTimeout)
	defer cancel()
	failure, fault := r.lock.Complete(lctx, r.name, r.deploy, r.holder, OnceRetain.Milliseconds())
	switch {
	case fault != nil:
		pickLogger(r.Log, r.Logf).Logf(logging.Warn, logging.EventStartup, []logging.Field{logging.F("route", r.name)}, "startup %s: it passed, but recording that faulted: %s", r.name, fault.Message)
	case failure != nil:
		pickLogger(r.Log, r.Logf).Logf(logging.Warn, logging.EventStartup, []logging.Field{logging.F("route", r.name)}, "startup %s: it passed, but recording that failed: %v", r.name, failure)
	}
}

// Check runs the route once. It returns nil when the process may start
// (the mapping says proceed), and the reason it may not otherwise. A `once`
// route is run by the replica that claims it for the deploy; the others wait
// for that replica to finish.
func (r *StartupRunner) Check(ctx context.Context) error {
	if !r.once() {
		return r.run(ctx)
	}
	if mine, err := r.claim(ctx); err != nil || !mine {
		return err
	}
	if err := r.run(ctx); err != nil {
		return err
	}
	// A degraded route is not recorded as done: the replicas waiting on it
	// run it themselves, each finding out for itself (ADR 0127 §3).
	if !r.degraded {
		r.complete(ctx)
	}
	return nil
}

// run runs the route's workflow under its deadline and applies its mapping.
func (r *StartupRunner) run(ctx context.Context) error {
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
		if d, ok := arm.Value.(*syntax.DispositionResponse); ok && d.Kind == "degrade" {
			r.degraded = true
			pickLogger(r.Log, r.Logf).Logf(logging.Warn, logging.EventStartup, []logging.Field{logging.F("route", r.name)}, "startup %s: starting degraded, for %s", r.name, result)
			return nil
		}
		if d, ok := arm.Value.(*syntax.DispositionResponse); ok && d.Kind == "proceed" {
			if res, isResult := result.(runtime.Result); isResult && !res.Ok {
				pickLogger(r.Log, r.Logf).Logf(logging.Warn, logging.EventStartup, []logging.Field{logging.F("route", r.name)}, "startup %s failed with %s, and its mapping lets the process start", r.name, res.Value)
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
