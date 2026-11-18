// Package checker implements Milestone 2's type checker, covering
// bug-classes.md:
//
//   - Category A (type errors): nominal type mismatches, `None` where a
//     non-optional type is required, missing required model fields,
//     implicit conversions between distinct types, structural types in
//     public signatures, two `conversion` declarations sharing a name
//     (A6 — built on types.BuildUniverse's cross-file duplicate-name
//     detection, tagged A6 only when both colliding declarations are
//     conversions; see conflictDiagnostic below), an HTTP adapter route
//     binding its request body into a value/primitive type instead of a
//     model (A7, ADR 0021 — checkBodyBinding), and a `match` that does not
//     cover every case of a type this package can fully enumerate — Result's own
//     Ok/Err, an enum type's variants, or a `T | None` union (A9, stmt.go's
//     checkExhaustive).
//
//   - Category B (effect/call-graph errors): the §3.2 composition matrix —
//     a rule calls only formulas (ADR 0029, narrowed by ADR 0049), a formula
//     calls only formulas (B11), every kind may call a formula, a conversion may call a rule
//     or conversion, an activity (ADR 0044) a rule, conversion, gateway, or
//     another activity (and a cycle among activities is B9), a gateway only
//     a rule/conversion (never another gateway), a workflow any of
//     rule/conversion/activity/gateway but never a workflow (nothing may
//     call one, B8), and an adapter route only an exported workflow. checkAdapterRoutes also enforces ADR
//     0009/0021's path/query/body parameter-binding rules, untagged since
//     they aren't their own numbered catalog entries (A7 is the one
//     exception, above).
//
//   - Category C (transaction errors): transaction blocks may not nest (C2),
//     a transaction block's active capability must match the capability of
//     any gateway called inside it (C4 — C1, "a network gateway inside
//     transaction Database," is documented in bug-classes.md as one
//     instance of this same general rule, not a separate mechanism), and a
//     workflow or activity may not be called inside a transaction block
//     (C5). C3 was
//     dropped outright (not deferred) — see transaction.go's doc comment.
//
//   - Category D (concurrency errors): a `concurrent` block may not request
//     an unbounded limit (D1), and a nested `concurrent` block's limit may
//     not exceed its parent's budget (D2, only checked when both limits are
//     literal integers).
//
//   - Category E (retry errors): `retry(...)` may only wrap a gateway declared
//     `idempotent`.
//
//   - Category G (file/import errors): wildcard imports (G2, a parse
//     error since ADR 0050 §2.4)
//     and duplicate import aliases (G3). G1, more than one public
//     declaration per file, is a parse error since ADR 0054.
//
//   - H4 (ADR 0054): a private member escaping its file, through its
//     public entry's signature or a reference from another file. Calls to
//     a file's own private members are exempt from Category B's matrix;
//     a call to its own public entry is not.
//
//   - Category F (error-handling errors): a bare expression statement whose
//     value is a typed Result, left unhandled — no `?`, no `match` (F1,
//     stmt.go's checkUnhandledResult).
//
// A capability call (Database.queryOne(...)) is always treated as
// Unknown, deliberately unmodeled rather than guessed at — Milestone 5
// built declarative capability *provider* configuration (ADR 0027), not
// a capability *contract* the checker type-checks a call's signature
// against; no such contract exists yet. A dotted/aliased type reference
// (systemTime.Instant) resolves for real once package context is given
// (packages below, Milestone 6, ADR 0032) — Unknown only when it isn't,
// matching every caller's behavior from before this milestone.
package checker

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"fiig-lang/compiler/lexer"
	"fiig-lang/compiler/manifest"
	"fiig-lang/compiler/parser"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// Diagnostic is one checker-reported problem. File is "" for Check, which
// operates on an in-memory program with no file association; CheckDir
// fills it in, since two files sharing a line number would otherwise be
// indistinguishable from Pos alone.
type Diagnostic struct {
	File string    `json:"file,omitempty"`
	Msg  string    `json:"message"`
	At   lexer.Pos `json:"at"`
	// End is where the token the diagnostic is about ends (ADR 0089 §5),
	// zero when it isn't in a file's text.
	End  lexer.Pos `json:"end,omitzero"`
	Rule string    `json:"rule,omitempty"` // bug-classes.md ID, e.g. "A1" — "" for infrastructure errors (unresolved names, duplicate decls) that aren't one of the catalog's numbered entries.
	// decl is the declaration a package-level diagnostic is about, for
	// CheckDir to attribute it to a file.
	decl syntax.Decl
}

func (d Diagnostic) String() string {
	loc := d.At.String()
	if d.At.Line == 0 {
		// Not in any source file's text: a manifest problem, located by File alone.
		loc = d.File
	} else if d.File != "" {
		loc = d.File + ":" + loc
	}
	if d.Rule == "" {
		return fmt.Sprintf("%s: %s", loc, d.Msg)
	}
	return fmt.Sprintf("%s: [%s] %s", loc, d.Rule, d.Msg)
}

// CheckDir parses every .fiig file under root and type-checks the whole
// program together — declarations constantly reference each other across
// files under §3.9's one-declaration-per-file rule, so checking has to see
// every file at once, not one at a time. Parse errors are returned
// alongside checker diagnostics rather than aborting, matching
// registry.Generate's approach. Every diagnostic's File is set to the
// path (relative to root) it actually came from. skip names directories
// under root, relative to it, that are programs of their own and are left
// out.
func CheckDir(root string, packages types.Packages, skip ...string) ([]Diagnostic, error) {
	sources := map[string]string{}
	var order []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			for _, s := range skip {
				if path == filepath.Join(root, s) {
					return filepath.SkipDir
				}
			}
		}
		if d.IsDir() || filepath.Ext(path) != ".fiig" {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = path
		}
		rel = filepath.ToSlash(rel)
		sources[rel] = string(src)
		order = append(order, rel)
		return nil
	})
	if err != nil {
		return nil, err
	}
	var path string
	var provides map[string]string
	if m, errs := manifest.Load(root); m != nil && len(errs) == 0 {
		path, provides = m.Path, m.Provides
	}
	diags, _ := checkSourceTexts(order, sources, packages, path, provides, false)
	return diags, nil
}

