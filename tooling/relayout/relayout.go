// Package relayout moves a module to ADRs 0102, 0104 and 0105's layout:
// its code into src/, each file named after its declaration in TitleCase,
// its tests into test/, merged into the file mirroring what each checks
// (test/scenarios/ when that isn't one file), and its manifest's provides
// into the capability groups. Other modules can be absorbed as folder
// packages, with every import of them rewritten so code keeps its aliases.
package relayout

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"fiig-lang/compiler/format"
	"fiig-lang/compiler/layout"
	"fiig-lang/compiler/manifest"
	"fiig-lang/compiler/parser"
	"fiig-lang/compiler/syntax"
)

// Absorb is a module to fold into the root module as the folder package
// Folder (src/<Folder>/).
type Absorb struct {
	Dir    string
	Folder string
}

// Move is one file the relayout writes, from the files it replaces.
type Move struct {
	To   string   // module-relative
	From []string // absolute
	Text string
}

// Plan is what Relayout will do: the files to write, the files and
// folders to remove, the manifest to write, and the tests it placed in
// test/scenarios/ because they check more than one file, or none.
type Plan struct {
	Root      string
	Moves     []Move
	Remove    []string
	Manifest  []byte
	Scenarios []string
}

type module struct {
	dir     string
	folder  string // "" for the root module
	m       *manifest.Manifest
	oldPath string
	newPath string
}

type source struct {
	mod  *module
	abs  string
	rel  string // module-relative destination
	text string
	file *syntax.File
	name string // its declaration's name
}

