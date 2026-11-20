package main

import (
	"os"
	"path/filepath"
	"testing"

	"fiig-lang/compiler/runtime"
	fiigsql "fiig-lang/libraries/sql"
	"fiig-lang/tooling/cli/testcmd"
)

// TestCorpusTestsPass is every corpus module (a folder with fiig.pkg.json,
// ADR 0105) checked with its dependencies and its tests run, as `fiig test`
// runs them: each gateway test on its own in-memory SQLite database. A
// module with no tests is still checked, every folder package of it.
func TestCorpusTestsPass(t *testing.T) {
	examples, err := filepath.Abs(filepath.Join("..", "..", "examples"))
	if err != nil {
		t.Fatal(err)
	}
	manifests, _ := filepath.Glob(filepath.Join(examples, "*", "fiig.pkg.json"))
	if len(manifests) == 0 {
		t.Fatal("no example is a module")
	}
	sql := runtime.FactoryRegistry{"sql": fiigsql.Factory}
	for _, m := range manifests {
		root := filepath.Dir(m)
		t.Run(filepath.Base(root), func(t *testing.T) {
			// fiig test writes the lockfile, with sources relative to the
			// working directory; put it back.
			lock := filepath.Join(root, "fiig.lock.json")
			if before, err := os.ReadFile(lock); err == nil {
				defer os.WriteFile(lock, before, 0o644)
			}
			if code := testcmd.Run(root, []string{examples}, sql); code != 0 {
				t.Fatalf("fiig test exited %d", code)
			}
		})
	}
}
