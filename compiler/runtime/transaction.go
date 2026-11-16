package runtime

import (
	"context"

	"fiig-lang/compiler/syntax"
)

// Tx is one open transaction against a capability — itself a
// CapabilityProvider (every call to the same capability made from inside
// the transaction block routes to it, not to the plain provider, so writes
// are visible to later reads in the same block — fiig-lang.md §3.8), plus
// Commit/Rollback to close it out.
type Tx interface {
	CapabilityProvider
	Commit() *Fault
	Rollback() *Fault
}

// TransactionalProvider is a CapabilityProvider that also supports
// `transaction Capability { ... }` blocks. A provider that doesn't
// implement this (most simulated/test providers) simply can't back a
// transaction block — evalTransaction reports that as a Fault rather than
// silently falling back to pass-through execution, now that real
// transaction semantics exist to fall back *to*.
type TransactionalProvider interface {
	CapabilityProvider
	Begin(ctx context.Context) (Tx, *Fault)
}

// activeTx is a context value: the Tx currently open for one specific
// capability, set for the duration of evalTransaction's call to
// evalBlock. evalQualifiedCall (callexpr.go) checks for one before falling
// back to the plain CapabilityRegistry — this is ADR 0015's "implicit
// transaction binding": no transaction handle is threaded through gateway
// signatures or call sites, a gateway's own `Database.execute(...)` call
// just automatically joins whichever transaction is active for that same
// capability name when it's called from inside one.
type activeTxKey struct{}

type activeTx struct {
	capability string
	tx         Tx
}

func withActiveTx(ctx context.Context, capability string, tx Tx) context.Context {
	return context.WithValue(ctx, activeTxKey{}, activeTx{capability: capability, tx: tx})
}

// lookupActiveTx returns the Tx open for capability in ctx, if evalContext
// is currently inside a `transaction capability { ... }` block for it.
// C2 (compiler/checker: transaction blocks may not nest) guarantees at most
// one activeTx value is ever relevant at a time in a checked program.
func lookupActiveTx(ctx context.Context, capability string) (Tx, bool) {
	v, ok := ctx.Value(activeTxKey{}).(activeTx)
	if !ok || v.capability != capability {
		return nil, false
	}
	return v.tx, true
}

// evalTransaction implements `transaction Capability { ... }` (§3.8) against
// a real TransactionalProvider: Begin, run the body with that Tx active for
// Capability, then decide Commit vs. Rollback from how the body finished —
// mirroring exactly what `?` already means everywhere else in this
// language (compiler/checker/stmt.go's own doc comment: "`?` only ever
// propagates an error unchanged"):
//
//   - A genuine Fault inside the body → Rollback, propagate the Fault.
//   - An early return carrying a typed Err (whether from an explicit
//     `return Err(...)` or a `?` that hit Err) → Rollback, then propagate
//     that Err out of the transaction block exactly like `?` would out of
//     any other block — the transaction's own work never completed, so
//     there's nothing to keep.
//   - Anything else (fell off the end, or an early return that isn't an
//     Err) → Commit.
//
// §3.8's "failure after an earlier commit requires explicit compensation;
// it is not described as rollback" is about a *later* gateway failing after
// *this* block already committed — checkout.fiig's compensation examples
// are workflow-level, outside any transaction block, and this function has
// nothing to do with that case; it only ever decides the fate of the one
// transaction it itself opened.
func (in *Interp) evalTransaction(ctx context.Context, s *syntax.TransactionStmt, env *Env, producingHere bool) (Value, bool, *Fault) {
	provider, ok := in.Capabilities.LookupIn(ctx, s.Capability)
	if !ok {
		return nil, false, Faultf("no provider registered for capability %q", s.Capability)
	}
	txProvider, ok := provider.(TransactionalProvider)
	if !ok {
		return nil, false, Faultf("capability %q does not support transactions", s.Capability)
	}
	tx, fault := txProvider.Begin(ctx)
	if fault != nil {
		return nil, false, fault
	}

	txCtx := withActiveTx(ctx, s.Capability, tx)
	val, returned, blockFault := in.evalBlock(txCtx, s.Body, env.Child(), producingHere)
	if blockFault != nil {
		tx.Rollback() // best-effort: the original defect is what gets reported either way.
		return nil, false, blockFault
	}
	if returned {
		if rv, isResult := val.(Result); isResult && !rv.Ok {
			if rbFault := tx.Rollback(); rbFault != nil {
				return nil, false, rbFault
			}
			return val, true, nil
		}
	}
	if commitFault := tx.Commit(); commitFault != nil {
		return nil, false, commitFault
	}
	return val, returned, nil
}
