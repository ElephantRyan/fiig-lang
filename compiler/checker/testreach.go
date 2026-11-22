package checker

import (
	"sort"

	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// ADR 0091 §4: T5 and T6 need what a test reaches — every capability
// group operation, config, and capability its calls lead to, through
// activities, gateways, and other packages. This walks the calls from the
// test's body without types, so it over-approximates: an operation called
// on a provider value counts as called on every group the walk saw used as
// a value (ADR 0043 §3's chosen providers).

// reachScope is where a body's names resolve.
type reachScope struct {
	universe *types.Universe
	imports  map[string]*types.Package
	groups   map[string]*types.GroupRef
}

// reached is what one test leads to.
type reached struct {
	ops         map[*syntax.CapabilityDecl]map[string]bool // called through the group
	selected    map[*syntax.CapabilityDecl]bool            // used as a value: a chosen provider
	viaProvider map[string]bool                            // operations called on a provider value
	configs     map[*syntax.ConfigDecl]bool
	seen        map[syntax.Decl]bool
}

func (r *reached) groupOps(g *syntax.CapabilityDecl) map[string]bool {
	out := map[string]bool{}
	for op := range r.ops[g] {
		out[op] = true
	}
	if r.selected[g] {
		for _, gw := range g.Gateways {
			if r.viaProvider[gw.Name] {
				out[gw.Name] = true
			}
		}
	}
	return out
}

// reachWalker walks one package's tests.
type reachWalker struct {
	rootScopes map[syntax.Decl]reachScope // the root package's declarations, by file
	universe   *types.Universe
	byDecl     map[syntax.Decl]*types.Package
	byUniverse map[*types.Universe]*types.Package
	r          *reached
	// faked is the current test's fakes: a faked operation's real body is
	// replaced, so it isn't walked.
	faked map[*syntax.CapabilityDecl]map[string]bool
}

// checkTestReach is T5 and T6 for every test in files, by file.
func checkTestReach(files []*syntax.File, universe *types.Universe, packages types.Packages) map[*syntax.File][]Diagnostic {
	var tests []*syntax.File
	for _, f := range files {
		if f != nil && len(f.Decls) > 0 {
			if _, ok := f.Decls[0].(*syntax.TestDecl); ok {
				tests = append(tests, f)
			}
		}
	}
	if len(tests) == 0 {
		return nil
	}
	w := &reachWalker{rootScopes: map[syntax.Decl]reachScope{}, universe: universe, byDecl: map[syntax.Decl]*types.Package{}, byUniverse: map[*types.Universe]*types.Package{}}
	for _, f := range files {
		if f == nil {
			continue
		}
		sc := reachScope{universe: universe, imports: ResolveFileImports(f, packages), groups: ResolveFileGroups(f, packages)}
		for _, d := range f.Decls {
			w.rootScopes[d] = sc
		}
		if f.Private != nil {
			for _, d := range f.Private.Members {
				w.rootScopes[d] = sc
			}
		}
	}
	var index func(types.Packages)
	index = func(pkgs types.Packages) {
		for _, p := range pkgs {
			if p == nil || p.Universe == nil || w.byUniverse[p.Universe] != nil {
				continue
			}
			w.byUniverse[p.Universe] = p
			for _, d := range p.Universe.Decls {
				w.byDecl[d] = p
			}
			index(p.Dependencies)
		}
	}
	index(packages)

	out := map[*syntax.File][]Diagnostic{}
	for _, f := range tests {
		for _, d := range f.Decls {
			if t, ok := d.(*syntax.TestDecl); ok {
				out[f] = append(out[f], w.checkTest(t)...)
			}
		}
	}
	return out
}

func (w *reachWalker) checkTest(t *syntax.TestDecl) []Diagnostic {
	sc := w.rootScopes[t]
	sc.universe = w.universe.ScopeOf(t)
	w.r = &reached{ops: map[*syntax.CapabilityDecl]map[string]bool{}, selected: map[*syntax.CapabilityDecl]bool{}, viaProvider: map[string]bool{}, configs: map[*syntax.ConfigDecl]bool{}, seen: map[syntax.Decl]bool{}}
	w.faked = map[*syntax.CapabilityDecl]map[string]bool{}
	type resolved struct {
		fake   *syntax.FakeDecl
		kind   fakeKind
		group  *syntax.CapabilityDecl
		config *syntax.ConfigDecl
	}
	var fakes []resolved
	for _, f := range t.Fakes {
		kind, group, config, _ := ResolveFake(f.Target, sc.universe, sc.imports, sc.groups)
		fakes = append(fakes, resolved{f, kind, group, config})
		if kind == fakeGroup {
			ops := map[string]bool{}
			for _, g := range f.Gateways {
				ops[g.Name] = true
			}
			w.faked[group] = ops
		}
	}
	w.walkBlock(t.Body, sc, nil)
	for _, f := range t.Fakes {
		for _, g := range f.Gateways {
			w.walkBlock(g.Body, sc, g.Params)
		}
	}

	var diags []Diagnostic
	report := func(f *syntax.FakeDecl, rule, format string, args ...any) {
		c := &checker{}
		c.report(f.Start, rule, format, args...)
		diags = append(diags, c.diags...)
	}
	fakedGroups := map[*syntax.CapabilityDecl]*syntax.FakeDecl{}
	for _, rf := range fakes {
		switch rf.kind {
		case fakeGroup:
			fakedGroups[rf.group] = rf.fake
			ops := w.r.groupOps(rf.group)
			if len(ops) == 0 {
				report(rf.fake, "T5", "fake %s is never reached by this test; remove it, or call what uses it (ADR 0091 §4)", rf.fake.TargetName())
				continue
			}
			for _, op := range sortedSet(ops) {
				if rf.fake.Gateway(op) == nil {
					report(rf.fake, "T6", "this test reaches %s.%s, which fake %s doesn't have; add `gateway %s(...)` to it (ADR 0091 §4)", rf.fake.TargetName(), op, rf.fake.TargetName(), op)
				}
			}
		case fakeConfig:
			if !w.r.configs[rf.config] {
				report(rf.fake, "T5", "fake %s is never reached by this test; nothing it calls reads %s (ADR 0091 §4)", rf.fake.TargetName(), rf.config.Name)
			}
		}
	}
	// Every provider a rule can return is reached: once one is faked, an
	// unfaked one would run for real.
	if len(fakedGroups) > 0 {
		var first *syntax.FakeDecl
		var missing []string
		for _, g := range sortedGroups(w.r.selected) {
			if f, ok := fakedGroups[g]; ok {
				if first == nil {
					first = f
				}
				continue
			}
			if len(w.r.groupOps(g)) > 0 {
				missing = append(missing, g.Name)
			}
		}
		if first != nil {
			for _, name := range missing {
				report(first, "T6", "this test can also reach provider %s, which it doesn't fake; a test of code that chooses a provider fakes every provider it could choose (ADR 0091 §4)", name)
			}
		}
	}
	return diags
}

// walkDecl walks a callable declaration's body in its own scope, once.
func (w *reachWalker) walkDecl(d syntax.Decl, sc reachScope) {
	if w.r.seen[d] {
		return
	}
	w.r.seen[d] = true
	body := callableBody(d)
	if body == nil {
		return
	}
	var params []syntax.Param
	switch d := d.(type) {
	case *syntax.RuleDecl:
		params = d.Params
	case *syntax.ConversionDecl:
		params = d.Params
	case *syntax.ActivityDecl:
		params = d.Params
	case *syntax.GatewayDecl:
		params = d.Params
	case *syntax.WorkflowDecl:
		params = d.Params
	case *syntax.HelperDecl:
		params = d.Params
	}
	w.walkBlock(body, sc, params)
}

// scopeOf is where a top-level declaration's body resolves; ok is false
// for a private member, which resolves where its caller does.
func (w *reachWalker) scopeOf(d syntax.Decl) (reachScope, bool) {
	if sc, ok := w.rootScopes[d]; ok {
		sc.universe = w.universe.ScopeOf(d)
		return sc, true
	}
	if p, ok := w.byDecl[d]; ok {
		return w.packageScope(p, d), true
	}
	return reachScope{}, false
}

func (w *reachWalker) packageScope(p *types.Package, d syntax.Decl) reachScope {
	imports := map[string]*types.Package{}
	for alias, u := range p.Imports {
		if dep := w.byUniverse[u]; dep != nil {
			imports[alias] = dep
		} else {
			imports[alias] = &types.Package{Universe: u}
		}
	}
	u := p.Universe
	if d != nil {
		u = u.ScopeOf(d)
	}
	return reachScope{universe: u, imports: imports, groups: p.Groups}
}

func (w *reachWalker) walkBlock(b *syntax.Block, sc reachScope, params []syntax.Param) {
	if b == nil {
		return
	}
	locals := map[string]bool{}
	for _, p := range params {
		locals[p.Name] = true
	}
	collectLocals(b, locals)
	callTargets := map[*syntax.Ident]bool{}
	syntax.WalkBlockExprs(b, func(e syntax.Expr) {
		switch e := e.(type) {
		case *syntax.CallExpr:
			w.call(e, sc, locals, callTargets)
		case *syntax.FieldAccess:
			if id, ok := e.Target.(*syntax.Ident); ok && !locals[id.Name] {
				if cd := configIn(sc, id.Name); cd != nil {
					w.r.configs[cd] = true
				}
			}
		case *syntax.Ident:
			if callTargets[e] || locals[e.Name] {
				return
			}
			if g, _ := groupIn(sc, e.Name); g != nil {
				w.r.selected[g] = true
			}
		}
	})
}

// call follows one call: into a declaration, a group's operation, or
// another package.
func (w *reachWalker) call(e *syntax.CallExpr, sc reachScope, locals map[string]bool, callTargets map[*syntax.Ident]bool) {
	switch callee := e.Callee.(type) {
	case *syntax.Ident:
		callTargets[callee] = true
		if locals[callee.Name] || sc.universe.IsAmbiguous(callee.Name) {
			return
		}
		if d, ok := sc.universe.Lookup(callee.Name); ok {
			w.follow(d, sc)
		}
	case *syntax.FieldAccess:
		id, ok := callee.Target.(*syntax.Ident)
		if !ok {
			return
		}
		callTargets[id] = true
		if locals[id.Name] {
			// An operation on a provider value, or a method on a local.
			w.r.viaProvider[callee.Field] = true
			return
		}
		if g, pkg := groupIn(sc, id.Name); g != nil {
			if w.r.ops[g] == nil {
				w.r.ops[g] = map[string]bool{}
			}
			w.r.ops[g][callee.Field] = true
			if w.faked[g] != nil && w.faked[g][callee.Field] {
				return
			}
			if gw := g.Gateway(callee.Field); gw != nil && !w.r.seen[gw] {
				w.r.seen[gw] = true
				gsc, found := w.scopeOf(g)
				if pkg != nil {
					gsc, found = w.packageScope(pkg, g), true
				}
				if found {
					w.walkBlock(gw.Body, gsc, gw.Params)
				}
			}
			return
		}
		if p, ok := sc.imports[id.Name]; ok && p.Universe != nil {
			if d, found := p.Universe.Lookup(callee.Field); found {
				w.follow(d, sc)
			}
		}
	}
}

// follow walks a called declaration in its own scope; a private member
// keeps the caller's, which is its file's.
func (w *reachWalker) follow(d syntax.Decl, caller reachScope) {
	if callableBody(d) == nil {
		return
	}
	if sc, ok := w.scopeOf(d); ok {
		w.walkDecl(d, sc)
		return
	}
	w.walkDecl(d, caller)
}

// groupIn resolves name to a capability group: imported by path tail, or
// declared in the scope's package.
func groupIn(sc reachScope, name string) (*syntax.CapabilityDecl, *types.Package) {
	if g, ok := sc.groups[name]; ok && g.Decl != nil {
		return g.Decl, g.Package
	}
	if sc.universe != nil && !sc.universe.IsAmbiguous(name) {
		if d, ok := sc.universe.Lookup(name); ok {
			if g, isGroup := d.(*syntax.CapabilityDecl); isGroup {
				return g, nil
			}
		}
	}
	return nil, nil
}

func configIn(sc reachScope, name string) *syntax.ConfigDecl {
	if g, ok := sc.groups[name]; ok && g.Config != nil {
		return g.Config
	}
	if sc.universe != nil && !sc.universe.IsAmbiguous(name) {
		if d, ok := sc.universe.Lookup(name); ok {
			if cd, isConfig := d.(*syntax.ConfigDecl); isConfig {
				return cd
			}
		}
	}
	return nil
}

// collectLocals adds every name b binds: assignments, loop variables, and
// match-arm bindings.
func collectLocals(b *syntax.Block, out map[string]bool) {
	if b == nil {
		return
	}
	for _, s := range b.Stmts {
		// A match used as a value (ADR 0142 §2) binds in its arms as a match
		// statement does.
		switch v := valueOf(s).(type) {
		case *syntax.MatchExpr:
			collectArmLocals(v.Match, out)
		}
		switch s := s.(type) {
		case *syntax.Binding:
			out[s.Name] = true
		case *syntax.ForStmt:
			out[s.Var] = true
			if s.Value != "" {
				out[s.Value] = true
			}
			collectLocals(s.Body, out)
		case *syntax.WhileStmt:
			collectLocals(s.Body, out)
		case *syntax.IfStmt:
			collectLocals(s.Then, out)
			for _, ei := range s.ElseIfs {
				collectLocals(ei.Then, out)
			}
			collectLocals(s.Else, out)
		case *syntax.MatchStmt:
			collectArmLocals(s, out)
		case *syntax.TransactionStmt:
			collectLocals(s.Body, out)
		}
	}
}

// collectArmLocals adds the names m's arms bind, and their blocks' locals.
func collectArmLocals(m *syntax.MatchStmt, out map[string]bool) {
	for _, arm := range m.Arms {
		for _, name := range patternNames(arm.Pattern) {
			out[name] = true
		}
		if blk, ok := arm.Body.(*syntax.Block); ok {
			collectLocals(blk, out)
		}
	}
}

// valueOf is the value a return or binding statement holds, or nil.
func valueOf(s syntax.Stmt) syntax.Expr {
	switch s := s.(type) {
	case *syntax.ReturnStmt:
		return s.Value
	case *syntax.Binding:
		return s.Value
	}
	return nil
}

func sortedSet(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedGroups(m map[*syntax.CapabilityDecl]bool) []*syntax.CapabilityDecl {
	out := make([]*syntax.CapabilityDecl, 0, len(m))
	for g := range m {
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// patternNames is every single-segment name a pattern binds (or names:
// a zero-argument constructor is harmless here, since it is no local).
func patternNames(p syntax.Pattern) []string {
	switch p := p.(type) {
	case *syntax.NamePattern:
		if len(p.Path) == 1 {
			return []string{p.Path[0]}
		}
	case *syntax.ConstructorPattern:
		var out []string
		for _, a := range p.Args {
			out = append(out, patternNames(a)...)
		}
		return out
	}
	return nil
}
