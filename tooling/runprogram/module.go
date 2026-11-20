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
		d, paths, files, err := checker.CheckLayoutPackage(root, m.Layout, m.Package.Dependencies, m.Package.Path, loaded.Manifest.Provides, nil)
		if err != nil {
			return nil, nil, err
		}
		diags = append(diags, d...)
		out = append(out, CheckedPackage{Path: m.Package.Path, Files: files, Paths: paths, Packages: m.Package.Dependencies})
	}
	return diags, out, nil
}
