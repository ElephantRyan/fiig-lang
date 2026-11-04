package checker

import (
	"fiig-lang/compiler/capability"
	"fiig-lang/compiler/lexer"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// ADR 0060: the primitives' operations, typed from types.PrimitiveOps:
// Socket's (ADR 0060 §3) and Random's (ADR 0068 §2). Clock and FileSystem
// aren't typed yet, so their arguments are checked and the result is
// Unknown.

// callPrimitive types `Primitive.op(args)` against its table, after
// checking that the caller may use the primitive at all.
func (c *checker) callPrimitive(name, op string, args []syntax.Arg, start lexer.Pos, env map[string]types.Type) types.Type {
	c.checkPrimitiveCaller(name, op, start)
	ops, typed := types.PrimitiveOps(name)
	m, ok := ops[op]
	if !ok {
		if typed {
			c.report(start, "K1", "%s has no operation %q; its operations are %s", name, op, methodList(ops))
		}
		for _, a := range args {
			c.inferExpr(a.Value, env)
		}
		return types.Unknown{}
	}
	return c.checkMethodArgs(name, op, m, args, start, env)
}

// checkPrimitiveCaller is where a primitive may be called. A local effect
// (Clock, Random) is read directly by an activity or a gateway, and by no
// other kind (ADR 0068 §4). Any other primitive is touched only by a
// gateway that requires it (ADR 0010).
func (c *checker) checkPrimitiveCaller(name, op string, start lexer.Pos) {
	if capability.IsLocalEffect(name) {
		switch c.callerKind {
		case kindActivity, kindGateway:
		case kindWorkflow:
			c.report(start, "B10", "a workflow may not call %s.%s; call an activity that does, so the workflow stays repeatable (ADR 0068 §4)", name, op)
		default:
			c.report(start, "B10", "a %s may not call %s.%s; its result would change from call to call, so take the value as a parameter, read by the activity or gateway that calls this %s (ADR 0068 §4)", c.callerKind, name, op, c.callerKind)
		}
		return
	}
	switch {
	case c.callerKind == kindGateway && c.requires != name:
		c.report(start, "P7", "this gateway requires %s, so it may not use %s; a gateway uses exactly the one capability it requires (ADR 0010)", c.requires, name)
	case c.callerKind != kindGateway:
		c.report(start, "B10", "a %s may not call %s.%s; only a gateway that requires %s touches it (§3.1)", c.callerKind, name, op, name)
	}
}

// checkNoConnectionInSignature is H5: a Connection, or a model holding one at
// any depth, may not cross the adapter boundary, which is a workflow's
// signature (ADR 0044). It cannot be serialized, and a handle arriving from
// outside would refer to nothing (ADR 0060 §2).
func (c *checker) checkNoConnectionInSignature(params []syntax.Param, retType syntax.Type, at lexer.Pos) {
	check := func(t syntax.Type, where string) {
		if t == nil {
			return
		}
		rt, _ := types.ResolveWithImports(t, c.universe, universesOf(c.imports))
		if types.HoldsConnection(rt, c.resolveField) {
			pos := at
			if sp, ok := t.(syntax.Spanned); ok {
				pos, _ = sp.Bounds()
			}
			c.report(pos, "H5", "%s holds a Connection (%s); a workflow is the adapter boundary, and a socket handle cannot be serialized or arrive from outside (ADR 0060)", where, types.SyntaxString(t))
		}
	}
	for _, p := range params {
		check(p.Type, "parameter "+p.Name)
	}
	check(retType, "the return type")
}

// resolveField resolves a model field's type in the model's own package, for
// walks that look inside models (H5, M1).
func (c *checker) resolveField(model *syntax.ModelDecl, f syntax.Field) types.Type {
	u, imports := c.universe, universesOf(c.imports)
	if pkg := c.pkgOf(model); pkg != nil {
		u, imports = pkg.Universe, pkg.Imports
	}
	t, _ := types.ResolveWithImports(f.Type, u, imports)
	return t
}
