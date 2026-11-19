package runtime

import (
	"context"
	"math/rand"
	"sync"

	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// Interp executes an already-checked program. Call starts a fresh Env every
// time and never mutates the loaded declarations, so one Interp is safe to
// share across concurrently running calls (concurrent.go's whole point) —
// except Rand, which math/rand.Rand itself isn't safe for concurrent use
// without a lock; randMu guards every access (see jitter in retry.go).
type Interp struct {
	Universe *types.Universe

	// Imports is this Interp's own root package's import aliases,
	// resolved per declaration name (one entry per root declaration,
	// pointing at whichever file it was declared in's own `use ... as
	// alias` aliases — compiler/checker.ResolveFileImports applied per
	// file, same as the checker itself, since two files in one package
	// may alias the same dependency differently). nil for every caller
	// before Milestone 6's package-aware execution, in which case every
	// qualified call still falls through to a capability call, exactly
	// as always.
	Imports map[string]map[string]*types.Package

	// Groups is Imports' counterpart for capability-group imports (`use
	// acme.http.Client`, ADR 0043): per root declaration name, its file's
	// alias -> group map (compiler/checker.ResolveFileGroups, per file).
	Groups map[string]map[string]*types.GroupRef

	// Packages is every package the build loaded, so a declaration of a
	// dependency the root never imports, such as one whose adapter is
	// served (ADR 0079 §6), still resolves in its own package. nil when
	// the caller has none; Imports' packages are walked first either way.
	Packages types.Packages

	// Config is every config declaration's values (ADR 0071), read once by
	// LoadConfig before the program starts.
	Config map[*syntax.ConfigDecl]Record

	Capabilities *CapabilityRegistry
	Clock        Clock
	Rand         *rand.Rand
	randMu       sync.Mutex

	// Tracer receives one Span per completed Call (and one per `concurrent`
	// block — concurrent.go) when non-nil; nil (the default) means
	// tracing costs nothing beyond the one nil check (trace.go).
	Tracer      Tracer
	spanCounter int64

	// DefaultConcurrencyLimit bounds a bare `concurrent(x in xs): ...` block
	// (no `limit:` clause) — fiig-lang.md §3.7 documents an "application
	// default" among the budgets that bound effective concurrency, but
	// nothing anywhere specifies its actual value (compiler/checker/
	// stmt.go's own doc comment makes the same observation for D2). This is
	// that number, made concrete and overridable per Interp rather than
	// hardcoded, since nothing in the spec fixes it.
	DefaultConcurrencyLimit int

	// MaxCallDepth bounds how deep a chain of declaration calls may go
	// (ADR 0054 §6): recursion is allowed, and this is its backstop. A call
	// that would exceed it produces an ordinary Fault instead of running.
	// It is a runtime setting, not part of the language; 0 means
	// DefaultMaxCallDepth.
	MaxCallDepth int
}

// DefaultMaxCallDepth is ADR 0054 §6's default call depth limit.
const DefaultMaxCallDepth = 1000

type callDepthKey struct{}

func callDepth(ctx context.Context) int {
	d, _ := ctx.Value(callDepthKey{}).(int)
	return d
}

func (in *Interp) maxCallDepth() int {
	if in.MaxCallDepth > 0 {
		return in.MaxCallDepth
	}
	return DefaultMaxCallDepth
}

// New creates an Interp with a real wall clock and a time-seeded Rand —
// NewDeterministic (clock.go) is what tests actually want instead, for
// reproducible retry/concurrency behavior.
func New(universe *types.Universe, caps *CapabilityRegistry) *Interp {
	in := &Interp{
		Universe:                universe,
		Capabilities:            caps,
		Clock:                   RealClock{},
		Rand:                    rand.New(rand.NewSource(1)),
		DefaultConcurrencyLimit: 16,
		MaxCallDepth:            DefaultMaxCallDepth,
	}
	// A provider written in fiig, such as a database driver's pool (ADR
	// 0084), runs its code on this interpreter.
	caps.bindInterp(in)
	return in
}

// Call invokes a top-level rule/conversion/activity/gateway/workflow by
// name, in this Interp's own root package.
func (in *Interp) Call(ctx context.Context, name string, args map[string]Value) (Value, *Fault) {
	if f := checkCancelled(ctx); f != nil {
		return nil, f
	}
	// ADR 0060 §5: this call owns every connection opened beneath it, and
	// closes whichever are still open when it returns, faults or is
	// cancelled.
	if pkg, entered := ctx.Value(entryKey{}).(*types.Package); entered {
		decl, found := pkg.Universe.Lookup(name)
		if !found {
			return nil, Faultf("package %s has no declaration %q", pkg.Path, name)
		}
		ctx, endOwner := withResourceOwner(ctx)
		defer endOwner()
		return in.callDecl(ctx, packageScope(pkg), name, decl, args)
	}
	decl, found := in.Universe.Lookup(name)
	if !found {
		return nil, Faultf("undefined declaration %q", name)
	}
	ctx, endOwner := withResourceOwner(ctx)
	defer endOwner()
	return in.callDecl(ctx, scope{universe: in.Universe, imports: in.Imports[name], groups: in.Groups[name]}, name, decl, args)
}

type entryKey struct{}

// EnterPackage marks ctx as entering pkg, a dependency of the root whose
// adapter is served (ADR 0079 §6): Call finds its declarations, EvalExpr
// resolves in it, and its workflows run for it (§6a).
func (in *Interp) EnterPackage(ctx context.Context, pkg *types.Package) context.Context {
	ctx = context.WithValue(ctx, entryKey{}, pkg)
	ctx = withScope(ctx, packageScope(pkg))
	return WithDomain(ctx, pkg.Path)
}

// callDecl binds args against decl's own params and evaluates its body
// with sc active for its whole duration, restored afterward — the
// shared core of every kind of call this language has (no closures, no
// first-class functions, so every call starts a brand-new Env seeded
// with just its own arguments, never inheriting a caller's locals):
// Call itself (a top-level invocation), an unqualified same-package
// call (evalIdentCall, which keeps the caller's own sc unchanged — an
// unqualified call never crosses a package boundary), and a qualified
// cross-package call (evalQualifiedCall, which builds a fresh sc for
// the imported package before calling this).
func (in *Interp) callDecl(ctx context.Context, sc scope, displayName string, decl syntax.Decl, args map[string]Value) (Value, *Fault) {
	if f := checkCancelled(ctx); f != nil {
		return nil, f
	}
	params, body, kind, ok := callableShape(decl)
	if !ok {
		return nil, Faultf("%q is not callable", displayName)
	}
	depth := callDepth(ctx) + 1
	if limit := in.maxCallDepth(); depth > limit {
		// Stack fills in on the way out, one frame per caller, so the
		// fault carries the whole chain from the entry point to here.
		return nil, Faultf("call depth limit of %d exceeded calling %s", limit, displayName).withFrame(displayName)
	}
	ctx = context.WithValue(ctx, callDepthKey{}, depth)
	// A declaration's body resolves names in its own file's scope, which
	// holds that file's private members when it has any (ADR 0054). A
	// call between files of one package keeps the package and imports.
	sc.universe = sc.universe.ScopeOf(decl)

	var span Span
	if in.Tracer != nil {
		ctx, span = in.startSpan(ctx, displayName, kind)
	}
	ctx = withScope(ctx, sc)

	env := NewEnv()
	for _, p := range params {
		v, ok := args[p.Name]
		if !ok {
			fault := Faultf("missing argument %q to %s", p.Name, displayName).withFrame(displayName)
			if in.Tracer != nil {
				in.finishSpan(span, nil, fault)
			}
			return nil, fault
		}
		env.Bind(p.Name, v)
	}
	val, _, fault := in.evalBlock(ctx, body, env, true)
	if fault != nil {
		fault = fault.withFrame(displayName)
	}
	if val == nil {
		val = None{}
	}
	if in.Tracer != nil {
		in.finishSpan(span, val, fault)
	}
	if fault != nil {
		return nil, fault
	}
	return val, nil
}

// callableShape extracts decl's own parameter list, body, and span kind
// — shared by callDecl and (params alone) evalQualifiedCall's
// cross-package dispatch, which needs the callee's own params to match
// arguments by name/position before callDecl runs.
func callableShape(decl syntax.Decl) ([]syntax.Param, *syntax.Block, SpanKind, bool) {
	switch decl := decl.(type) {
	case *syntax.RuleDecl:
		return decl.Params, decl.Body, SpanRule, true
	case *syntax.ConversionDecl:
		return decl.Params, decl.Body, SpanConversion, true
	case *syntax.ActivityDecl:
		return decl.Params, decl.Body, SpanActivity, true
	case *syntax.GatewayDecl:
		return decl.Params, decl.Body, SpanGateway, true
	case *syntax.WorkflowDecl:
		return decl.Params, decl.Body, SpanWorkflow, true
	case *syntax.HelperDecl:
		return decl.Params, decl.Body, SpanHelper, true
	case *syntax.FormulaDecl:
		return decl.Params, decl.Body, SpanFormula, decl.Body != nil
	default:
		return nil, nil, "", false
	}
}

// EvalExpr evaluates a bare expression against env — exported for
// compiler/adapter, which evaluates a route's response-mapping values
// (ADR 0017: a status-code shorthand's body, or an ExprResponse like a
// shared conversion call) outside of any declaration body. There's no
// enclosing function for a `?` to return out of at this position, so an
// early return here is itself treated as a Fault rather than silently
// discarded — `?` inside a response-mapping value isn't attested anywhere
// and has no sensible target to propagate to.
func (in *Interp) EvalExpr(ctx context.Context, e syntax.Expr, env *Env) (Value, *Fault) {
	val, earlyReturn, fault := in.evalExpr(ctx, e, env)
	if fault != nil {
		return nil, fault
	}
	if earlyReturn {
		return nil, Faultf("`?` is not supported in this position")
	}
	return val, nil
}

// runProducingBody evaluates a match-arm/concurrent/retry Body in a
// producing position — every attested one in the corpus is a bare
// expression (a gateway/workflow call), but the grammar also allows a Block,
// which is evaluated the same way a declaration's own outermost block
// would be.
func (in *Interp) runProducingBody(ctx context.Context, body syntax.Body, env *Env) (Value, bool, *Fault) {
	switch body := body.(type) {
	case syntax.ExprBody:
		return in.evalExpr(ctx, body.Expr, env)
	case *syntax.Block:
		return in.evalBlock(ctx, body, env.Child(), true)
	default:
		return nil, false, Faultf("unsupported body %T", body)
	}
}

func checkCancelled(ctx context.Context) *Fault {
	select {
	case <-ctx.Done():
		return Faultf("cancelled: %v", ctx.Err())
	default:
		return nil
	}
}

// evalBlock runs every statement in b. producing mirrors
// compiler/checker/stmt.go's checkBlock: whether b's own last statement, if
// not an explicit `return`, implicitly produces the enclosing declaration's
// return value. The bool result is "an early return happened" — either an
// explicit `return`, or a `?` that hit Err partway through evaluating some
// statement's expression (TryExpr in expr.go) — in both cases every
// remaining statement in this block (and any enclosing one) is skipped, the
// same way a real `return` unwinds a call stack.
func (in *Interp) evalBlock(ctx context.Context, b *syntax.Block, env *Env, producing bool) (Value, bool, *Fault) {
	for i, s := range b.Stmts {
		if f := checkCancelled(ctx); f != nil {
			return nil, false, f
		}
		isLast := i == len(b.Stmts)-1
		val, returned, fault := in.evalStmt(ctx, s, env, isLast && producing)
		if fault != nil {
			return nil, false, fault
		}
		if returned {
			return val, true, nil
		}
	}
	return nil, false, nil
}

func (in *Interp) evalStmt(ctx context.Context, s syntax.Stmt, env *Env, producingHere bool) (Value, bool, *Fault) {
	switch s := s.(type) {
	case *syntax.Binding:
		val, earlyReturn, fault := in.evalExpr(ctx, s.Value, env)
		if fault != nil {
			return nil, false, fault
		}
		if earlyReturn {
			return val, true, nil
		}
		switch {
		case s.Type != nil:
			env.BindReassignable(s.Name, val)
		case !env.Reassign(s.Name, val):
			env.Bind(s.Name, val)
		}
		return nil, false, nil
	case *syntax.ForStmt:
		return in.evalFor(ctx, s, env)
	case *syntax.WhileStmt:
		return in.evalWhile(ctx, s, env)
	case *syntax.BreakStmt:
		return loopJump{label: s.Label, brk: true}, true, nil
	case *syntax.ContinueStmt:
		return loopJump{label: s.Label}, true, nil
	case *syntax.ReturnStmt:
		if s.Value == nil {
			return None{}, true, nil
		}
		val, _, fault := in.evalExpr(ctx, s.Value, env)
		if fault != nil {
			return nil, false, fault
		}
		return val, true, nil
	case *syntax.ExprStmt:
		val, earlyReturn, fault := in.evalExpr(ctx, s.Value, env)
		if fault != nil {
			return nil, false, fault
		}
		if earlyReturn || producingHere {
			return val, true, nil
		}
		return nil, false, nil
	case *syntax.IfStmt:
		return in.evalIf(ctx, s, env, producingHere)
	case *syntax.MatchStmt:
		return in.evalMatchStmt(ctx, s, env, producingHere)
	case *syntax.TransactionStmt:
		return in.evalTransaction(ctx, s, env, producingHere)
	case *syntax.ExpectStmt:
		return in.evalExpect(ctx, s, env)
	default:
		return nil, false, Faultf("unsupported statement %T", s)
	}
}

// loopJump is the value a `break` or `continue` statement produces. It travels
// up the statement stack exactly like an early `return`, since evalBlock
// unwinds on any true bool, and the nearest enclosing for loop consumes it.
// The checker guarantees one never escapes a function, a concurrent body, or
// a transaction.
type loopJump struct {
	label string // "" targets the innermost loop
	brk   bool   // true for break, false for continue
}

func (loopJump) isValue()         {}
func (j loopJump) String() string { return "loop jump" }

// evalFor runs a sequential loop over a List (ADR 0053). Each element gets a
// fresh child scope holding the loop variable, so anything declared in the
// body is gone by the next element. A jump addressed to this loop is consumed
// here: break stops the loop, continue starts the next element. A return, or a
// jump labeled for an outer loop, passes through.
func (in *Interp) evalFor(ctx context.Context, s *syntax.ForStmt, env *Env) (Value, bool, *Fault) {
	iterVal, earlyReturn, fault := in.evalExpr(ctx, s.Iterable, env)
	if fault != nil || earlyReturn {
		return iterVal, earlyReturn, fault
	}
	var elems []Value
	switch it := iterVal.(type) {
	case Map:
		return in.evalMapFor(ctx, s, it, env)
	case List:
		elems = it.Elems
	case Bytes:
		// ADR 0058 §1: each byte as an int.
		elems = make([]Value, len(it.B))
		for i, x := range it.B {
			elems[i] = Int(x)
		}
	default:
		return nil, false, Faultf("for ... in iterable is not a List or bytes (got %s)", iterVal)
	}
	for _, elem := range elems {
		if f := checkCancelled(ctx); f != nil {
			return nil, false, f
		}
		bodyEnv := env.Child()
		bodyEnv.Bind(s.Var, elem)
		val, jumped, fault := in.evalBlock(ctx, s.Body, bodyEnv, false)
		if exit, out, outJumped, outFault := loopExit(val, jumped, fault, s.Label); exit {
			return out, outJumped, outFault
		}
	}
	return nil, false, nil
}

// evalMapFor is `for key, value in m` (ADR 0065 §7), in insertion order;
// `_` binds nothing.
func (in *Interp) evalMapFor(ctx context.Context, s *syntax.ForStmt, m Map, env *Env) (Value, bool, *Fault) {
	type entry struct{ k, v Value }
	var entries []entry
	m.Each(func(k, v Value) bool { entries = append(entries, entry{k, v}); return true })
	for _, en := range entries {
		if f := checkCancelled(ctx); f != nil {
			return nil, false, f
		}
		bodyEnv := env.Child()
		if s.Var != "_" {
			bodyEnv.Bind(s.Var, en.k)
		}
		if s.Value != "" && s.Value != "_" {
			bodyEnv.Bind(s.Value, en.v)
		}
		val, jumped, fault := in.evalBlock(ctx, s.Body, bodyEnv, false)
		if exit, out, outJumped, outFault := loopExit(val, jumped, fault, s.Label); exit {
			return out, outJumped, outFault
		}
	}
	return nil, false, nil
}

// evalWhile runs `while cond { ... }` (ADR 0059). Each iteration checks for
// cancellation first, so a loop that never ends stops at the call's deadline
// (§4); there is no iteration cap.
func (in *Interp) evalWhile(ctx context.Context, s *syntax.WhileStmt, env *Env) (Value, bool, *Fault) {
	for {
		if f := checkCancelled(ctx); f != nil {
			return nil, false, f
		}
		matched, earlyReturn, fault := in.evalCond(ctx, s.Cond, env)
		if fault != nil || earlyReturn {
			return matched.value, earlyReturn, fault
		}
		if !matched.ok {
			return nil, false, nil
		}
		val, jumped, fault := in.evalBlock(ctx, s.Body, env.Child(), false)
		if exit, out, outJumped, outFault := loopExit(val, jumped, fault, s.Label); exit {
			return out, outJumped, outFault
		}
	}
}

// loopExit decides what one loop iteration's result means for the loop
// labeled label: exit reports whether the loop stops, and out/outJumped/
// outFault are what it then returns. A fault, a return, or a jump aimed at an
// outer loop propagates; break stops this loop; continue and a normal end of
// the body go on to the next iteration.
func loopExit(val Value, jumped bool, fault *Fault, label string) (exit bool, out Value, outJumped bool, outFault *Fault) {
	if fault != nil {
		return true, nil, false, fault
	}
	if !jumped {
		return false, nil, false, nil
	}
	jump, isJump := val.(loopJump)
	if !isJump || (jump.label != "" && jump.label != label) {
		return true, val, true, nil
	}
	if jump.brk {
		return true, nil, false, nil
	}
	return false, nil, false, nil
}

func (in *Interp) evalIf(ctx context.Context, s *syntax.IfStmt, env *Env, producingHere bool) (Value, bool, *Fault) {
	matched, earlyReturn, fault := in.evalCond(ctx, s.Cond, env)
	if fault != nil {
		return nil, false, fault
	}
	if earlyReturn {
		return matched.value, true, nil
	}
	if matched.ok {
		return in.evalBlock(ctx, s.Then, env.Child(), producingHere)
	}
	for _, ei := range s.ElseIfs {
		m, earlyReturn, fault := in.evalCond(ctx, ei.Cond, env)
		if fault != nil {
			return nil, false, fault
		}
		if earlyReturn {
			return m.value, true, nil
		}
		if m.ok {
			return in.evalBlock(ctx, ei.Then, env.Child(), producingHere)
		}
	}
	if s.Else != nil {
		return in.evalBlock(ctx, s.Else, env.Child(), producingHere)
	}
	return nil, false, nil
}

type condResult struct {
	ok    bool
	value Value
}

// evalCond evaluates an if/else-if condition, which must be a Bool.
func (in *Interp) evalCond(ctx context.Context, e syntax.Expr, env *Env) (condResult, bool, *Fault) {
	val, earlyReturn, fault := in.evalExpr(ctx, e, env)
	if fault != nil || earlyReturn {
		return condResult{}, earlyReturn, fault
	}
	b, ok := val.(Bool)
	if !ok {
		return condResult{}, false, Faultf("condition did not evaluate to a boolean (got %s)", val)
	}
	return condResult{ok: bool(b), value: val}, false, nil
}

func (in *Interp) evalMatchStmt(ctx context.Context, s *syntax.MatchStmt, env *Env, producingHere bool) (Value, bool, *Fault) {
	subject, earlyReturn, fault := in.evalExpr(ctx, s.Subject, env)
	if fault != nil {
		return nil, false, fault
	}
	if earlyReturn {
		return subject, true, nil
	}
	for _, arm := range s.Arms {
		armEnv := env.Child()
		if !MatchPattern(arm.Pattern, subject, armEnv) {
			continue
		}
		switch body := arm.Body.(type) {
		case syntax.ExprBody:
			val, earlyReturn, fault := in.evalExpr(ctx, body.Expr, armEnv)
			if fault != nil {
				return nil, false, fault
			}
			if earlyReturn || producingHere {
				return val, true, nil
			}
			return nil, false, nil
		case *syntax.Block:
			return in.evalBlock(ctx, body, armEnv, producingHere)
		default:
			return nil, false, Faultf("unsupported match-arm body %T", body)
		}
	}
	// compiler/checker's A9 guarantees exhaustiveness for every subject type
	// it can enumerate; reaching here means either a subject type it
	// deliberately doesn't model (Unknown — a capability result, most
	// commonly) produced a shape no arm covers, a genuine checker gap, or
	// an unchecked program. Either way it's a real runtime defect, not a
	// typed error.
	return nil, false, Faultf("no match arm matched %s", subject)
}
