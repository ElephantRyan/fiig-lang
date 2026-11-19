package runtime

import (
	"context"
	"sort"
)

// Role, per ADR 0042: the native floor is the closed primitive set in
// compiler/capability (Socket, Clock, FileSystem, Random; ADR 0060 folded
// TlsSocket into Socket). A CapabilityProvider written in Go is the
// mechanism for those; a domain capability (Database, PaymentGateway, ...)
// is still registered this way
// only while it sits on a manifest's nativeCapabilities grandfather list,
// until a fiig fulfiller replaces it (ADR 0043).
//
// CapabilityProvider answers calls against one named capability
// (`Database.queryOne(...)` dispatches to the "Database" provider's
// Call(ctx, "queryOne", args)). ctx carries whatever deadline/cancellation
// the enclosing Interp.Call was given (interp.go) — a real provider doing
// actual I/O (libraries/sql, for instance) needs it to honor a timeout
// mid-query, not just between capability calls; a simulated/test provider
// is free to ignore it, same as any other context.Context consumer.
// Milestone 5 (transaction.go) adds real transaction-aware providers on
// top of the same interface, via the optional TransactionalProvider
// extension below.
type CapabilityProvider interface {
	Call(ctx context.Context, method string, args []Value) (Value, *Fault)
}

// CapabilityRegistry maps a capability name (`Database`, `EmailSender`, ...)
// to the provider backing it for one interpreter run: one for the
// process, and optionally one per package (ADR 0079 §6a), used while that
// package's workflow runs.
type CapabilityRegistry struct {
	providers map[string]CapabilityProvider
	packages  map[string]map[string]CapabilityProvider
}

func NewCapabilityRegistry() *CapabilityRegistry {
	return &CapabilityRegistry{providers: map[string]CapabilityProvider{}, packages: map[string]map[string]CapabilityProvider{}}
}

// interpBound is a provider whose code runs on the program's interpreter:
// a database driver written in fiig (ADR 0084).
type interpBound interface{ bindInterp(*Interp) }

// bindInterp hands in to every provider that runs fiig code.
func (r *CapabilityRegistry) bindInterp(in *Interp) {
	if r == nil {
		return
	}
	r.each(func(p CapabilityProvider) {
		if b, ok := p.(interpBound); ok {
			b.bindInterp(in)
		}
	})
}

func (r *CapabilityRegistry) each(f func(CapabilityProvider)) {
	for _, p := range r.providers {
		f(p)
	}
	for _, byCap := range r.packages {
		for _, p := range byCap {
			f(p)
		}
	}
}

// Close stops every provider that holds connections between calls, a
// driver's pool (ADR 0084 §3), when the program stops.
func (r *CapabilityRegistry) Close() {
	if r == nil {
		return
	}
	r.each(func(p CapabilityProvider) {
		if c, ok := p.(interface{ Close() }); ok {
			c.Close()
		}
	})
}

// Copy is a registry holding r's bindings, to which one test adds its own
// (ADR 0091 §5) without touching r.
func (r *CapabilityRegistry) Copy() *CapabilityRegistry {
	out := NewCapabilityRegistry()
	for name, p := range r.providers {
		out.providers[name] = p
	}
	for path, byCap := range r.packages {
		out.packages[path] = map[string]CapabilityProvider{}
		for name, p := range byCap {
			out.packages[path][name] = p
		}
	}
	return out
}

// Register installs provider as the process-wide backing for capability.
func (r *CapabilityRegistry) Register(capability string, provider CapabilityProvider) {
	r.providers[capability] = provider
}

// RegisterFor installs provider as capability's backing while a workflow
// of the package at path runs (ADR 0079 §6a).
func (r *CapabilityRegistry) RegisterFor(path, capability string, provider CapabilityProvider) {
	if r.packages[path] == nil {
		r.packages[path] = map[string]CapabilityProvider{}
	}
	r.packages[path][capability] = provider
}

// BoundFor reports whether the package at path has its own binding for
// capability, rather than the process-wide one.
func (r *CapabilityRegistry) BoundFor(path, capability string) bool {
	_, ok := r.packages[path][capability]
	return ok
}

// Packages is every package path with a binding of its own.
func (r *CapabilityRegistry) Packages() []string {
	out := make([]string, 0, len(r.packages))
	for path := range r.packages {
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}

// Lookup returns the process-wide provider registered for capability, if
// any.
func (r *CapabilityRegistry) Lookup(capability string) (CapabilityProvider, bool) {
	p, ok := r.providers[capability]
	return p, ok
}

// LookupIn returns the provider for capability in ctx's domain (ADR 0079
// §6a): the running package's own, else the process-wide one —
// transaction.go uses this to find a TransactionalProvider to Begin
// against when entering a `transaction Capability { ... }` block.
func (r *CapabilityRegistry) LookupIn(ctx context.Context, capability string) (CapabilityProvider, bool) {
	if p, ok := r.packages[Domain(ctx)][capability]; ok {
		return p, true
	}
	p, ok := r.providers[capability]
	return p, ok
}

// Call dispatches capability.method(args) — a Fault, not a missing-name
// error, when capability was never registered: by the time a program is
// running, an unregistered capability is a host-configuration problem the
// checker has no way to catch (it treats every capability call as
// deliberately unmodeled, compiler/checker/checker.go's own doc comment).
func (r *CapabilityRegistry) Call(ctx context.Context, capability, method string, args []Value) (Value, *Fault) {
	p, ok := r.LookupIn(ctx, capability)
	if !ok {
		if f, inTest := unprovided(ctx, capability); inTest {
			return nil, f
		}
		return nil, Faultf("no provider registered for capability %q", capability)
	}
	return p.Call(ctx, method, args)
}

type domainKey struct{}

// WithDomain marks ctx as running for the package at path: its workflow
// was entered by an adapter route or a gateway (ADR 0079 §6a). "" is the
// root package, which uses the process-wide bindings.
func WithDomain(ctx context.Context, path string) context.Context {
	return context.WithValue(ctx, domainKey{}, path)
}

// Domain is the package ctx is running for, "" for the root.
func Domain(ctx context.Context) string {
	d, _ := ctx.Value(domainKey{}).(string)
	return d
}

// FuncProvider is a CapabilityProvider backed by plain Go functions — the
// simplest way to simulate a capability in a test: FuncProvider{"queryOne":
// func(ctx context.Context, args []Value) (Value, *Fault) { ... }}. Most
// test functions ignore ctx entirely, same as any synchronous fake with no
// real I/O to cancel.
type FuncProvider map[string]func(context.Context, []Value) (Value, *Fault)

func (p FuncProvider) Call(ctx context.Context, method string, args []Value) (Value, *Fault) {
	fn, ok := p[method]
	if !ok {
		return nil, Faultf("capability has no method %q", method)
	}
	return fn(ctx, args)
}
