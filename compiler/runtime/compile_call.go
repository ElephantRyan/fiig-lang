package runtime

import (
	"context"

	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// ADR 0115 §4: the calls fiig code makes most, compiled: `Ok(x)` and
// `Err(x)`, a call to a declaration by its bare name, and a built-in
// method on a local value, `data.slice(a, b)`. compileCall returns nil
// for any other call, which evalCall handles.
func compileCall(e *syntax.CallExpr) cexpr {
	args := make([]cexpr, len(e.Args))
	for i, a := range e.Args {
		args[i] = compileExpr(a.Value)
	}
	fallback := func(in *Interp, ctx context.Context, env *Env) (Value, bool, *Fault) {
		return in.evalCall(ctx, e, env)
	}
	switch callee := e.Callee.(type) {
	case *syntax.Ident:
		switch callee.Name {
		case "Ok", "Err":
			if len(args) != 1 {
				return nil
			}
			ok, arg := callee.Name == "Ok", args[0]
			return func(in *Interp, ctx context.Context, env *Env) (Value, bool, *Fault) {
				v, earlyReturn, fault := arg(in, ctx, env)
				if fault != nil || earlyReturn {
					return v, earlyReturn, fault
				}
				return Result{Ok: ok, Value: v}, false, nil
			}
		case types.HttpResponseName, "copy", "append", "calls":
			return nil
		}
		return compileDeclCall(e, callee.Name, args, fallback)
	case *syntax.FieldAccess:
		target, ok := callee.Target.(*syntax.Ident)
		if !ok || target.Name == types.JsonGroup || e.HandlesConflicts || e.DecodeInto != nil {
			return nil
		}
		return compileMethodCall(e, target.Name, callee.Field, args, fallback)
	}
	return nil
}

// compileDeclCall is evalIdentCall's call to a declaration: the name is
// resolved in the running scope each time, as evalIdentCall does, and the
// compiled arguments go straight into the callee's frame. A name that
// resolves to anything but a callable declaration (a model, an enum, a
// formulas group's member) goes to evalCall.
func compileDeclCall(e *syntax.CallExpr, name string, args []cexpr, fallback cexpr) cexpr {
	return func(in *Interp, ctx context.Context, env *Env) (Value, bool, *Fault) {
		sc := in.scope(ctx)
		if sc.group != nil && sc.group.Member(name) != nil {
			return fallback(in, ctx, env)
		}
		decl, found := sc.universe.Lookup(name)
		if !found || constructible(decl) {
			return fallback(in, ctx, env)
		}
		if _, isEnum := decl.(*syntax.EnumDecl); isEnum {
			return fallback(in, ctx, env)
		}
		params, _, _, ok := callableShape(decl)
		if !ok {
			return nil, false, Faultf("%q is not callable", name)
		}
		fr, vals := newCallFrame(len(params))
		posIdx := 0
		for i, a := range e.Args {
			v, earlyReturn, fault := args[i](in, ctx, env)
			if fault != nil || earlyReturn {
				return v, earlyReturn, fault
			}
			if a.Name != "" {
				j := 0
				for j < len(params) && params[j].Name != a.Name {
					j++
				}
				if j == len(params) {
					return nil, false, Faultf("no parameter named %q", a.Name)
				}
				vals[j] = v
				continue
			}
			for posIdx < len(params) && vals[posIdx] != nil {
				posIdx++
			}
			if posIdx >= len(params) {
				return nil, false, Faultf("too many arguments")
			}
			vals[posIdx] = v
			posIdx++
		}
		v, f := in.callDeclValues(ctx, in.localScope(sc, name), name, decl, vals, fr)
		if f != nil {
			return nil, false, f
		}
		return v, false, nil
	}
}

// compileMethodCall is evalQualifiedCall's built-in method on a local
// value. A target that isn't a local, or holds a capability group, goes to
// evalCall, which reads a capability, a group, or a prelude group by name.
func compileMethodCall(e *syntax.CallExpr, target, method string, args []cexpr, fallback cexpr) cexpr {
	return func(in *Interp, ctx context.Context, env *Env) (Value, bool, *Fault) {
		t, isLocal := env.Get(target)
		if !isLocal {
			return fallback(in, ctx, env)
		}
		if _, isProvider := t.(Provider); isProvider {
			return fallback(in, ctx, env)
		}
		argVals := make([]Value, len(args))
		for i, a := range args {
			v, earlyReturn, fault := a(in, ctx, env)
			if fault != nil || earlyReturn {
				return v, earlyReturn, fault
			}
			argVals[i] = v
		}
		kind := methodKind(t)
		if sigs, ok := types.MethodsOfKind(kind); ok {
			if m, found := sigs[method]; found {
				var fault *Fault
				if argVals, fault = placeArgs(kind+"."+method, m.ParamNames(), m.Required(), e.Args, argVals); fault != nil {
					return nil, false, fault
				}
			}
		}
		v, f := evalBuiltinMethod(t, method, argVals)
		if f != nil {
			return nil, false, f
		}
		return v, false, nil
	}
}
