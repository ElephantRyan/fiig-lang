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
					out = append(out, pkgInfo{root: path})
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
		out = append(out, pkgInfo{root: root})
	} else if loose {
		out = append(out, pkgInfo{root: root, loose: true})
	}
	return out
}

// owner is the package a file belongs to: the deepest manifest package
// holding it, or the loose files.
func owner(pkgs []pkgInfo, file string) (pkgInfo, bool) {
	best, found := pkgInfo{}, false
	for _, p := range pkgs {
		if !within(p.root, file) {
			continue
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
	var packages types.Packages
	var provides map[string]string
	if !p.loose {
		s.diags[manifestFile] = nil
		m, errs := manifest.Load(p.root)
		if len(errs) > 0 {
			for _, e := range errs {
				s.diags[manifestFile] = append(s.diags[manifestFile], checker.Diagnostic{Msg: e.Error()})
			}
		} else {
			s.path, provides = m.Path, m.Provides
			if loader.Wanted(p.root, []string{workspace}) {
				loaded, errs := loader.Load(p.root, []string{workspace})
				for _, e := range errs {
					s.diags[manifestFile] = append(s.diags[manifestFile], checker.Diagnostic{Msg: e.Error()})
				}
				if loaded != nil {
					packages = loaded.Packages
					for _, e := range loaded.Lockfile.Dependencies {
						s.depDirs[e.Path] = sourceDir(e.Source, p.root)
						s.depPaths = append(s.depPaths, e.Path)
					}
				}
			}
		}
	}
	byRel := map[string]string{}
	for rel, abs := range rels {
		byRel[rel] = s.sources[abs]
	}
	diags, checked := checker.CheckSources(byRel, packages, s.path, provides)
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
	case *syntax.FormulasDecl:
		if f := d.Member(member); f != nil {
			s, e := nameAt(src, f.Start, member)
			return s, e, true
		}
	}
	return lexer.Pos{}, lexer.Pos{}, false
}
