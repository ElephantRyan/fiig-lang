// Package fixture lays test fixtures out as modules (ADRs 0102, 0104,
// 0105), so a test can still write a package as a map of loose files.
package fixture

import (
	"path"
	"strings"

	"fiig-lang/compiler/layout"
	"fiig-lang/compiler/parser"
	"fiig-lang/compiler/syntax"
)

// Module returns files with every .fiig file of a package (a folder with a
// fiig.pkg.json among files) moved into the module layout: source files to
// src/<Declaration>.fiig, test files to test/scenarios/. Files outside any
// package, and files already under src/ or test/, are kept as they are.
func Module(files map[string]string) map[string]string {
	var roots []string
	for name := range files {
		if path.Base(name) == "fiig.pkg.json" {
			roots = append(roots, path.Dir(name))
		}
	}
	out := map[string]string{}
	for name, text := range files {
		root := packageRoot(name, roots)
		if path.Ext(name) != ".fiig" || root == "" {
			out[name] = text
			continue
		}
		rel := strings.TrimPrefix(name, root+"/")
		if root == "." {
			rel = name
		}
		if strings.HasPrefix(rel, layout.Src+"/") || strings.HasPrefix(rel, layout.Test+"/") {
			out[name] = text
			continue
		}
		file := path.Base(rel)
		if f, errs := parser.Parse(text); len(errs) == 0 && len(f.Decls) > 0 {
			file = layout.FileName(syntax.DeclName(f.Decls[0]))
		}
		dest := path.Join(layout.Src, file)
		if syntax.IsTestPath(rel) {
			dest = path.Join(layout.Test, layout.Scenarios, file)
		}
		out[path.Join(root, dest)] = text
	}
	return out
}

// packageRoot is the deepest root that name is under, or "".
func packageRoot(name string, roots []string) string {
	best := ""
	for _, r := range roots {
		if (r == "." || strings.HasPrefix(name, r+"/")) && len(r) >= len(best) {
			best = r
		}
	}
	return best
}
