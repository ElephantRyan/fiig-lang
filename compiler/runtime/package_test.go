package runtime

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"fiig-lang/compiler/checker"
	"fiig-lang/compiler/parser"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// loadPackageFiles parses every .fiig file under dir, recursively,
// without merging them into one Universe — compiler/checker_test.go's
// parsePackageFiles does the identical thing; duplicated here rather
// than exported across packages for one test-only helper.
func loadPackageFiles(t *testing.T, dir string) []*syntax.File {
	t.Helper()
	var files []*syntax.File
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(path) != ".fiig" {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		f, errs := parser.Parse(string(src))
		if len(errs) > 0 {
			t.Fatalf("parse %s: %v", path, errs)
		}
		files = append(files, f)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func buildTestPackage(t *testing.T, files []*syntax.File, packages types.Packages) *types.Package {
	t.Helper()
	pkg, diags := checker.BuildPackage(files, packages)
	if len(diags) > 0 {
		t.Fatalf("unexpected diagnostics: %+v", diags)
	}
	return pkg
}

// TestCallCrossPackage dogfoods Milestone 6's package-aware execution
// (ADR 0038) against the same real packages compiler/checker's own
// TestCrossPackageResolution checks: examples/standard-time (a plain
// value), examples/company-time (a rule depending on it, under its own
// "time" alias), and examples/time-library (an activity calling into
// company.time under yet another alias, "businessTime"). Proves
// execution actually crosses the package boundary and returns the real
// result, not a misrouted capability-call fault — the gap this ADR
// closes (compiler/runtime previously had no notion of a package alias
// at all).
func TestCallCrossPackage(t *testing.T) {
	root := findExamplesDir(t)

	standardTimeFiles := loadPackageFiles(t, filepath.Join(root, "standard-time"))
	standardTimePkg := buildTestPackage(t, standardTimeFiles, nil)

	companyTimeFiles := loadPackageFiles(t, filepath.Join(root, "company-time"))
	companyTimePkg := buildTestPackage(t, companyTimeFiles, types.Packages{"standard.time": standardTimePkg})

	timeLibraryFiles := loadPackageFiles(t, filepath.Join(root, "time-library"))
	universe, conflicts := types.BuildUniverse(timeLibraryFiles)
	if len(conflicts) > 0 {
		t.Fatalf("unexpected conflicts: %+v", conflicts)
	}

	packages := types.Packages{"company.time": companyTimePkg, "standard.time": standardTimePkg}
	imports := map[string]map[string]*types.Package{}
	for _, f := range timeLibraryFiles {
		aliases := checker.ResolveFileImports(f, packages)
		if len(aliases) == 0 {
			continue
		}
		for _, d := range f.Decls {
			if name := types.DeclName(d); name != "" {
				imports[name] = aliases
			}
		}
	}

	in := New(universe, NewCapabilityRegistry())
	in.Imports = imports

	from := Record{TypeName: "Instant", Fields: map[string]Value{"days": Int(10)}}
	v, fault := in.Call(context.Background(), "computeFollowUpDate", map[string]Value{"from": from})
	if fault != nil {
		t.Fatalf("Call: %v", fault)
	}
	rec := mustRecord(t, v)
	if rec.TypeName != "Instant" || rec.Fields["days"] != Int(13) {
		t.Fatalf("got %+v, want Instant{days: 13}", rec)
	}
}

// TestCallCrossPackageUndefinedDeclarationFaults proves a qualified
// call naming a real package alias but a nonexistent declaration faults
// clearly, rather than being silently misrouted to a capability call —
// the specific confusion this ADR's fix exists to avoid.
func TestCallCrossPackageUndefinedDeclarationFaults(t *testing.T) {
	root := findExamplesDir(t)
	standardTimeFiles := loadPackageFiles(t, filepath.Join(root, "standard-time"))
	standardTimePkg := buildTestPackage(t, standardTimeFiles, nil)

	file := mustParseFile(t, `
use standard.time as systemTime

activity bogus(from: systemTime.Instant) -> systemTime.Instant {
    return systemTime.doesNotExist(from)
}`)
	universe, conflicts := types.BuildUniverse([]*syntax.File{file})
	if len(conflicts) > 0 {
		t.Fatalf("unexpected conflicts: %+v", conflicts)
	}
	packages := types.Packages{"standard.time": standardTimePkg}
	aliases := checker.ResolveFileImports(file, packages)

	in := New(universe, NewCapabilityRegistry())
	in.Imports = map[string]map[string]*types.Package{"bogus": aliases}

	from := Record{TypeName: "Instant", Fields: map[string]Value{"days": Int(1)}}
	_, fault := in.Call(context.Background(), "bogus", map[string]Value{"from": from})
	if fault == nil {
		t.Fatal("want a fault for a qualified call to a nonexistent declaration")
	}
}

func mustParseFile(t *testing.T, src string) *syntax.File {
	t.Helper()
	f, errs := parser.Parse(src)
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	return f
}
