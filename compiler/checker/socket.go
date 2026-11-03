package checker

import (
	"fiig-lang/compiler/lexer"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// ADR 0060: the Socket primitive's operations, the first typed primitive
// capability calls. They come from this table, not from fiig source.

func socketResult(ok types.Type) types.Type {
	return types.Generic{Name: "Result", Args: []types.Type{ok, types.SocketErrorType}}
}

var socketOps = map[string]method{
	"connect":    {Params: []types.Type{stringType, intType}, Result: socketResult(types.ConnectionType)},
	"connectTls": {Params: []types.Type{stringType, intType}, Result: socketResult(types.ConnectionType)},
	"startTls":   {Params: []types.Type{types.ConnectionType, stringType}, Result: socketResult(types.ConnectionType)},
	"read":       {Params: []types.Type{types.ConnectionType}, Result: socketResult(bytesType)},
	"write":      {Params: []types.Type{types.ConnectionType, bytesType}, Result: socketResult(types.NoneType{})},
	"close":      {Params: []types.Type{types.ConnectionType}, Result: types.NoneType{}},
}

// callSocket types `Socket.op(args)` against socketOps. Only a gateway
// that requires Socket may make the call: B10 for any other kind, P7 for a
// gateway that requires something else.
func (c *checker) callSocket(op string, args []syntax.Arg, start lexer.Pos, env map[string]types.Type) types.Type {
	c.checkPrimitiveCaller("Socket", op, start)
	m, ok := socketOps[op]
	if !ok {
		c.report(start, "", "Socket has no operation %q; it has connect, connectTls, startTls, read, write, and close (ADR 0060)", op)
		for _, a := range args {
			c.inferExpr(a.Value, env)
		}
		return types.Unknown{}
	}
	return c.checkMethodArgs("Socket", op, m, args, start, env)
}

// checkPrimitiveCaller is where a primitive capability may be called: only
// in a gateway, and only the one that gateway requires (ADR 0010).
func (c *checker) checkPrimitiveCaller(capabilityName, op string, start lexer.Pos) {
	switch {
	case c.callerKind == kindGateway && c.requires != capabilityName:
		c.report(start, "P7", "this gateway requires %s, so it may not use %s; a gateway uses exactly the one capability it requires (ADR 0010)", c.requires, capabilityName)
	case c.callerKind != kindGateway:
		c.report(start, "B10", "a %s may not call %s.%s; only a gateway that requires %s touches it (§3.1)", c.callerKind, capabilityName, op, capabilityName)
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
