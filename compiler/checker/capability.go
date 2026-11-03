package checker

import (
	"strings"
	"unicode"

	"fiig-lang/compiler/capability"
	"fiig-lang/compiler/lexer"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// ADR 0043: capability groups, interfaces, and provider values.
//
// A capability is a primitive or a `capability` group, local or imported by
// path tail (`use acme.http.Client`). A gateway reaches the one it names in
// `requires`; an activity or workflow calls any group directly. A group used
// as a value is a provider, assignable to every interface its manifest says
// it provides. The bug classes:
//
//   - G4: an import alias shares a name with a local binding, a package-level
//     declaration, or (§6b) a capitalized binding with a declaration.
//   - N1: a name's case contradicts what it names — a capability group or
//     interface not capitalized, a group alias not keeping its group's case,
//     a package alias capitalized.
//   - N2: a member's case contradicts its kind — an enum variant not
//     capitalized, a capability operation or interface member capitalized.
//   - N3: a binding holding a capability is not capitalized, or one that
//     doesn't is.
//   - P1/P2: a `provides` entry the group doesn't cover, or covers with a
//     different signature (types.BindProvides).
//   - P3: a rule or conversion invokes a provider.
//   - P4: another package references a gateway that isn't in a group.
//   - P5: capability groups that require each other in a cycle.
//   - P6: a model or enum field holding a capability.
//   - P7: a gateway uses a capability other than the one it requires.

func isUpper(name string) bool {
	for _, r := range name {
		return unicode.IsUpper(r)
	}
	return false
}

// ResolveFileGroups turns file's path-tail imports (`use acme.http.Client`,
// `use standard.geometry.vector`) into alias -> group, the counterpart of
// ResolveFileImports for imports that name a capability group (ADR 0043 §5)
// or a formulas group (ADR 0049 §2.5) inside a package rather than the
// package itself. The package is the longest prefix that is a loaded package; with
// only direct dependencies loaded, that is the path minus its last segment.
func ResolveFileGroups(file *syntax.File, packages types.Packages) map[string]*types.GroupRef {
	if len(packages) == 0 {
		return nil
	}
	groups := map[string]*types.GroupRef{}
	for _, imp := range file.Imports {
		pkg, tail, ok := groupImport(imp, packages)
		if !ok {
			continue
		}
		if d, found := pkg.Universe.Lookup(tail); found {
			switch g := d.(type) {
			case *syntax.CapabilityDecl:
				groups[importAlias(imp)] = &types.GroupRef{Package: pkg, Decl: g}
			case *syntax.FormulasDecl:
				groups[importAlias(imp)] = &types.GroupRef{Package: pkg, Formulas: g}
			}
		}
	}
	return groups
}

// groupImport splits imp into a loaded package and the name after it, when
// imp names something inside a package rather than the package itself.
func groupImport(imp *syntax.Import, packages types.Packages) (*types.Package, string, bool) {
	if len(imp.Path) < 2 {
		return nil, "", false
	}
	if _, isPackage := packages[strings.Join(imp.Path, ".")]; isPackage {
		return nil, "", false
	}
	pkg, ok := packages[strings.Join(imp.Path[:len(imp.Path)-1], ".")]
	if !ok {
		return nil, "", false
	}
	return pkg, imp.Path[len(imp.Path)-1], true
}

// checkImportNames checks every import alias against what it names (N1) and
// against the package's declarations (G4), and reports a path-tail import
// that names something other than a capability group.
func (c *checker) checkImportNames(file *syntax.File, packages types.Packages) {
	for _, imp := range file.Imports {
		alias := importAlias(imp)
		if _, dup := c.aliases[alias]; !dup {
			c.aliases[alias] = imp
		}
		path := strings.Join(imp.Path, ".")
		c.mathNameTaken(alias, imp.Start, "import alias", "alias the import: use "+path+" as "+alias+"Other")
		if g, ok := c.groups[alias]; ok && g.Formulas != nil {
			if isUpper(alias) {
				c.report(imp.Start, "N1", "alias %q must start lowercase, like formulas group %s, which it names (ADR 0050 §2.6)", alias, g.Formulas.Name)
			}
		} else if ok {
			if !isUpper(alias) {
				c.report(imp.Start, "N1", "alias %q must keep the case of capability group %s, which it names: capability names are capitalized (ADR 0043 §5c)", alias, g.Decl.Name)
			}
		} else if _, ok := c.imports[alias]; ok {
			if isUpper(alias) {
				c.report(imp.Start, "N1", "package alias %q must be lowercase; a package is never itself a capability, so import the capability group you need instead (use %s.GroupName)", alias, path)
			}
		} else if pkg, tail, ok := groupImport(imp, packages); ok {
			if d, found := pkg.Universe.Lookup(tail); found {
				if _, isGateway := d.(*syntax.GatewayDecl); isGateway {
					c.report(imp.Start, "P4", "gateway %q is not in a capability group, so it is private to its package; only a package's capability groups are reachable from outside it (ADR 0043 §5)", tail)
				} else {
					c.report(imp.Start, "", "%q names a %s; an import names a package, or one of its capability or formulas groups", path, syntax.DeclKindName(d))
				}
			} else {
				c.report(imp.Start, "", "package %q has no capability or formulas group %q", strings.Join(imp.Path[:len(imp.Path)-1], "."), tail)
			}
		}
		if d, found := c.universe.Lookup(alias); found && !c.ambiguous(alias) && !c.mayBeTheImportedGroup(imp, d) && !c.isImportOf(alias, d) {
			c.report(imp.Start, "G4", "import alias %q has the same name as %s %q in this package; alias the import: use %s as %sOther", alias, syntax.DeclKindName(d), alias, path, alias)
		}
	}
}

// mayBeTheImportedGroup reports whether d might be the very group imp
// imports. That happens only when imp is unresolved — no package context,
// as when the whole examples/ corpus is checked as one universe — and d is
// a capability group named for imp's tail; there is then no telling the
// importing package's own declaration from the imported one, so G4 stays
// silent rather than guess.
func (c *checker) mayBeTheImportedGroup(imp *syntax.Import, d syntax.Decl) bool {
	g, isGroup := d.(*syntax.CapabilityDecl)
	if !isGroup || g.Name != imp.Path[len(imp.Path)-1] {
		return false
	}
	alias := importAlias(imp)
	_, resolvedGroup := c.groups[alias]
	_, resolvedPackage := c.imports[alias]
	return !resolvedGroup && !resolvedPackage
}

// isImportOf reports whether alias imports d itself: a formulas group of
// this package, which §2.5 imports like any other (ADR 0049), so its alias
// and its declaration are one name, not two.
func (c *checker) isImportOf(alias string, d syntax.Decl) bool {
	g, ok := c.formulasImport(alias)
	return ok && syntax.Decl(g.Formulas) == d
}

// checkLocalName checks a name a declaration introduces — a parameter, a
// binding, a loop or pattern variable — against the file's import aliases
// (G4), its case against its type (N3), and, for a capitalized name, against
// the package's declarations (G4, §6b).
func (c *checker) checkLocalName(name string, t types.Type, at lexer.Pos) {
	if name == "" || name == "_" {
		return
	}
	c.mathNameTaken(name, at, "local name", "rename this")
	if imp, ok := c.aliases[name]; ok {
		c.report(at, "G4", "%q is already the alias of import %s in this file; rename this, or alias the import (use %s as Other)", name, strings.Join(imp.Path, "."), strings.Join(imp.Path, "."))
		if !c.g4Reported[imp] {
			c.g4Reported[imp] = true
			c.report(imp.Start, "G4", "import alias %q is also bound as a local name in this file; a bare name must mean one thing, so alias the import or rename the local", name)
		}
	}
	holds := types.HoldsCapability(t)
	upper := isUpper(name)
	_, unknown := t.(types.Unknown)
	switch {
	case holds && !upper:
		c.report(at, "N3", "%q holds a capability (%s), so it must be capitalized; a lowercase name never reaches the outside world (ADR 0043 §6b)", name, t)
	case !holds && upper && !unknown:
		c.report(at, "N3", "%q is capitalized but holds %s; only a binding that holds a capability is capitalized (ADR 0043 §6b)", name, t)
	}
	if upper {
		if d, found := c.universe.Lookup(name); found && !c.ambiguous(name) {
			c.report(at, "G4", "%q has the same name as %s %q in this package; rename one", name, syntax.DeclKindName(d), name)
		}
	}
}

// groupValue resolves a bare name used as a value to a capability group: a
// provider (ADR 0043 §3).
func (c *checker) groupValue(name string) (types.Type, bool) {
	if g, ok := c.groups[name]; ok && g.Decl != nil {
		return types.Named{Name: name, Decl: g.Decl}, true
	}
	if d, found := c.lookup(name); found {
		if g, ok := d.(*syntax.CapabilityDecl); ok {
			return types.Named{Name: name, Decl: g}, true
		}
	}
	return nil, false
}

// checkRequires checks that a gateway's `requires` names a capability: a
// primitive, a capability group, or (until ADR 0042's grandfather list
// empties) a host-backed capability the loader vets. A package alias or a
// declaration of another kind is not one.
func (c *checker) checkRequires(g *syntax.GatewayDecl) {
	name := g.Requires
	if name == "" || capability.IsPrimitive(name) {
		return
	}
	if ref, ok := c.groups[name]; ok {
		if ref.Formulas != nil {
			c.report(g.Start, "", "gateway %q requires %q, which is a formulas group, not a capability; call its formulas directly", g.Name, name)
		}
		return
	}
	if _, ok := c.imports[name]; ok {
		c.report(g.Start, "", "gateway %q requires %q, which is a package, not a capability; import the capability group it needs (use path.GroupName)", g.Name, name)
		return
	}
	if d, found := c.lookup(name); found {
		if _, ok := d.(*syntax.CapabilityDecl); !ok {
			c.report(g.Start, "", "gateway %q requires %s %q, which is not a capability", g.Name, syntax.DeclKindName(d), name)
		}
	}
}

// checkGroupCycle is P5: a group whose gateways require, directly or through
// other groups in this package, the group itself. Between packages the
// loader already rejects dependency cycles.
func (c *checker) checkGroupCycle(cd *syntax.CapabilityDecl) {
	seen := map[*syntax.CapabilityDecl]bool{}
	var reaches func(g *syntax.CapabilityDecl) bool
	reaches = func(g *syntax.CapabilityDecl) bool {
		for _, gw := range g.Gateways {
			d, found := c.lookup(gw.Requires)
			next, isGroup := d.(*syntax.CapabilityDecl)
			if !found || !isGroup {
				continue
			}
			if next == cd {
				return true
			}
			if !seen[next] {
				seen[next] = true
				if reaches(next) {
					return true
				}
			}
		}
		return false
	}
	if reaches(cd) {
		c.report(cd.Start, "P5", "capability %s requires itself, directly or through other capability groups; a capability's gateways must bottom out at primitives", cd.Name)
	}
}

// checkCapabilityDecl checks a capability group: its name, and each member
// gateway as a gateway.
func (c *checker) checkCapabilityDecl(d *syntax.CapabilityDecl) {
	if !isUpper(d.Name) {
		c.report(d.Start, "N1", "capability %q must be capitalized; it denotes a capability (ADR 0043 §5c)", d.Name)
	}
	seen := map[string]bool{}
	for _, gw := range d.Gateways {
		if isUpper(gw.Name) {
			c.report(gw.Start, "N2", "capability operation %s.%s must start lowercase, so `Upper.lower(...)` always reads as a capability call (ADR 0043 §6a)", d.Name, gw.Name)
		}
		if seen[gw.Name] {
			c.report(gw.Start, "", "capability %s declares %q twice", d.Name, gw.Name)
		}
		seen[gw.Name] = true
		c.checkDecl(gw)
	}
	c.checkGroupCycle(d)
}

// checkInterfaceDecl checks an interface: its name, its members' names, and
// their signatures' types.
func (c *checker) checkInterfaceDecl(d *syntax.InterfaceDecl) {
	if !isUpper(d.Name) {
		c.report(d.Start, "N1", "interface %q must be capitalized; it is a type (ADR 0050 §2.1)", d.Name)
	}
	seen := map[string]bool{}
	for _, m := range d.Members {
		if isUpper(m.Name) {
			c.report(m.Start, "N2", "interface member %s.%s must start lowercase; it is an operation (ADR 0043 §6a)", d.Name, m.Name)
		}
		if seen[m.Name] {
			c.report(m.Start, "", "interface %s declares %q twice", d.Name, m.Name)
		}
		seen[m.Name] = true
		for _, p := range m.Params {
			c.checkLocalName(p.Name, c.checkPublicType(p.Type), p.Start)
		}
		c.checkPublicType(m.ReturnType)
	}
}

// checkNoCapabilityField is P6: a model is data, and data never carries a
// capability (ADR 0043 §6c). Choosing a provider is a rule's job, made from
// the data, not stored in it.
func (c *checker) checkNoCapabilityField(owner string, f syntax.Field, t types.Type) {
	if types.HoldsCapability(t) {
		c.report(f.Start, "P6", "%s.%s holds a capability (%s); a model is data and never carries one — providers can use models, but models don't dictate how they are used (ADR 0043 §6c)", owner, f.Name, t)
	}
}

// pkgOf finds the imported package that declares decl, or nil when decl is
// this package's own.
func (c *checker) pkgOf(decl syntax.Decl) *types.Package {
	name := syntax.DeclName(decl)
	if d, found := c.universe.Lookup(name); found && d == decl {
		return nil
	}
	for _, pkg := range c.imports {
		if pkg.Universe.Decls[name] == decl {
			return pkg
		}
	}
	for _, g := range c.groups {
		if g.Package.Universe.Decls[name] == decl {
			return g.Package
		}
	}
	return nil
}

// checkArgs checks a call's arguments against params resolved in scope (nil
// for this package), while the arguments themselves are checked here, in
// the caller's scope.
func (c *checker) checkArgs(params []syntax.Param, scope *types.Package, args []syntax.Arg, subject string, at lexer.Pos, env map[string]types.Type) {
	names := make([]string, len(params))
	want := make(map[string]types.Type, len(params))
	for i, p := range params {
		names[i] = p.Name
		want[p.Name] = c.resolveIn(scope, p.Type)
	}
	for n, e := range c.matchArgs(names, args, subject, at) {
		c.checkExpected(e, want[n], env)
	}
}

func (c *checker) resolveIn(scope *types.Package, t syntax.Type) types.Type {
	if scope == nil {
		return c.resolve(t)
	}
	return c.callInScope(scope, func() types.Type { return c.resolve(t) })
}

// callGroup checks `Group.op(args)`: a call to one of a capability group's
// gateways, through the group (via is the name the caller used for it). A
// gateway may use only the group it requires (P7); otherwise it is judged
// like any gateway call.
func (c *checker) callGroup(group *syntax.CapabilityDecl, scope *types.Package, via, member string, args []syntax.Arg, start lexer.Pos, env map[string]types.Type) types.Type {
	gw := group.Gateway(member)
	if gw == nil {
		c.report(start, "", "capability %s has no operation %q", via, member)
		for _, a := range args {
			c.inferExpr(a.Value, env)
		}
		return types.Unknown{}
	}
	name := via + "." + member
	if c.callerKind == kindGateway {
		if c.requires != via {
			c.report(start, "P7", "this gateway requires %s, so it may not use %s; a gateway uses exactly the one capability it requires (ADR 0010)", c.requires, via)
		}
	} else {
		c.checkEffect(gw, kindGateway, name, start)
	}
	c.checkTransactionForGateway(gw, start)
	c.checkArgs(gw.Params, scope, args, name, start, env)
	return c.resolveIn(scope, gw.ReturnType)
}

// callProvider checks `Provider.op(args)` on a value of an interface type:
// a dispatch to whichever group was chosen. Only an activity or workflow may
// make one (ADR 0043 §4).
func (c *checker) callProvider(iface *syntax.InterfaceDecl, via, member string, args []syntax.Arg, start lexer.Pos, env map[string]types.Type) types.Type {
	var m *syntax.InterfaceMember
	for i := range iface.Members {
		if iface.Members[i].Name == member {
			m = &iface.Members[i]
		}
	}
	if m == nil {
		c.report(start, "", "interface %s has no operation %q", iface.Name, member)
		for _, a := range args {
			c.inferExpr(a.Value, env)
		}
		return types.Unknown{}
	}
	switch c.callerKind {
	case kindRule, kindConversion, kindFormula:
		c.report(start, "P3", "a %s may select and return a provider but may not invoke one; call %s.%s from an activity (ADR 0043 §4)", c.callerKind, via, member)
	case kindGateway:
		c.report(start, "P7", "this gateway requires %s, so it may not use provider %s; a gateway uses exactly the one capability it requires (ADR 0010)", c.requires, via)
	}
	scope := c.pkgOf(iface)
	c.checkArgs(m.Params, scope, args, via+"."+member, start, env)
	return c.resolveIn(scope, m.ReturnType)
}

// commonInterface is the one interface every group in gs provides, so a
// literal `[Stripe, Square]` can be typed List<PaymentProvider>.
func commonInterface(gs ...*syntax.CapabilityDecl) (*syntax.InterfaceDecl, bool) {
	var found *syntax.InterfaceDecl
	for _, cand := range gs[0].Provides {
		all := true
		for _, g := range gs[1:] {
			has := false
			for _, p := range g.Provides {
				has = has || p == cand
			}
			all = all && has
		}
		if all {
			if found != nil {
				return nil, false
			}
			found = cand
		}
	}
	return found, found != nil
}
