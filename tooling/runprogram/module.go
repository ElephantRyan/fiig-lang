package runprogram

import (
	"fiig-lang/compiler/checker"
	"fiig-lang/compiler/loader"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// CheckedPackage is one of a module's folder packages (ADR 0105), checked
// with its tests: its files, their module-relative paths, and the
// packages it was checked against.
type CheckedPackage struct {
	Path     string
	Files    []*syntax.File
	Paths    []string
	Packages types.Packages
}

// CheckModulePackages checks each folder package of the module at root
// with its tests, against the dependencies the loader built it with. The
// loader checked its sources; this adds its tests, and gives the commands
// trees of their own to run them on.
func CheckModulePackages(root string, loaded *loader.Loaded) ([]checker.Diagnostic, []CheckedPackage, error) {
	var diags []checker.Diagnostic
	var out []CheckedPackage
	for _, m := range loaded.Module {
		siblings := moduleSiblings(loaded, m.Package.Path)
		d, paths, files, err := checker.CheckLayoutPackage(root, m.Layout, m.Package.Dependencies, siblings, m.Package.Path, loaded.Manifest.Provides, nil)
		if err != nil {
			return nil, nil, err
		}
		diags = append(diags, d...)
		// Tests run against the siblings they import, as they were checked.
		known := types.Packages{}
		for k, v := range siblings {
			known[k] = v
		}
		for k, v := range m.Package.Dependencies {
			known[k] = v
		}
		out = append(out, CheckedPackage{Path: m.Package.Path, Files: files, Paths: paths, Packages: known})
	}
	return diags, out, nil
}

// moduleSiblings is the module's folder packages other than the one at
// path (ADR 0105), which that package's tests may import beyond what its
// code does. Module holds no top-level package: its folders can't import it.
func moduleSiblings(loaded *loader.Loaded, path string) types.Packages {
	siblings := types.Packages{}
	for _, m := range loaded.Module {
		if m.Package.Path != path {
			siblings[m.Package.Path] = m.Package
		}
	}
	return siblings
}
