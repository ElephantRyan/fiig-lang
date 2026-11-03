package runtime

import "context"

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
// to the provider backing it for one interpreter run.
type CapabilityRegistry struct {
	providers map[string]CapabilityProvider
}

func NewCapabilityRegistry() *CapabilityRegistry {
	return &CapabilityRegistry{providers: map[string]CapabilityProvider{}}
}

// Register installs provider as the backing for capability.
func (r *CapabilityRegistry) Register(capability string, provider CapabilityProvider) {
	r.providers[capability] = provider
}

// Lookup returns the provider registered for capability, if any —
// transaction.go uses this to find a TransactionalProvider to Begin against
// when entering a `transaction Capability { ... }` block.
func (r *CapabilityRegistry) Lookup(capability string) (CapabilityProvider, bool) {
	p, ok := r.providers[capability]
	return p, ok
}

// Call dispatches capability.method(args) — a Fault, not a missing-name
// error, when capability was never registered: by the time a program is
// running, an unregistered capability is a host-configuration problem the
// checker has no way to catch (it treats every capability call as
// deliberately unmodeled, compiler/checker/checker.go's own doc comment).
func (r *CapabilityRegistry) Call(ctx context.Context, capability, method string, args []Value) (Value, *Fault) {
	p, ok := r.providers[capability]
	if !ok {
		return nil, Faultf("no provider registered for capability %q", capability)
	}
	return p.Call(ctx, method, args)
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
