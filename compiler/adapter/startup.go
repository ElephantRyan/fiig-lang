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

// StartupRunner runs one startup route.
type StartupRunner struct {
	name  string // `Preflight run -> verifyStorage`
	route *syntax.Route
	in    *runtime.Interp
	enter func(context.Context) context.Context
	// Deadline is how long the route may run; StartupDeadline unless a test
	// shortens it.
	Deadline time.Duration
	// Logf reports a failed check the mapping lets the process start after.
	Logf func(format string, args ...any)
}

// Name is the route's name, as the report of a failure gives it.
func (r *StartupRunner) Name() string { return r.name }

// StartupRunners plans every route of the startup adapters in adapters.
func StartupRunners(adapters []Served, u *types.Universe, in *runtime.Interp) ([]*StartupRunner, error) {
	var out []*StartupRunner
	for _, a := range adapters {
		d := a.Decl
		if d.Kind != syntax.AdapterStartup {
			return nil, fmt.Errorf("adapter %q is not a startup adapter", a.Name())
		}
		scope, enter := u, func(ctx context.Context) context.Context { return ctx }
		if pkg := a.Package; pkg != nil {
			scope = pkg.Universe
			enter = func(ctx context.Context) context.Context { return in.EnterPackage(ctx, pkg) }
		}
		for _, route := range d.Routes {
			if _, err := routeWorkflow(route, scope); err != nil {
				return nil, err
			}
			out = append(out, &StartupRunner{
				name:     fmt.Sprintf("%s run -> %s", a.Name(), workflowName(route)),
				route:    route,
				in:       in,
				enter:    enter,
				Deadline: StartupDeadline,
				Logf:     func(string, ...any) {},
			})
		}
	}
	return out, nil
}

// Check runs the route once. It returns nil when the process may start
// (the mapping says proceed), and the reason it may not otherwise.
func (r *StartupRunner) Check(ctx context.Context) error {
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