// Checked is one file of a CheckSources run: its syntax tree, its
// comments, and what the check resolved in it.
type Checked struct {
	File     *syntax.File
	Comments []lexer.Comment
	Index    *Index
	// ParseErrors is how many syntax errors the file has; with any, File
	// is the parser's recovery, missing what it couldn't read.
	ParseErrors int
}

// CheckSources is CheckDir for text in hand, keyed by path relative to the
// package root, as an editor holds unsaved files (ADR 0089 §2): every
// diagnostic is attributed to its file and has an End, and each file comes
// back with its index (§4). path and provides are the package manifest's.
func CheckSources(sources map[string]string, packages types.Packages, path string, provides map[string]string) ([]Diagnostic, map[string]*Checked) {
	order := make([]string, 0, len(sources))
	for rel := range sources {
		order = append(order, rel)
	}
	sort.Strings(order)
	return checkSourceTexts(order, sources, packages, path, provides, true)
}

func checkSourceTexts(order []string, sources map[string]string, packages types.Packages, path string, provides map[string]string, indexed bool) ([]Diagnostic, map[string]*Checked) {
	type parsed struct {
		path string
		file *syntax.File
	}
	var parsedFiles []parsed
	var diags []Diagnostic
	checked := map[string]*Checked{}
	for _, rel := range order {
		file, comments, errs := parser.ParseWithComments(sources[rel])
		for _, e := range errs {
			diags = append(diags, Diagnostic{File: rel, Msg: e.Msg, At: e.At, Rule: e.Rule})
		}
		parsedFiles = append(parsedFiles, parsed{rel, file})
		checked[rel] = &Checked{File: file, Comments: comments, ParseErrors: len(errs)}
	}

	files := make([]*syntax.File, len(parsedFiles))
	for i, pf := range parsedFiles {
		files[i] = pf.file
	}
	universe, conflicts := types.BuildUniverse(files)
	declFile := map[syntax.Decl]string{}
	for _, pf := range parsedFiles {
		for _, d := range pf.file.Decls {
			declFile[d] = pf.path
		}
		if pf.file.Private != nil {
			for _, d := range pf.file.Private.Members {
				declFile[d] = pf.path
			}
		}
	}
	fileOf := func(d syntax.Decl) string { return declFile[d] }
	for _, cf := range conflicts {
		diags = append(diags, conflictDiagnostics(cf, fileOf)...)
	}
	for _, d := range BindProvides(path, universe, files, packages, provides) {
		diags = append(diags, d)
	}
	for i := range diags {
		if diags[i].File == "" && diags[i].decl != nil {
			diags[i].File = fileOf(diags[i].decl)
		}
	}
	for _, pf := range parsedFiles {
		var index *Index
		if indexed {
			index = &Index{}
			checked[pf.path].Index = index
		}
		for _, d := range checkFileIndexed(pf.file, universe, packages, path, index) {
			d.File = pf.path
			diags = append(diags, d)
		}
	}
	setEnds(diags, sources)
	return diags, checked
}

// setEnds gives each diagnostic the end of the token it points at.
func setEnds(diags []Diagnostic, sources map[string]string) {
	ends := map[string]map[int]lexer.Pos{}
	for i := range diags {
		d := &diags[i]
		src, ok := sources[d.File]
		if !ok || d.At.Line == 0 {
			continue
		}
		byStart, ok := ends[d.File]
		if !ok {
			byStart = map[int]lexer.Pos{}
			for _, t := range lexer.New(src).AllTokens() {
				byStart[t.Start.Offset] = t.End
			}
			ends[d.File] = byStart
		}
		if end, ok := byStart[d.At.Offset]; ok && end.Offset > d.At.Offset {
			d.End = end
		} else {
			d.End = d.At
			d.End.Column++
			d.End.Offset++
		}
	}
}

// Check type-checks an already-parsed program.
func Check(files []*syntax.File, packages types.Packages) []Diagnostic {
	return CheckPackage(files, packages, "", nil)
}

// CheckPackage is Check for a package with a manifest: path is its own
// canonical path and provides its manifest's `provides` map, verified and
// bound (ADR 0043 §2) before any file is checked, so a group is assignable
// to the interfaces it provides everywhere in the package.
func CheckPackage(files []*syntax.File, packages types.Packages, path string, provides map[string]string) []Diagnostic {
	universe, conflicts := types.BuildUniverse(files)
	var diags []Diagnostic
	for _, cf := range conflicts {
		diags = append(diags, conflictDiagnostics(cf, nil)...)
	}
	diags = append(diags, BindProvides(path, universe, files, packages, provides)...)
	for _, f := range files {
		diags = append(diags, checkFile(f, universe, packages, path)...)
	}
	return diags
}

// BuildPackage wraps files (already parsed) as a *types.Package — its own
// Universe, plus its own import aliases resolved against packages
// (already-built dependencies, keyed by canonical path). This is the
// production counterpart of what compiler/loader assembles bottom-up over
// a whole dependency graph, reusing the same ResolveFileImports this
// package's own CheckFile already uses so a dependency's imports resolve
// exactly as they would if it were checked directly. Any duplicate-name
// conflict within files is reported as a Diagnostic rather than silently
// picking a winner, same as Check/CheckDir.
func BuildPackage(files []*syntax.File, packages types.Packages) (*types.Package, []Diagnostic) {
	universe, conflicts := types.BuildUniverse(files)
	var diags []Diagnostic
	for _, cf := range conflicts {
		diags = append(diags, conflictDiagnostics(cf, nil)...)
	}
	imports, groups := packageImports(files, packages)
	return &types.Package{Universe: universe, Imports: imports, Groups: groups, Dependencies: packages}, diags
}

// packageImports merges every file's import aliases into the one-level
// maps a types.Package carries.
func packageImports(files []*syntax.File, packages types.Packages) (map[string]*types.Universe, map[string]*types.GroupRef) {
	imports := map[string]*types.Universe{}
	groups := map[string]*types.GroupRef{}
	for _, f := range files {
		for alias, pkg := range ResolveFileImports(f, packages) {
			imports[alias] = pkg.Universe
		}
		for alias, g := range ResolveFileGroups(f, packages) {
			groups[alias] = g
		}
	}
	return imports, groups
}

