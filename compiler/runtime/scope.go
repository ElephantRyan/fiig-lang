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

// callInfo is what a declaration call puts in its context: the scope its
// body resolves names in, and its call depth (ADR 0054 §6), in one value
// so a call adds one context layer rather than two (ADR 0115 §5).
type callInfo struct {
	sc    scope
	depth int
}

type callKey struct{}

// callFrame is what one declaration call allocates, as one allocation
// (ADR 0115 §3, §5): the context layer carrying its scope and depth, its
// body's root scope, and room for its first arguments. Nothing holds a
// frame past its call, since fiig has no closures.
type callFrame struct {
	callCtx
	env  Env
	args [frameArgs]Value
}

const frameArgs = 4

// newCallFrame is a frame for a call with n parameters, and the slice its
// arguments go in, inside the frame when they fit.
func newCallFrame(n int) (*callFrame, []Value) {
	fr := &callFrame{}
	fr.env.vars = fr.env.inline[:0]
	if n <= frameArgs {
		return fr, fr.args[:n]
	}
	return fr, make([]Value, n)
}

// callCtx is a call's context layer: it answers callKey with its own
// callInfo and passes every other key, and its deadline and cancellation,
// to the caller's context, as context.WithValue would.
type callCtx struct {
	context.Context
	info callInfo
}

func (c *callCtx) Value(key any) any {
	if _, ok := key.(callKey); ok {
		return &c.info
	}
	return c.Context.Value(key)
}

// withScope is ctx with s active, at ctx's call depth.
func withScope(ctx context.Context, s scope) context.Context {
	return context.WithValue(ctx, callKey{}, &callInfo{sc: s, depth: callDepth(ctx)})
}

// scope returns ctx's active scope, falling back to this Interp's own
// root package when none has been set yet — EvalExpr (compiler/adapter
// evaluating a route's response-mapping value) is the one caller that
// never goes through Call first, and every program without package
// context (every one before Milestone 6's package-aware execution)
// never sets one at all, so this is exactly today's existing behavior
// for both.
func (in *Interp) scope(ctx context.Context) scope {
	if c, ok := ctx.Value(callKey{}).(*callInfo); ok {
		return c.sc
	}
	return scope{universe: in.Universe}
}