// Relayout plans the move of the module at root, with absorb folded in.
func Relayout(root string, absorb []Absorb) (*Plan, error) {
	rm, errs := manifest.Load(root)
	if len(errs) > 0 {
		return nil, fmt.Errorf("%s: %v (a module needs a fiig.pkg.json)", root, errs[0])
	}
	mods := []*module{{dir: root, m: rm, oldPath: rm.Path, newPath: rm.Path}}
	rename := map[string]string{}
	for _, a := range absorb {
		am, errs := manifest.Load(a.Dir)
		if len(errs) > 0 {
			return nil, fmt.Errorf("%s: %v", a.Dir, errs[0])
		}
		if _, err := os.Stat(filepath.Join(a.Dir, "migrations")); err == nil {
			return nil, fmt.Errorf("%s has migrations; move them into %s's migrations/ by hand, renumbered, before absorbing it", a.Dir, root)
		}
		mod := &module{dir: a.Dir, folder: a.Folder, m: am, oldPath: am.Path, newPath: layout.PathOf(rm.Path, a.Folder)}
		mods = append(mods, mod)
		rename[mod.oldPath] = mod.newPath
	}

	plan := &Plan{Root: root}
	var sources []*source
	var tests []*source
	for _, mod := range mods {
		err := filepath.WalkDir(mod.dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(mod.dir, p)
			rel = filepath.ToSlash(rel)
			if d.IsDir() {
				if rel != "." && (strings.HasPrefix(d.Name(), ".") || rel == "migrations" || rel == "build" || rel == "node_modules") {
					return fs.SkipDir
				}
				return nil
			}
			if path.Ext(p) != ".fiig" {
				return nil
			}
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			f, perrs := parser.Parse(string(data))
			if len(perrs) > 0 {
				return fmt.Errorf("%s: %s", p, perrs[0].String())
			}
			s := &source{mod: mod, abs: p, text: rewriteImports(string(data), rename), file: f}
			if len(f.Decls) > 0 {
				s.name = syntax.DeclName(f.Decls[0])
			}
			if syntax.IsTestPath(rel) {
				tests = append(tests, s)
				return nil
			}
			s.rel = path.Join(layout.Src, mod.folder, layout.FileName(s.name))
			if strings.HasPrefix(rel, layout.Src+"/") && mod.folder == "" {
				// Already under src/: keep its folder, fix only the name.
				s.rel = path.Join(path.Dir(rel), layout.FileName(s.name))
			}
			sources = append(sources, s)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	// Provides moves from the manifests into the groups (ADR 0105 §3).
	provides := map[string]string{} // group -> interface path
	for _, mod := range mods {
		for iface, group := range mod.m.Provides {
			provides[mod.newPath+"\x00"+group] = rewritePath(iface, rename)
		}
	}
	for _, s := range sources {
		for _, d := range s.file.Decls {
			g, ok := d.(*syntax.CapabilityDecl)
			if !ok {
				continue
			}
			if iface, ok := provides[s.mod.newPath+"\x00"+g.Name]; ok {
				s.text = addProvides(s.text, g.Name, iface, s.mod.newPath)
				delete(provides, s.mod.newPath+"\x00"+g.Name)
			}
		}
	}
	for k := range provides {
		_, group, _ := strings.Cut(k, "\x00")
		return nil, fmt.Errorf("a manifest says capability group %s provides an interface, but no file declares it", group)
	}

	seen := map[string]*source{}
	for _, s := range sources {
		key := strings.ToLower(s.rel)
		if other, taken := seen[key]; taken {
			return nil, fmt.Errorf("%s and %s would both be %s; rename one declaration", other.abs, s.abs, s.rel)
		}
		seen[key] = s
		text, err := formatted(s.text)
		if err != nil {
			return nil, fmt.Errorf("%s: %v", s.abs, err)
		}
		plan.Moves = append(plan.Moves, Move{To: s.rel, From: []string{s.abs}, Text: text})
	}

	// Tests go into the file mirroring what they check.
	byName := map[*module]map[string]*source{}
	for _, s := range sources {
		if byName[s.mod] == nil {
			byName[s.mod] = map[string]*source{}
		}
		if s.name != "" && len(s.file.Decls) > 0 && callable(s.file.Decls[0]) {
			byName[s.mod][s.name] = s
		}
	}
	groups := map[string][]*source{}
	var order []string
	for _, t := range tests {
		dest := ""
		if target := subject(t.file, byName[t.mod]); target != nil {
			dest = layout.Test + strings.TrimPrefix(target.rel, layout.Src)
		} else {
			dest = path.Join(layout.Test, layout.Scenarios, layout.FileName(t.name))
			plan.Scenarios = append(plan.Scenarios, fmt.Sprintf("%s (from %s)", dest, t.abs))
		}
		if _, ok := groups[dest]; !ok {
			order = append(order, dest)
		}
		groups[dest] = append(groups[dest], t)
	}
	for _, dest := range order {
		text, err := mergeTests(groups[dest])
		if err != nil {
			return nil, fmt.Errorf("%s: %v", dest, err)
		}
		var from []string
		for _, t := range groups[dest] {
			from = append(from, t.abs)
		}
		plan.Moves = append(plan.Moves, Move{To: dest, From: from, Text: text})
	}

	// The root manifest takes the absorbed modules' dependencies and
	// capabilities, and drops its dependencies on them.
	merged := *rm
	merged.Provides = nil
	deps := map[string]string{}
	caps := map[string]bool{}
	native := map[string]bool{}
	for _, mod := range mods {
		for d, v := range mod.m.Dependencies {
			if _, absorbed := rename[d]; !absorbed {
				deps[d] = v
			}
		}
		for _, c := range mod.m.Capabilities {
			caps[c] = true
		}
		for _, c := range mod.m.NativeCapabilities {
			native[c] = true
		}
	}
	merged.Dependencies = nil
	if len(deps) > 0 {
		merged.Dependencies = deps
	}
	merged.Capabilities = sortedSet(caps)
	merged.NativeCapabilities = sortedSet(native)
	data, err := json.MarshalIndent(&merged, "", "    ")
	if err != nil {
		return nil, err
	}
	plan.Manifest = append(data, '\n')
	for _, mod := range mods[1:] {
		plan.Remove = append(plan.Remove, mod.dir)
	}
	sort.Slice(plan.Moves, func(i, j int) bool { return plan.Moves[i].To < plan.Moves[j].To })
	return plan, nil
}

// Apply writes p: the moved files, the manifest, then removes every file
// a move replaced, the absorbed modules, and the folders left empty.
func (p *Plan) Apply() error {
	written := map[string]bool{}
	for _, m := range p.Moves {
		dst := filepath.Join(p.Root, filepath.FromSlash(m.To))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dst, []byte(m.Text), 0o644); err != nil {
			return err
		}
		abs, _ := filepath.Abs(dst)
		written[strings.ToLower(abs)] = true
	}
	if err := os.WriteFile(filepath.Join(p.Root, manifest.Filename), p.Manifest, 0o644); err != nil {
		return err
	}
	for _, m := range p.Moves {
		for _, from := range m.From {
			abs, _ := filepath.Abs(from)
			if written[strings.ToLower(abs)] {
				continue
			}
			if err := os.Remove(from); err != nil && !os.IsNotExist(err) {
				return err
			}
			removeEmptyDirs(filepath.Dir(from), p.Root)
		}
	}
	for _, dir := range p.Remove {
		if err := os.RemoveAll(dir); err != nil {
			return err
		}
	}
	return nil
}

func removeEmptyDirs(dir, stop string) {
	stopAbs, _ := filepath.Abs(stop)
	for {
		abs, _ := filepath.Abs(dir)
		if abs == stopAbs || len(abs) <= len(stopAbs) {
			return
		}
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) > 0 {
			return
		}
		os.Remove(dir)
		dir = filepath.Dir(dir)
	}
}

