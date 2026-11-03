package registry

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFiigFile(t *testing.T, dir, name, src string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestMergeTagsCardsWithPackageIdentity(t *testing.T) {
	leaf := t.TempDir()
	writeFiigFile(t, leaf, "marker.fiig", "model Marker { n: int }")

	app := t.TempDir()
	writeFiigFile(t, app, "marker.fiig", "model Marker { n: int }")

	cards, fileErrs, err := Merge([]MergeSource{
		{Path: "app", Version: "1.0.0", Root: app},
		{Path: "leaf", Version: "2.0.0", Root: leaf},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(fileErrs) != 0 {
		t.Fatalf("unexpected file errors: %+v", fileErrs)
	}
	if len(cards) != 2 {
		t.Fatalf("want 2 cards (one per package, same bare name), got %+v", cards)
	}
	// Sorted by Package first, so "app" sorts before "leaf".
	if cards[0].Package != "app" || cards[0].PackageVersion != "1.0.0" {
		t.Fatalf("got %+v", cards[0])
	}
	if cards[1].Package != "leaf" || cards[1].PackageVersion != "2.0.0" {
		t.Fatalf("got %+v", cards[1])
	}
	if cards[0].Name != cards[1].Name {
		t.Fatalf("want the same bare name distinguishable only by Package, got %q and %q", cards[0].Name, cards[1].Name)
	}
}

func TestMergeSinglePackageUnaffectedByDefault(t *testing.T) {
	dir := t.TempDir()
	writeFiigFile(t, dir, "marker.fiig", "model Marker { n: int }")

	cards, _, err := Generate(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cards[0].Package != "" || cards[0].PackageVersion != "" {
		t.Fatalf("want Generate alone to leave Package/PackageVersion empty, got %+v", cards[0])
	}
}
