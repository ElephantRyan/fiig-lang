package servecmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"fiig-lang/compiler/adapter"
	"fiig-lang/compiler/runtime"
	"fiig-lang/compiler/syntax"
	"fiig-lang/tooling/migrate"
	"fiig-lang/tooling/runprogram"
)

// ReplayOptions is what `fiig run -replay` was given (ADR 0111 open
// question 1, as amended 2026-11-21; ADR 0047 §6): the stream adapter, the
// element's id, and, when the adapter has more than one route, the
// source of the one to replay.
type ReplayOptions struct {
	Root, Adapter, ID, Source string
	CapsPath                  string
	Workspaces                []string
	Factories                 runtime.FactoryRegistry
	Env                       *runprogram.Environment
	Migrate                   migrate.Mode
	// Timeout bounds the workflow's run, as REQUEST_TIMEOUT does under
	// fiig serve.
	Timeout time.Duration
	// Tracer, when set, receives the workflow's spans.
	Tracer runtime.Tracer
}

// Replayed is what a replay found: the element as the source has it, and
// what the route's mapping chose for it. Nothing was settled.
type Replayed struct {
	Route       string
	Delivery    runtime.Delivery
	Disposition adapter.Disposition
}

// Replay loads root as fiig serve does, fetches one element of a stream
// adapter's route by its id, and runs the route's workflow on it, but
// settles nothing: replay is for investigating, and the operator who
// wants the element acked, retried, or moved settles it by other means.
// The workflow's own effects happen, as they would under fiig serve.
func Replay(ctx context.Context, o ReplayOptions) (*Replayed, error) {
	p, err := check(o.Root, []string{o.Adapter}, o.Workspaces, "replay")
	if err != nil {
		return nil, err
	}
	served := p.served[0]
	if served.Decl.Kind != syntax.AdapterStream {
		return nil, fmt.Errorf("adapter %s isn't a stream adapter, so it has no element to replay; `fiig run <root> <workflow>` runs a workflow directly", o.Adapter)
	}
	caps, problems, err := runprogram.LoadCapabilitiesFileWith(o.CapsPath, o.Factories, o.Env.Lookup)
	if err != nil {
		return nil, fmt.Errorf("-caps: %w", err)
	}
	if caps == nil {
		caps = runtime.NewCapabilityRegistry()
	}
	defer caps.Close()
	problems = append(problems, runprogram.UnknownPackages(caps.Packages(), p.packages)...)
	runprogram.RegisterDefaults(caps)
	in := runtime.New(p.universe, caps)
	runprogram.UseConcurrency(in, o.Root)
	in.Imports = runprogram.BuildImports(p.files, p.packages)
	in.Groups = runprogram.BuildGroups(p.files, p.packages)
	in.Packages = p.packages
	in.Tracer = o.Tracer
	runners, err := adapter.StreamRunners([]adapter.Served{served}, p.universe, in, consumerName(), o.Timeout)
	if err != nil {
		return nil, err
	}
	runner, err := pickRoute(runners, o.Adapter, o.Source)
	if err != nil {
		return nil, err
	}
	pkg, group, _ := runner.Source().Resolve()
	program := runprogram.Program{Universe: p.universe, Imports: in.Imports, Groups: in.Groups, Renames: runprogram.Renames(o.Root)}
	entries := []runprogram.Entry{{Package: served.Package, Name: served.Decl.Name}, {Package: pkg, Name: group}}
	sources := runprogram.ReachableConfigFrom(program, entries)
	problems = append(problems, runprogram.UseDatabaseSettings(ctx, in, caps, sources)...)
	problems = append(problems, in.LoadConfig(sources, o.Env.Lookup)...)
	if err := runprogram.ConfigReport(problems); err != nil {
		return nil, err
	}
	if err := runprogram.Migrate(ctx, o.Root, p.packages, caps, o.Migrate); err != nil {
		return nil, err
	}
	d, err := runner.Fetch(ctx, o.ID)
	if err != nil {
		return nil, err
	}
	return &Replayed{Route: runner.Name(), Delivery: d, Disposition: runner.Decide(ctx, d)}, nil
}

// pickRoute is the route source names, or the adapter's only one.
func pickRoute(runners []*adapter.StreamRunner, name, source string) (*adapter.StreamRunner, error) {
	var sources []string
	for _, r := range runners {
		if (source == "" && len(runners) == 1) || r.SourceName() == source {
			return r, nil
		}
		sources = append(sources, fmt.Sprintf("%q", r.SourceName()))
	}
	if source == "" {
		return nil, fmt.Errorf("adapter %s reads %d sources (%s); -source names the one to replay from", name, len(runners), strings.Join(sources, ", "))
	}
	return nil, fmt.Errorf("adapter %s has no route reading source %q; it reads %s", name, source, strings.Join(sources, ", "))
}

// Describe is the disposition as a line, in the mapping's own words (ack,
// retry, deadLetter, halt), and why.
func (r *Replayed) Describe() string {
	d := r.Disposition
	head := fmt.Sprintf("stream %s, id %s (attempt %d): ", r.Route, r.Delivery.ID, r.Delivery.Attempt)
	switch {
	case d.Halt != nil:
		return head + "halt: " + d.Halt.Reason
	case d.Outcome == "Retry":
		return head + fmt.Sprintf("retry, attempt %d of %d, for %s", r.Delivery.Attempt, d.Attempts, d.Result)
	case d.Outcome == "DeadLetter" && d.Attempts > 0:
		return head + fmt.Sprintf("deadLetter %q, its %d attempts used, for %s", d.Target, d.Attempts, d.Result)
	case d.Outcome == "DeadLetter":
		return head + fmt.Sprintf("deadLetter %q, for %s", d.Target, d.Result)
	}
	return head + fmt.Sprintf("ack, for %s", d.Result)
}

// JSON is the replay as `fiig run -json -replay` prints it.
func (r *Replayed) JSON() map[string]any {
	d := r.Disposition
	out := map[string]any{"route": r.Route, "id": r.Delivery.ID, "attempt": r.Delivery.Attempt, "settled": false}
	switch {
	case d.Halt != nil:
		out["disposition"] = "halt"
		out["reason"] = d.Halt.Reason
	case d.Outcome == "Retry":
		out["disposition"] = "retry"
	case d.Outcome == "DeadLetter":
		out["disposition"] = "deadLetter"
		out["target"] = d.Target
	default:
		out["disposition"] = "ack"
	}
	if d.Attempts > 0 {
		out["attempts"] = d.Attempts
	}
	if d.Result != nil {
		out["result"] = runtime.EncodeJSON(d.Result)
	}
	return out
}
