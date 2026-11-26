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

// Every package the toolchain ships passes its own fiig tests: the
// standard library, and the packages bundled with it (ADR 0152). A
// standard library path under fiig. is reserved for the toolchain, and a
// bundled one is already taken by the embedded copy, so each runs as a
// copy under another path, allowed the primitives the packages use, with
// its imports of itself following it.
func TestTheStandardLibraryPassesItsTests(t *testing.T) {
	var dirs []string
	err := fs.WalkDir(stdlib.FS, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return err
		}
		if _, err := fs.Stat(stdlib.FS, path.Join(p, "fiig.pkg.json")); err != nil {
			return nil
		}
		if _, err := fs.Stat(stdlib.FS, path.Join(p, "test")); err == nil {
			dirs = append(dirs, p)
		}
		return fs.SkipDir
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range dirs {
		t.Run(dir, func(t *testing.T) {
			root := t.TempDir()
			raw, err := fs.ReadFile(stdlib.FS, path.Join(dir, "fiig.pkg.json"))
			if err != nil {
				t.Fatal(err)
			}
			var m map[string]any
			if err := json.Unmarshal(raw, &m); err != nil {
				t.Fatal(err)
			}
			own, _ := m["path"].(string)
			copyPath := "stdlibtest." + path.Base(dir)
			ownImport := regexp.MustCompile(`(?m)^use ` + regexp.QuoteMeta(own) + `([.\s]|$)`)
			err = fs.WalkDir(stdlib.FS, dir, func(p string, e fs.DirEntry, err error) error {
				if err != nil || e.IsDir() {
					return err
				}
				data, err := fs.ReadFile(stdlib.FS, p)
				if err != nil {
					return err
				}
				dest := filepath.Join(root, filepath.FromSlash(p[len(dir):]))
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
