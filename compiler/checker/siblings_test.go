package checker

import (
	"strings"
	"testing"

	"fiig-lang/compiler/parser"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// ADR 0102: a test in a module's package may import any other package of the
// module, as a Go external test package may, though the package's code
// imports only what it depends on (abs-recommendations G47).

func siblingPackage(t *testing.T, src string) *types.Package {
	t.Helper()
	f, errs := parser.Parse(src)
	if len(errs) > 0 {
		t.Fatalf("parse: %v", errs)
	}
	pkg, diags := BuildPackage([]*syntax.File{f}, nil)
	if len(diags) > 0 {
		t.Fatalf("sibling: %+v", diags)
	}
	return pkg
}

const (
	siblingRule = "rule double(n: int) -> int {\n    return n * 2\n}\n"
	usesSibling = "use t.app.b\n\ntest doubles {\n    expect b.double(3) == 6\n}\n"
	ownRule     = "rule own() -> int {\n    return 1\n}\n"
)

func notADependency(diags []Diagnostic) bool {
	for _, d := range diags {
		if strings.Contains(d.Msg, "isn't a dependency of this package") {
			return true
		}
	}
	return false
}

func TestTestMayImportASiblingPackage(t *testing.T) {
	b := types.Packages{"t.app.b": siblingPackage(t, siblingRule)}
	sources := map[string]string{
		"src/Own.fiig":      ownRule,
		"test/Doubles.fiig": usesSibling,
	}

	diags, _ := CheckSources(sources, types.Packages{"t.app.other": b["t.app.b"]}, "t.app.a", nil)
	if !notADependency(diags) {
		t.Fatalf("without siblings the import should be refused, got %+v", diags)
	}

	diags, _ = CheckSourcesWithSiblings(sources, types.Packages{"t.app.other": b["t.app.b"]}, b, "t.app.a", nil)
	if len(diags) > 0 {
		t.Fatalf("a test importing a sibling: %+v", diags)
	}
}

func TestSourceStillImportsOnlyItsDependencies(t *testing.T) {
	b := types.Packages{"t.app.b": siblingPackage(t, siblingRule)}
	sources := map[string]string{
		"src/Own.fiig": "use t.app.b\n\nrule own() -> int {\n    return b.double(1)\n}\n",
	}
	diags, _ := CheckSourcesWithSiblings(sources, types.Packages{"t.app.other": b["t.app.b"]}, b, "t.app.a", nil)
	if !notADependency(diags) {
		t.Fatalf("a source file may not import a package its package doesn't depend on, got %+v", diags)
	}
}
