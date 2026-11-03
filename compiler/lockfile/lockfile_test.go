package lockfile

import (
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
