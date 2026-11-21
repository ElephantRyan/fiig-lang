package testcmd

import (
	"encoding/json"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"testing"

	"fiig-lang/compiler/stdlib"
)

// Every package the toolchain ships passes its own fiig tests. A package
// path under fiig. is reserved for the toolchain, so each runs as a copy
// under another path, allowed the primitives the packages use, with its
// imports of itself following it.
func TestTheStandardLibraryPassesItsTests(t *testing.T) {
	dirs, err := fs.ReadDir(stdlib.FS, ".")
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		if _, err := fs.Stat(stdlib.FS, path.Join(d.Name(), "test")); err != nil {
			continue
		}
		t.Run(d.Name(), func(t *testing.T) {
			root := t.TempDir()
			copyPath := "stdlibtest." + d.Name()
			ownImport := regexp.MustCompile(`(?m)^use fiig\.` + regexp.QuoteMeta(d.Name()) + `([.\s]|$)`)
			err := fs.WalkDir(stdlib.FS, d.Name(), func(p string, e fs.DirEntry, err error) error {
				if err != nil || e.IsDir() {
					return err
				}
				data, err := fs.ReadFile(stdlib.FS, p)
				if err != nil {
					return err
				}
				dest := filepath.Join(root, filepath.FromSlash(p[len(d.Name()):]))
				if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
					return err
				}
				if path.Ext(p) == ".fiig" {
					data = ownImport.ReplaceAll(data, []byte("use "+copyPath+"$1"))
				}
				return os.WriteFile(dest, data, 0o644)
			})
			if err != nil {
				t.Fatal(err)
			}
			manifestPath := filepath.Join(root, "fiig.pkg.json")
			raw, err := os.ReadFile(manifestPath)
			if err != nil {
				t.Fatal(err)
			}
			var m map[string]any
			if err := json.Unmarshal(raw, &m); err != nil {
				t.Fatal(err)
			}
			m["path"] = copyPath
			m["capabilities"] = []string{"Socket", "Session"}
			out, _ := json.Marshal(m)
			if err := os.WriteFile(manifestPath, out, 0o644); err != nil {
				t.Fatal(err)
			}
			code, results := runJSON(t, root)
			for name, r := range results {
				if r.Outcome != "pass" {
					t.Errorf("%s failed: %+v", name, r)
				}
			}
			if code != 0 || len(results) == 0 {
				t.Fatalf("fiig test exited %d with %d results", code, len(results))
			}
		})
	}
}
