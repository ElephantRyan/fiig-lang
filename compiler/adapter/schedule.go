package adapter

import (
	"context"
	"fmt"
	"math/rand/v2"
	"strconv"
	"time"

	"fiig-lang/compiler/logging"
	"fiig-lang/compiler/runtime"
	"fiig-lang/compiler/schedule"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// ADR 0112 §2-§4: a schedule adapter's loop. For each route the runtime
// waits for the next firing, claims it with the bound ScheduleLock, runs
// the route's workflow with the next firing as its deadline, and logs an
// Err the mapping sends to done, or halts.

const (
	lockTimeout  = 10 * time.Second
	minClaimHold = time.Minute
	// MaxScheduleJitter is the most SCHEDULE_JITTER_MS may be: five
	// minutes.
	MaxScheduleJitter = 5 * time.Minute
)

// ParseScheduleJitter reads SCHEDULE_JITTER_MS through lookup (ADR 0112
// open question 3, as amended 2026-11-21): 0, the default, for none, up
// to 300000. A bad value is a problem for ADR 0071 §3's one report.
func ParseScheduleJitter(lookup func(string) (string, bool)) (time.Duration, []string) {
	v, ok := lookup("SCHEDULE_JITTER_MS")
	if !ok || v == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 || n > int(MaxScheduleJitter/time.Millisecond) {
		return 0, []string{fmt.Sprintf("SCHEDULE_JITTER_MS: %q isn't a whole number of milliseconds from 0 to %d", v, MaxScheduleJitter/time.Millisecond)}
	}
	return time.Duration(n) * time.Millisecond, nil
}

// ScheduleRunner runs one schedule route.
type ScheduleRunner struct {
	name    string // the job: `Maintenance every "15m"`
	route   *syntax.Route
	sched   schedule.Schedule
	argName string // the firing-time parameter, "" when the workflow takes none
	in      *runtime.Interp
	lock    runtime.ScheduleLock
	holder  string
	enter   func(context.Context) context.Context
	// Now and Wait are the clock: the real one unless a test sets them.
	// Wait returns false when ctx ends first.
	Now  func() time.Time
	Wait func(ctx context.Context, d time.Duration) bool
	// Jitter is the most each firing waits, at random, before it is
	// claimed, so replicas and services firing at the same moment spread
	// out (ADR 0112 open question 3, as amended 2026-11-21); 0 is none.
	// Rand(n) picks the wait in [0, n): math/rand unless a test sets it.
	Jitter time.Duration
	Rand   func(n int64) int64
	// Logf reports a failed job, an overrun, and a claim that failed. Log,
	// when set, is used instead, with each line's level and fields (ADR
	// 0150).
	Logf func(format string, args ...any)
	Log  *logging.Logger
}

func (r *ScheduleRunner) lg() *logging.Logger { return pickLogger(r.Log, r.Logf) }

// Name is the job's name, which its lock claims are keyed by.
func (r *ScheduleRunner) Name() string { return r.name }

// Lock is the route's lock, so the caller can read the config a lock
// written in fiig uses.
func (r *ScheduleRunner) Lock() runtime.ScheduleLock { return r.lock }

// ScheduleRunners plans every route of the schedule adapters in adapters,
// each with the ScheduleLock bound for its package (ADR 0112 §4), and
// fails before anything runs when one has none. holder names this process
// in its claims.
func ScheduleRunners(adapters []Served, u *types.Universe, in *runtime.Interp, holder string) ([]*ScheduleRunner, error) {
	var out []*ScheduleRunner
	for _, a := range adapters {
		d := a.Decl
		if d.Kind != syntax.AdapterSchedule {
			return nil, fmt.Errorf("adapter %q is not a schedule adapter", a.Name())
		}
		scope, enter, pkgPath := u, func(ctx context.Context) context.Context { return ctx }, ""
		if pkg := a.Package; pkg != nil {
			scope, pkgPath = pkg.Universe, pkg.Path
			enter = func(ctx context.Context) context.Context { return in.EnterPackage(ctx, pkg) }
		}
		bound, ok := in.Capabilities.LookupFor(pkgPath, runtime.ScheduleLockCapability)
		lock, isLock := bound.(runtime.ScheduleLock)
		if !ok || !isLock {
			return nil, fmt.Errorf(`adapter %s runs on a schedule, but the capabilities file binds no ScheduleLock, so every replica would run each job; add {"ScheduleLock": {"type": "lock", "lock": "fiig.redis"}}, or {"ScheduleLock": {"type": "local"}} when one process runs the schedule (ADR 0112 §4)`, a.Name())
		}
		if p, isFiig := lock.(*runtime.LockProvider); isFiig {
			if _, _, f := p.Resolve(); f != nil {
				return nil, fmt.Errorf("adapter %s: %s", a.Name(), f.Message)
			}
		}
		for _, route := range d.Routes {
			sched, err := schedule.Parse(route.Verb, route.Path)
			if err != nil {
				return nil, fmt.Errorf("schedule route %s %q: %w", route.Verb, route.Path, err)
			}
			if _, err := routeWorkflow(route, scope); err != nil {
				return nil, err
			}
			argName := ""
			if len(route.Args) == 1 {
				argName = route.Args[0].Name
			}
			out = append(out, &ScheduleRunner{
				name:    fmt.Sprintf("%s %s %q", a.Name(), route.Verb, route.Path),
				route:   route,
				sched:   sched,
				argName: argName,
				in:      in,
				lock:    lock,
				holder:  holder,
				enter:   enter,
				Now:     time.Now,
				Wait:    realWait,
				Rand:    rand.Int64N,
				Logf:    func(string, ...any) {},
			})
		}
	}
	return out, nil
}

func realWait(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// Run fires the job until ctx is done, returning nil, or until it halts,
// returning the *Halt. A run in progress when ctx ends is finished first,
// within its deadline. A firing missed while nothing ran is skipped (§3).
func (r *ScheduleRunner) Run(ctx context.Context) error {
	for ctx.Err() == nil {
		fire := r.sched.Next(r.Now())
		if fire.IsZero() {
			return nil
		}
		if !r.Wait(ctx, fire.Sub(r.Now())) {
			return nil
		}
		// Shutdown during the jitter runs nothing.
		if d := r.jitter(fire); d > 0 && !r.Wait(ctx, d) {
			return nil
		}
		if halt := r.fire(ctx, fire); halt != nil {
			return halt
		}
	}
	return nil
}

// jitter is how long the firing at fire waits before it is claimed: a
// random part of Jitter, but never as long as the time to the next
// firing, so a short schedule still runs every firing.
func (r *ScheduleRunner) jitter(fire time.Time) time.Duration {
	limit := r.Jitter
	if after := r.sched.Next(fire); !after.IsZero() {
		limit = min(limit, after.Sub(fire))
	}
	if limit <= 0 {
		return 0
	}
	return time.Duration(r.Rand(int64(limit)))
}

// fire claims and runs the firing at fire. The workflow's firing time is
// fire, the scheduled time, however long the jitter waited.
func (r *ScheduleRunner) fire(ctx context.Context, fire time.Time) *Halt {
	after := r.sched.Next(fire)
	element := "the firing at " + fire.UTC().Format(time.RFC3339)
	log := r.lg()
	fields := []logging.Field{logging.F("route", r.name), logging.F("elementId", fire.UTC().Format(time.RFC3339))}
	lctx, cancel := context.WithTimeout(r.enter(ctx), lockTimeout)
	// Held to the next firing, and never less than a minute, so a replica
	// whose clock runs late by less than that can't claim a firing that
	// has already run once its claim expired (ADR 0112 §4).
	hold := max(after.Sub(fire), minClaimHold)
	claimed, failure, fault := r.lock.Claim(lctx, r.name, fire.UnixMilli(), r.holder, hold.Milliseconds())
	cancel()
	if fault != nil {
		return &Halt{Route: r.name, Element: element, Reason: "claiming it faulted: " + fault.Message}
	}
	if failure != nil {
		log.Logf(logging.Warn, logging.EventSchedule, fields, "schedule %s: skipping %s, since it couldn't be claimed (%v)", r.name, element, failure)
		return nil
	}
	if !claimed {
		return nil
	}
	args := map[string]runtime.Value{}
	if r.argName != "" {
		args[r.argName] = runtime.Int(fire.UnixMilli())
	}
	// Finished even if ctx ends meanwhile, but never past the next firing.
	wctx, cancelRun := context.WithTimeout(r.enter(context.WithoutCancel(ctx)), after.Sub(r.Now()))
	defer cancelRun()
	// ADR 0150 §4: a firing that failed writes its failing calls.
	var c *calls
	what := "schedule " + r.name + " " + element
	if CountsCalls(log) {
		wctx, c = elementCalls(wctx, log, what, fields)
	}
	failedCalls := func() {
		if c != nil {
			c.writeFailed(log, what, fields)
		}
	}
	result, fault := r.in.Call(wctx, workflowName(r.route), args)
	if fault != nil {
		failedCalls()
		if wctx.Err() == context.DeadlineExceeded {
			log.Logf(logging.Error, logging.EventSchedule, fields, "schedule %s: %s overran into the next firing, so it was stopped (ADR 0112 §3)", r.name, element)
			return nil
		}
		// A fault fails this firing only (ADR 0112 §2, as amended): the
		// next firing is the retry, and only the mapping's halt stops the
		// process.
		log.Logf(logging.Error, logging.EventSchedule, append(fields, logging.F("detail", fault.Message)), "schedule %s: %s faulted, so it was skipped: %s", r.name, element, fault.Message)
		return nil
	}
	for _, arm := range r.route.Mapping {
		if !runtime.MatchPattern(arm.Pattern, result, runtime.NewEnv()) {
			continue
		}
		if d, ok := arm.Value.(*syntax.DispositionResponse); ok && d.Kind == "done" {
			if res, isResult := result.(runtime.Result); isResult && !res.Ok {
				failedCalls()
				log.Logf(logging.Warn, logging.EventSchedule, append(fields, logging.F("detail", res.Value.String())), "schedule %s: %s failed with %s", r.name, element, res.Value)
			}
			return nil
		}
		return &Halt{Route: r.name, Element: element, Reason: fmt.Sprintf("its mapping says halt, for %s", result)}
	}
	return &Halt{Route: r.name, Element: element, Reason: fmt.Sprintf("no arm of its mapping matched %s", result)}
}
