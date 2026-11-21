package adapter

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"fiig-lang/compiler/runtime"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// ADR 0111 §5: a stream adapter's loop. For each route the runtime asks
// the bound source for elements, runs the route's workflow on each in
// order, matches the result against the route's mapping, and settles the
// element with the source, or halts.

const (
	sourceTimeout  = 30 * time.Second
	settleTimeout  = 10 * time.Second
	firstBackoff   = time.Second
	longestBackoff = 30 * time.Second
)

// StreamSettings is how much one receive asks a source for: at most Batch
// elements, waiting up to Wait for the first. They are process-wide,
// fiig serve's STREAM_BATCH_SIZE and STREAM_WAIT_MS (ADR 0111 open
// question 4, as amended 2026-11-21).
type StreamSettings struct {
	Batch int
	Wait  time.Duration
}

// The bounds ParseStreamSettings holds the settings to.
const (
	MaxStreamBatch = 1000
	MaxStreamWait  = time.Minute
)

// DefaultStreamSettings is 16 elements and a 2 s wait.
func DefaultStreamSettings() StreamSettings {
	return StreamSettings{Batch: 16, Wait: 2 * time.Second}
}

// ParseStreamSettings reads STREAM_BATCH_SIZE (1 to 1000) and
// STREAM_WAIT_MS (1 to 60000) through lookup, each else its default. A bad
// value is a problem for ADR 0071 §3's one report. No wait isn't one of
// them: Redis reads BLOCK 0 as waiting for ever, and a source that
// returned at once would spin.
func ParseStreamSettings(lookup func(string) (string, bool)) (StreamSettings, []string) {
	s := DefaultStreamSettings()
	var problems []string
	if v, ok := lookup("STREAM_BATCH_SIZE"); ok && v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > MaxStreamBatch {
			problems = append(problems, fmt.Sprintf("STREAM_BATCH_SIZE: %q isn't a whole number from 1 to %d", v, MaxStreamBatch))
		} else {
			s.Batch = n
		}
	}
	if v, ok := lookup("STREAM_WAIT_MS"); ok && v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > int(MaxStreamWait/time.Millisecond) {
			problems = append(problems, fmt.Sprintf("STREAM_WAIT_MS: %q isn't a whole number of milliseconds from 1 to %d", v, MaxStreamWait/time.Millisecond))
		} else {
			s.Wait = time.Duration(n) * time.Millisecond
		}
	}
	return s, problems
}

// Halt is a stream adapter stopping (ADR 0111 §4): a halt disposition, a
// retry out of attempts, a fault, or an element that doesn't decode. The
// element is left unsettled, so the next process to run the adapter
// receives it again.
type Halt struct {
	Route   string // `Adapter source "name"`
	Element string // the element's key and id
	Reason  string
}

func (h *Halt) Error() string {
	return fmt.Sprintf("stream %s halted on %s: %s", h.Route, h.Element, h.Reason)
}

// StreamRunner runs one stream route.
type StreamRunner struct {
	name    string
	route   *syntax.Route
	argName string
	argType types.Type
	in      *runtime.Interp
	source  *runtime.SourceProvider
	sub     runtime.Subscription
	enter   func(context.Context) context.Context
	timeout time.Duration
	// SettleTimeout bounds one settle (10 s unless a test sets it); with
	// the workflow's timeout it is the handleMillis the source is told
	// (ADR 0111 §5a).
	SettleTimeout time.Duration
	// Settings is each receive's batch and wait (DefaultStreamSettings
	// unless fiig serve or a test sets it).
	Settings StreamSettings
	// Logf reports what the loop can carry on past: a source that's
	// unavailable, a settle that failed.
	Logf func(format string, args ...any)
}

// Name is the route as logs and a Halt name it.
func (r *StreamRunner) Name() string { return r.name }

// Source is the route's source, so the caller can read the config its
// gateways use (ADR 0071 §3).
func (r *StreamRunner) Source() *runtime.SourceProvider { return r.source }

