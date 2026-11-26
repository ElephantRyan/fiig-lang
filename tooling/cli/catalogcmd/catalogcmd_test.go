package catalogcmd

import (
	"path/filepath"
	"testing"
)

// A package built into the toolchain has no directory, and its cards come
// from the toolchain's own files (ADR 0074). examples/inbox-sender depends
// on fiig.http; before this, its catalog failed with "builtin:http: ... cannot
// find the file specified".
func TestACatalogReadsAPackageBuiltIntoTheToolchain(t *testing.T) {
	cards := loadCatalog(filepath.Join("..", "..", "..", "examples", "inbox-sender"), nil)
	byPackage := map[string]int{}
	for _, c := range cards {
		byPackage[c.Package]++
	}
	if byPackage["fiig.http"] == 0 || byPackage["examples.inboxsender"] == 0 {
		t.Errorf("cards by package = %v, want the app's and fiig.http's", byPackage)
	}
}
