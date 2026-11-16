package runtime

import (
	"context"

	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// headerConstants evaluates a headed enum variant's row (ADR 0046 §4). The
// checker admits only constants (§4a), so evaluating them needs no locals
// and cannot reach a capability.
func (in *Interp) headerConstants(ctx context.Context, d *syntax.EnumDecl, variant syntax.EnumVariant) (map[string]Value, *Fault) {
	consts := make(map[string]Value, len(d.Header))
	for _, f := range d.Header {
		expr, ok := variant.Value(f.Name)
		if !ok {
			return nil, Faultf("%s.%s does not supply header field %q", d.Name, variant.Name, f.Name)
		}
		v, _, fault := in.evalExpr(ctx, expr, NewEnv())
		if fault != nil {
			return nil, fault
		}
		consts[f.Name] = v
	}
	return consts, nil
}

// lookupByKey evaluates `DeductionSource(code: raw)` (ADR 0046 §5): the
// variant whose key field equals raw, or, for a list key, contains it;
// None when no variant does. The checker guarantees one named key argument
// and unique key values. The argument is evaluated in the caller's ctx; the
// header constants in declCtx, the enum's own package.
func (in *Interp) lookupByKeyIn(ctx, declCtx context.Context, d *syntax.EnumDecl, args []syntax.Arg, env *Env) (Value, bool, *Fault) {
	if len(args) != 1 || args[0].Name == "" {
		return nil, false, Faultf("a lookup on %s takes exactly one named key argument", d.Name)
	}
	field, ok := d.HeaderField(args[0].Name)
	if !ok || !field.Key {
		return nil, false, Faultf("%s.%s is not a key field", d.Name, args[0].Name)
	}
	raw, earlyReturn, fault := in.evalExpr(ctx, args[0].Value, env)
	if fault != nil || earlyReturn {
		return raw, earlyReturn, fault
	}
	for _, variant := range d.Variants {
		v, _, fault := in.constructEnum(declCtx, d, variant, nil, env)
		if fault != nil {
			return nil, false, fault
		}
		if keyMatches(v.(EnumValue).Consts[field.Name], raw) {
			return v, false, nil
		}
	}
	return None{}, false, nil
}

func keyMatches(key, raw Value) bool {
	if list, ok := key.(List); ok {
		for _, el := range list.Elems {
			if Equal(el, raw) {
				return true
			}
		}
		return false
	}
	return Equal(key, raw)
}

// headerConsts returns ev's header constants, evaluating them in the
// enum's own package when ev has none yet: a headed enum decoded from JSON
// carries only its variant's name (ADR 0072 §1).
func (in *Interp) headerConsts(ctx context.Context, ev EnumValue) (map[string]Value, *Fault) {
	if ev.Consts != nil || ev.Decl == nil || ev.Decl.Header == nil {
		return ev.Consts, nil
	}
	variant, ok := ev.Decl.Variant(ev.Variant)
	if !ok {
		return nil, Faultf("%s has no variant %q", ev.Decl.Name, ev.Variant)
	}
	return in.headerConstants(withScope(ctx, in.declScope(ev.Decl)), ev.Decl, variant)
}

// declScope is the scope d's own package runs in: the root package's, or
// that of the dependency that declares it.
func (in *Interp) declScope(d syntax.Decl) scope {
	name := syntax.DeclName(d)
	if in.Universe.Root().Decls[name] == d {
		return in.localScope(scope{universe: in.Universe}, name)
	}
	seen := map[*types.Package]bool{}
	var walk func(p *types.Package) (scope, bool)
	walk = func(p *types.Package) (scope, bool) {
		if p == nil || seen[p] {
			return scope{}, false
		}
		seen[p] = true
		if p.Universe != nil && p.Universe.Decls[name] == d {
			return packageScope(p), true
		}
		for _, dep := range p.Dependencies {
			if sc, ok := walk(dep); ok {
				return sc, true
			}
		}
		return scope{}, false
	}
	for _, aliases := range in.Imports {
		for _, p := range aliases {
			if sc, ok := walk(p); ok {
				return sc
			}
		}
	}
	for _, p := range in.Packages {
		if sc, ok := walk(p); ok {
			return sc
		}
	}
	return scope{universe: in.Universe}
}
