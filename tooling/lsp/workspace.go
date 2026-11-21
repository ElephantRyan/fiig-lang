package lsp

import (
	"io/fs"
	"os"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"fiig-lang/compiler/checker"
	"fiig-lang/compiler/layout"
	"fiig-lang/compiler/lexer"
	"fiig-lang/compiler/loader"
	"fiig-lang/compiler/manifest"
	"fiig-lang/compiler/parser"
	"fiig-lang/compiler/stdlib"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// ADR 0089 §2-§4: the workspace's packages, each checked with the
// compiler's own code against the text the editor holds.

// pkgInfo is one package of the workspace: a directory with a manifest,
// or (loose) every .fiig file outside any package.
type pkgInfo struct {
	root  string // absolute directory
	loose bool
	// module marks a folder package of a module (ADR 0105): root is the
	// module's directory and folder the package's folder under src/ ("" for
	// the top-level package).
	module bool
	folder string
}

// id is p's identity among the workspace's packages.
func (p pkgInfo) id() string {
	if p.module {
		return p.root + "#" + p.folder
	}
	return p.root
}

// declID names a declaration across checks: its package and its name
// (a private member's name carries its file).
type declID struct {
	Pkg  string
	Name string
}

// site is where a declaration is written.
type site struct {
	path string // absolute file
	decl syntax.Decl
}

// snapshot is one check of one package.
type snapshot struct {
	pkg      pkgInfo
	path     string            // the manifest's package path, "" when loose
	sources  map[string]string // absolute file -> the text checked
	checked  map[string]*checker.Checked
	diags    map[string][]checker.Diagnostic // absolute file -> its diagnostics
	ids      map[syntax.Decl]declID
	own      map[declID]site
	depDirs  map[string]string // dependency package path -> its source directory
	depPaths []string          // dependency package paths, for rechecking dependents
}

// pkgKey is the declID package of p's own declarations.
func (s *snapshot) pkgKey() string {
	if s.path != "" {
		return s.path
	}
	return "loose:" + s.pkg.root
}

// discover finds the workspace's packages: every directory with a
// manifest, and the loose files outside them.
func discover(root string) []pkgInfo {
	var out []pkgInfo
	loose := false
	filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if path != root && (strings.HasPrefix(name, ".") || name == "node_modules" || name == "build" || name == "out") {
				return filepath.SkipDir
			}
			if path != root {
				if _, err := os.Stat(filepath.Join(path, manifest.Filename)); err == nil {
					out = append(out, packagesAt(path)...)
					return filepath.SkipDir
				}
			}
			return nil
		}
		if filepath.Ext(path) == ".fiig" {
			loose = true
		}
		return nil
	})
	if _, err := os.Stat(filepath.Join(root, manifest.Filename)); err == nil {
		out = append(out, packagesAt(root)...)
	} else if loose {
		out = append(out, pkgInfo{root: root, loose: true})
	}
	return out
}

// packagesAt is the package at dir, which has a manifest: one per folder
// under src/ for a module (ADR 0105), else the directory.
func packagesAt(dir string) []pkgInfo {
	if !layout.IsModule(os.DirFS(dir)) {
		return []pkgInfo{{root: dir}}
	}
	pkgs, _ := layout.Packages(os.DirFS(dir))
	out := []pkgInfo{{root: dir, module: true}}
	for _, p := range pkgs {
		if p.Folder != "" {
			out = append(out, pkgInfo{root: dir, module: true, folder: p.Folder})
		}
	}
	return out
}

// folderOf is the folder package a module's file belongs to: src/a/b/X is
// a/b's, test/a/b/X mirrors it, test/scenarios/ is the top level's.
func folderOf(moduleRoot, file string) (string, bool) {
	rel, err := filepath.Rel(moduleRoot, file)
	if err != nil {
		return "", false
	}
	rel = filepath.ToSlash(rel)
	var under string
	switch {
	case strings.HasPrefix(rel, layout.Src+"/"):
		under = strings.TrimPrefix(rel, layout.Src+"/")
	case strings.HasPrefix(rel, layout.Test+"/"+layout.Scenarios+"/"):
		return "", true
	case strings.HasPrefix(rel, layout.Test+"/"):
		under = strings.TrimPrefix(rel, layout.Test+"/")
	default:
		return "", false
	}
	dir := pathpkg.Dir(under)
	if dir == "." {
		dir = ""
	}
	return dir, true
}