// BindProvides verifies a package's `provides` map against its own
// universe (types.BindProvides) and returns any problem as diagnostics.
func BindProvides(path string, universe *types.Universe, files []*syntax.File, packages types.Packages, provides map[string]string) []Diagnostic {
	if len(provides) == 0 {
		return nil
	}
	imports, groups := packageImports(files, packages)
	own := &types.Package{Universe: universe, Imports: imports, Groups: groups}
	var diags []Diagnostic
	for _, e := range types.BindProvides(path, own, packages, provides) {
		d := Diagnostic{Msg: e.Msg, At: e.At, Rule: e.Rule, decl: e.Decl}
		if e.Decl == nil {
			d.File = manifest.Filename
		}
		diags = append(diags, d)
	}
	return diags
}

// conflictDiagnostics turns a types.Conflict into a Diagnostic at *each*
// of the two colliding declarations, tagged "A6" when it's specifically
// two `conversion` declarations colliding — the one case bug-classes.md's
// catalog numbers (a plain naming/identity collision between two
// conversions, per ADR 0004; ADR 0006 already ruled out the broader
// "ambiguous automatic selection by type" risk this entry used to cover).
// Any other kind of name collision (two gateways, a model and a rule,
// etc.) stays untagged infrastructure, same as before — the catalog has no
// numbered entry for those.
//
// Both sites are reported, rather than just the later one, because
// "later" means later in the order files happened to be walked, not
// anything the author can see or reason about: under §3.9 the two
// declarations are always in two different files, and blaming one of them
// sends the reader to a file that may be entirely correct. fileOf, when
// non-nil, resolves a declaration to the path its diagnostic belongs to
// so each message can also name where its counterpart lives; callers
// without per-file attribution (Check, BuildPackage) pass nil and get the
// same two diagnostics without the cross-reference.
func conflictDiagnostics(cf types.Conflict, fileOf func(syntax.Decl) string) []Diagnostic {
	rule := ""
	if _, firstIsConversion := cf.First.(*syntax.ConversionDecl); firstIsConversion {
		if _, secondIsConversion := cf.Second.(*syntax.ConversionDecl); secondIsConversion {
			rule = "A6"
		}
	}
	firstStart, _ := cf.First.(syntax.Spanned).Bounds()
	return []Diagnostic{
		conflictSide(cf.Name, rule, cf.First, firstStart, cf.Second, fileOf),
		conflictSide(cf.Name, rule, cf.Second, cf.At, cf.First, fileOf),
	}
}

// conflictSide builds one half of a duplicate-name report: the diagnostic
// that lands on this declaration and points at the other one.
func conflictSide(name, rule string, this syntax.Decl, at lexer.Pos, other syntax.Decl, fileOf func(syntax.Decl) string) Diagnostic {
	where := ""
	if fileOf != nil {
		// Named only when it's somewhere else. §3.9 makes that the normal
		// case, but the invalid-programs fixtures deliberately pack many
		// declarations into one file, where repeating the reader's own path
		// back at them is noise.
		if path := fileOf(other); path != "" && path != fileOf(this) {
			where = " in " + path
		}
	}
	d := Diagnostic{
		Rule: rule,
		Msg:  fmt.Sprintf("duplicate declaration name %q; also declared as a %s%s", name, types.DeclKindName(other), where),
		At:   at,
	}
	if rule == "A6" {
		d.Msg = fmt.Sprintf("conversion %q collides with another conversion of the same name%s", name, where)
	}
	if fileOf != nil {
		d.File = fileOf(this)
	}
	return d
}

// CheckFile type-checks one file's own declarations against a universe
// spanning the whole program — the same cross-file name resolution Check
// uses, just scoped so callers (tooling that checks one file at a time, or
// anything that wants diagnostics attributed to a specific file) don't
// have to re-derive it themselves. packages is every other package this
// run has loaded (Milestone 6, ADR 0032) — ResolveFileImports turns
// file's own `use ... as alias` imports into the alias -> *types.Package
// map a qualified reference actually resolves against; nil/empty behaves
// exactly as before this milestone (every qualified reference Unknown).
func CheckFile(file *syntax.File, universe *types.Universe, packages types.Packages) []Diagnostic {
	return checkFile(file, universe, packages, "")
}

// checkFile is CheckFile for a file of the package at ownPath ("" when the
// package has no manifest), which is what a `use` of one of the package's
// own formulas groups names (ADR 0049 §2.5).
func checkFile(file *syntax.File, universe *types.Universe, packages types.Packages, ownPath string) []Diagnostic {
	return checkFileIndexed(file, universe, packages, ownPath, nil)
}

// checkFileIndexed is checkFile filling index, when it isn't nil, with what
// the check resolves (ADR 0089 §4).
func checkFileIndexed(file *syntax.File, universe *types.Universe, packages types.Packages, ownPath string, index *Index) []Diagnostic {
	c := &checker{index: index,
		universe: universe, imports: ResolveFileImports(file, packages), groups: ResolveFileGroups(file, packages),
		aliases: map[string]*syntax.Import{}, g4Reported: map[*syntax.Import]bool{}, ownPath: ownPath,
	}
	if len(file.Decls) > 0 {
		c.entry = file.Decls[0]
	}
	c.bindOwnFormulas(file, packages)

	// G1 (more than one public declaration in a file) is a parse error
	// since ADR 0054, reported by compiler/parser.

	// G2 (a wildcard import) is a parse error since ADR 0050 §2.4: the
	// grammar has no wildcard form, and compiler/parser reports `.*`.

	// G3: two imports in one file must not bind the same alias. §3.10's
	// whole mechanism is that a qualified reference names exactly one
	// package, and an alias bound twice silently resolves to whichever
	// import was seen last — so `time.CompanyStamp` reports as an
	// unresolved type while both `use` lines look individually correct.
	// Both forms count: an explicit `as`, and the implicit
	// last-path-segment default, which is how two unaliased imports
	// (`use company.time` and `use standard.time`) collide without either
	// one naming the alias they're fighting over.
	//
	// Checked here rather than inside ResolveFileImports, where the
	// collision actually happens: that function is exported and reused by
	// compiler/runtime, which builds programs rather than reporting on
	// them, and it returns early when a run has no package context at all.
	// This fires either way.
	aliasAt := map[string]*syntax.Import{}
	for _, imp := range file.Imports {
		alias := importAlias(imp)
		if prior, dup := aliasAt[alias]; dup {
			c.report(imp.Start, "G3", "import alias %q is already bound to %q in this file", alias, strings.Join(prior.Path, "."))
			continue
		}
		aliasAt[alias] = imp
	}
	c.checkImportNames(file, packages)

	for _, d := range file.Decls {
		c.universe = universe.ScopeOf(d)
		c.preludeNameTaken(d)
		c.checkDecl(d)
		if ad, ok := d.(*syntax.ActivityDecl); ok {
			c.checkActivityCycle(ad)
		}
	}
	if file.Private != nil && len(file.Decls) > 0 {
		c.checkPrivateBlock(file.Decls[0], file.Private, universe)
	}
	return c.diags
}

