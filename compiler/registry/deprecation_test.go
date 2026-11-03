package registry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"fiig-lang/compiler/manifest"
)

func writeManifest(t *testing.T, dir string, m manifest.Manifest) {
	t.Helper()
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, manifest.Filename), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestGenerateTagsDeprecatedCard(t *testing.T) {
	dir := t.TempDir()
	writeFiigFile(t, dir, "add_business_days.fiig", `rule addBusinessDays(from: int, days: int) -> int {
    return from + days
}`)
	writeManifest(t, dir, manifest.Manifest{
		Path: "company.time", Kind: manifest.Library, Version: "2.4.0",
		Deprecations: map[string]manifest.Deprecation{
			"addBusinessDays": {Since: "2.0.0", Message: "use addWorkingDays instead"},
		},
	})

	cards, _, err := Generate(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != 1 {
		t.Fatalf("got %+v", cards)
	}
	if cards[0].Deprecated == nil || cards[0].Deprecated.Since != "2.0.0" || cards[0].Deprecated.Message != "use addWorkingDays instead" {
		t.Fatalf("want a deprecation notice, got %+v", cards[0].Deprecated)
	}
}

func TestGenerateNoManifestLeavesDeprecatedNil(t *testing.T) {
	dir := t.TempDir()
	writeFiigFile(t, dir, "marker.fiig", "model Marker { n: int }")

	cards, _, err := Generate(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cards[0].Deprecated != nil {
		t.Fatalf("want nil Deprecated with no manifest present, got %+v", cards[0].Deprecated)
	}
}

func TestFindDeprecatedFilter(t *testing.T) {
	cards := []*Card{
		{Name: "old", Kind: "rule", Catalog: Default, Deprecated: &manifest.Deprecation{Since: "2.0.0"}},
		{Name: "current", Kind: "rule", Catalog: Default},
	}
	results := Find(cards, Query{Deprecated: true})
	if len(results) != 1 || results[0].Name != "old" {
		t.Fatalf("got %+v", results)
	}
}
