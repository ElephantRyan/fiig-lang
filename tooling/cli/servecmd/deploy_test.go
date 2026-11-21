package servecmd

import (
	"os"
	"path/filepath"
	"testing"
)

func none(string) (string, bool) { return "", false }

func writeApp(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, src := range files {
		p := filepath.Join(root, name)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func mustID(t *testing.T, root string, lookup func(string) (string, bool)) int64 {
	t.Helper()
	id, err := deployID(root, lookup)
	if err != nil {
		t.Fatal(err)
	}
	if id < 0 {
		t.Fatalf("id = %d, want a non-negative int", id)
	}
	return id
}

// ADR 0124 §6: replicas of one deploy agree, changed code is a new deploy,
// and neither line endings nor files that aren't the program matter.
func TestDeployIDFollowsTheProgram(t *testing.T) {
	app := map[string]string{"a.fiig": "workflow a() -> int {\n    return 1\n}\n", "fiig.pkg.json": `{"path": "x"}`}
	a := mustID(t, writeApp(t, app), none)
	if b := mustID(t, writeApp(t, app), none); a != b {
		t.Errorf("the same program has ids %d and %d", a, b)
	}
	changed := map[string]string{"a.fiig": "workflow a() -> int {\n    return 2\n}\n", "fiig.pkg.json": app["fiig.pkg.json"]}
	if c := mustID(t, writeApp(t, changed), none); c == a {
		t.Errorf("changed code has the same id %d", a)
	}
	crlf := map[string]string{"a.fiig": "workflow a() -> int {\r\n    return 1\r\n}\r\n", "fiig.pkg.json": app["fiig.pkg.json"]}
	if d := mustID(t, writeApp(t, crlf), none); d != a {
		t.Errorf("CRLF line endings changed the id: %d, %d", d, a)
	}
	extra := map[string]string{"a.fiig": app["a.fiig"], "fiig.pkg.json": app["fiig.pkg.json"], "README.md": "hello", ".git/config.json": "{}"}
	if e := mustID(t, writeApp(t, extra), none); e != a {
		t.Errorf("a file that isn't the program changed the id: %d, %d", e, a)
	}
}

// $FIIG_DEPLOY names the deploy outright.
func TestDeployIDCanBeNamed(t *testing.T) {
	root := writeApp(t, map[string]string{"a.fiig": "workflow a() -> int {\n    return 1\n}\n"})
	named := func(v string) func(string) (string, bool) {
		return func(k string) (string, bool) { return v, k == "FIIG_DEPLOY" }
	}
	v1, v1again, v2 := mustID(t, root, named("v1")), mustID(t, root, named("v1")), mustID(t, root, named("v2"))
	if v1 != v1again || v1 == v2 {
		t.Errorf("ids = %d, %d, %d", v1, v1again, v2)
	}
	if v1 == mustID(t, root, none) {
		t.Error("a named deploy has the id of the unnamed one")
	}
}