// checkPrivateBlock checks a file's private members (ADR 0054) in the
// file's own scope. A helper is checked as if it were the public entry's
// kind: §4 and §5 give it the entry's composition rules and effect
// ceiling, so the per-kind tables stay the single source of truth.
func (c *checker) checkPrivateBlock(entry syntax.Decl, block *syntax.PrivateBlock, universe *types.Universe) {
	c.universe = universe.ScopeOf(entry)
	kind, _ := kindOf(entry)
	for _, m := range block.Members {
		c.preludeNameTaken(m)
		if h, ok := m.(*syntax.HelperDecl); ok {
			c.callerKind = kind
			if gw, ok := entry.(*syntax.GatewayDecl); ok {
				c.requires = gw.Requires
			}
			c.checkCallable(h.Params, h.ReturnType, h.Body)
			c.requires = ""
			continue
		}
		c.checkDecl(m)
	}
}

// kindOf classifies a declaration that has a body for the §3.2
// composition matrix.
func kindOf(d syntax.Decl) (declKind, bool) {
	switch d.(type) {
	case *syntax.RuleDecl:
		return kindRule, true
	case *syntax.ConversionDecl:
		return kindConversion, true
	case *syntax.ActivityDecl:
		return kindActivity, true
	case *syntax.GatewayDecl:
		return kindGateway, true
	case *syntax.WorkflowDecl:
		return kindWorkflow, true
	}
	return "", false
}

// sameFile reports whether d is a member of the file being checked: its
// public entry or one of its private members.
func (c *checker) sameFile(d syntax.Decl) bool {
	return d == c.entry || (c.universe != nil && c.universe.Parent != nil && c.universe.Decls[syntax.DeclName(d)] == d)
}

// fileCallees is every declaration outside d's file that d's file calls:
// the calls in d's own body and in each of its file's private helpers,
// each resolved in that file's scope. Calls that stay inside the file are
// left out. Ambiguous and unresolvable names are skipped; they are reported
// where they occur.
func fileCallees(d syntax.Decl, universe *types.Universe) []syntax.Decl {
	scope := universe.ScopeOf(d)
	bodies := []*syntax.Block{callableBody(d)}
	if scope.Parent != nil {
		for _, m := range scope.Decls {
			if h, ok := m.(*syntax.HelperDecl); ok {
				bodies = append(bodies, h.Body)
			}
		}
	}
	seen := map[syntax.Decl]bool{}
	var out []syntax.Decl
	for _, body := range bodies {
		for _, name := range syntax.BodyCallNames(body) {
			if scope.IsAmbiguous(name) {
				continue
			}
			callee, ok := scope.Lookup(name)
			if !ok || callee == d || seen[callee] || (scope.Parent != nil && scope.Decls[name] == callee) {
				continue
			}
			seen[callee] = true
			out = append(out, callee)
		}
	}
	return out
}

func callableBody(d syntax.Decl) *syntax.Block {
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
	case *syntax.HelperDecl:
		return d.Body
	}
	return nil
}

// checkActivityCycle is bug class B9 (ADR 0044): once an activity may call
// other activities, recursion becomes expressible — the first place fiig
// needs call-graph cycle detection, since a rule calls nothing and a
// conversion's recursion is structural, terminating on the type graph.
// Walks the activities reachable from ad through the program-wide universe
// and reports when ad itself is among them, so every activity on a cycle is
// reported at its own declaration (the file-local view this checker works
// in) rather than once at some arbitrary member.
//
// ADR 0054 §6 narrows this to cycles that leave the file: an activity
// calling itself, directly or through its private helpers, is allowed and
// bounded by the runtime's call depth limit instead. A file is walked as one unit,
// its helpers' calls included (fileCallees).
func (c *checker) checkActivityCycle(ad *syntax.ActivityDecl) {
	seen := map[syntax.Decl]bool{}
	var reaches func(d syntax.Decl) bool
	reaches = func(d syntax.Decl) bool {
		for _, callee := range fileCallees(d, c.universe) {
			if callee == syntax.Decl(ad) {
				return true
			}
			if seen[callee] {
				continue
			}
			seen[callee] = true
			if next, ok := callee.(*syntax.ActivityDecl); ok && reaches(next) {
				return true
			}
		}
		return false
	}
	if reaches(ad) {
		start, _ := ad.Bounds()
		c.report(start, "B9", "activity %q is part of a call cycle; activities must not recurse, directly or through other activities", ad.Name)
	}
}

