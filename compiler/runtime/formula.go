package runtime

import (
	"context"

	"fiig-lang/compiler/syntax"
)

// ADR 0049: calls into a formulas group.

// formulasGroup resolves a bare name to a formulas group in sc, and the
// scope its members run in: a group imported from another package by path
// tail, or one of this package's own. The checker has already required the
// import for the latter (§2.5); here the package's own group is found by
// name, since a package never appears among its own imports.
func (in *Interp) formulasGroup(sc scope, name string) (*syntax.FormulasDecl, scope, bool) {
	if g, ok := sc.groups[name]; ok && g.Formulas != nil {
		if g.Package == nil {
			return g.Formulas, in.localScope(sc, g.Formulas.Name), true
		}
		return g.Formulas, packageScope(g.Package), true
	}
	if d, found := sc.universe.Lookup(name); found {
		if g, ok := d.(*syntax.FormulasDecl); ok {
			return g, in.localScope(sc, g.Name), true
		}
	}
	return nil, scope{}, false
}

// callFormula calls `group.member(args)`, running the member in its group's
// scope with the group active, so its body's bare calls reach its siblings.
func (in *Interp) callFormula(ctx context.Context, g *syntax.FormulasDecl, sc scope, via, member string, args []syntax.Arg, env *Env) (Value, bool, *Fault) {
	f := g.Member(member)
	if f == nil {
		return nil, false, Faultf("formulas group %s has no formula %q", via, member)
	}
	sc.group = g
	return in.evalDeclCall(ctx, sc, via+"."+member, f, args, env)
}
