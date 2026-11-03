package runtime

import (
	"context"

	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// scope is which package's own Universe and import aliases are active
// for whatever declaration's body is currently executing — the runtime
// counterpart of compiler/checker's own callInScope (Milestone 6, ADR
// 0032's follow-up): an unqualified call never changes it (fiig has no
// implicit cross-package reference, so a bare name always means
// "declared in this same package"), matching the checker's own
// behavior exactly; a qualified call into an imported package's own
// declaration swaps to that package's Universe/Imports for the
// duration of its body, then the caller's own scope is restored.
type scope struct {
	universe *types.Universe
	imports  map[string]*types.Package
	// groups is the capability-group imports (`use acme.http.Client`, ADR
	// 0043) active alongside imports.
	groups map[string]*types.GroupRef
	// group is the formulas group whose member is executing, so a bare call
	// in its body may name a sibling member (ADR 0049 §2.1); nil otherwise.
	group *syntax.FormulasDecl
}

type scopeKey struct{}

func withScope(ctx context.Context, s scope) context.Context {
	return context.WithValue(ctx, scopeKey{}, s)
}

// scope returns ctx's active scope, falling back to this Interp's own
// root package when none has been set yet — EvalExpr (compiler/adapter
// evaluating a route's response-mapping value) is the one caller that
// never goes through Call first, and every program without package
// context (every one before Milestone 6's package-aware execution)
// never sets one at all, so this is exactly today's existing behavior
// for both.
func (in *Interp) scope(ctx context.Context) scope {
	if s, ok := ctx.Value(scopeKey{}).(scope); ok {
		return s
	}
	return scope{universe: in.Universe}
}