// owner is the package a file belongs to: the deepest manifest package
// holding it (in a module, the folder package it's in), or the loose
// files.
func owner(pkgs []pkgInfo, file string) (pkgInfo, bool) {
	best, found := pkgInfo{}, false
	for _, p := range pkgs {
		if !within(p.root, file) {
			continue
		}
		if p.module {
			if folder, ok := folderOf(p.root, file); !ok || folder != p.folder {
				continue
			}
		}
		if p.loose && found {
			continue
		}
		if !found || best.loose || len(p.root) > len(best.root) {
			best, found = p, true
		}
	}
	return best, found
}

func within(dir, file string) bool {
	rel, err := filepath.Rel(dir, file)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// files lists p's .fiig files, leaving out packages nested in it.
func files(p pkgInfo) []string {
	var out []string
	if p.module {
		pkgs, _ := layout.Packages(os.DirFS(p.root))
		for _, lp := range pkgs {
			if lp.Folder == p.folder {
				for _, rel := range append(append([]string(nil), lp.Sources...), lp.Tests...) {
					out = append(out, filepath.Join(p.root, filepath.FromSlash(rel)))
				}
			}
		}
		sort.Strings(out)
		return out
	}
	filepath.WalkDir(p.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if path != p.root && (strings.HasPrefix(name, ".") || name == "node_modules" || name == "build" || name == "out") {
				return filepath.SkipDir
			}
			if path != p.root {
				if _, err := os.Stat(filepath.Join(path, manifest.Filename)); err == nil {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if filepath.Ext(path) == ".fiig" {
			out = append(out, path)
		}
		return nil
	})
	sort.Strings(out)
	return out
}

// check checks p with open's text over what is on disk, as `fiig check
// -workspace <workspace>` would.
func check(p pkgInfo, workspace string, open func(path string) (string, bool)) *snapshot {
	s := &snapshot{pkg: p, sources: map[string]string{}, diags: map[string][]checker.Diagnostic{},
		ids: map[syntax.Decl]declID{}, own: map[declID]site{}, depDirs: map[string]string{}, checked: map[string]*checker.Checked{}}
	rels := map[string]string{} // relative path -> absolute
	for _, f := range files(p) {
		text, ok := open(f)
		if !ok {
			data, err := os.ReadFile(f)
			if err != nil {
				continue
			}
			text = string(data)
		}
		rel, _ := filepath.Rel(p.root, f)
		rel = filepath.ToSlash(rel)
		rels[rel] = f
		s.sources[f] = text
		s.diags[f] = nil
	}
	manifestFile := filepath.Join(p.root, manifest.Filename)
	var packages, siblings types.Packages
	var provides map[string]string
	application := false
	if !p.loose {
		s.diags[manifestFile] = nil
		m, errs := manifest.Load(p.root)
		if len(errs) > 0 {
			for _, e := range errs {
				s.diags[manifestFile] = append(s.diags[manifestFile], checker.Diagnostic{Msg: e.Error()})
			}
		} else {
			s.path, provides = m.Path, m.Provides
			application = m.Kind == manifest.Application
			if p.module {
				s.path = layout.PathOf(m.Path, p.folder)
			}
			if loader.Wanted(p.root, []string{workspace}) {
				loaded, errs := loader.LoadForEditor(p.root, []string{workspace})
				if !p.module || p.folder == "" {
					for _, e := range errs {
						s.diags[manifestFile] = append(s.diags[manifestFile], checker.Diagnostic{Msg: e.Error()})
					}
				}
				if loaded != nil {
					packages = loaded.Packages
					for _, mp := range loaded.Module {
						if p.module && mp.Layout.Folder == p.folder {
							packages = mp.Package.Dependencies
						}
					}
					// A package's tests may import the module's other packages
					// (ADR 0102), so navigation reaches them too.
					if p.module {
						siblings = types.Packages{}
						for _, mp := range loaded.Module {
							if mp.Layout.Folder != p.folder {
								siblings[mp.Package.Path] = mp.Package
							}
						}
					}
					for _, e := range loaded.Lockfile.Dependencies {
						s.depDirs[e.Path] = moduleSrc(sourceDir(e.Source, p.root))
					}
					// A package of a module has the folder of its path under
					// that module's src/ (ADR 0105).
					reachable := types.Packages{}
					for path, pkg := range siblings {
						reachable[path] = pkg
					}
					for path, pkg := range packages {
						reachable[path] = pkg
					}
					for path := range reachable {
						s.depPaths = append(s.depPaths, path)
						if _, ok := s.depDirs[path]; ok {
							continue
						}
						if path == m.Path || strings.HasPrefix(path, m.Path+".") {
							s.depDirs[path] = filepath.Join(p.root, layout.Src, filepath.FromSlash(strings.ReplaceAll(strings.TrimPrefix(strings.TrimPrefix(path, m.Path), "."), ".", "/")))
							continue
						}
						for _, e := range loaded.Lockfile.Dependencies {
							if strings.HasPrefix(path, e.Path+".") {
								s.depDirs[path] = filepath.Join(sourceDir(e.Source, p.root), layout.Src, filepath.FromSlash(strings.ReplaceAll(strings.TrimPrefix(path, e.Path+"."), ".", "/")))
							}
						}
					}
				}
			}
		}
	}
	byRel := map[string]string{}
	for rel, abs := range rels {
		byRel[rel] = s.sources[abs]
	}
	diags, checked := checker.CheckSourcesWithSiblings(byRel, packages, siblings, s.path, provides)
	if p.module {
		// ADR 0104: a source file is named after its declaration.
		for rel, c := range checked {
			if strings.HasPrefix(rel, layout.Src+"/") && len(c.File.Decls) > 0 {
				if problem := layout.FileNameProblem(rel, syntax.DeclName(c.File.Decls[0])); problem != "" {
					diags = append(diags, checker.Diagnostic{File: rel, Msg: problem})
				}
			}
			// ADR 0108: an application declares nothing at the top level.
			// ADR 0132: but for the application declaration.
			if application && !checker.DeclaresApplication(c.File) {
				if problem := layout.TopLevelProblem(rel); problem != "" {
					diags = append(diags, checker.Diagnostic{File: rel, Rule: "H6", Msg: problem})
				}
			}
		}
	}
	for _, d := range diags {
		file := rels[d.File]
		if file == "" {
			file = manifestFile
			d.At, d.End = lexer.Pos{}, lexer.Pos{}
		}
		s.diags[file] = append(s.diags[file], d)
	}
	for rel, c := range checked {
		abs := rels[rel]
		s.checked[abs] = c
		for _, d := range c.File.Decls {
			id := declID{s.pkgKey(), syntax.DeclName(d)}
			s.ids[d] = id
			s.own[id] = site{abs, d}
		}
		if c.File.Private != nil {
			for _, d := range c.File.Private.Members {
				id := declID{s.pkgKey(), rel + "#" + syntax.DeclName(d)}
				s.ids[d] = id
				s.own[id] = site{abs, d}
			}
		}
	}
	for path, pkg := range packages {
		for name, d := range pkg.Universe.Decls {
			if _, mine := s.ids[d]; !mine {
				s.ids[d] = declID{path, name}
			}
		}
	}
	return s
}

// sourceDir is a lockfile entry's Source as a directory on disk, the
// packages built into the toolchain extracted to a cache (§7's
// go-to-declaration into fiig.http).
func sourceDir(source, root string) string {
	if dir, ok := strings.CutPrefix(source, "builtin:"); ok {
		return builtinDir(dir)
	}
	if !filepath.IsAbs(source) {
		return filepath.Join(root, filepath.FromSlash(source))
	}
	return filepath.FromSlash(source)
}

var (
	builtinOnce sync.Once
	builtinRoot string
)

// builtinDir writes the toolchain's packages once to a cache directory,
// so an editor can open their files.
func builtinDir(dir string) string {
	builtinOnce.Do(func() {
		base, err := os.UserCacheDir()
		if err != nil {
			base = os.TempDir()
		}
		builtinRoot = filepath.Join(base, "fiig", "stdlib")
		fs.WalkDir(stdlib.FS, ".", func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			data, err := fs.ReadFile(stdlib.FS, path)
			if err != nil {
				return nil
			}
			dst := filepath.Join(builtinRoot, filepath.FromSlash(path))
			if old, err := os.ReadFile(dst); err == nil && string(old) == string(data) {
				return nil
			}
			os.MkdirAll(filepath.Dir(dst), 0o755)
			os.WriteFile(dst, data, 0o644)
			return nil
		})
	})
	return filepath.Join(builtinRoot, filepath.FromSlash(pathpkg.Clean(dir)))
}

// declIn finds the file in dir that declares name, for a declaration of a
// package outside the workspace.
func declIn(dir, name string) (string, syntax.Decl, string, bool) {
	var found string
	var decl syntax.Decl
	var text string
	filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || found != "" || d.IsDir() || filepath.Ext(path) != ".fiig" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		f, _ := parser.Parse(string(data))
		for _, dd := range f.Decls {
			if syntax.DeclName(dd) == name {
				found, decl, text = path, dd, string(data)
			}
		}
		return nil
	})
	return found, decl, text, found != ""
}

