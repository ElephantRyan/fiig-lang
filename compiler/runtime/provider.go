package runtime

import (
	"context"

	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// ADR 0043: a capability group used as a value, and calls through one.

// Provider is a capability group used as a value (`return Stripe`), the
// runtime side of a value of an interface type. It carries the scope of the
// package that declares the group, which is where its gateways' bodies run.
type Provider struct {
	Group *syntax.CapabilityDecl
	scope scope
}

func (Provider) isValue()         {}
func (p Provider) String() string { return p.Group.Name }

// groupValue resolves a bare name to a capability group in sc: a path-tail
// import (`use acme.stripe.Stripe`) or a group declared in this package.
func (in *Interp) groupValue(sc scope, name string) (Provider, bool) {
	if g, ok := sc.groups[name]; ok && g.Decl != nil {
		return Provider{Group: g.Decl, scope: packageScope(g.Package)}, true
	}
	if d, found := sc.universe.Lookup(name); found {
		if g, ok := d.(*syntax.CapabilityDecl); ok {
			return Provider{Group: g, scope: in.localScope(sc, g.Name)}, true
		}
	}
	return Provider{}, false
}

// localScope is the scope a declaration of the root package runs in: its
// own file's imports, which the root package tracks per declaration name.
// A call from one of the root package's files into a group declared in
// another needs the group's own imports — that file is where `use
// acme.http.Client` is written — not the caller's. Outside the root package
// (sc is some dependency's scope), a package's imports are already
// flattened across its files, so sc is kept; so is a private member (ADR
// 0054), which is only ever reached from its own file.
func (in *Interp) localScope(sc scope, name string) scope {
	root := in.Universe.Root()
	if sc.universe.Root() != root {
		return sc
	}
	if _, topLevel := root.Decls[name]; !topLevel {
		return sc
	}
	return scope{universe: in.Universe, imports: in.Imports[name], groups: in.Groups[name]}
}

// packageScope is the scope a dependency package's declarations run in.
// An import keeps its whole Package when pkg lists it as a dependency, so
// a call onward from there knows its path (ADR 0079 §6a) and its imports.
func packageScope(pkg *types.Package) scope {
	imports := map[string]*types.Package{}
	for alias, u := range pkg.Imports {
		imports[alias] = &types.Package{Universe: u}
		for _, dep := range pkg.Dependencies {
			if dep.Universe == u {
				imports[alias] = dep
				break
			}
		}
	}
	return scope{universe: pkg.Universe, imports: imports, groups: pkg.Groups}
}

// callProvider calls one of a provider's gateways: `Stripe.charge(...)`
// through the group directly, or `Provider.charge(...)` through a value of
// an interface type, which is the same thing once the value is in hand.
func (in *Interp) callProvider(ctx context.Context, p Provider, member string, args []syntax.Arg, env *Env) (Value, bool, *Fault) {
	if v, earlyReturn, fault, faked := in.fakeCall(ctx, p, member, args, env); faked {
		return v, earlyReturn, fault
	}
	gw := p.Group.Gateway(member)
	if gw == nil {
		return nil, false, Faultf("capability %s has no operation %q", p.Group.Name, member)
	}
	supplied, earlyReturn, fault := in.evalArgs(ctx, env, args, paramNames(gw.Params))
	if fault != nil || earlyReturn {
		return nil, earlyReturn, fault
	}
	v, f := in.callDecl(ctx, p.scope, p.Group.Name+"."+member, gw, supplied)
	if f != nil {
		return nil, false, f
	}
	return v, false, nil
}
