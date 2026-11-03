package checker

import (
	"strings"

	"fiig-lang/compiler/lexer"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// ADR 0049: the `formula` tier, grouped into `formulas` groups.
//
// A formula is pure computation every kind may call and that calls only
// formulas (§2.3). A group is addressable only through an import, even from
// its own package (§2.5), and its members call each other unqualified. The
// bug classes:
//
//   - K3: a formula returns a Result (§2.2 restriction 3).
//   - K4: a formula has a `requires` clause (§2.2 restriction 1).
//   - K5: a formula has no body. Only the prelude math group's members
//     have none, and they are a table (compiler/types), not source.
//   - B11: a formula calls something other than a formula (§2.2
//     restriction 2), through the ordinary composition matrix.

// bindOwnFormulas binds the file's imports of its own package's formulas
// groups (`use acme.geometry.vector` inside acme.geometry), which §2.5
// requires just as for another package's group. ResolveFileGroups can't see
// them, since a package is never among the packages it imports. With the
// package's path known, the import must name it; without one (the examples/
// corpus checked as one universe), an import no loaded package accounts for
// binds to a same-named local group, as mayBeTheImportedGroup does for G4.
func (c *checker) bindOwnFormulas(file *syntax.File, packages types.Packages) {
	for _, imp := range file.Imports {
		if len(imp.Path) < 2 {
			continue
		}
		alias := importAlias(imp)
		if _, bound := c.groups[alias]; bound {
			continue
		}
		prefix := strings.Join(imp.Path[:len(imp.Path)-1], ".")
		if c.ownPath != "" && prefix != c.ownPath {
			continue
		}
		if _, isPackage := packages[prefix]; isPackage {
			continue
		}
		if _, isPackage := packages[strings.Join(imp.Path, ".")]; isPackage {
			continue
		}
		d, found := c.universe.Lookup(imp.Path[len(imp.Path)-1])
		if g, ok := d.(*syntax.FormulasDecl); found && ok {
			if c.groups == nil {
				c.groups = map[string]*types.GroupRef{}
			}
			c.groups[alias] = &types.GroupRef{Formulas: g}
		}
	}
}

// formulasImport returns the formulas group a bare name is imported as in
// this file, if it is one.
func (c *checker) formulasImport(name string) (*types.GroupRef, bool) {
	g, ok := c.groups[name]
	if !ok || g.Formulas == nil {
		return nil, false
	}
	return g, true
}

// checkFormulasDecl checks a formulas group: its name, and each member as a
// formula (K3-K5), with the group in scope so members call each other
// unqualified.
func (c *checker) checkFormulasDecl(d *syntax.FormulasDecl) {
	if isUpper(d.Name) {
		c.report(d.Start, "N1", "formulas group %q must start lowercase; a call site reads it where a package alias goes, `%s.member(...)` (ADR 0049 §2.1, ADR 0050 §2.6)", d.Name, d.Name)
	}
	c.group = d
	c.callerKind = kindFormula
	seen := map[string]bool{}
	for _, f := range d.Members {
		if isUpper(f.Name) {
			c.report(f.Start, "N2", "formula %s.%s must start lowercase; it is an operation (ADR 0043 §6a)", d.Name, f.Name)
		}
		if seen[f.Name] {
			c.report(f.Start, "", "formulas group %s declares %q twice", d.Name, f.Name)
		}
		seen[f.Name] = true
		if f.Requires != "" {
			c.report(f.Start, "K4", "formula %s.%s may not require a capability (%s); a formula is pure, so it reaches nothing outside the program — that is a gateway's job (ADR 0049 §2.2)", d.Name, f.Name, f.Requires)
		}
		if f.Body == nil {
			c.report(f.Start, "K5", "formula %s.%s has no body; only the prelude math group's members, which the compiler supplies, have none (ADR 0049 §2.1)", d.Name, f.Name)
		}
		ret := c.checkCallable(f.Params, f.ReturnType, f.Body)
		if holdsResult(ret) {
			c.report(f.Start, "K3", "formula %s.%s returns %s; a formula can only fault, never fail in domain terms. A check with a reason to give is a rule (ADR 0049 §2.2)", d.Name, f.Name, ret)
		}
	}
	c.group = nil
}

// holdsResult reports whether t is a Result, or a union with one in it.
func holdsResult(t types.Type) bool {
	switch t := t.(type) {
	case types.Generic:
		return t.Name == "Result"
	case types.Union:
		for _, m := range t.Members {
			if holdsResult(m) {
				return true
			}
		}
	}
	return false
}

// callFormula checks `group.member(args)`: a call to a formula through its
// group (via is the name the caller imported it as). Every kind may call a
// formula (§2.3); scope is the package that declares the group, nil for the
// caller's own.
func (c *checker) callFormula(g *types.GroupRef, via, member string, args []syntax.Arg, start lexer.Pos, env map[string]types.Type) types.Type {
	f := g.Formulas.Member(member)
	if f == nil {
		c.report(start, "", "formulas group %s has no formula %q", via, member)
		for _, a := range args {
			c.inferExpr(a.Value, env)
		}
		return types.Unknown{}
	}
	name := via + "." + member
	c.checkEffect(f, kindFormula, name, start)
	c.checkArgs(f.Params, g.Package, args, name, start, env)
	return c.resolveIn(g.Package, f.ReturnType)
}

// callSibling checks a bare call, inside a formulas group, to another
// member of the same group (§2.1): the one place a formula resolves
// unqualified.
func (c *checker) callSibling(f *syntax.FormulaDecl, args []syntax.Arg, start lexer.Pos, env map[string]types.Type) types.Type {
	c.checkArgs(f.Params, nil, args, f.Name, start, env)
	return c.resolve(f.ReturnType)
}