type checker struct {
	// index receives what this check resolves, for tooling (ADR 0089 §4);
	// nil when nobody asked.
	index *Index

	// universe is the scope names resolve in: the package Universe, or the
	// current file's scope when it has a private block (ADR 0054).
	universe *types.Universe
	// entry is the public declaration of the file being checked; with the
	// file's private members it makes up what sameFile accepts.
	entry syntax.Decl
	// imports is the current file's own `use ... as alias` aliases,
	// resolved to the imported package (Milestone 6, ADR 0032) — nil when
	// this checker run has no package context, same as before this
	// milestone. Lookups against it go through universesOf (type
	// resolution) or directly (a qualified call, which also needs the
	// target's own Imports to check its signature in its own scope — see
	// callInScope).
	imports map[string]*types.Package
	// groups is the current file's capability-group imports (`use
	// acme.http.Client`, ADR 0043 §5), alias -> group.
	groups map[string]*types.GroupRef
	// ownPath is the canonical path of the package being checked, "" when
	// it has no manifest (ADR 0049 §2.5's own-package group imports).
	ownPath string
	// group is the formulas group being checked, whose members a bare call
	// may name (ADR 0049 §2.1); nil outside one.
	group *syntax.FormulasDecl
	// aliases is every alias the current file's imports bind, for G4.
	aliases map[string]*syntax.Import
	// g4Reported keeps G4's import-side report to one per import.
	g4Reported map[*syntax.Import]bool
	// requires is the capability the gateway being checked requires, "" when
	// the declaration isn't a gateway. A gateway may use only that one (P7).
	requires string
	// foreignWorkflow is the import alias of the package whose workflow
	// the call being checked names, set just before callDecl and read
	// once by checkEffect (ADR 0079 §1); "" otherwise.
	foreignWorkflow string
	diags           []Diagnostic
	// argScope is where the current cross-package call's arguments
	// resolve (inCallerScope); nil outside one.
	argScope *scopeState
	// callerKind is the kind of declaration currently being checked — set
	// once per top-level checkDecl call (rule/conversion/activity/gateway/workflow
	// never nest inside each other's bodies, so a single mutable field is
	// safe here rather than threading a parameter through every function).
	// Used by checkEffect (Category B).
	callerKind declKind
	// activeTransaction is the capability named by the transaction block currently in
	// scope, or "" when there isn't one. Used by Category C.
	activeTransaction string
	// loops is the stack of enclosing `for` statements in the current
	// function, innermost last, each holding its label ("" when unlabeled).
	// break/continue check against it (ADR 0053). It is cleared while
	// checking a concurrent or transaction body, since a jump may not cross
	// either boundary.
	loops []string
	// inConcurrent is true while checking a concurrent body. Reassigning a
	// local there is rejected (ADR 0053): those bodies run in parallel.
	inConcurrent bool
	// concurrencyBudget is the numeric limit of the innermost enclosing
	// concurrent block that has one, or nil when there isn't one (no
	// enclosing concurrent block, or it used the bare/unbounded form —
	// bug class D2 only compares two explicit numeric limits, since the
	// bare form's actual safe-default bound is never specified anywhere to
	// compare against). Used by Category D.
	concurrencyBudget *int
	// methodCalls marks each `x.m(...)` that resolved to a closed-table
	// method (ADR 0049 §2.6). Those are pure, so discarding one's result
	// is A13 (ADR 0065 §6).
	methodCalls map[*syntax.FieldAccess]bool
	// inSortKey is true while checking a sorted form's key (ADR 0065 §3),
	// which may call only what a rule may: callerKind is rule meanwhile,
	// and report notes why on an effect diagnostic.
	inSortKey bool
	// mapKeyReported keeps checkMapKey to one M1 per position, since a
	// type can be resolved more than once.
	mapKeyReported map[lexer.Pos]bool
}

// lookup resolves a bare name against the universe for the purpose of
// *checking* it. A name declared more than once resolves to nothing here,
// even though types.Universe.Decls still holds the first declaration that
// claimed it: which one that is depends on the order files were walked,
// so every judgement that reads the result's kind — B6's "is this a
// workflow", E1's "is this gateway idempotent", the composition matrix —
// would be decided by a coin flip, and a wrong answer points the reader
// at the file that is actually correct. The duplicate is already reported
// once at both of its declarations (conflictDiagnostics); treating it as
// unresolvable here is what stops that one real error from also producing
// a second, derived, misdirecting one.
//
// Callers whose not-found path *reports* something, rather than falling
// silent, must check ambiguous separately — "undefined name" is just as
// wrong as B6 was.
func (c *checker) lookup(name string) (syntax.Decl, bool) {
	if c.ambiguous(name) {
		return nil, false
	}
	return c.universe.Lookup(name)
}

func (c *checker) ambiguous(name string) bool {
	return c.universe != nil && c.universe.IsAmbiguous(name)
}

func (c *checker) report(at lexer.Pos, rule, format string, args ...any) {
	for _, a := range args {
		if a == syntax.ErrorName {
			return // the parser already reported the syntax error this name stands in for
		}
	}
	msg := fmt.Sprintf(format, args...)
	if c.inSortKey && rule != "" && (rule[0] == 'B' || rule[0] == 'P') {
		msg += " (a sorted key may call only what a rule may: formulas and methods, ADR 0065 §3)"
	}
	c.diags = append(c.diags, Diagnostic{Msg: msg, At: at, Rule: rule})
}

// resolve wraps types.Resolve, reporting an unresolved name as a
// diagnostic (this is necessary infrastructure every Category A check
// depends on, not itself a numbered bug class) and returning
// types.Unknown{} as the safe fallback so one bad type name doesn't
// cascade into unrelated false positives elsewhere in the same signature.
func (c *checker) resolve(t syntax.Type) types.Type {
	c.recordTypeRefs(t)
	rt, ok := types.ResolveWithImports(t, c.universe, universesOf(c.imports))
	start, _ := t.(syntax.Spanned).Bounds()
	if !ok {
		c.reportUndefined(start, "type", types.SyntaxString(t))
		return types.Unknown{}
	}
	c.checkMapKeysIn(rt, start)
	return rt
}

// resolveOwn is resolve for a type written in the file being checked: a
// field, a signature, or a typed local. types.Resolve leaves an alias it
// doesn't know unmodeled, for a package this check wasn't given, but an
// alias that no use line of the file binds can't name anything. (A call's
// callee may be declared in another file, whose types are resolved with
// resolve, so the check can't live there.)
func (c *checker) resolveOwn(t syntax.Type) types.Type {
	if alias, ok := c.unimportedAlias(t); ok {
		start, _ := t.(syntax.Spanned).Bounds()
		c.report(start, "", "unresolved type %q: this file has no use line for %q", types.SyntaxString(t), alias)
		return types.Unknown{}
	}
	return c.resolve(t)
}