// subject is the source file a test checks: the declaration its expects
// call most, when one is called more than any other; else the one its
// body calls, when there's exactly one; else nil (a scenario).
func subject(f *syntax.File, decls map[string]*source) *source {
	counts := map[string]int{}
	var bodyCalls []string
	for _, d := range f.Decls {
		t, ok := d.(*syntax.TestDecl)
		if !ok {
			continue
		}
		for _, st := range t.Body.Stmts {
			isExpect := false
			if _, ok := st.(*syntax.ExpectStmt); ok {
				isExpect = true
			}
			for _, name := range stmtCalls(st) {
				if decls[name] == nil {
					continue
				}
				bodyCalls = append(bodyCalls, name)
				if isExpect {
					counts[name]++
				}
			}
		}
	}
	best, bestN, tie := "", 0, false
	for name, n := range counts {
		switch {
		case n > bestN:
			best, bestN, tie = name, n, false
		case n == bestN:
			tie = true
		}
	}
	if best != "" && !tie {
		return decls[best]
	}
	if len(counts) == 0 {
		distinct := map[string]bool{}
		for _, n := range bodyCalls {
			distinct[n] = true
		}
		if len(distinct) == 1 {
			return decls[bodyCalls[0]]
		}
	}
	return nil
}

// stmtCalls is the bare names st calls.
func stmtCalls(st syntax.Stmt) []string {
	var out []string
	syntax.WalkBlockExprs(&syntax.Block{Stmts: []syntax.Stmt{st}}, func(e syntax.Expr) {
		if c, ok := e.(*syntax.CallExpr); ok {
			switch callee := c.Callee.(type) {
			case *syntax.Ident:
				out = append(out, callee.Name)
			case *syntax.FieldAccess:
				// A capability group's operation, AbsApi.me(...): the
				// group's file is what's tested.
				if id, ok := callee.Target.(*syntax.Ident); ok {
					out = append(out, id.Name)
				}
			}
		}
	})
	return out
}

