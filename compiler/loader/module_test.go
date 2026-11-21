package loader

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fiig-lang/compiler/checker"
)

// writeTree writes files (slash paths) under a fresh directory.
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
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

func appModule(extra map[string]string) map[string]string {
	files := map[string]string{
		"app/fiig.pkg.json":                   `{"path": "t.app", "kind": "library", "version": "1.0.0"}`,
		"app/src/Greeting.fiig":               "use t.app.ratings\n\nconversion greeting(score: int) -> string {\n    return \"rated ${ratings.clamp(score)}\"\n}\n",
		"app/src/ratings/Clamp.fiig":          "use t.app.ratings.limits\n\nconversion clamp(score: int) -> int {\n    return math.min(score, limits.highest())\n}\n",
		"app/src/ratings/limits/Highest.fiig": "rule highest() -> int {\n    return 10\n}\n",
		"app/test/ratings/Clamp.fiig":         "use t.app.ratings\n\ntest clampsHigh {\n    expect ratings.clamp(11) == 10\n}\n\ntest keepsLow {\n    expect ratings.clamp(3) == 3\n}\n",
		"app/test/Greeting.fiig":              "test greets {\n    expect greeting(4) == \"rated 4\"\n}\n",
	}
	for k, v := range extra {
		if v == "" {
			delete(files, k)
		} else {
			files[k] = v
		}
	}
	return files
}

// ADR 0105: each folder under src/ is a package, built in import order;
// the top-level package is the commands', checked with its tests.
func TestAModulesFoldersArePackages(t *testing.T) {
	ws := writeTree(t, appModule(nil))
	root := filepath.Join(ws, "app")
	loaded, errs := Load(root, nil)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	var folders []string
	for _, m := range loaded.Module {
		folders = append(folders, m.Layout.Folder)
	}
	if strings.Join(folders, ",") != "ratings/limits,ratings" {
		t.Errorf("module packages = %v, want ratings/limits then ratings", folders)
	}
	if _, ok := loaded.Packages["t.app.ratings"]; !ok {
		t.Errorf("packages = %v, want t.app.ratings", loaded.Packages)
	}
	diags, order, _, err := checker.CheckDirFiles(root, loaded.Packages)
	if err != nil || len(diags) != 0 {
		t.Fatalf("top-level package: %v %v", diags, err)
	}
	if strings.Join(order, ",") != "src/Greeting.fiig,test/Greeting.fiig" {
		t.Errorf("top-level files = %v", order)
	}
}

func TestTheLayoutsRules(t *testing.T) {
	cases := []struct {
		name  string
		extra map[string]string
		want  string
	}{
		{"file named after its declaration", map[string]string{"app/src/ratings/Clamp.fiig": "", "app/src/ratings/clamp.fiig": "rule clamp(score: int) -> int {\n    return score\n}\n", "app/test/ratings/Clamp.fiig": ""}, "so its file is src/ratings/Clamp.fiig (ADR 0104)"},
		{"code outside src", map[string]string{"app/rules/Extra.fiig": "rule extra() -> int {\n    return 1\n}\n"}, "rules/Extra.fiig: a module's code is under src/"},
		{"test mirrors nothing", map[string]string{"app/test/ratings/Missing.fiig": "test nothing {\n    expect 1 == 1\n}\n"}, "src/ratings/Missing.fiig doesn't exist"},
		{"folder name", map[string]string{"app/src/Bad_Folder/X.fiig": "rule x() -> int {\n    return 1\n}\n"}, "src/Bad_Folder: a package folder is named in lowercase"},
		{"cycle", map[string]string{"app/src/ratings/limits/Highest.fiig": "use t.app.ratings\n\nconversion highest() -> int {\n    return ratings.clamp(10)\n}\n"}, "import each other in a cycle"},
		{"top-level import", map[string]string{"app/src/ratings/limits/Highest.fiig": "use t.app\n\nrule highest() -> int {\n    return 10\n}\n"}, "the module's top-level package, which its folders can't import"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ws := writeTree(t, appModule(c.extra))
			_, errs := Load(filepath.Join(ws, "app"), nil)
			var all []string
			for _, e := range errs {
				all = append(all, e.Error())
			}
			if !strings.Contains(strings.Join(all, "\n"), c.want) {
				t.Errorf("errors = %v\nwant one containing %q", all, c.want)
			}
		})
	}
}

// One run reports every failing package that can be built, not only the first
// (docs/improvement-ideas.md B4): two independent packages that both fail are
// both reported, and a package that imports a failed one waits for the next run.
func TestEveryFailingPackageIsReportedInOneRun(t *testing.T) {
	ws := writeTree(t, map[string]string{
		"app/fiig.pkg.json":        `{"path": "t.app", "kind": "library", "version": "1.0.0"}`,
		"app/src/aaa/Broken.fiig":  "rule broken() -> int {\n    return missing\n}\n",
		"app/src/bbb/Broken.fiig":  "rule broken() -> int {\n    return alsoMissing\n}\n",
		"app/src/ccc/Fine.fiig":    "rule fine() -> int {\n    return 1\n}\n",
		"app/src/ddd/Needs.fiig":   "use t.app.aaa\n\nconversion needs() -> int {\n    return aaa.broken()\n}\n",
		"app/src/ddd/Another.fiig": "rule another() -> int {\n    return stillMissing\n}\n",
	})
	_, errs := Load(filepath.Join(ws, "app"), nil)
	var all []string
	for _, e := range errs {
		all = append(all, e.Error())
	}
	got := strings.Join(all, "\n")
	for _, want := range []string{`undefined name "missing"`, `undefined name "alsoMissing"`} {
		if !strings.Contains(got, want) {
			t.Errorf("errors = %v\nwant one containing %q", all, want)
		}
	}
	if strings.Contains(got, "stillMissing") {
		t.Errorf("errors = %v\na package that imports a failed one was checked anyway", all)
	}
}

// ADR 0108: an application declares nothing at the top level; a library's
// top-level package is its public face.
func TestAnApplicationHasNoTopLevelDeclarations(t *testing.T) {
	topLevel := func(kind string) []checker.Diagnostic {
		files := appModule(nil)
		files["app/fiig.pkg.json"] = `{"path": "t.app", "kind": "` + kind + `", "version": "1.0.0"}`
		ws := writeTree(t, files)
		root := filepath.Join(ws, "app")
		loaded, errs := Load(root, nil)
		if len(errs) > 0 {
			t.Fatal(errs)
		}
		diags, _, _, err := checker.CheckDirFiles(root, loaded.Packages)
		if err != nil {
			t.Fatal(err)
		}
		return diags
	}
	if diags := topLevel("library"); len(diags) != 0 {
		t.Errorf("a library's top level: %v", diags)
	}
	diags := topLevel("application")
	if len(diags) != 1 || diags[0].Rule != "H6" || diags[0].File != "src/Greeting.fiig" ||
		!strings.Contains(diags[0].Msg, "move src/Greeting.fiig to src/<domain>/Greeting.fiig") {
		t.Errorf("an application's top level = %v, want one H6 on src/Greeting.fiig", diags)
	}
}
