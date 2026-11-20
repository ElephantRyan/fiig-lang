package relayout

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fiig-lang/compiler/layout"
)

func writeModule(t *testing.T, kind string) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"fiig.pkg.json":     `{"path": "t.app", "kind": "` + kind + `", "version": "1.0.0"}`,
		"rules/double.fiig": "rule double(n: int) -> int {\n    return n * 2\n}\n",
		"rules/triple.fiig": "rule triple(n: int) -> int {\n    return n * 3\n}\n",
		"tests/double.fiig": "test doubles {\n    expect double(2) == 4\n}\n",
		"tests/both.fiig":   "test bothAgree {\n    expect double(1) + triple(1) == 5\n}\n",
	}
	for name, text := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func destinations(p *Plan) []string {
	var out []string
	for _, m := range p.Moves {
		out = append(out, m.To)
	}
	return out
}

// ADR 0108: an application's loose files need a folder the author names.
func TestAnApplicationsLooseFilesNeedAFolder(t *testing.T) {
	root := writeModule(t, "application")
	if _, err := Relayout(root, nil, ""); err == nil || !strings.Contains(err.Error(), "-into") {
		t.Fatalf("an application with loose files and no -into: %v, want an error naming -into", err)
	}
	if _, err := Relayout(root, nil, "Not A Folder"); err == nil || !strings.Contains(err.Error(), "isn't a folder name") {
		t.Fatalf("a bad -into: %v", err)
	}
}

func TestIntoPutsLooseFilesInAFolderAndTheirTestsBeside(t *testing.T) {
	root := writeModule(t, "application")
	plan, err := Relayout(root, nil, "math")
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(destinations(plan), " ")
	for _, want := range []string{"src/math/Double.fiig", "src/math/Triple.fiig", "test/math/Double.fiig", "test/scenarios/BothAgree.fiig"} {
		if !strings.Contains(got, want) {
			t.Errorf("destinations %q lack %s", got, want)
		}
	}
	if len(plan.Notes) != 1 || !strings.Contains(plan.Notes[0], "use t.app.math") {
		t.Errorf("notes = %v, want one about the scenario importing t.app.math", plan.Notes)
	}

	if err := plan.Apply(); err != nil {
		t.Fatal(err)
	}
	pkgs, errs := layout.Packages(os.DirFS(root))
	if len(errs) > 0 {
		t.Fatalf("the moved module breaks the layout: %v", errs)
	}
	for _, p := range pkgs {
		if p.Folder == "" && len(p.Sources) > 0 {
			t.Errorf("the top level still holds %v", p.Sources)
		}
	}
}

// A library's top-level package is its public face, so it keeps its files.
func TestALibraryKeepsItsTopLevelFiles(t *testing.T) {
	root := writeModule(t, "library")
	plan, err := Relayout(root, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(destinations(plan), " "); !strings.Contains(got, "src/Double.fiig") || strings.Contains(got, "src/math/") {
		t.Errorf("a library's destinations = %s", got)
	}
}
