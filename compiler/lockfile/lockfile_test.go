package lockfile

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"fiig-lang/compiler/version"
)

func TestHashFilesDeterministicRegardlessOfMapOrder(t *testing.T) {
	a := HashFiles(map[string][]byte{"b.fiig": []byte("B"), "a.fiig": []byte("A")})
	b := HashFiles(map[string][]byte{"a.fiig": []byte("A"), "b.fiig": []byte("B")})
	if a != b {
		t.Fatalf("want the same hash regardless of map iteration order, got %q and %q", a, b)
	}
}

func TestHashFilesDiffersOnContentChange(t *testing.T) {
	a := HashFiles(map[string][]byte{"a.fiig": []byte("A")})
	b := HashFiles(map[string][]byte{"a.fiig": []byte("B")})
	if a == b {
		t.Fatal("want different hashes for different content")
	}
}

func TestWriteLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	lf := &Lockfile{Dependencies: []Entry{
		{Path: "standard.time", Version: "1.0.0", Hash: "sha256:abc", Source: "examples/standard-time"},
		{Path: "company.time", Version: "2.4.0", Hash: "sha256:def", Source: "examples/company-time"},
	}}
	if err := Write(dir, lf); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Dependencies) != 2 {
		t.Fatalf("got %+v", got)
	}
	// Write sorts by Path, so company.time (written second above) should
	// come first on disk.
	if got.Dependencies[0].Path != "company.time" || got.Dependencies[1].Path != "standard.time" {
		t.Fatalf("want sorted by path, got %+v", got.Dependencies)
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := Load(t.TempDir()); err == nil {
		t.Fatal("want an error for a missing lockfile")
	}
}

// TestWriteRecordsToolchain: the lockfile names the toolchain that wrote
// it, whatever the caller's Lockfile held.
func TestWriteRecordsToolchain(t *testing.T) {
	root := t.TempDir()
	if err := Write(root, &Lockfile{Toolchain: "stale"}); err != nil {
		t.Fatal(err)
	}
	lf, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if lf.Toolchain != version.Version {
		t.Fatalf("toolchain = %q, want %q", lf.Toolchain, version.Version)
	}
}

// ADR 0138 §2 (Hearsay GAPS G62): a builtin's bytes are pinned by the
// toolchain, so its entry carries no hash and a newer toolchain build
// doesn't rewrite the file; a package from a workspace keeps its hash.
func TestWriteOmitsTheHashOfABuiltin(t *testing.T) {
	dir := t.TempDir()
	lf := &Lockfile{Dependencies: []Entry{
		{Path: "fiig.http", Version: "1.0.0", Hash: "sha256:aaaa", Source: "builtin:http"},
		{Path: "acme.client", Version: "2.0.0", Hash: "sha256:bbbb", Source: "../acme-client"},
	}}
	if err := Write(dir, lf); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if h := got.Dependencies[1].Hash; h != "" {
		t.Errorf("fiig.http hash = %q, want none", h)
	}
	if h := got.Dependencies[0].Hash; h != "sha256:bbbb" {
		t.Errorf("acme.client hash = %q, want sha256:bbbb", h)
	}
	if lf.Dependencies[0].Hash != "sha256:aaaa" {
		t.Error("Write changed its caller's entries")
	}
}

// Hearsay GAPS G62: a checkout that turned the file's LFs into CRLFs keeps
// it byte for byte when nothing in it changed, and gets LFs when something did.
func TestWriteLeavesAnUnchangedFileAlone(t *testing.T) {
	dir := t.TempDir()
	lf := &Lockfile{Dependencies: []Entry{{Path: "acme.client", Version: "2.0.0", Source: "../acme-client"}}}
	if err := Write(dir, lf); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, Filename)
	written, _ := os.ReadFile(path)
	crlf := bytes.ReplaceAll(written, []byte("\n"), []byte("\r\n"))
	if err := os.WriteFile(path, crlf, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Write(dir, lf); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); !bytes.Equal(got, crlf) {
		t.Errorf("an unchanged file was rewritten:\n%q", got)
	}
	lf.Dependencies[0].Version = "2.1.0"
	if err := Write(dir, lf); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); bytes.Contains(got, []byte("\r")) || !bytes.Contains(got, []byte("2.1.0")) {
		t.Errorf("a changed file wasn't written: %q", got)
	}
}
