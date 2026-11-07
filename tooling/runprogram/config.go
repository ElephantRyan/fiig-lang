package runprogram

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"fiig-lang/compiler/format"
	"fiig-lang/compiler/manifest"
	"fiig-lang/compiler/runtime"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// ADR 0071: the one lookup config, the caps file, and fiig serve's own
// settings read, and which config an entry point reaches.

// Environment is the process environment, plus the variables of an
// -env-file when one is given (§3a). A variable set in the process wins.
type Environment struct {
	file map[string]string
}

// NewEnvironment reads envFile ("" for none) into an Environment. A file
// that is named but missing is an error, as is a line that isn't one of
// §3a's forms; the error names the file and line.
func NewEnvironment(envFile string) (*Environment, error) {
	e := &Environment{}
	if envFile == "" {
		return e, nil
	}
	f, err := os.Open(envFile)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	e.file = map[string]string{}
	scanner := bufio.NewScanner(f)
	for n := 1; scanner.Scan(); n++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		name, value, ok := strings.Cut(line, "=")
		name = strings.TrimSpace(name)
		if !ok || !envName(name) {
			return nil, fmt.Errorf("%s:%d: expected NAME=value", envFile, n)
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
			value = value[1 : len(value)-1]
		}
		e.file[name] = value
	}
	return e, scanner.Err()
}

