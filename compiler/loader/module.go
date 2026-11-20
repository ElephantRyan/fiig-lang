package loader

import (
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"fiig-lang/compiler/checker"
	"fiig-lang/compiler/layout"
	"fiig-lang/compiler/parser"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// ModulePackage is one package of the root module, other than its
// top-level one (ADR 0105): what the loader built and checked, and the
// files it came from, for the commands that also check and run its tests.
type ModulePackage struct {
	Package *types.Package
	Layout  *layout.Package
}

// builtModule is a module in ADR 0105's layout, built: every package by
// path, the parsed files of all of them (for the capability allowlist), the
// sources the lockfile hashes, and the root module's packages in order.
type builtModule struct {
	packages types.Packages
	files    []*syntax.File
	hashed   map[string][]byte
	ordered  []ModulePackage
}

// buildModule builds the module in fsys, at modulePath, against deps (every
// package of every module its manifest depends on). Each folder under src/
// is a package; a package may import a sibling, and siblings are built in
// import order. skipRoot leaves out the top-level package, which the
// commands check for the root module, with its tests. where names the
// module in errors: "" for the root module, whose paths are relative to it.
func buildModule(fsys fs.FS, modulePath, where string, deps types.Packages, provides map[string]string, skipRoot, lenient bool) (*builtModule, []error) {
	prefix := func(e error) error {
		if where == "" {
			return e
		}
		return fmt.Errorf("package %s (%s): %v", modulePath, where, e)
	}
	pkgs, layoutErrs := layout.Packages(fsys)
	var errs []error
	for _, e := range layoutErrs {
		errs = append(errs, prefix(e))
	}
	if len(errs) > 0 {
		return nil, errs
	}
	b := &builtModule{packages: types.Packages{}, hashed: map[string][]byte{}}
	sources := map[*layout.Package]map[string]string{}
	byPath := map[string]*layout.Package{}
	for _, p := range pkgs {
		byPath[layout.PathOf(modulePath, p.Folder)] = p
		src := map[string]string{}
		for _, rel := range p.Sources {
			data, err := fs.ReadFile(fsys, rel)
			if err != nil {
				return nil, []error{prefix(err)}
			}
			src[rel] = string(data)
			b.hashed[rel] = data
		}
		sources[p] = src
	}

	// Which siblings each package imports, from its files' use lines.
	imports := map[*layout.Package][]*layout.Package{}
	for _, p := range pkgs {
		seen := map[*layout.Package]bool{}
		for rel, text := range sources[p] {
			for _, imp := range importPaths(text) {
				sib := byPath[imp]
				if sib == nil {
					if i := strings.LastIndex(imp, "."); i > 0 {
						sib = byPath[imp[:i]] // a group imported by path tail
					}
				}
				if sib == nil || sib == p || seen[sib] {
					continue
				}
				if sib.Folder == "" {
					errs = append(errs, prefix(fmt.Errorf("%s imports %s, the module's top-level package, which its folders can't import; move what they share into a folder (ADR 0105)", rel, modulePath)))
					continue
				}
				seen[sib] = true
				imports[p] = append(imports[p], sib)
			}
		}
	}
	order, cycle := topoOrder(pkgs, imports)
	if cycle != "" {
		errs = append(errs, prefix(fmt.Errorf("packages import each other in a cycle: %s (ADR 0105)", cycle)))
	}
	if len(errs) > 0 {
		return nil, errs
	}

	for _, p := range order {
		path := layout.PathOf(modulePath, p.Folder)
		if p.Folder == "" && skipRoot {
			continue
		}
		pkgDeps := types.Packages{}
		for k, v := range deps {
			pkgDeps[k] = v
		}
		for _, sib := range imports[p] {
			sibPath := layout.PathOf(modulePath, sib.Folder)
			pkgDeps[sibPath] = b.packages[sibPath]
		}
		diags, checked := checker.CheckSources(sources[p], pkgDeps, path, provides)
		var files []*syntax.File
		rels := make([]string, 0, len(checked))
		for rel := range checked {
			rels = append(rels, rel)
		}
		sort.Strings(rels)
		for _, rel := range rels {
			f := checked[rel].File
			files = append(files, f)
			if len(f.Decls) > 0 {
				if problem := layout.FileNameProblem(rel, syntax.DeclName(f.Decls[0])); problem != "" {
					errs = append(errs, prefix(fmt.Errorf("%s", problem)))
				}
			}
		}
		for _, d := range diags {
			errs = append(errs, prefix(fmt.Errorf("%s", d)))
		}
		if len(errs) > 0 && !lenient {
			return nil, errs
		}
		// Lenient (an editor's load): build it anyway, so its dependents
		// still resolve; the editor checks each package itself.
		errs = nil
		pkg, _ := checker.BuildPackage(files, pkgDeps)
		pkg.Path = path
		pkg.Provides = checker.CodeProvides(files, path, provides)
		b.packages[path] = pkg
		b.files = append(b.files, files...)
		if p.Folder != "" {
			b.ordered = append(b.ordered, ModulePackage{Package: pkg, Layout: p})
		}
	}
	if skipRoot {
		// The top-level package's files still count for the allowlist.
		for _, p := range pkgs {
			if p.Folder == "" {
				for _, text := range sources[p] {
					f, _ := parser.Parse(text)
					b.files = append(b.files, f)
				}
			}
		}
	}
	return b, nil
}

// importPaths is the dotted path of each `use` line in text, read
// without a full parse.
func importPaths(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		rest, ok := strings.CutPrefix(line, "use ")
		if !ok {
			continue
		}
		path, _, _ := strings.Cut(strings.TrimSpace(rest), " ")
		out = append(out, path)
	}
	return out
}

// topoOrder is pkgs with each after the siblings it imports, or the
// packages of a cycle.
func topoOrder(pkgs []*layout.Package, imports map[*layout.Package][]*layout.Package) ([]*layout.Package, string) {
	state := map[*layout.Package]int{}
	var out []*layout.Package
	var stack []string
	var cycle string
	var visit func(p *layout.Package) bool
	visit = func(p *layout.Package) bool {
		switch state[p] {
		case 1:
			cycle = strings.Join(append(stack, folderName(p)), " -> ")
			return false
		case 2:
			return true
		}
		state[p] = 1
		stack = append(stack, folderName(p))
		for _, sib := range imports[p] {
			if !visit(sib) {
				return false
			}
		}
		stack = stack[:len(stack)-1]
		state[p] = 2
		out = append(out, p)
		return true
	}
	for _, p := range pkgs {
		if !visit(p) {
			return nil, cycle
		}
	}
	return out, ""
}

func folderName(p *layout.Package) string {
	if p.Folder == "" {
		return "src/"
	}
	return "src/" + p.Folder
}