// unimportedAlias finds a qualified type in t (`http.Reply`) whose alias no
// use line of the current file binds.
func (c *checker) unimportedAlias(t syntax.Type) (string, bool) {
	switch t := t.(type) {
	case *syntax.QualifiedType:
		if len(t.Path) == 2 {
			if _, imported := c.aliases[t.Path[0]]; !imported {
				return t.Path[0], true
			}
		}
		for _, a := range t.Args {
			if alias, ok := c.unimportedAlias(a); ok {
				return alias, true
			}
		}
	case *syntax.RecordType:
		for _, f := range t.Fields {
			if alias, ok := c.unimportedAlias(f.Type); ok {
				return alias, true
			}
		}
	case *syntax.UnionType:
		for _, m := range t.Members {
			if alias, ok := c.unimportedAlias(m); ok {
				return alias, true
			}
		}
	}
	return "", false
}

// reportUndefined reports a name that does not resolve here. When it names
// another file's private member, that is an escape (H4, ADR 0054 §3), and
// saying so is more useful than "undefined". what is "name" or "type".
func (c *checker) reportUndefined(at lexer.Pos, what, name string) {
	if owners := c.universe.PrivateOwners(name); len(owners) > 0 {
		c.report(at, "H4", "%q is private to the file of %s %q; nothing outside that file may reference it", name, syntax.DeclKindName(owners[0]), syntax.DeclName(owners[0]))
		return
	}
	if what == "type" {
		c.report(at, "", "unresolved type %q", name)
		return
	}
	c.report(at, "", "undefined name %q", name)
}

// ResolveFileImports turns file's own `use ... as alias` imports into
// the alias -> *types.Package map this checker run can actually resolve
// — packages it wasn't given (an alias naming a dependency this run
// doesn't have loaded) are simply absent, same as having no package
// context at all. An import with no explicit `as` defaults to its
// path's last segment (`use standard.time` would be referred to as
// `time`) — the conventional default every mainstream dotted-import
// syntax uses, though nothing in the corpus attests it yet (every
// attested import names an explicit alias). Exported so compiler/
// runtime (and anything else assembling a package-aware executable
// program, not just a checker run) can resolve a file's own aliases
// the exact same way, rather than a parallel hand-rolled copy.
func ResolveFileImports(file *syntax.File, packages types.Packages) map[string]*types.Package {
	if len(packages) == 0 {
		return nil
	}
	imports := map[string]*types.Package{}
	for _, imp := range file.Imports {
		if pkg, ok := packages[strings.Join(imp.Path, ".")]; ok {
			imports[importAlias(imp)] = pkg
		}
	}
	return imports
}

// importAlias is the name one import binds in its file: its explicit `as`
// alias, or its path's last segment. Shared with CheckFile's G3 check so
// the name a collision is reported against is the same name resolution
// will actually use.
func importAlias(imp *syntax.Import) string {
	if imp.Alias != "" {
		return imp.Alias
	}
	return imp.Path[len(imp.Path)-1]
}

// universesOf flattens an alias -> *types.Package map to alias ->
// *types.Universe — all type resolution needs.
func universesOf(imports map[string]*types.Package) map[string]*types.Universe {
	if len(imports) == 0 {
		return nil
	}
	us := make(map[string]*types.Universe, len(imports))
	for alias, pkg := range imports {
		us[alias] = pkg.Universe
	}
	return us
}

// callInScope evaluates fn with c.universe/c.imports temporarily switched
// to a different package's own scope — needed when checking a call into
// an imported declaration, so its own parameter/return types resolve
// against its own imports (which may alias a given package differently
// than the caller does), not the caller's. Restored afterward, the same
// save/restore shape c.concurrencyBudget already uses around a
// concurrent block.
func (c *checker) callInScope(pkg *types.Package, fn func() types.Type) types.Type {
	savedUniverse, savedImports, savedGroups := c.universe, c.imports, c.groups
	caller := &scopeState{universe: c.universe, imports: c.imports, groups: c.groups, argScope: c.argScope}
	c.argScope = caller
	defer func() { c.argScope = caller.argScope }()
	c.universe, c.imports, c.groups = pkg.Universe, nil, pkg.Groups
	if len(pkg.Imports) > 0 {
		c.imports = make(map[string]*types.Package, len(pkg.Imports))
		for alias, u := range pkg.Imports {
			c.imports[alias] = &types.Package{Universe: u}
		}
	}
	result := fn()
	c.universe, c.imports, c.groups = savedUniverse, savedImports, savedGroups
	return result
}

// scopeState is where names resolve: a package's universe and the
// imports active in it. argScope is the scope the current call's
// arguments are written in, when callInScope has moved resolution into the
// callee's package.
type scopeState struct {
	universe *types.Universe
	imports  map[string]*types.Package
	groups   map[string]*types.GroupRef
	argScope *scopeState
}

// inCallerScope runs fn where the current call's arguments were written:
// a cross-package call resolves its parameter types in the callee's
// package, but its argument expressions name the caller's declarations
// (`http.WebRequest(authorization: StripeAccount.apiKey...)`).
func (c *checker) inCallerScope(fn func()) {
	if c.argScope == nil {
		fn()
		return
	}
	here := scopeState{universe: c.universe, imports: c.imports, groups: c.groups, argScope: c.argScope}
	a := c.argScope
	c.universe, c.imports, c.groups, c.argScope = a.universe, a.imports, a.groups, a.argScope
	fn()
	c.universe, c.imports, c.groups, c.argScope = here.universe, here.imports, here.groups, here.argScope
}