func envName(s string) bool {
	if s == "" || s[0] >= '0' && s[0] <= '9' {
		return false
	}
	for _, r := range s {
		if !(r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

// Lookup reads one variable: the process's, else the file's.
func (e *Environment) Lookup(name string) (string, bool) {
	if v, ok := os.LookupEnv(name); ok {
		return v, true
	}
	v, ok := e.file[name]
	return v, ok
}

// Program is a checked program, everything ReachableConfig needs to
// resolve names the way the runtime will.
type Program struct {
	Universe *types.Universe
	Imports  map[string]map[string]*types.Package  // BuildImports
	Groups   map[string]map[string]*types.GroupRef // BuildGroups
	// Renames is the application manifest's `environment` (§5).
	Renames map[string]map[string]string
}

// scope is where a body's names resolve: its package's declarations and
// imports, and that package's path ("" for the application).
type configScope struct {
	path     string
	base     *types.Universe // the package's universe
	universe *types.Universe // base, or the current file's scope
	imports  map[string]*types.Package
	groups   map[string]*types.GroupRef
}

// ReachableConfig is §3: every config declaration a body reachable from
// entries reads, through calls, capability groups, and imported packages.
// A capability group used as a value (a provider a rule selects, ADR 0043
// §3) makes every gateway in it reachable, so a program that can pay
// through Stripe or Square needs both keys. entries are root declarations
// by name; an adapter's entry reaches its routes' workflows and mappings.
func ReachableConfig(p Program, entries []string) []runtime.ConfigSource {
	w := &walker{program: p, seen: map[syntax.Decl]bool{}, configs: map[*syntax.ConfigDecl]configScope{}}
	for _, name := range entries {
		if d, ok := p.Universe.Lookup(name); ok {
			w.visit(d, w.rootScope(name))
		}
	}
	var out []runtime.ConfigSource
	for d, sc := range w.configs {
		out = append(out, runtime.ConfigSource{
			Decl: d, Universe: sc.universe, Imports: UniversesOf(sc.imports),
			Package: sc.path, Rename: p.Renames[sc.path],
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Package != out[j].Package {
			return out[i].Package < out[j].Package
		}
		return out[i].Decl.Name < out[j].Decl.Name
	})
	return out
}

type walker struct {
	program Program
	seen    map[syntax.Decl]bool
	configs map[*syntax.ConfigDecl]configScope
}

func (w *walker) rootScope(declName string) configScope {
	u := w.program.Universe
	return configScope{base: u, universe: u, imports: w.program.Imports[declName], groups: w.program.Groups[declName]}
}

// packageScope is the scope a dependency's own bodies run in.
func packageScope(pkg *types.Package) configScope {
	imports := map[string]*types.Package{}
	for alias, u := range pkg.Imports {
		for _, dep := range pkg.Dependencies {
			if dep.Universe == u {
				imports[alias] = dep
			}
		}
	}
	return configScope{path: pkg.Path, base: pkg.Universe, universe: pkg.Universe, imports: imports, groups: pkg.Groups}
}

func (w *walker) visit(d syntax.Decl, sc configScope) {
	if d == nil || w.seen[d] {
		return
	}
	w.seen[d] = true
	switch d := d.(type) {
	case *syntax.AdapterDecl:
		for _, r := range d.Routes {
			if r.Workflow != nil {
				w.call(r.Workflow.Name, sc)
			}
			for _, arm := range r.Mapping {
				if v, ok := arm.Value.(*syntax.ExprResponse); ok {
					w.expr(v.Value, sc)
				}
			}
		}
	case *syntax.CapabilityDecl:
		for _, g := range d.Gateways {
			w.visit(g, sc)
		}
	case *syntax.FormulasDecl:
		for _, m := range d.Members {
			w.visit(m, sc)
		}
	case *syntax.FormulaDecl:
		w.block(d.Body, sc)
	case *syntax.HelperDecl:
		w.block(d.Body, sc)
	default:
		if body := bodyOf(d); body != nil {
			w.block(body, w.fileScope(d, sc))
		}
	}
}

// fileScope narrows sc to d's file, so its private helpers resolve.
func (w *walker) fileScope(d syntax.Decl, sc configScope) configScope {
	sc.universe = sc.base.ScopeOf(d)
	return sc
}

func bodyOf(d syntax.Decl) *syntax.Block {
	switch d := d.(type) {
	case *syntax.RuleDecl:
		return d.Body
	case *syntax.ConversionDecl:
		return d.Body
	case *syntax.ActivityDecl:
		return d.Body
	case *syntax.GatewayDecl:
		return d.Body
	case *syntax.WorkflowDecl:
		return d.Body
	}
	return nil
}

func (w *walker) block(b *syntax.Block, sc configScope) {
	if b == nil {
		return
	}
	syntax.WalkBlockExprs(b, func(e syntax.Expr) { w.one(e, sc) })
}

func (w *walker) expr(e syntax.Expr, sc configScope) {
	syntax.WalkExpr(e, func(e syntax.Expr) { w.one(e, sc) })
}

// one handles a single expression; the walk reaches the ones inside it.
func (w *walker) one(e syntax.Expr, sc configScope) {
	switch e := e.(type) {
	case *syntax.CallExpr:
		if id, ok := e.Callee.(*syntax.Ident); ok {
			w.call(id.Name, sc)
			return
		}
		fa, ok := e.Callee.(*syntax.FieldAccess)
		if !ok {
			return
		}
		target, ok := fa.Target.(*syntax.Ident)
		if !ok {
			return
		}
		if pkg, ok := sc.imports[target.Name]; ok {
			if d, found := pkg.Universe.Lookup(fa.Field); found {
				w.visit(d, packageScope(pkg))
			}
			return
		}
		if g, ok := sc.groups[target.Name]; ok {
			psc := packageScope(g.Package)
			switch {
			case g.Decl != nil:
				w.visit(g.Decl.Gateway(fa.Field), psc)
			case g.Formulas != nil:
				w.visit(g.Formulas.Member(fa.Field), psc)
			}
			return
		}
		if d, found := sc.universe.Lookup(target.Name); found {
			switch d := d.(type) {
			case *syntax.CapabilityDecl:
				w.visit(d.Gateway(fa.Field), sc)
			case *syntax.FormulasDecl:
				w.visit(d.Member(fa.Field), sc)
			}
		}
	case *syntax.Ident:
		// A capability group used as a value is a provider (ADR 0043 §3).
		if g, ok := sc.groups[e.Name]; ok && g.Decl != nil {
			w.visit(g.Decl, packageScope(g.Package))
		} else if d, found := sc.universe.Lookup(e.Name); found {
			if cd, ok := d.(*syntax.CapabilityDecl); ok {
				w.visit(cd, sc)
			}
		}
	case *syntax.FieldAccess:
		target, ok := e.Target.(*syntax.Ident)
		if !ok {
			return
		}
		if g, ok := sc.groups[target.Name]; ok && g.Config != nil {
			if _, done := w.configs[g.Config]; !done {
				w.configs[g.Config] = packageScope(g.Package)
			}
		} else if d, found := sc.universe.Lookup(target.Name); found {
			if cd, ok := d.(*syntax.ConfigDecl); ok {
				if _, done := w.configs[cd]; !done {
					w.configs[cd] = sc
				}
			}
		}
	}
}

func (w *walker) call(name string, sc configScope) {
	d, found := sc.universe.Lookup(name)
	if !found {
		return
	}
	_, isHelper := d.(*syntax.HelperDecl)
	switch {
	case isHelper:
		w.visit(d, sc) // a helper runs in its own file's scope
	case sc.path == "":
		w.visit(d, w.rootScope(name)) // each root file has its own imports
	default:
		w.visit(d, configScope{path: sc.path, base: sc.base, universe: sc.base, imports: sc.imports, groups: sc.groups})
	}
}

// Renames is root's manifest's `environment` renames (§5), or nil when it
// has none or no manifest.
func Renames(root string) map[string]map[string]string {
	if _, err := os.Stat(filepath.Join(root, manifest.Filename)); err != nil {
		return nil
	}
	m, errs := manifest.Load(root)
	if len(errs) > 0 {
		return nil
	}
	return m.Environment
}

// ConfigReport is §3's one startup report: every configuration problem,
// from config, the caps file, and fiig serve's own settings, at once.
func ConfigReport(problems []string) error {
	const nl = "\n"
	if len(problems) == 0 {
		return nil
	}
	noun := "problems"
	if len(problems) == 1 {
		noun = "problem"
	}
	return fmt.Errorf("configuration (%d %s):"+nl+"  %s", len(problems), noun, strings.Join(problems, nl+"  "))
}

// Variable is one row of `fiig config` (ADR 0071 §5): an environment
// variable a program reads, what it must be, and who reads it.
type Variable struct {
	Name        string
	Type        string
	Requirement string // "required", "optional", or "= default"
	Where       string
}

// Variables lists what sources read, and the `${NAME}`s in the caps file
// at capsPath (none when ""), sorted by name.
func Variables(sources []runtime.ConfigSource, capsPath string) ([]Variable, error) {
	var out []Variable
	for _, src := range sources {
		for _, f := range src.Decl.Fields {
			v := Variable{Name: src.EnvName(f), Type: types.SyntaxString(f.Type), Requirement: "required", Where: src.Where(f)}
			if f.Default != nil {
				v.Requirement = "= " + format.Expr(f.Default)
			} else if _, ok := f.Type.(*syntax.UnionType); ok {
				v.Requirement = "optional"
			}
			out = append(out, v)
		}
	}
	if capsPath != "" {
		raw, err := os.ReadFile(capsPath)
		if err != nil {
			return nil, err
		}
		parsed, err := runtime.ParseJSON(raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", capsPath, err)
		}
		var refs []string
		expandEnv(parsed, func(string) (string, bool) { return "", false }, filepath.Base(capsPath), &refs)
		for _, r := range refs {
			name, where, _ := strings.Cut(r, " is unset; ")
			out = append(out, Variable{Name: name, Type: "string", Requirement: "required", Where: where})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// AllConfig is every config declaration root's own declarations reach,
// and root's own config declarations even when nothing reads them yet.
func AllConfig(p Program) []runtime.ConfigSource {
	var names []string
	for name := range p.Universe.Decls {
		names = append(names, name)
	}
	sort.Strings(names)
	return ReachableConfig(p, names)
}
