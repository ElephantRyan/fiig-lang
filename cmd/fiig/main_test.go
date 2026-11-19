package main

import (
	"os"
	"path/filepath"
	"testing"

	"fiig-lang/compiler/runtime"
	fiigsql "fiig-lang/libraries/sql"
	"fiig-lang/tooling/cli/testcmd"
)

// TestCorpusTestsPass runs every example's tests/ as `fiig test -workspace
// examples` does (ADR 0091 §7): checked with its dependencies, then run,
// each gateway test on its own in-memory SQLite database.
func TestCorpusTestsPass(t *testing.T) {
	examples, err := filepath.Abs(filepath.Join("..", "..", "examples"))
	if err != nil {
		t.Fatal(err)
	}
	dirs, _ := filepath.Glob(filepath.Join(examples, "*", "tests"))
	if len(dirs) == 0 {
		t.Fatal("no example has tests/")
	}
	sql := runtime.FactoryRegistry{"sql": fiigsql.Factory}
	for _, dir := range dirs {
		root := filepath.Dir(dir)
		t.Run(filepath.Base(root), func(t *testing.T) {
			// fiig test writes the lockfile, with sources relative to the
			// working directory; put it back.
			lock := filepath.Join(root, "fiig.lock.json")
			if before, err := os.ReadFile(lock); err == nil {
				defer os.WriteFile(lock, before, 0o644)
			}
			var workspaces []string
			if _, err := os.Stat(filepath.Join(root, "fiig.pkg.json")); err == nil {
				workspaces = []string{examples}
			}
			if code := testcmd.Run(root, workspaces, sql); code != 0 {
				t.Fatalf("fiig test exited %d", code)
			}
		})
	}
}
