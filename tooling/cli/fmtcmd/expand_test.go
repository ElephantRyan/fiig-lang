package fmtcmd

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

// abs-recommendations G48: a directory argument is the .fiig files under it,
// outside hidden folders; a file argument stays itself.
func TestADirectoryIsItsFiigFiles(t *testing.T) {
	root := t.TempDir()
	for _, f := range []string{"src/B.fiig", "src/a/A.fiig", "src/notes.txt", ".git/Hidden.fiig", "test/T.fiig"} {
		p := filepath.Join(root, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := expandDirs([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	var rel []string
	for _, p := range got {
		r, _ := filepath.Rel(root, p)
		rel = append(rel, filepath.ToSlash(r))
	}
	sort.Strings(rel)
	if want := []string{"src/B.fiig", "src/a/A.fiig", "test/T.fiig"}; !reflect.DeepEqual(rel, want) {
		t.Errorf("a directory expanded to %v, want %v", rel, want)
	}

	one := filepath.Join(root, "src", "B.fiig")
	got, err = expandDirs([]string{one})
	if err != nil || len(got) != 1 || got[0] != one {
		t.Errorf("a file argument = %v, %v; want itself", got, err)
	}
}
