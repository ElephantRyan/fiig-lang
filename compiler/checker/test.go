package checker

import (
	"fmt"
	"sort"
	"strings"

	"fiig-lang/compiler/capability"
	"fiig-lang/compiler/lexer"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// ADR 0091: tests, their fakes, `expect`, and `calls`.

// fakeKind is what a fake's target turned out to be.
type fakeKind int

const (
	fakeUnknown   fakeKind = iota
	fakeGroup              // a capability group: gateways
	fakeConfig             // a config declaration: values
	fakeClock              // Clock: `now`
	fakeRandom             // Random: `seed`
	fakeToolchain          // Database, Socket, FileSystem: declared by the toolchain
	fakeHost               // a capability with no declaration in fiig (T7)
)

// fakeTarget is a fake's target, resolved.
type fakeTarget struct {
	kind   fakeKind
	group  *syntax.CapabilityDecl
	config *syntax.ConfigDecl
	pkg    *types.Package // the declaring package; nil for this one
}

// identity is one key per thing a fake can replace, so the same target
// faked twice under two names is still found.
func (t fakeTarget) identity(name string) string {
	switch t.kind {
	case fakeGroup:
		return fmt.Sprintf("group %p", t.group)
	case fakeConfig:
		return fmt.Sprintf("config %p", t.config)
	}
	return name
}

// ResolveFake resolves a fake's target in a file's scope: a capability
// group or config declared in the package or imported by path tail, one
// named through a package alias (`core.LedgerApi`), or a primitive.
func ResolveFake(target []string, universe *types.Universe, imports map[string]*types.Package, groups map[string]*types.GroupRef) (kind fakeKind, group *syntax.CapabilityDecl, config *syntax.ConfigDecl, pkg *types.Package) {
	if len(target) == 2 {
		p, ok := imports[target[0]]
		if !ok || p.Universe == nil {
			return fakeUnknown, nil, nil, nil
		}
		switch d, _ := p.Universe.Lookup(target[1]); d := d.(type) {
		case *syntax.CapabilityDecl:
			return fakeGroup, d, nil, p
		case *syntax.ConfigDecl:
			return fakeConfig, nil, d, p
		}
		return fakeUnknown, nil, nil, nil
	}
	if len(target) != 1 {
		return fakeUnknown, nil, nil, nil
	}
	name := target[0]
	switch name {
	case "Clock":
		return fakeClock, nil, nil, nil
	case "Random":
		return fakeRandom, nil, nil, nil
	case types.DatabaseName:
		return fakeToolchain, nil, nil, nil
	}
	if g, ok := groups[name]; ok {
		if g.Decl != nil {
			return fakeGroup, g.Decl, nil, g.Package
		}
		if g.Config != nil {
			return fakeConfig, nil, g.Config, g.Package
		}
	}
	if universe != nil && !universe.IsAmbiguous(name) {
		switch d, _ := universe.Lookup(name); d := d.(type) {
		case *syntax.CapabilityDecl:
			return fakeGroup, d, nil, nil
		case *syntax.ConfigDecl:
			return fakeConfig, nil, d, nil
		}
	}
	if capability.IsPrimitive(name) {
		return fakeToolchain, nil, nil, nil
	}
	if isUpper(name) {
		return fakeHost, nil, nil, nil
	}
	return fakeUnknown, nil, nil, nil
}

func (c *checker) resolveFake(f *syntax.FakeDecl) fakeTarget {
	kind, group, config, pkg := ResolveFake(f.Target, c.universe, c.imports, c.groups)
	return fakeTarget{kind: kind, group: group, config: config, pkg: pkg}
}

// inTest reports whether the code being checked is a test's: its body,
// its private helpers, or one of its fakes' operations.
func (c *checker) inTest() bool { return c.callerKind == kindTest || c.inFake }

// checkTestDecl checks a test (ADR 0091): where it lives (T2), each fake
// against what it replaces (T4, T7), and its body as a test's.
func (c *checker) checkTestDecl(d *syntax.TestDecl) {
	if !c.inTestsFile {
		c.report(d.Start, "T2", "test %q is outside test/; a package's tests live under test/ (tests/ for a package with no manifest; ADRs 0091 §2, 0102)", d.Name)
	}
	c.callerKind = kindTest
	c.fakes, c.fakeOps = map[string]fakeTarget{}, map[string]bool{}
	seen := map[string]bool{}
	targets := make([]fakeTarget, len(d.Fakes))
	for i, f := range d.Fakes {
		t := c.resolveFake(f)
		targets[i] = t
		id := t.identity(f.TargetName())
		if seen[id] {
			c.report(f.Start, "T4", "%s is faked twice in this test", f.TargetName())
		}
		seen[id] = true
		c.fakes[f.TargetName()] = t
		for _, g := range f.Gateways {
			c.fakeOps[f.TargetName()+"."+g.Name] = true
		}
	}
	for i, f := range d.Fakes {
		c.checkFake(f, targets[i])
	}
	c.callerKind = kindTest
	c.tryTarget = nil
	c.checkBlock(d.Body, types.Unknown{}, map[string]types.Type{}, false)
}

// checkFake is T4 and T7: a fake has the shape of what it replaces, and
// replaces something declared in fiig.
func (c *checker) checkFake(f *syntax.FakeDecl, t fakeTarget) {
	name := f.TargetName()
	switch t.kind {
	case fakeUnknown:
		c.report(f.Start, "T4", "fake %s: there is no capability group, config, Clock, or Random of that name here", name)
	case fakeHost:
		if path, what := c.declaredElsewhere(name); path != "" {
			// abs-recommendations GAPS G14: declared, just not imported.
			c.report(f.Start, "T4", "%s is a %s declared in %s, which this file doesn't import; add `use %s.%s`", name, what, path, path, name)
			return
		}
		c.report(f.Start, "T7", "%s has no declaration in fiig, so a fake of it can't be checked against it; declare it as a capability group first (ADR 0091 §4)", name)
	case fakeToolchain:
		c.report(f.Start, "T7", "a fake of %s isn't built yet: every test gets a fresh in-memory Database, and a Socket comes from `fiig test -caps` (ADR 0091 implementation notes)", name)
	case fakeClock:
		c.checkFakeValues(f, map[string]types.Type{"now": types.Primitive{Name: "int"}}, nil)
	case fakeRandom:
		c.checkFakeValues(f, map[string]types.Type{"seed": types.Primitive{Name: "int"}}, nil)
	case fakeConfig:
		fields := map[string]types.Type{}
		for _, field := range t.config.Fields {
			fields[field.Name] = c.resolveIn(t.pkg, field.Type)
		}
		c.checkFakeValues(f, fields, t.config)
	case fakeGroup:
		for _, v := range f.Values {
			c.report(exprStart(v.Value), "T4", "fake %s replaces a capability group, which has operations, not values; write `gateway %s(...)`", name, v.Name)
		}
		for _, g := range f.Gateways {
			c.checkFakeGateway(name, g, t)
		}
	}
}

// checkFakeValues checks a config's, Clock's, or Random's values: each
// names a field and has its type. A Secret field takes a string literal
// here and nowhere else (ADR 0091 §4).
func (c *checker) checkFakeValues(f *syntax.FakeDecl, fields map[string]types.Type, config *syntax.ConfigDecl) {
	for _, g := range f.Gateways {
		c.report(g.Start, "T4", "fake %s takes values (`name: value`), not operations", f.TargetName())
	}
	seen := map[string]bool{}
	for _, v := range f.Values {
		at := exprStart(v.Value)
		want, ok := fields[v.Name]
		if !ok {
			c.report(at, "T4", "%s has no field %q to fake", f.TargetName(), v.Name)
			c.inferExpr(v.Value, map[string]types.Type{})
			continue
		}
		if seen[v.Name] {
			c.report(at, "T4", "%s.%s is faked twice", f.TargetName(), v.Name)
		}
		seen[v.Name] = true
		if want == types.SecretType {
			if lit, ok := v.Value.(*syntax.StringLit); !ok || hasInterpolation(lit) {
				c.report(at, "T4", "%s.%s is a Secret; its fake is a string literal (ADR 0091 §4)", f.TargetName(), v.Name)
			}
			continue
		}
		// `Secret | None` takes a literal as the Secret, or None (Hearsay G53).
		if lit, ok := v.Value.(*syntax.StringLit); ok && optionalSecret(want) {
			if hasInterpolation(lit) {
				c.report(at, "T4", "%s.%s is a Secret | None; its fake is a string literal or None (ADR 0091 §4)", f.TargetName(), v.Name)
			}
			continue
		}
		got := c.inferExpr(v.Value, map[string]types.Type{})
		if !types.AssignableTo(got, want) {
			c.report(at, "T4", "%s.%s is %s, so its fake can't be %s", f.TargetName(), v.Name, want, got)
		}
	}
}

// optionalSecret is `Secret | None`.
func optionalSecret(t types.Type) bool {
	u, ok := t.(types.Union)
	if !ok || len(u.Members) != 2 {
		return false
	}
	var secret, none bool
	for _, m := range u.Members {
		if m == types.SecretType {
			secret = true
		} else if _, ok := m.(types.NoneType); ok {
			none = true
		}
	}
	return secret && none
}

func hasInterpolation(lit *syntax.StringLit) bool {
	for _, part := range lit.Parts {
		if part.Interp != nil {
			return true
		}
	}
	return false
}

// checkFakeGateway is T4 for one faked operation: the group has it, with
// the same parameters and result; then its body is checked as a gateway's.
func (c *checker) checkFakeGateway(name string, g *syntax.GatewayDecl, t fakeTarget) {
	real := t.group.Gateway(g.Name)
	if real == nil {
		var ops []string
		for _, rg := range t.group.Gateways {
			ops = append(ops, rg.Name)
		}
		c.report(g.Start, "T4", "%s has no operation %q to fake; its operations are %s", name, g.Name, strings.Join(ops, ", "))
	} else if !c.sameSignature(g, real, t.pkg) {
		c.report(g.Start, "T4", "fake %s.%s%s -> %s doesn't match %s.%s%s -> %s (ADR 0091 §4)", name, g.Name, printSig(g.Params), types.SyntaxString(g.ReturnType), name, real.Name, printSig(real.Params), types.SyntaxString(real.ReturnType))
	}
	saved := c.callerKind
	c.callerKind, c.requires, c.inFake = kindGateway, g.Requires, true
	c.checkCallable(g.Params, g.ReturnType, g.Body)
	c.callerKind, c.requires, c.inFake = saved, "", false
}

func (c *checker) sameSignature(fake, real *syntax.GatewayDecl, pkg *types.Package) bool {
	if len(fake.Params) != len(real.Params) {
		return false
	}
	for i, p := range fake.Params {
		r := real.Params[i]
		if p.Name != r.Name || !strictEqual(c.resolveQuiet(nil, p.Type), c.resolveQuiet(pkg, r.Type)) {
			return false
		}
	}
	return strictEqual(c.resolveQuiet(nil, fake.ReturnType), c.resolveQuiet(pkg, real.ReturnType))
}

// strictEqual is types.Equal, except that a type that didn't resolve
// matches nothing: it is reported where it's written.
func strictEqual(a, b types.Type) bool {
	if _, unknown := a.(types.Unknown); unknown {
		return true
	}
	return types.Equal(a, b)
}

// resolveQuiet resolves t in pkg's scope (nil: the file's) without
// reporting; the signature's own check reports an unknown name.
func (c *checker) resolveQuiet(pkg *types.Package, t syntax.Type) types.Type {
	universe, imports := c.universe, universesOf(c.imports)
	if pkg != nil {
		universe, imports = pkg.Universe, pkg.Imports
	}
	rt, ok := types.ResolveWithImports(t, universe, imports)
	if !ok {
		return types.Unknown{}
	}
	return rt
}

func printSig(params []syntax.Param) string {
	parts := make([]string, len(params))
	for i, p := range params {
		parts[i] = p.Name + ": " + types.SyntaxString(p.Type)
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

// checkExpect is T2 and T3: `expect` is a test's, and takes a bool.
func (c *checker) checkExpect(s *syntax.ExpectStmt, env map[string]types.Type) {
	if !c.inTest() {
		c.report(s.Start, "T2", "expect is legal only in a test and its fakes (ADR 0091 §3)")
	}
	t := c.inferExpr(s.Cond, env)
	if s.Pattern != nil {
		// As a match arm does; the names it binds stay for the rest of
		// the block.
		if !c.patternFits(s.Pattern, t, env) {
			start, _ := s.Pattern.(syntax.Spanned).Bounds()
			c.report(start, "T3", "a value of type %s can never match this pattern", t)
		}
		c.bindPattern(s.Pattern, t, env)
		return
	}
	if _, unknown := t.(types.Unknown); unknown {
		return
	}
	if !types.Equal(t, types.Primitive{Name: "bool"}) {
		start, _ := s.Cond.(syntax.Spanned).Bounds()
		c.report(start, "T3", "expect takes a bool, got %s", t)
	}
}

// patternFits reports whether a value of type t could match p: an `Ok` or
// `Err` needs a Result, a variant needs its enum, `None` needs a type
// with None in it. A match's arms get this from A9, since an arm that
// can't fit leaves a case uncovered; an `is` has no other arms. A type
// the checker doesn't model fits anything.
func (c *checker) patternFits(p syntax.Pattern, t types.Type, env map[string]types.Type) bool {
	if _, unknown := t.(types.Unknown); unknown || t == nil {
		return true
	}
	var path []string
	var args []syntax.Pattern
	switch p := p.(type) {
	case *syntax.NamePattern:
		if len(p.Path) == 1 && p.Path[0] != "None" {
			return true
		}
		path = p.Path
	case *syntax.ConstructorPattern:
		path, args = p.Path, p.Args
	case *syntax.LiteralPattern:
		return literalFits(t, literalPrim(p))
	default:
		return true
	}
	if len(path) == 1 && path[0] == "None" {
		_, isNone := t.(types.NoneType)
		return isNone || hasNone(t)
	}
	if len(path) == 1 && (path[0] == "Ok" || path[0] == "Err") {
		g, ok := t.(types.Generic)
		if !ok || g.Name != "Result" || len(g.Args) != 2 {
			return false
		}
		inner := g.Args[0]
		if path[0] == "Err" {
			inner = g.Args[1]
		}
		return len(args) != 1 || c.patternFits(args[0], inner, env)
	}
	var enumDecl *syntax.EnumDecl
	switch len(path) {
	case 1:
		return false // no constructor but Ok and Err stands alone
	case 2:
		if decl, found := c.lookup(path[0]); found {
			enumDecl, _ = decl.(*syntax.EnumDecl)
		}
	case 3:
		_, enumDecl, _ = c.importedEnumNamed(path[0], path[1], env)
	}
	if enumDecl == nil {
		return true
	}
	variant := path[len(path)-1]
	known := false
	for _, v := range enumDecl.Variants {
		known = known || v.Name == variant
	}
	if !known {
		return false
	}
	members := []types.Type{t}
	if u, ok := t.(types.Union); ok {
		members = u.Members
	}
	for _, m := range members {
		if n, ok := m.(types.Named); ok && n.Decl == syntax.Decl(enumDecl) {
			return true
		}
	}
	return false
}

func hasNone(t types.Type) bool {
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

// checkCalls is `calls(Fake.op)` (ADR 0091 §4): how many times the test
// has called one of its faked operations, as an int.
func (c *checker) checkCalls(e *syntax.CallExpr, env map[string]types.Type) types.Type {
	start, _ := e.Bounds()
	intType := types.Primitive{Name: "int"}
	if len(e.Args) != 1 || e.Args[0].Name != "" {
		c.report(start, "", "calls takes one faked operation, such as calls(LedgerApi.record)")
		return intType
	}
	target, op, ok := FakedOperation(e.Args[0].Value)
	t, faked := c.fakes[target]
	if !ok || !faked || t.kind != fakeGroup || !c.fakeHasOp(target, op) {
		c.report(start, "", "calls takes an operation this test fakes, such as calls(LedgerApi.record)")
	}
	return intType
}

// FakedOperation reads `calls`'s argument, `Target.op` or `pkg.Target.op`,
// as the target as written and the operation.
func FakedOperation(e syntax.Expr) (target, op string, ok bool) {
	fa, isAccess := e.(*syntax.FieldAccess)
	if !isAccess {
		return "", "", false
	}
	switch t := fa.Target.(type) {
	case *syntax.Ident:
		return t.Name, fa.Field, true
	case *syntax.FieldAccess:
		if pkg, isIdent := t.Target.(*syntax.Ident); isIdent {
			return pkg.Name + "." + t.Field, fa.Field, true
		}
	}
	return "", "", false
}

// fakeHasOp is set while a test is checked: whether its fake of target
// has the operation op.
func (c *checker) fakeHasOp(target, op string) bool {
	return c.fakeOps[target+"."+op]
}

// checkTestRef is T1: a declaration under tests/ is named only from
// tests/.
func (c *checker) checkTestRef(start lexer.Pos, text string, decl syntax.Decl) {
	if c.testOnly == nil || c.inTestsFile || decl == nil || !c.testOnly[decl] {
		return
	}
	if c.t1Reported == nil {
		c.t1Reported = map[lexer.Pos]bool{}
	}
	if c.t1Reported[start] {
		return
	}
	c.t1Reported[start] = true
	c.report(start, "T1", "%s is declared in a test file, so only tests may name it (ADR 0091 §2)", text)
}

// testOnlyDecls is every declaration in files under tests/, or nil when
// there are none.
func testOnlyDecls(files []*syntax.File) map[syntax.Decl]bool {
	var out map[syntax.Decl]bool
	for _, f := range files {
		if f == nil || !f.Tests {
			continue
		}
		if out == nil {
			out = map[syntax.Decl]bool{}
		}
		for _, d := range f.Decls {
			out[d] = true
		}
	}
	return out
}

func exprStart(e syntax.Expr) lexer.Pos {
	start, _ := e.(syntax.Spanned).Bounds()
	return start
}

// declaredElsewhere finds a capability group or config named name in a
// package this one was built against, at any depth, and returns the
// package's path and what it is.
func (c *checker) declaredElsewhere(name string) (string, string) {
	seen := map[*types.Package]bool{}
	var walk func(types.Packages) (string, string)
	walk = func(pkgs types.Packages) (string, string) {
		paths := make([]string, 0, len(pkgs))
		for p := range pkgs {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		for _, p := range paths {
			pkg := pkgs[p]
			if pkg == nil || seen[pkg] {
				continue
			}
			seen[pkg] = true
			if pkg.Universe != nil {
				switch d, _ := pkg.Universe.Lookup(name); d.(type) {
				case *syntax.CapabilityDecl:
					return p, "capability group"
				case *syntax.ConfigDecl:
					return p, "config"
				}
			}
			if path, what := walk(pkg.Dependencies); path != "" {
				return path, what
			}
		}
		return "", ""
	}
	return walk(c.packages)
}
