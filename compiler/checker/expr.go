package checker

import (
	"fiig-lang/compiler/capability"
	"fiig-lang/compiler/lexer"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// checkExpected checks e against an expected type, specially handling the
// prelude's Ok/Err constructors relative to an expected Result<S, E> —
// inferExpr alone can't type Ok(x)/Err(x) without knowing which Result
// they're building, since Ok/Err aren't declarations with a fixed
// signature the way everything else in the universe is. Falls back to
// ordinary inferExpr + AssignableTo for anything else. expected == nil
// means "no expectation" (a non-producing position) — still infers e for
// its own errors, just doesn't compare the result to anything.
func (c *checker) checkExpected(e syntax.Expr, expected types.Type, env map[string]types.Type) {
	if lit, ok := e.(*syntax.ListLit); ok {
		c.checkListLit(lit, expected, env)
		return
	}
	if lit, ok := e.(*syntax.MapLit); ok {
		c.checkMapLit(lit, expected, env)
		return
	}
	if call, ok := e.(*syntax.CallExpr); ok {
		if ident, ok := call.Callee.(*syntax.Ident); ok && (ident.Name == "Ok" || ident.Name == "Err") {
			c.checkOkErr(call, ident.Name, expected, env)
			return
		}
		if ident, ok := call.Callee.(*syntax.Ident); ok && ident.Name == types.HttpResponseName {
			got := c.checkHttpResponse(call, expected, env)
			if expected != nil && !types.AssignableTo(got, expected) {
				start, _ := call.Bounds()
				c.report(start, "A1", "type mismatch: got %s, expected %s", got, expected)
			}
			return
		}
	}
	got, isConst := c.landConstant(e, expected)
	if !isConst {
		got = c.inferExpr(e, env)
	}
	if expected == nil {
		return
	}
	if !types.AssignableTo(got, expected) {
		start, _ := e.(syntax.Spanned).Bounds()
		rule := "A1"
		if _, isNone := got.(types.NoneType); isNone {
			rule = "A2"
		}
		c.report(start, rule, "type mismatch: got %s, expected %s", got, expected)
	}
}

// checkOkErr handles Ok(x)/Err(x) relative to an expected Result<S, E> —
// bug class A2 ("returning None where a non-optional type is required")
// falls out of this naturally: Ok(None) against Result<Customer, _> checks
// None against Customer like any other mismatch.
func (c *checker) checkOkErr(call *syntax.CallExpr, which string, expected types.Type, env map[string]types.Type) {
	result, ok := expected.(types.Generic)
	if !ok || result.Name != "Result" || len(result.Args) != 2 {
		// Ok/Err used somewhere that doesn't expect a Result isn't attested
		// anywhere in the corpus — infer args loosely rather than guess a
		// diagnostic shape nothing exercises.
		for _, a := range call.Args {
			c.inferExpr(a.Value, env)
		}
		return
	}
	want := result.Args[0]
	if which == "Err" {
		want = result.Args[1]
	}
	if len(call.Args) != 1 {
		start, _ := call.Bounds()
		c.report(start, "", "%s(...) takes exactly one argument", which)
		for _, a := range call.Args {
			c.inferExpr(a.Value, env)
		}
		return
	}
	c.checkExpected(call.Args[0].Value, want, env)
}

func (c *checker) inferExpr(e syntax.Expr, env map[string]types.Type) types.Type {
	// A constant with no context to land in takes its default type.
	if t, ok := c.landConstant(e, nil); ok {
		return t
	}
	switch e := e.(type) {
	case *syntax.Ident:
		if e.Name == "None" {
			return types.NoneType{}
		}
		if e.Name == "true" || e.Name == "false" {
			return types.Primitive{Name: "bool"}
		}
		if t, ok := env[e.Name]; ok {
			return t
		}
		if t, ok := c.groupValue(e.Name); ok {
			return t
		}
		c.report(e.Start, "", "undefined name %q", e.Name)
		return types.Unknown{}
	case *syntax.StringLit:
		for _, part := range e.Parts {
			if part.Interp != nil {
				start, _ := part.Interp.(syntax.Spanned).Bounds()
				c.checkInterpolatedSecret(c.inferExpr(part.Interp, env), start)
			}
		}
		return types.Primitive{Name: "string"}
	case *syntax.FieldAccess:
		return c.inferFieldAccess(e, env)
	case *syntax.CallExpr:
		return c.inferCall(e, env)
	case *syntax.NotExpr:
		c.checkExpected(e.Operand, types.Primitive{Name: "bool"}, env)
		return types.Primitive{Name: "bool"}
	case *syntax.NegExpr:
		return c.negate(e, env)
	case *syntax.BinaryExpr:
		return c.inferBinary(e, env)
	case *syntax.TryExpr:
		return c.inferTry(e, env)
	case *syntax.ParenExpr:
		return c.inferExpr(e.Inner, env)
	case *syntax.ListLit:
		return c.inferListLit(e, env)
	case *syntax.MapLit:
		return c.inferMapLit(e, env)
	case *syntax.BytesLit:
		return c.inferBytesLit(e, env)
	case *syntax.ConcurrentExpr:
		return c.inferConcurrent(e, env)
	case *syntax.SortedExpr:
		return c.inferSorted(e, env)
	case *syntax.RetryExpr:
		c.checkRetry(e)
		return c.inferBody(e.Body, env)
	default:
		return types.Unknown{}
	}
}

// inferBody handles a match-arm/concurrent/retry Body. Every attested
// concurrent/retry body in the corpus is a bare expression; a Block body
// is legal grammar but unattested here (same note as format.go's
// printBodyInline) — checked as an ordinary non-producing block rather
// than guessing what value it should produce.
func (c *checker) inferBody(b syntax.Body, env map[string]types.Type) types.Type {
	switch b := b.(type) {
	case syntax.ExprBody:
		return c.inferExpr(b.Expr, env)
	case *syntax.Block:
		c.checkBlock(b, nil, childEnv(env), false)
		return types.Unknown{}
	default:
		return types.Unknown{}
	}
}

// checkRetry is bug class E1: retry(...) is only legal around a gateway
// declared idempotent. Only checks the one attested shape — a bare call
// directly as retry's body (every attested retry, valid or rejected, is
// exactly this); anything else (retry wrapping something other than a
// direct gateway call) isn't attested and isn't guessed at here.
func (c *checker) checkRetry(e *syntax.RetryExpr) {
	body, ok := e.Body.(syntax.ExprBody)
	if !ok {
		return
	}
	call, ok := body.Expr.(*syntax.CallExpr)
	if !ok {
		return
	}
	ident, ok := call.Callee.(*syntax.Ident)
	if !ok {
		return
	}
	decl, found := c.lookup(ident.Name)
	if !found {
		return
	}
	gw, ok := decl.(*syntax.GatewayDecl)
	if !ok {
		return
	}
	if !gw.Idempotent {
		c.report(e.Start, "E1", "retry wraps %q, which isn't declared idempotent", ident.Name)
	}
}

func (c *checker) inferFieldAccess(e *syntax.FieldAccess, env map[string]types.Type) types.Type {
	// A zero-field enum variant is constructed with no call parens at all
	// (CheckoutError.OutOfStock), so it parses as a bare FieldAccess, not a
	// CallExpr — inferQualifiedCall (which handles the call-shaped form)
	// never sees it. Checked here, against the universe rather than env,
	// before falling through to ordinary field access, since the target
	// name is a type reference, not a local variable.
	if targetIdent, ok := e.Target.(*syntax.Ident); ok {
		if _, isLocal := env[targetIdent.Name]; !isLocal {
			if targetIdent.Name == types.MathGroup {
				c.mathMember(e)
				return types.Unknown{}
			}
			if c.ambiguous(targetIdent.Name) {
				return types.Unknown{}
			}
			if cd, ok := c.configRef(targetIdent.Name); ok {
				return c.readConfig(cd, e)
			}
			if decl, found := c.universe.Lookup(targetIdent.Name); found {
				if enumDecl, ok := decl.(*syntax.EnumDecl); ok {
					for _, v := range enumDecl.Variants {
						if v.Name == e.Field {
							if len(v.Fields) > 0 {
								start, _ := e.Bounds()
								c.report(start, "A3", "%s.%s requires %d field(s); none supplied", enumDecl.Name, v.Name, len(v.Fields))
							}
							return types.Named{Name: enumDecl.Name, Decl: enumDecl}
						}
					}
					start, _ := e.Bounds()
					c.report(start, "", "%s has no variant %q", enumDecl.Name, e.Field)
					return types.Unknown{}
				}
			}
		}
	}

	target := c.inferExpr(e.Target, env)
	if body, ok := types.IsHttpResponse(target); ok {
		if t, ok := httpResponseField(body, e.Field); ok {
			return t
		}
		start, _ := e.Bounds()
		c.report(start, "", "%s has no field %q", target, e.Field)
		return types.Unknown{}
	}
	named, ok := target.(types.Named)
	if !ok {
		// Unknown target (capability result, unresolved name) — nothing to
		// check the field against.
		return types.Unknown{}
	}
	switch decl := named.Decl.(type) {
	case *syntax.ModelDecl:
		for _, f := range decl.AllFields() {
			if f.Name == e.Field {
				return c.resolve(f.Type)
			}
		}
	case *syntax.EnumDecl:
		// ADR 0046 §4b: a header field is readable on any value of the enum.
		if f, ok := decl.HeaderField(e.Field); ok {
			return c.resolve(f.Type)
		}
	}
	start, _ := e.Bounds()
	c.report(start, "", "%s has no field %q", named.Name, e.Field)
	return types.Unknown{}
}

func (c *checker) inferBinary(e *syntax.BinaryExpr, env map[string]types.Type) types.Type {
	switch e.Op {
	case syntax.OpAdd, syntax.OpSub, syntax.OpMul, syntax.OpDiv, syntax.OpMod:
		// ADR 0019, amended by ADR 0048 §2.2: arithmetic is on int or on
		// float, fixed, non-overloadable, and never mixes the two.
		lt, rt := c.operands(e, env)
		return c.arithmetic(e, lt, rt)
	case syntax.OpAnd, syntax.OpOr:
		// ADR 0041: and/or are bool-only, same fixed-operand-type treatment
		// arithmetic already gets.
		c.checkExpected(e.Left, types.Primitive{Name: "bool"}, env)
		c.checkExpected(e.Right, types.Primitive{Name: "bool"}, env)
		return types.Primitive{Name: "bool"}
	default:
		// Comparison operators (==, !=, <, <=, >, >=): which operand types
		// each takes is ADR 0052's (compare.go); the result is always bool.
		lt, rt := c.operands(e, env)
		c.checkComparable(e, lt, rt)
		return types.Primitive{Name: "bool"}
	}
}

func (c *checker) inferTry(e *syntax.TryExpr, env map[string]types.Type) types.Type {
	t := c.inferExpr(e.Operand, env)
	if g, ok := t.(types.Generic); ok && g.Name == "Result" && len(g.Args) == 2 {
		return g.Args[0]
	}
	return types.Unknown{}
}

// inferSorted checks ADR 0065 §3's keyed sort. The key must be ordered
// (M3), and may call only what a rule body may, whatever kind encloses it,
// so a sort can't reach a gateway and order by outside state.
func (c *checker) inferSorted(e *syntax.SortedExpr, env map[string]types.Type) types.Type {
	iterType := c.inferExpr(e.Iterable, env)
	elemType := types.Type(types.Unknown{})
	g, isList := iterType.(types.Generic)
	if isList && g.Name == "List" && len(g.Args) == 1 {
		elemType = g.Args[0]
	} else if _, unknown := iterType.(types.Unknown); !unknown {
		start, _ := e.Iterable.(syntax.Spanned).Bounds()
		c.report(start, "A1", "sorted takes a List, got %s", iterType)
		iterType = types.Unknown{}
	}
	keyEnv := childEnv(env)
	keyEnv[e.Var] = elemType
	c.checkLocalName(e.Var, elemType, e.Start)

	savedKind, savedKey := c.callerKind, c.inSortKey
	c.callerKind, c.inSortKey = kindRule, true
	keyType := c.inferExpr(e.Key, keyEnv)
	c.callerKind, c.inSortKey = savedKind, savedKey

	if !ordered(keyType) {
		start, _ := e.Key.(syntax.Spanned).Bounds()
		if admitsNone(keyType) {
			c.report(start, "M3", "a sorted key of type %s is not ordered; decide where None goes, for example with a match in a private helper", keyType)
		} else {
			c.report(start, "M3", "a sorted key must be int, float, decimal, or string, got %s", keyType)
		}
	}
	return iterType
}

// admitsNone reports whether t is a union with None in it: `T | None`.
func admitsNone(t types.Type) bool {
	u, ok := t.(types.Union)
	if !ok {
		return false
	}
	for _, m := range u.Members {
		if _, none := m.(types.NoneType); none {
			return true
		}
	}
	return false
}

func (c *checker) inferConcurrent(e *syntax.ConcurrentExpr, env map[string]types.Type) types.Type {
	iterType := c.inferExpr(e.Iterable, env)
	elemType := types.Type(types.Unknown{})
	if g, ok := iterType.(types.Generic); ok && g.Name == "List" && len(g.Args) == 1 {
		elemType = g.Args[0]
	}
	bodyEnv := childEnv(env)
	bodyEnv[e.Var] = elemType
	c.checkLocalName(e.Var, elemType, e.Start)

	// D1/D2: concurrency bounds. thisLimit becomes the active budget for
	// anything nested inside this block's own body, restored afterward —
	// the same stack-like pattern as activeTransaction.
	var thisLimit *int
	if e.Limit != nil {
		if e.Limit.Unbounded {
			c.report(e.Start, "D1", "concurrent block requests limit: unbounded; concurrency is always bounded")
		} else if e.Limit.Value != nil {
			c.inferExpr(e.Limit.Value, bodyEnv)
			if n, ok := limitAsInt(e.Limit.Value); ok {
				thisLimit = &n
				if c.concurrencyBudget != nil && n > *c.concurrencyBudget {
					c.report(e.Start, "D2", "nested concurrent limit %d exceeds the parent budget of %d", n, *c.concurrencyBudget)
				}
			}
		}
	}

	savedBudget := c.concurrencyBudget
	if thisLimit != nil {
		c.concurrencyBudget = thisLimit
	}
	savedLoops, savedInConcurrent := c.loops, c.inConcurrent
	c.loops, c.inConcurrent = nil, true
	bodyType := c.inferBody(e.Body, bodyEnv)
	c.loops, c.inConcurrent = savedLoops, savedInConcurrent
	c.concurrencyBudget = savedBudget

	// Concurrency result typing (fiig-lang.md §3.7/§4): a concurrent block
	// over a List<Key> whose body produces a Result<Success, Failure>
	// produces BatchResult<Key, Success, Failure> — every attested
	// concurrent block wraps exactly this shape (a gateway or workflow call),
	// per-item failures preserved rather than aborting the whole batch.
	// Anything else (a Block body, a body that isn't itself a Result — e.g.
	// the nested case in concurrency_error.fiig, whose outer body is
	// another concurrent block producing a BatchResult, not a Result)
	// isn't attested anywhere as a valid program, so it's left as Unknown
	// rather than guessed at.
	if result, ok := bodyType.(types.Generic); ok && result.Name == "Result" && len(result.Args) == 2 {
		return types.Generic{Name: "BatchResult", Args: []types.Type{elemType, result.Args[0], result.Args[1]}}
	}
	return types.Unknown{}
}

func (c *checker) inferCall(e *syntax.CallExpr, env map[string]types.Type) types.Type {
	start, _ := e.Bounds()

	if ident, ok := e.Callee.(*syntax.Ident); ok && (ident.Name == "Ok" || ident.Name == "Err") {
		// Reached without a known expected type (e.g. bound to a local
		// variable rather than returned/matched directly) — not attested
		// anywhere in the corpus; infer the argument loosely rather than
		// guess a Result<S, E> shape with no evidence for S or E.
		for _, a := range e.Args {
			c.inferExpr(a.Value, env)
		}
		return types.Unknown{}
	}

	if ident, ok := e.Callee.(*syntax.Ident); ok && ident.Name == "copy" {
		return c.checkCopy(e, env)
	}
	if ident, ok := e.Callee.(*syntax.Ident); ok && ident.Name == types.HttpResponseName {
		return c.checkHttpResponse(e, nil, env)
	}
	if ident, ok := e.Callee.(*syntax.Ident); ok && ident.Name == "append" {
		return c.checkAppend(e, env)
	}

	if fa, ok := e.Callee.(*syntax.FieldAccess); ok {
		return c.inferQualifiedCall(fa, e.Args, start, env)
	}

	ident, ok := e.Callee.(*syntax.Ident)
	if !ok {
		return types.Unknown{}
	}
	if c.group != nil {
		if f := c.group.Member(ident.Name); f != nil {
			return c.callSibling(f, e.Args, start, env)
		}
	}
	decl, found := c.universe.Lookup(ident.Name)
	if !found || c.ambiguous(ident.Name) {
		// A duplicate name is reported at its declarations, not at every
		// call of it — and calling one is neither undefined nor checkable,
		// since which declaration's signature applies is undecided.
		if !found {
			c.reportUndefined(start, "name", ident.Name)
		}
		for _, a := range e.Args {
			c.inferExpr(a.Value, env)
		}
		return types.Unknown{}
	}
	return c.callDecl(decl, ident.Name, e.Args, start, env)
}

// callDecl type-checks a call against an already-resolved declaration —
// shared by inferCall (a bare, local name) and inferQualifiedCall (an
// aliased name, possibly from an imported package, Milestone 6) since the
// composition-matrix and argument checks are identical either way; only
// how the declaration itself was found differs. For a cross-package
// decl, the caller wraps this in callInScope first, so c.resolve (used
// for decl's own field/param/return types below) resolves against
// decl's own package, not the caller's.
func (c *checker) callDecl(decl syntax.Decl, name string, args []syntax.Arg, start lexer.Pos, env map[string]types.Type) types.Type {
	switch decl := decl.(type) {
	case *syntax.ModelDecl:
		c.checkFieldArgs(decl.Fields, args, name, start, env, true)
		if syntax.Decl(decl) == types.CookieType.Decl {
			c.checkCookieConstants(args, start)
		}
		return types.Named{Name: name, Decl: decl}
	case *syntax.EnumDecl:
		return c.checkKeyLookup(decl, name, args, start, env)
	case *syntax.RuleDecl:
		c.checkEffect(decl, kindRule, name, start)
		c.checkFieldArgs(paramsToFields(decl.Params), args, name, start, env, false)
		return c.resolve(decl.ReturnType)
	case *syntax.ConversionDecl:
		c.checkEffect(decl, kindConversion, name, start)
		c.checkFieldArgs(paramsToFields(decl.Params), args, name, start, env, false)
		return c.resolve(decl.ReturnType)
	case *syntax.ActivityDecl:
		c.checkEffect(decl, kindActivity, name, start)
		if c.activeTransaction != "" {
			c.report(start, "C5", "activity %q may not be called inside a transaction block", name)
		}
		c.checkFieldArgs(paramsToFields(decl.Params), args, name, start, env, false)
		return c.resolve(decl.ReturnType)
	case *syntax.GatewayDecl:
		c.checkEffect(decl, kindGateway, name, start)
		c.checkTransactionForGateway(decl, start)
		c.checkFieldArgs(paramsToFields(decl.Params), args, name, start, env, false)
		return c.resolve(decl.ReturnType)
	case *syntax.WorkflowDecl:
		c.checkEffect(decl, kindWorkflow, name, start)
		if c.activeTransaction != "" {
			c.report(start, "C5", "workflow %q may not be called inside a transaction block", name)
		}
		c.checkFieldArgs(paramsToFields(decl.Params), args, name, start, env, false)
		return c.resolve(decl.ReturnType)
	case *syntax.HelperDecl:
		// Only reachable from inside its own file (ADR 0054), where every
		// call is allowed, so there is no composition check to make.
		c.checkFieldArgs(paramsToFields(decl.Params), args, name, start, env, false)
		return c.resolve(decl.ReturnType)
	default:
		c.report(start, "", "%q is not callable", name)
		return types.Unknown{}
	}
}

// inferQualifiedCall handles `Target.Field(args)` — constructing a named
// enum variant (CustomerError.NotFound(id)), a call into an imported
// package's own declaration (businessTime.addBusinessDays(...),
// Milestone 6, ADR 0032), or an opaque capability call
// (Database.queryOne(...)) when Target resolves to neither. No
// capability-contract system exists yet (Milestone 5), so the last case
// is deliberately unchecked rather than guessed at.
func (c *checker) inferQualifiedCall(fa *syntax.FieldAccess, args []syntax.Arg, start lexer.Pos, env map[string]types.Type) types.Type {
	targetIdent, ok := fa.Target.(*syntax.Ident)
	if !ok {
		return c.inferMethodCall(c.inferExpr(fa.Target, env), fa, args, start, env)
	}
	if _, isLocal := env[targetIdent.Name]; !isLocal {
		if targetIdent.Name == types.MathGroup {
			return c.callMath(fa.Field, args, start, env)
		}
		if g, found := c.formulasImport(targetIdent.Name); found {
			return c.callFormula(g, targetIdent.Name, fa.Field, args, start, env)
		}
	}
	if decl, found := c.lookup(targetIdent.Name); found {
		if enumDecl, ok := decl.(*syntax.EnumDecl); ok {
			for _, v := range enumDecl.Variants {
				if v.Name == fa.Field {
					c.checkFieldArgs(v.Fields, args, targetIdent.Name+"."+fa.Field, start, env, true)
					return types.Named{Name: enumDecl.Name, Decl: enumDecl}
				}
			}
			c.report(start, "", "%s has no variant %q", targetIdent.Name, fa.Field)
			return types.Unknown{}
		}
		if group, ok := decl.(*syntax.CapabilityDecl); ok {
			return c.callGroup(group, nil, targetIdent.Name, fa.Field, args, start, env)
		}
	}
	if g, found := c.groups[targetIdent.Name]; found && g.Decl != nil {
		return c.callGroup(g.Decl, g.Package, targetIdent.Name, fa.Field, args, start, env)
	}
	if pkg, found := c.imports[targetIdent.Name]; found {
		if decl, declFound := pkg.Universe.Lookup(fa.Field); declFound {
			qualifiedName := targetIdent.Name + "." + fa.Field
			if _, isGateway := decl.(*syntax.GatewayDecl); isGateway {
				c.report(start, "P4", "gateway %s is not in a capability group, so it is private to its package; only a package's capability groups are reachable from outside it (ADR 0043 §5)", qualifiedName)
				for _, a := range args {
					c.inferExpr(a.Value, env)
				}
				return types.Unknown{}
			}
			return c.callInScope(pkg, func() types.Type {
				return c.callDecl(decl, qualifiedName, args, start, env)
			})
		}
		c.report(start, "", "package %q has no declaration %q", targetIdent.Name, fa.Field)
		for _, a := range args {
			c.inferExpr(a.Value, env)
		}
		return types.Unknown{}
	}
	// A local binding (`email.contains("@")`) is a builtin method call on
	// that value, not a capability — before ADR 0055 every attested
	// receiver was `x.value`, so a bare-local receiver was never reached.
	if t, isLocal := env[targetIdent.Name]; isLocal {
		if iface, ok := types.Interface(t); ok {
			return c.callProvider(iface, targetIdent.Name, fa.Field, args, start, env)
		}
		return c.inferMethodCall(c.inferExpr(fa.Target, env), fa, args, start, env)
	}
	if capability.IsPrimitive(targetIdent.Name) {
		return c.callPrimitive(targetIdent.Name, fa.Field, args, start, env)
	}
	if decl, found := c.lookup(targetIdent.Name); found {
		if g, ok := decl.(*syntax.FormulasDecl); ok {
			path := c.ownPath
			if path == "" {
				path = "<package path>"
			}
			c.report(start, "", "formulas group %s is not imported in this file; add `use %s.%s`, even in its own package (ADR 0049 §2.5)", g.Name, path, g.Name)
			for _, a := range args {
				c.inferExpr(a.Value, env)
			}
			return types.Unknown{}
		}
	}
	// Not a known declaration or import alias: an opaque capability call.
	for _, a := range args {
		c.inferExpr(a.Value, env)
	}
	return types.Unknown{}
}

// checkCopy is the checker side of the `copy(base, field: value, ...)`
// prelude builtin (docs/specification/ownership.md): produces a value of
// base's own model type, with every field not named among the remaining
// arguments taken unchanged from base, and every named argument
// overriding exactly that one field. Checked specially here, the same way
// Ok/Err are, since copy has no fixed declared signature the way an
// ordinary call's callee does — which fields it even accepts depends on
// whatever model base turns out to be, discovered only by inferring it.
// Unlike Ok/Err, copy's result type never depends on an expected type from
// context — it's always exactly base's own type — so it needs no
// checkExpected special-casing, only this one inferCall branch.
func (c *checker) checkCopy(call *syntax.CallExpr, env map[string]types.Type) types.Type {
	start, _ := call.Bounds()
	if len(call.Args) == 0 {
		c.report(start, "", "copy(...) requires a base value as its first argument")
		return types.Unknown{}
	}
	base := call.Args[0]
	if base.Name != "" {
		baseStart, _ := base.Value.(syntax.Spanned).Bounds()
		c.report(baseStart, "", "copy(...)'s first argument is the base value and must not be named")
	}
	baseType := c.inferExpr(base.Value, env)

	var modelDecl *syntax.ModelDecl
	if named, ok := baseType.(types.Named); ok {
		modelDecl, _ = named.Decl.(*syntax.ModelDecl)
	}
	if modelDecl == nil {
		if _, isUnknown := baseType.(types.Unknown); !isUnknown {
			baseStart, _ := base.Value.(syntax.Spanned).Bounds()
			c.report(baseStart, "", "copy(...)'s first argument must be a model value, got %s", baseType)
		}
		for _, a := range call.Args[1:] {
			c.inferExpr(a.Value, env)
		}
		return baseType
	}

	for _, a := range call.Args[1:] {
		if a.Name == "" {
			argStart, _ := a.Value.(syntax.Spanned).Bounds()
			c.report(argStart, "", "copy(...) overrides must be named (e.g. name: value), not positional")
			c.inferExpr(a.Value, env)
			continue
		}
		var fieldType types.Type
		found := false
		for _, f := range modelDecl.AllFields() {
			if f.Name == a.Name {
				fieldType = c.resolve(f.Type)
				found = true
				break
			}
		}
		if !found {
			argStart, _ := a.Value.(syntax.Spanned).Bounds()
			c.report(argStart, "", "%s has no field %q", modelDecl.Name, a.Name)
			c.inferExpr(a.Value, env)
			continue
		}
		c.checkExpected(a.Value, fieldType, env)
	}
	return baseType
}

// isOptional reports whether t is a union including None (§3.5: "model
// fields are required unless their type includes None").
func (c *checker) isOptional(t types.Type) bool {
	u, ok := t.(types.Union)
	if !ok {
		return false
	}
	for _, m := range u.Members {
		if _, isNone := m.(types.NoneType); isNone {
			return true
		}
	}
	return false
}

// withoutNone is t with its None member removed, when t is a union that
// has one: the type a bare-name match arm binds.
func withoutNone(t types.Type) types.Type {
	u, ok := t.(types.Union)
	if !ok {
		return t
	}
	var rest []types.Type
	for _, m := range u.Members {
		if _, isNone := m.(types.NoneType); !isNone {
			rest = append(rest, m)
		}
	}
	switch {
	case len(rest) == len(u.Members):
		return t
	case len(rest) == 1:
		return rest[0]
	default:
		return types.Union{Members: rest}
	}
}

func paramsToFields(params []syntax.Param) []syntax.Field {
	fields := make([]syntax.Field, len(params))
	for i, p := range params {
		fields[i] = syntax.Field{Span: p.Span, Name: p.Name, Type: p.Type}
	}
	return fields
}

// matchArgs pairs each arg with the field/param it supplies, by name when
// named and by position (filling whichever names haven't been claimed yet,
// in declared order) otherwise — grammar.md: "never mixed in the corpus,
// nothing forbids it." Reports an unknown-name error for a named arg that
// doesn't match anything, and a too-many-arguments error for excess
// positional args.
func (c *checker) matchArgs(names []string, args []syntax.Arg, subject string, at lexer.Pos) map[string]syntax.Expr {
	supplied := map[string]syntax.Expr{}
	nameSet := make(map[string]bool, len(names))
	for _, n := range names {
		nameSet[n] = true
	}
	posIdx, named := 0, false
	for _, a := range args {
		if a.Name != "" {
			named = true
			start, _ := a.Value.(syntax.Spanned).Bounds()
			if !nameSet[a.Name] {
				c.report(start, "", "%s has no parameter named %q", subject, a.Name)
				continue
			}
			if _, dup := supplied[a.Name]; dup {
				c.report(start, "", "%s's %q is given twice", subject, a.Name)
				continue
			}
			supplied[a.Name] = a.Value
			continue
		}
		if named {
			start, _ := a.Value.(syntax.Spanned).Bounds()
			c.report(start, "", "a positional argument to %s follows a named one; name it, or move it before the named arguments", subject)
			continue
		}
		for posIdx < len(names) {
			if _, taken := supplied[names[posIdx]]; !taken {
				break
			}
			posIdx++
		}
		if posIdx >= len(names) {
			start, _ := a.Value.(syntax.Spanned).Bounds()
			c.report(start, "", "too many arguments to %s", subject)
			continue
		}
		supplied[names[posIdx]] = a.Value
		posIdx++
	}
	return supplied
}

// checkFieldArgs checks a call's arguments against a field/param list —
// used for ordinary calls (requireAll=false: a missing non-model argument
// isn't Category A's scope) and for model/enum-variant construction
// (requireAll=true: bug class A3, a missing required field).
func (c *checker) checkFieldArgs(fields []syntax.Field, args []syntax.Arg, name string, at lexer.Pos, env map[string]types.Type, requireAll bool) {
	names := make([]string, len(fields))
	fieldTypes := make(map[string]types.Type, len(fields))
	for i, f := range fields {
		names[i] = f.Name
		fieldTypes[f.Name] = c.resolve(f.Type)
	}
	supplied := c.matchArgs(names, args, name, at)
	for _, n := range names {
		expr, ok := supplied[n]
		if !ok {
			if requireAll && !c.isOptional(fieldTypes[n]) {
				c.report(at, "A3", "%s.%s is required and not supplied", name, n)
			}
			continue
		}
		c.inCallerScope(func() { c.checkExpected(expr, fieldTypes[n], env) })
	}
}

func (c *checker) bindPattern(p syntax.Pattern, subject types.Type, env map[string]types.Type) {
	switch p := p.(type) {
	case *syntax.WildcardPattern:
		return
	case *syntax.NamePattern:
		if len(p.Path) == 1 && p.Path[0] != "None" {
			// Against `T | None` a bare name covers only the non-None
			// remainder (grammar.md §5; patternCoverage), so it binds T.
			subject = withoutNone(subject)
			env[p.Path[0]] = subject
			c.checkLocalName(p.Path[0], subject, p.Start)
		}
	case *syntax.ConstructorPattern:
		c.bindConstructorPattern(p, subject, env)
	case *syntax.BytesPattern:
		c.bindBytesPattern(p, subject, env)
	}
}

func (c *checker) bindConstructorPattern(p *syntax.ConstructorPattern, subject types.Type, env map[string]types.Type) {
	if len(p.Path) == 1 && (p.Path[0] == "Ok" || p.Path[0] == "Err") {
		if g, ok := subject.(types.Generic); ok && g.Name == "Result" && len(g.Args) == 2 {
			inner := g.Args[0]
			if p.Path[0] == "Err" {
				inner = g.Args[1]
			}
			if len(p.Args) == 1 {
				c.bindPattern(p.Args[0], inner, env)
			}
			return
		}
	}
	if len(p.Path) == 2 {
		if decl, found := c.lookup(p.Path[0]); found {
			if enumDecl, ok := decl.(*syntax.EnumDecl); ok {
				for _, v := range enumDecl.Variants {
					if v.Name == p.Path[1] {
						for i, sub := range p.Args {
							if i < len(v.Fields) {
								c.bindPattern(sub, c.resolve(v.Fields[i].Type), env)
							}
						}
						return
					}
				}
			}
		}
	}
	// Fallback: bind every sub-pattern to Unknown rather than leaving
	// variables unbound, which would surface as a spurious "undefined
	// name" later on.
	for _, sub := range p.Args {
		c.bindPattern(sub, types.Unknown{}, env)
	}
}