// StreamRunners plans every route of the stream adapters in adapters,
// each with the StreamSource bound for its package (ADR 0111 §2), and
// fails before anything runs when one has none. consumer names this
// process among the group's consumers; timeout bounds one workflow run.
func StreamRunners(adapters []Served, u *types.Universe, in *runtime.Interp, consumer string, timeout time.Duration) ([]*StreamRunner, error) {
	var out []*StreamRunner
	for _, a := range adapters {
		d := a.Decl
		if d.Kind != syntax.AdapterStream {
			return nil, fmt.Errorf("adapter %q is not a stream adapter", a.Name())
		}
		scope, enter, pkgPath := u, func(ctx context.Context) context.Context { return ctx }, ""
		if pkg := a.Package; pkg != nil {
			scope, pkgPath = pkg.Universe, pkg.Path
			enter = func(ctx context.Context) context.Context { return in.EnterPackage(ctx, pkg) }
		}
		bound, ok := in.Capabilities.LookupFor(pkgPath, runtime.StreamSourceCapability)
		source, isSource := bound.(*runtime.SourceProvider)
		if !ok || !isSource {
			return nil, fmt.Errorf(`adapter %s reads a stream, but the capabilities file binds no StreamSource; add {"StreamSource": {"type": "source", "source": "<package>"}} (ADR 0111 §2)`, a.Name())
		}
		if _, _, f := source.Resolve(); f != nil {
			return nil, fmt.Errorf("adapter %s: %s", a.Name(), f.Message)
		}
		for _, route := range d.Routes {
			plan, err := planRoute(route, false, scope, in)
			if err != nil {
				return nil, fmt.Errorf("stream route %q: %w", route.Path, err)
			}
			if len(route.Args) != 1 {
				return nil, fmt.Errorf("stream route %q binds %d arguments, not one (I3)", route.Path, len(route.Args))
			}
			arg := route.Args[0].Name
			out = append(out, &StreamRunner{
				name:    fmt.Sprintf("%s source %q", a.Name(), route.Path),
				route:   route,
				argName: arg,
				argType: plan.paramTypes[arg],
				in:      in,
				source:  source,
				// The route names its group (ADR 0111 §3a), so moving or
				// renaming the adapter keeps it.
				sub:           runtime.Subscription{Source: route.Path, Group: route.Group, Consumer: consumer, Latest: route.From == "latest"},
				enter:         enter,
				timeout:       timeout,
				SettleTimeout: settleTimeout,
				Settings:      DefaultStreamSettings(),
				Logf:          func(string, ...any) {},
			})
		}
	}
	return out, nil
}

// Run receives and handles elements until ctx is done, returning nil, or
// until the route halts, returning the *Halt. An element already being
// handled when ctx ends is finished and settled first (§5).
func (r *StreamRunner) Run(ctx context.Context) error {
	r.sub.HandleMillis = int((r.timeout + r.SettleTimeout) / time.Millisecond)
	r.sub.WaitMillis = int(r.Settings.Wait / time.Millisecond)
	backoff := firstBackoff
	for ctx.Err() == nil {
		rctx, cancel := context.WithTimeout(r.enter(ctx), sourceTimeout+r.Settings.Wait)
		deliveries, failure, fault := r.source.Receive(rctx, r.sub, r.Settings.Batch)
		cancel()
		if ctx.Err() != nil {
			return nil
		}
		if fault != nil {
			return &Halt{Route: r.name, Element: "receive", Reason: "the source faulted: " + fault.Message}
		}
		var sf *runtime.SourceFailure
		if errors.As(failure, &sf) && sf.Variant == "Misconfigured" {
			return &Halt{Route: r.name, Element: "receive", Reason: "the source is misconfigured: " + sf.Reason}
		}
		if failure != nil {
			r.Logf("stream %s: the source can't be read (%v); trying again in %s", r.name, failure, backoff)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(backoff):
			}
			backoff = min(2*backoff, longestBackoff)
			continue
		}
		backoff = firstBackoff
		// The orders with a retried element in this batch: the rest of
		// each comes again after it, so it stays unsettled (ADR 0117 §6).
		var retried map[string]bool
		for _, d := range deliveries {
			if ctx.Err() != nil {
				// Unsettled, so it comes back from a later receive.
				return nil
			}
			if retried[d.Order] {
				continue
			}
			// Finished even if ctx ends meanwhile, so shutdown doesn't
			// turn an element in hand into a fault.
			ectx := context.WithoutCancel(ctx)
			outcome, target, halt := r.handle(ectx, d)
			if halt != nil {
				return halt
			}
			sctx, cancel := context.WithTimeout(r.enter(ectx), r.SettleTimeout)
			failure, fault := r.source.Settle(sctx, r.sub, d, outcome, target)
			cancel()
			if fault != nil {
				return &Halt{Route: r.name, Element: "id " + d.ID, Reason: "settling it faulted: " + fault.Message}
			}
			if failure != nil {
				r.Logf("stream %s: settling id %s as %s failed (%v); the source will deliver it again", r.name, d.ID, outcome, failure)
			}
			if outcome == "Retry" && d.Order != "" {
				if retried == nil {
					retried = map[string]bool{}
				}
				retried[d.Order] = true
			}
		}
	}
	return nil
}

