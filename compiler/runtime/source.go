package runtime

import (
	"context"
	"fmt"
	"sync"

	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// ADR 0111: a stream source written in fiig. The capabilities file binds
// StreamSource to a package that provides fiig.stream.StreamSource, and
// a stream adapter's loop (compiler/adapter) calls its receive and settle
// gateways, and `fiig run -replay` its fetch, through this provider, as
// the driver pool calls a driver's (ADR 0084).

const (
	// StreamSourceInterface is the interface a source package provides.
	StreamSourceInterface = "fiig.stream.StreamSource"
	// StreamSourceCapability is the name a capabilities file binds.
	StreamSourceCapability = "StreamSource"
	streamPackage          = "fiig.stream"
)

// SourceFactory is the "source" capability type (§2):
// {"type": "source", "source": "<package path>"}.
func SourceFactory(cfg map[string]any) (CapabilityProvider, error) {
	path, _ := cfg["source"].(string)
	if path == "" {
		return nil, fmt.Errorf(`source config requires "source", the package path of a stream source (ADR 0111 §2)`)
	}
	for k := range cfg {
		if k != "type" && k != "source" {
			return nil, fmt.Errorf("source config has no setting %q; a source reads its own settings from its config declarations (ADR 0111 §2)", k)
		}
	}
	return &SourceProvider{path: path}, nil
}

// SourceProvider is one bound stream source.
type SourceProvider struct {
	path string
	in   *Interp

	once     sync.Once
	err      *Fault
	pkg      *types.Package
	group    *syntax.CapabilityDecl
	sc       scope
	sub      *syntax.ModelDecl
	delivery *syntax.ModelDecl
	outcome  *syntax.EnumDecl
	startAt  *syntax.EnumDecl
}

func (p *SourceProvider) bindInterp(in *Interp) { p.in = in }

// Path is the source package's path.
func (p *SourceProvider) Path() string { return p.path }

// Call refuses: programs don't call a source, an adapter's loop does.
func (p *SourceProvider) Call(context.Context, string, []Value) (Value, *Fault) {
	return nil, Faultf("StreamSource is called by stream adapters, not by a program (ADR 0111 §1)")
}

// Resolve finds the source's capability group and fiig.stream's types,
// once, returning the package and the group's name so the caller can read
// the config its gateways use.
func (p *SourceProvider) Resolve() (*types.Package, string, *Fault) {
	p.once.Do(func() {
		if p.in == nil {
			p.err = Faultf("StreamSource: source %s has no interpreter", p.path)
			return
		}
		pkg, ok := p.in.Packages[p.path]
		if !ok {
			p.err = Faultf("StreamSource: package %s isn't part of this program; add it to the application manifest's dependencies (ADR 0111 §2)", p.path)
			return
		}
		name := pkg.Provides[StreamSourceInterface]
		d, found := pkg.Universe.Lookup(name)
		group, isGroup := d.(*syntax.CapabilityDecl)
		if name == "" || !found || !isGroup {
			p.err = Faultf("StreamSource: package %s doesn't provide %s; its manifest names no capability group for it (ADR 0111 §1)", p.path, StreamSourceInterface)
			return
		}
		streamPkg := pkg.Dependencies[streamPackage]
		if streamPkg == nil {
			streamPkg = p.in.Packages[streamPackage]
		}
		if streamPkg == nil {
			p.err = Faultf("StreamSource: package %s doesn't depend on %s", p.path, streamPackage)
			return
		}
		lookup := func(n string) syntax.Decl { d, _ := streamPkg.Universe.Lookup(n); return d }
		p.sub, _ = lookup("Subscription").(*syntax.ModelDecl)
		p.delivery, _ = lookup("Delivery").(*syntax.ModelDecl)
		p.outcome, _ = lookup("Outcome").(*syntax.EnumDecl)
		p.startAt, _ = lookup("StartAt").(*syntax.EnumDecl)
		p.pkg, p.group, p.sc = pkg, group, packageScope(pkg)
	})
	if p.err != nil {
		return nil, "", p.err
	}
	return p.pkg, p.group.Name, nil
}

// Subscription is fiig.stream.Subscription (§1).
type Subscription struct {
	Source, Group, Consumer string
	WaitMillis              int
	// Latest is a new group starting after the source's last element,
	// rather than at its first (§3a).
	Latest bool
	// HandleMillis is the longest one element may take, its workflow and
	// its settle together (§5a).
	HandleMillis int
}

// Delivery is one element a receive returned (§1). value is the record as
// the source built it, handed back to settle unchanged. Order is the
// sequence it belongs to, or "" (ADR 0117 §6).
type Delivery struct {
	ID      string
	Payload []byte
	Attempt int
	Order   string
	value   Value
}

// SourceFailure is an Err(SourceError...) from receive or settle: a value
// the loop logs and waits out, not a fault.
type SourceFailure struct{ Variant, Reason string }

func (f *SourceFailure) Error() string { return f.Variant + ": " + f.Reason }

func (p *SourceProvider) subscription(s Subscription) Value {
	start := "Earliest"
	if s.Latest {
		start = "Latest"
	}
	return Record{TypeName: "Subscription", Decl: p.sub, Fields: map[string]Value{
		"source": Str(s.Source), "group": Str(s.Group), "consumer": Str(s.Consumer), "waitMillis": Int(s.WaitMillis),
		"start":        EnumValue{TypeName: "StartAt", Variant: start, Decl: p.startAt},
		"handleMillis": Int(s.HandleMillis),
	}}
}

// gateway calls one of the source's gateways as its own top-level call:
// sockets it opens and doesn't keep close when it returns (ADR 0060 §5).
func (p *SourceProvider) gateway(ctx context.Context, name string, args ...Value) (Value, *Fault) {
	if _, _, f := p.Resolve(); f != nil {
		return nil, f
	}
	gw := p.group.Gateway(name)
	if gw == nil {
		return nil, Faultf("StreamSource: source %s has no gateway %q", p.path, name)
	}
	if len(gw.Params) != len(args) {
		return nil, Faultf("StreamSource: source %s's %s takes %d parameters, not %d", p.path, name, len(gw.Params), len(args))
	}
	named := make(map[string]Value, len(args))
	for i, prm := range gw.Params {
		named[prm.Name] = args[i]
	}
	ctx, end := withResourceOwner(context.WithValue(ctx, resourceOwnerKey{}, nil))
	defer end()
	return p.in.callDecl(ctx, p.sc, p.group.Name+"."+name, gw, named)
}

// Receive calls the source's receive (§1).
func (p *SourceProvider) Receive(ctx context.Context, s Subscription, limit int) ([]Delivery, error, *Fault) {
	out, f := p.gateway(ctx, "receive", p.subscription(s), Int(limit))
	if f != nil {
		return nil, nil, f
	}
	list, failure, f := p.result("receive", out)
	if f != nil || failure != nil {
		return nil, failure, f
	}
	l, ok := list.(List)
	if !ok {
		return nil, nil, Faultf("StreamSource: %s's receive returned %s, not a list of deliveries", p.path, list)
	}
	deliveries := make([]Delivery, 0, len(l.Elems))
	for _, e := range l.Elems {
		d, f := p.readDelivery("receive", e)
		if f != nil {
			return nil, nil, f
		}
		deliveries = append(deliveries, d)
	}
	return deliveries, nil, nil
}

// Fetch calls the source's fetch (open question 1, as amended
// 2026-11-21): one element by its id, or nil when the source no longer
// has it. Nothing is received or settled, so `fiig run -replay` can run
// the route's workflow on it and only report what the mapping chose.
func (p *SourceProvider) Fetch(ctx context.Context, s Subscription, id string) (*Delivery, error, *Fault) {
	out, f := p.gateway(ctx, "fetch", p.subscription(s), Str(id))
	if f != nil {
		return nil, nil, f
	}
	v, failure, f := p.result("fetch", out)
	if f != nil || failure != nil {
		return nil, failure, f
	}
	if _, none := v.(None); none {
		return nil, nil, nil
	}
	d, f := p.readDelivery("fetch", v)
	if f != nil {
		return nil, nil, f
	}
	return &d, nil, nil
}

// readDelivery reads one fiig.stream.Delivery a gateway returned.
func (p *SourceProvider) readDelivery(op string, e Value) (Delivery, *Fault) {
	rec, ok := e.(Record)
	id, okID := rec.Fields["id"].(Str)
	payload, okPayload := rec.Fields["payload"].(Bytes)
	attempt, okAttempt := rec.Fields["attempt"].(Int)
	order, okOrder := rec.Fields["order"].(Str)
	if !ok || !okID || !okPayload || !okAttempt || !okOrder {
		return Delivery{}, Faultf("StreamSource: %s's %s returned %s, not a Delivery", p.path, op, e)
	}
	return Delivery{ID: string(id), Payload: payload.B, Attempt: int(attempt), Order: string(order), value: rec}, nil
}

// Settle reports one element's outcome (§1): "Ack", "Retry", or
// "DeadLetter" with target.
func (p *SourceProvider) Settle(ctx context.Context, s Subscription, d Delivery, outcome, target string) (error, *Fault) {
	o := EnumValue{TypeName: "Outcome", Variant: outcome, Decl: p.outcome}
	if outcome == "DeadLetter" {
		o.Fields = []Value{Str(target)}
	}
	out, f := p.gateway(ctx, "settle", p.subscription(s), d.value, o)
	if f != nil {
		return nil, f
	}
	_, failure, f := p.result("settle", out)
	return failure, f
}

// result unwraps a gateway's Result<T, SourceError>.
func (p *SourceProvider) result(op string, out Value) (Value, error, *Fault) {
	r, ok := out.(Result)
	if !ok {
		return nil, nil, Faultf("StreamSource: %s's %s returned %s, not a Result", p.path, op, out)
	}
	if r.Ok {
		return r.Value, nil, nil
	}
	ev, ok := r.Value.(EnumValue)
	if !ok {
		return nil, nil, Faultf("StreamSource: %s's %s failed with %s, not a SourceError", p.path, op, r.Value)
	}
	reason := ""
	if len(ev.Fields) == 1 {
		if s, ok := ev.Fields[0].(Str); ok {
			reason = string(s)
		}
	}
	return nil, &SourceFailure{Variant: ev.Variant, Reason: reason}, nil
}