// checkPublicType is resolve, plus bug class A8: a structural record type
// is only legal as a local binding's inferred type (§3.3), never directly
// in a public param/return/field type. Only checks the type's own top
// level — `List<{ x: int }>` isn't attested anywhere and isn't guarded
// against here, matching the project's habit of not guessing past what's
// attested.
func (c *checker) checkPublicType(t syntax.Type) types.Type {
	if _, ok := t.(*syntax.RecordType); ok {
		start, _ := t.(syntax.Spanned).Bounds()
		c.report(start, "A8", "a structural record type isn't allowed in a public signature; declare a named model instead")
	}
	return c.resolveOwn(t)
}

func (c *checker) checkDecl(d syntax.Decl) {
	switch d := d.(type) {
	case *syntax.ModelDecl:
		for _, f := range d.Fields {
			c.checkNoCapabilityField(d.Name, f, c.resolveOwn(f.Type))
		}
		c.checkJsonNames(d.Name, d.Fields, false)
		c.checkPromotedJsonNames(d)
		for _, e := range types.EmbedDiagnostics(d, c.universe) {
			c.report(e.At, e.Rule, "%s", e.Msg)
		}
	case *syntax.EnumDecl:
		for _, v := range d.Variants {
			if !isUpper(v.Name) {
				c.report(v.Start, "N2", "enum variant %s.%s must be capitalized, so `Upper.Upper` always reads as an enum variant (ADR 0043 §6a)", d.Name, v.Name)
			}
			for _, f := range v.Fields {
				c.checkNoCapabilityField(d.Name+"."+v.Name, f, c.resolveOwn(f.Type))
			}
			c.checkJsonNames(d.Name+"."+v.Name, v.Fields, true)
		}
		c.checkVariantSpellings(d)
		if d.Header != nil {
			c.checkEnumHeader(d)
		}
	case *syntax.RuleDecl:
		c.callerKind = kindRule
		c.checkNoPrivateEscape(d.Params, d.ReturnType)
		c.checkCallable(d.Params, d.ReturnType, d.Body)
	case *syntax.ConversionDecl:
		c.callerKind = kindConversion
		c.checkNoPrivateEscape(d.Params, d.ReturnType)
		c.checkCallable(d.Params, d.ReturnType, d.Body)
	case *syntax.ActivityDecl:
		c.callerKind = kindActivity
		c.checkNoPrivateEscape(d.Params, d.ReturnType)
		c.checkCallable(d.Params, d.ReturnType, d.Body)
	case *syntax.GatewayDecl:
		c.callerKind = kindGateway
		c.requires = d.Requires
		c.checkRequires(d)
		c.checkNoPrivateEscape(d.Params, d.ReturnType)
		c.checkCallable(d.Params, d.ReturnType, d.Body)
		c.requires = ""
	case *syntax.WorkflowDecl:
		c.callerKind = kindWorkflow
		c.checkNoPrivateEscape(d.Params, d.ReturnType)
		c.checkNoConnectionInSignature(d.Params, d.ReturnType, d.Start)
		c.checkNoSecretInSignature(d.Params, d.ReturnType, d.Start)
		c.checkCallable(d.Params, d.ReturnType, d.Body)
	case *syntax.AdapterDecl:
		c.checkAdapterRoutes(d)
	case *syntax.InterfaceDecl:
		c.checkInterfaceDecl(d)
	case *syntax.CapabilityDecl:
		c.checkCapabilityDecl(d)
	case *syntax.FormulasDecl:
		c.checkFormulasDecl(d)
	case *syntax.ConfigDecl:
		c.checkConfigDecl(d)
	}
}

// declKind classifies a declaration for the §3.2 composition matrix
// (bug-classes.md Category B).
type declKind string

const (
	kindRule       declKind = "rule"
	kindConversion declKind = "conversion"
	kindActivity   declKind = "activity"
	kindGateway    declKind = "gateway"
	kindWorkflow   declKind = "workflow"
	kindFormula    declKind = "formula" // ADR 0049
)

// allowedCallees is §3.2's composition matrix. Rule calls only formulas —
// isolated from everything that carries judgment or effects (ADR 0029,
// reverting ADR 0028's "rules may call rules", narrowed by ADR 0049 §2.1).
// A formula is the bottom of the lattice: every kind may call one, and it
// calls only other formulas (ADR 0049 §2.3).
// Conversion may call Rule and other Conversions — recursive structural
// mapping stays pure and judgment-free (ADR 0029). Activity (ADR 0044,
// superseding ADR 0030's pure-only matrix) is the reusable effectful
// coordinator: it may call Rule/Conversion/Gateway/Activity. Gateway (ADR
// 0031, renamed from Step) always touches exactly one capability (ADR 0010)
// and may call Rule/Conversion to shape/interpret the result, but never
// another Gateway, an Activity, or a Workflow. Workflow is the adapter
// boundary (ADR 0044): it may call Rule/Conversion/Activity/Gateway, and
// nothing may call a Workflow. Adapter->Workflow is checked separately in
// checkAdapterRoutes, since adapters don't have a body of
// statements/expressions the way these five do.
var allowedCallees = map[declKind]map[declKind]bool{
	kindFormula:    {kindFormula: true},
	kindRule:       {kindFormula: true},
	kindConversion: {kindRule: true, kindConversion: true, kindFormula: true},
	kindActivity:   {kindRule: true, kindConversion: true, kindGateway: true, kindActivity: true, kindFormula: true},
	kindGateway:    {kindRule: true, kindConversion: true, kindFormula: true},
	kindWorkflow:   {kindRule: true, kindConversion: true, kindActivity: true, kindGateway: true, kindFormula: true},
}

// bugClassForEffect maps a disallowed (caller, callee) pair to its
// bug-classes.md ID. B1/B2 keep their original meaning exactly (just
// renamed step->gateway); B6 is checkAdapterRoutes's own pre-existing tag
// (an adapter calling something other than a workflow), not assigned
// here. B7 is new (ADR 0029 — a rule calling another rule or a
// conversion, now forbidden again); B5 covers a conversion calling a
// gateway, workflow, or activity; B8 (ADR 0044, replacing ADR 0030's
// "an activity calls a gateway...") covers an activity or workflow
// calling a workflow — nothing in fiig may call one. B11 (ADR 0049 §2.2)
// is a formula calling anything but a formula.
func bugClassForEffect(caller, callee declKind) string {
	switch caller {
	case kindFormula:
		return "B11"
	case kindRule:
		if callee == kindGateway {
			return "B1"
		}
		if callee == kindWorkflow {
			return "B2"
		}
		return "B7"
	case kindGateway:
		if callee == kindGateway {
			return "B3"
		}
		if callee == kindWorkflow {
			return "B4"
		}
	case kindConversion:
		return "B5"
	case kindActivity, kindWorkflow:
		return "B8"
	}
	return ""
}