// nameAt is where name is first written at or after start in src: a
// declaration's name after its keyword.
func nameAt(src string, start lexer.Pos, name string) (lexer.Pos, lexer.Pos) {
	if start.Offset < 0 || start.Offset > len(src) {
		return start, start
	}
	for _, t := range lexer.New(src[start.Offset:]).AllTokens() {
		if t.Kind == lexer.Ident && t.Text == name {
			s, e := t.Start, t.End
			s.Offset += start.Offset
			e.Offset += start.Offset
			return s, e
		}
		if t.Kind == lexer.LBrace || t.Kind == lexer.EOF {
			break
		}
	}
	return start, start
}

// memberSpan is where a field, variant, or gateway named member is
// declared in decl.
func memberSpan(src string, decl syntax.Decl, member string) (lexer.Pos, lexer.Pos, bool) {
	switch d := decl.(type) {
	case *syntax.ModelDecl:
		for _, f := range d.Fields {
			if f.Name == member {
				s, e := nameAt(src, f.Start, member)
				return s, e, true
			}
		}
	case *syntax.EnumDecl:
		for _, v := range d.Variants {
			if v.Name == member {
				s, e := nameAt(src, v.Start, member)
				return s, e, true
			}
		}
		for _, f := range d.Header {
			if f.Name == member {
				s, e := nameAt(src, f.Start, member)
				return s, e, true
			}
		}
	case *syntax.CapabilityDecl:
		if g := d.Gateway(member); g != nil {
			s, e := nameAt(src, g.Start, member)
			return s, e, true
		}
	case *syntax.ConfigDecl:
		// A config's field, `Limits.max`.
		for _, f := range d.Fields {
			if f.Name == member {
				s, e := nameAt(src, f.Start, member)
				return s, e, true
			}
		}
	case *syntax.FormulasDecl:
		if f := d.Member(member); f != nil {
			s, e := nameAt(src, f.Start, member)
			return s, e, true
		}
	}
	return lexer.Pos{}, lexer.Pos{}, false
}

// moduleSrc is dir's src/ for a module's top-level package, else dir.
func moduleSrc(dir string) string {
	if layout.IsModule(os.DirFS(dir)) {
		return filepath.Join(dir, layout.Src)
	}
	return dir
}