// handle runs the workflow on one element and decides its outcome:
// "Ack", "Retry", or "DeadLetter" with its target; or halts.
func (r *StreamRunner) handle(ctx context.Context, d runtime.Delivery) (string, string, *Halt) {
	element := "id " + d.ID
	halt := func(format string, args ...any) (string, string, *Halt) {
		return "", "", &Halt{Route: r.name, Element: element, Reason: fmt.Sprintf(format, args...)}
	}
	var arg runtime.Value = runtime.Bytes{B: d.Payload}
	if !isBytes(r.argType) {
		raw, err := runtime.ReadJSON(d.Payload)
		if err != nil {
			return halt("its payload isn't JSON (%v); bind the element as bytes to dead-letter payloads like it (ADR 0111 §4)", err)
		}
		decoded, err := r.in.DecodeBodyJSON(raw, r.argType)
		if err != nil {
			return halt("its payload doesn't decode into %s: %v; bind the element as bytes to dead-letter payloads like it (ADR 0111 §4)", r.argType, err)
		}
		arg = decoded
		if r.route.Key != nil {
			if rec, ok := decoded.(runtime.Record); ok {
				element = fmt.Sprintf("%s %s (id %s)", r.route.Key.Name, keyText(rec.Fields[r.route.Key.Name]), d.ID)
			}
		}
	}
	wctx, cancel := context.WithTimeout(r.enter(ctx), r.timeout)
	defer cancel()
	result, fault := r.in.Call(wctx, workflowName(r.route), map[string]runtime.Value{r.argName: arg})
	if fault != nil {
		return halt("the workflow faulted: %s", fault.Message)
	}
	for _, arm := range r.route.Mapping {
		if !runtime.MatchPattern(arm.Pattern, result, runtime.NewEnv()) {
			continue
		}
		disp, ok := arm.Value.(*syntax.DispositionResponse)
		if !ok {
			return halt("its mapping arm isn't a disposition")
		}
		if disp.Kind == "retry" {
			if d.Attempt < disp.Attempts {
				return "Retry", "", nil
			}
			if disp.Else == nil || disp.Else.Kind == "halt" {
				return halt("it still failed after %d attempts, with %s", disp.Attempts, result)
			}
			disp = disp.Else
		}
		switch disp.Kind {
		case "ack":
			return "Ack", "", nil
		case "deadLetter":
			return "DeadLetter", disp.Target, nil
		}
		return halt("its mapping says halt, for %s", result)
	}
	return halt("no arm of its mapping matched %s", result)
}

// keyText is a key field's value as a log line writes it.
func keyText(v runtime.Value) string {
	if s, ok := v.(runtime.Str); ok {
		return string(s)
	}
	if v == nil {
		return "?"
	}
	return v.String()
}