// checkEffect is bug-classes.md Category B: is c.callerKind allowed to
// call calleeKind at all? Called from inferCall once a callee resolves to
// one of rule/conversion/activity/gateway/workflow.
//
// A call to one of the caller's own private members is never judged (ADR
// 0054 §4). A call to the file's own public entry, from the entry or from
// one of its helpers, is judged like a call to another declaration of
// that kind (§6): an activity or conversion may call itself, but a rule
// may not (B7), nor a gateway (B3) or a workflow (B8).
func (c *checker) checkEffect(callee syntax.Decl, calleeKind declKind, calleeName string, at lexer.Pos) {
	foreign := c.foreignWorkflow
	c.foreignWorkflow = ""
	if !c.inSortKey && callee != c.entry && c.sameFile(callee) {
		return
	}
	// ADR 0079 §1: another package's workflow is reached from a gateway
	// that requires that package, and from nothing else.
	if calleeKind == kindWorkflow && foreign != "" && !c.inSortKey {
		switch c.callerKind {
		case kindGateway:
			if c.requires != foreign {
				c.report(at, "B4", "gateway may call workflow %s only if it requires %s; this one requires %s, and a gateway requires exactly one thing (ADR 0079 §1, ADR 0010)", calleeName, foreign, c.requires)
			}
			return
		case kindActivity, kindWorkflow:
			c.report(at, "B8", "%s may not call workflow %s; another package's workflow is called through a gateway that requires %s (ADR 0079 §1)", c.callerKind, calleeName, foreign)
			return
		}
	}
	allowed, tracked := allowedCallees[c.callerKind]
	if !tracked || allowed[calleeKind] {
		return
	}
	if c.inSortKey {
		c.inSortKey = false
		c.report(at, bugClassForEffect(c.callerKind, calleeKind), "a sorted key may not call %s %q; it may call only what a rule may, formulas and methods, so the order can't depend on outside state (ADR 0065 §3)", calleeKind, calleeName)
		c.inSortKey = true
		return
	}
	if calleeKind == kindWorkflow && (c.callerKind == kindGateway || c.callerKind == kindActivity || c.callerKind == kindWorkflow) {
		c.report(at, bugClassForEffect(c.callerKind, calleeKind), "%s may not call workflow %q; a workflow is called only from outside its package, by an adapter or by another package's gateway (ADR 0079 §2)", c.callerKind, calleeName)
		return
	}
	c.report(at, bugClassForEffect(c.callerKind, calleeKind), "%s may not call %s %q", c.callerKind, calleeKind, calleeName)
}

// httpMethods is ADR 0021's allow-list for http-route verbs. The parser
// accepts any identifier here (verb validation is this package's job, not
// the parser's), so this is the first place an unknown verb is rejected.
var httpMethods = map[string]bool{
	"GET": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true, "QUERY": true,
}

// bodyCarryingHTTPMethods is ADR 0021's body-carrying subset: POST, PUT,
// PATCH, and QUERY (RFC 10008 — safe and idempotent like GET, but with a
// request body) may bind one unclaimed parameter from the request body.
// GET and DELETE have no body in this model: every parameter must be
// claimed by a path or query placeholder.
var bodyCarryingHTTPMethods = map[string]bool{
	"POST": true, "PUT": true, "PATCH": true, "QUERY": true,
}

var placeholderPattern = regexp.MustCompile(`\{(\w+)\}`)

func extractPlaceholders(s string) []string {
	var names []string
	for _, m := range placeholderPattern.FindAllStringSubmatch(s, -1) {
		names = append(names, m[1])
	}
	return names
}

// checkNoPrivateEscape is H4's signature half (ADR 0054 §3): a public
// entry's parameter and return types may not name one of its file's
// private members, at any depth (a generic argument, a union member, a
// record field). That is also the whole of the transitive rule: the only
// types that can hold a private model are this file's own types, and every
// one of those that reaches the signature is walked here.
func (c *checker) checkNoPrivateEscape(params []syntax.Param, retType syntax.Type) {
	if c.universe == nil || c.universe.Parent == nil {
		return
	}
	for _, p := range params {
		c.findPrivateType(p.Type, "parameter "+strconv.Quote(p.Name))
	}
	c.findPrivateType(retType, "return type")
}

func (c *checker) findPrivateType(t syntax.Type, where string) {
	switch t := t.(type) {
	case *syntax.QualifiedType:
		if len(t.Path) == 1 {
			if d, ok := c.universe.Decls[t.Path[0]]; ok && !c.ambiguous(t.Path[0]) {
				start, _ := t.Bounds()
				c.report(start, "H4", "private %s %q may not appear in the public signature (%s); a caller would receive a type it cannot name", syntax.DeclKindName(d), t.Path[0], where)
			}
		}
		for _, a := range t.Args {
			c.findPrivateType(a, where)
		}
	case *syntax.UnionType:
		for _, m := range t.Members {
			c.findPrivateType(m, where)
		}
	case *syntax.RecordType:
		for _, f := range t.Fields {
			c.findPrivateType(f.Type, where)
		}
	}
}

// checkCallable checks a signature and the body it guards, and returns the
// resolved return type. A nil body (a native formula, ADR 0049 §2.4) checks
// the signature alone.
func (c *checker) checkCallable(params []syntax.Param, retType syntax.Type, body *syntax.Block) types.Type {
	env := map[string]types.Type{}
	for _, p := range params {
		env[p.Name] = c.checkPublicType(p.Type)
		c.checkLocalName(p.Name, env[p.Name], p.Start)
	}
	ret := c.checkPublicType(retType)
	if body != nil {
		c.checkBlock(body, ret, env, true)
	}
	return ret
}