// mergeTests is one test file of tests' tests: their imports once, each
// test, and one private block of all their helpers.
func mergeTests(tests []*source) (string, error) {
	imports := map[string]bool{}
	var bodies, private []string
	helpers := map[string]bool{}
	for _, t := range tests {
		text := t.text
		if t.file.Private != nil {
			start, end := t.file.Private.Start.Offset, t.file.Private.End.Offset
			block := text[start:end]
			open, close := strings.Index(block, "{"), strings.LastIndex(block, "}")
			if open >= 0 && close > open {
				private = append(private, strings.TrimSpace(block[open+1:close]))
			}
			for _, m := range t.file.Private.Members {
				name := syntax.DeclName(m)
				if helpers[name] {
					return "", fmt.Errorf("two tests' files both have a helper %s; rename one, then run fiig fmt -layout again", name)
				}
				helpers[name] = true
			}
			text = text[:start] + text[end:]
		}
		var body []string
		for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "use ") {
				imports[strings.TrimSpace(line)] = true
				continue
			}
			body = append(body, line)
		}
		bodies = append(bodies, strings.TrimSpace(strings.Join(body, "\n")))
	}
	var b strings.Builder
	for _, imp := range sortedSet(imports) {
		b.WriteString(imp + "\n")
	}
	if len(imports) > 0 {
		b.WriteString("\n")
	}
	b.WriteString(strings.Join(bodies, "\n\n"))
	b.WriteString("\n")
	if len(private) > 0 {
		b.WriteString("\nprivate {\n" + strings.Join(private, "\n\n") + "\n}\n")
	}
	return formatted(b.String())
}

func formatted(text string) (string, error) {
	out, errs := format.Format(text)
	if len(errs) > 0 {
		return "", fmt.Errorf("%s", errs[0].String())
	}
	return out, nil
}

// rewriteImports rewrites each use line naming an absorbed module, keeping
// the alias the code uses: `use abs.client` becomes `use
// abs.app.absclient as client`.
func rewriteImports(text string, rename map[string]string) string {
	if len(rename) == 0 {
		return text
	}
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		trimmed := strings.TrimRight(line, "\r")
		rest, ok := strings.CutPrefix(strings.TrimSpace(trimmed), "use ")
		if !ok {
			continue
		}
		p, alias, hasAlias := strings.Cut(rest, " as ")
		p = strings.TrimSpace(p)
		np := rewritePath(p, rename)
		if np == p {
			continue
		}
		if !hasAlias && path.Ext("."+p) != path.Ext("."+np) {
			alias, hasAlias = p[strings.LastIndex(p, ".")+1:], true
		}
		out := "use " + np
		if hasAlias {
			out += " as " + strings.TrimSpace(alias)
		}
		lines[i] = out + strings.TrimPrefix(line, trimmed)
	}
	return strings.Join(lines, "\n")
}

// rewritePath is p with an absorbed module's path replaced by its new one.
func rewritePath(p string, rename map[string]string) string {
	best := ""
	for old := range rename {
		if (p == old || strings.HasPrefix(p, old+".")) && len(old) > len(best) {
			best = old
		}
	}
	if best == "" {
		return p
	}
	return rename[best] + strings.TrimPrefix(p, best)
}

// addProvides writes `provides alias.Interface` after the group's name,
// importing the interface's package when the file doesn't.
func addProvides(text, group, iface, ownPath string) string {
	pkg, name := iface[:strings.LastIndex(iface, ".")], iface[strings.LastIndex(iface, ".")+1:]
	ref := name
	if pkg != ownPath {
		alias := ""
		for _, line := range strings.Split(text, "\n") {
			rest, ok := strings.CutPrefix(strings.TrimSpace(line), "use ")
			if !ok {
				continue
			}
			p, a, hasAlias := strings.Cut(rest, " as ")
			if strings.TrimSpace(p) == pkg {
				alias = pkg[strings.LastIndex(pkg, ".")+1:]
				if hasAlias {
					alias = strings.TrimSpace(a)
				}
			}
		}
		if alias == "" {
			alias = pkg[strings.LastIndex(pkg, ".")+1:]
			text = "use " + pkg + "\n" + text
		}
		ref = alias + "." + name
	}
	return strings.Replace(text, "capability "+group+" {", "capability "+group+" provides "+ref+" {", 1)
}

func sortedSet(m map[string]bool) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// callable reports whether d is something a test exercises by calling it,
// rather than a model or an enum it builds values of.
func callable(d syntax.Decl) bool {
	switch d.(type) {
	case *syntax.RuleDecl, *syntax.ConversionDecl, *syntax.ActivityDecl, *syntax.GatewayDecl, *syntax.WorkflowDecl, *syntax.CapabilityDecl:
		return true
	}
	return false
}
