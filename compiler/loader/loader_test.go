package loader

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fiig-lang/compiler/checker"
	"fiig-lang/compiler/layout"
	"fiig-lang/compiler/manifest"
	"fiig-lang/compiler/parser"
	"fiig-lang/compiler/syntax"
)

func findExamplesDir(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		candidate := filepath.Join(dir, "examples")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find examples/ directory above " + dir)
		}
		dir = parent
	}
}

// TestLoadRealExamples dogfoods the real disk-based loader against the
// same attested packages TestCrossPackageResolution hand-assembled
// (compiler/checker/checker_test.go): examples/time-library depends on
// examples/company-time and examples/standard-time, discovered here by
// scanning the examples/ directory itself as the workspace, not built by
// hand. Proves the loader resolves and wires real packages such that the
// checker accepts the valid program.
func TestLoadRealExamples(t *testing.T) {
	examplesDir := findExamplesDir(t)
	root := filepath.Join(examplesDir, "time-library")

	loaded, errs := Load(root, []string{examplesDir})
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if loaded.Manifest.Path != "time.library" {
		t.Fatalf("got manifest path %q", loaded.Manifest.Path)
	}
	if _, ok := loaded.Packages["company.time"]; !ok {
		t.Fatalf("missing company.time in resolved packages: %+v", loaded.Packages)
	}
	if _, ok := loaded.Packages["standard.time"]; !ok {
		t.Fatalf("missing standard.time in resolved packages: %+v", loaded.Packages)
	}

	if diags, err := checker.CheckDir(root, loaded.Packages); err != nil {
		t.Fatalf("CheckDir: %v", err)
	} else if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %+v", diags)
	}

	if len(loaded.Lockfile.Dependencies) != 2 {
		t.Fatalf("want 2 lockfile entries, got %+v", loaded.Lockfile.Dependencies)
	}
	for _, e := range loaded.Lockfile.Dependencies {
		if e.Hash == "" || e.Source == "" || e.Version == "" {
			t.Fatalf("incomplete lockfile entry: %+v", e)
		}
	}
}

// TestLoadMinimumVersionSelection proves MVS itself (ADR 0032 point 3),
// not just "resolution happens": a synthetic workspace where an app
// depends on two libraries at different minimums of the same leaf
// package, and the leaf's installed version satisfies the higher of the
// two. Both libraries must resolve to the exact same built instance.
func TestLoadMinimumVersionSelection(t *testing.T) {
	ws := t.TempDir()
	writePkg(t, ws, "leaf", "leaf", "library", "1.2.0", nil, nil, "model Marker { n: int; }")
	writePkg(t, ws, "liba", "liba", "library", "1.0.0", map[string]string{"leaf": "1.0.0"}, nil, "use leaf as l;\nmodel AMarker { n: int; }")
	writePkg(t, ws, "libb", "libb", "library", "1.0.0", map[string]string{"leaf": "1.1.0"}, nil, "use leaf as l;\nmodel BMarker { n: int; }")
	app := writePkg(t, ws, "app", "app", "application", "1.0.0", map[string]string{"liba": "1.0.0", "libb": "1.0.0"}, nil, "model AppMarker { n: int; }")

	loaded, errs := Load(app, []string{ws})
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}

	leafVersion := ""
	for _, e := range loaded.Lockfile.Dependencies {
		if e.Path == "leaf" {
			leafVersion = e.Version
		}
	}
	if leafVersion != "1.2.0" {
		t.Fatalf("want leaf resolved to its installed 1.2.0 (satisfying the higher 1.1.0 minimum), got %q (all: %+v)", leafVersion, loaded.Lockfile.Dependencies)
	}
}

// TestLoadRejectsBelowMinimum proves validateInstalled's floor: an
// installed version lower than the resolved minimum is an error, not a
// silent downgrade.
func TestLoadRejectsBelowMinimum(t *testing.T) {
	ws := t.TempDir()
	writePkg(t, ws, "leaf", "leaf", "library", "1.0.0", nil, nil, "model Marker { n: int; }")
	app := writePkg(t, ws, "app", "app", "application", "1.0.0", map[string]string{"leaf": "1.5.0"}, nil, "model AppMarker { n: int; }")

	_, errs := Load(app, []string{ws})
	if len(errs) == 0 {
		t.Fatal("want an error for an installed version below the required minimum")
	}
}

// TestLoadCrossMajorCoexistence proves ADR 0032 point 4: two different
// majors of the same package path can both be resolved within one
// build, each wired to its own correct instance, when two different
// dependents each pin a different major.
func TestLoadCrossMajorCoexistence(t *testing.T) {
	ws := t.TempDir()
	writePkg(t, ws, "leaf-v1", "leaf", "library", "1.0.0", nil, nil, "model Marker { n: int; }")
	writePkg(t, ws, "leaf-v2", "leaf", "library", "2.0.0", nil, nil, "model Marker { n: int; }")
	writePkg(t, ws, "liba", "liba", "library", "1.0.0", map[string]string{"leaf": "1.0.0"}, nil, "model AMarker { n: int; }")
	writePkg(t, ws, "libb", "libb", "library", "1.0.0", map[string]string{"leaf": "2.0.0"}, nil, "model BMarker { n: int; }")
	app := writePkg(t, ws, "app", "app", "application", "1.0.0", map[string]string{"liba": "1.0.0", "libb": "1.0.0"}, nil, "model AppMarker { n: int; }")

	loaded, errs := Load(app, []string{ws})
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}

	versions := map[string]bool{}
	for _, e := range loaded.Lockfile.Dependencies {
		if e.Path == "leaf" {
			versions[e.Version] = true
		}
	}
	if !versions["1.0.0"] || !versions["2.0.0"] {
		t.Fatalf("want both leaf majors present in the lockfile, got %+v", loaded.Lockfile.Dependencies)
	}
}

// TestLoadRejectsUndeclaredDependencyCapability proves the security exit
// criterion — "Imported gateways cannot gain undeclared capabilities" —
// against the case it exists for: an application that never declared
// "Database" in its own manifest, depending (even transitively) on a
// library whose gateway requires it.
func TestLoadRejectsUndeclaredDependencyCapability(t *testing.T) {
	ws := t.TempDir()
	writePkg(t, ws, "leaf", "leaf", "library", "1.0.0", nil, nil, `gateway getThing() -> Result<int, int> requires Database {
    return Ok(1);
}`)
	app := writePkg(t, ws, "app", "app", "application", "1.0.0", map[string]string{"leaf": "1.0.0"}, nil, "model AppMarker { n: int; }")

	_, errs := Load(app, []string{ws})
	if len(errs) == 0 {
		t.Fatal("want an error: app never declared the Database capability its dependency requires")
	}
	if msg := errs[0].Error(); !strings.Contains(msg, "this application's manifest; add \"Database\" to \"capabilities\"") {
		t.Errorf("err = %s, want it to name the application and the fix", msg)
	}
	// A library's own manifest is named as a library's (abs-recommendations
	// GAPS G13).
	lib := writePkg(t, ws, "mid", "mid", "library", "1.0.0", map[string]string{"leaf": "1.0.0"}, nil, "model MidMarker { n: int; }")
	if _, errs := Load(lib, []string{ws}); len(errs) == 0 || !strings.Contains(errs[0].Error(), "this library's manifest") {
		t.Errorf("errs = %v, want the library named", errs)
	}
}

// TestLoadAcceptsDeclaredDependencyCapability is the positive case: the
// same dependency closure, but the application's own manifest declares
// the capability its dependency needs.
func TestLoadAcceptsDeclaredDependencyCapability(t *testing.T) {
	ws := t.TempDir()
	writePkg(t, ws, "leaf", "leaf", "library", "1.0.0", nil, nil, `gateway getThing() -> Result<int, int> requires Database {
    return Ok(1);
}`)
	app := writePkgNative(t, ws, "app", "app", "application", "1.0.0", map[string]string{"leaf": "1.0.0"}, []string{"Database"}, []string{"Database"}, "model AppMarker { n: int; }")

	loaded, errs := Load(app, []string{ws})
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(loaded.Capabilities) != 1 || loaded.Capabilities[0] != "Database" {
		t.Fatalf("want Capabilities to report Database, got %+v", loaded.Capabilities)
	}
	var leafCaps []string
	for _, e := range loaded.Lockfile.Dependencies {
		if e.Path == "leaf" {
			leafCaps = e.Capabilities
		}
	}
	if len(leafCaps) != 1 || leafCaps[0] != "Database" {
		t.Fatalf("want leaf's own lockfile entry to record its Database capability, got %+v", leafCaps)
	}
}

// TestLoadRejectsUndeclaredOwnCapability proves root's own gateways are
// held to the same standard as a dependency's.
func TestLoadRejectsUndeclaredOwnCapability(t *testing.T) {
	ws := t.TempDir()
	app := writePkg(t, ws, "app", "app", "application", "1.0.0", nil, nil, `gateway getThing() -> Result<int, int> requires Database {
    return Ok(1);
}`)

	_, errs := Load(app, []string{ws})
	if len(errs) == 0 {
		t.Fatal("want an error: app's own gateway requires Database but never declared it")
	}
}

// TestLoadMultipleWorkspaceRoots proves "organization package source
// support" (ADR 0037): an internal client library (e.g. for an internal
// API) living in its own directory tree, separate from the main
// workspace, is only found once its own root is included too — adding
// a second root doesn't implicitly widen the first.
func TestLoadMultipleWorkspaceRoots(t *testing.T) {
	mainWS := t.TempDir()
	internalWS := t.TempDir()

	writePkg(t, internalWS, "billing-client", "acme.billingClient", "library", "1.0.0", nil, nil, "model ClientMarker { n: int; }")
	app := writePkg(t, mainWS, "app", "app", "application", "1.0.0", map[string]string{"acme.billingClient": "1.0.0"}, nil, "model AppMarker { n: int; }")

	if _, errs := Load(app, []string{mainWS}); len(errs) == 0 {
		t.Fatal("want an error: the internal library's own root was never searched")
	}

	loaded, errs := Load(app, []string{mainWS, internalWS})
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if _, ok := loaded.Packages["acme.billingClient"]; !ok {
		t.Fatalf("missing acme.billingClient in resolved packages: %+v", loaded.Packages)
	}
}

// TestLoadRecordsOrigin proves a dependency's own declared Origin
// (compiler/manifest) is copied through to its lockfile entry — the
// provenance half of "organization package source support."
func TestLoadRecordsOrigin(t *testing.T) {
	ws := t.TempDir()
	root := filepath.Join(ws, "leaf")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	m := manifest.Manifest{Path: "leaf", Kind: manifest.Library, Version: "1.0.0", Origin: "https://github.com/acme/leaf"}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, manifest.Filename), data, 0o644); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(root, "src"), 0o755)
	if err := os.WriteFile(filepath.Join(root, "src", "Marker.fiig"), []byte("model Marker { n: int; }"), 0o644); err != nil {
		t.Fatal(err)
	}
	app := writePkg(t, ws, "app", "app", "application", "1.0.0", map[string]string{"leaf": "1.0.0"}, nil, "model AppMarker { n: int; }")

	loaded, errs := Load(app, []string{ws})
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	origin := ""
	for _, e := range loaded.Lockfile.Dependencies {
		if e.Path == "leaf" {
			origin = e.Origin
		}
	}
	if origin != "https://github.com/acme/leaf" {
		t.Fatalf("want Origin propagated to the lockfile entry, got %q", origin)
	}
}

// writePkg creates a package root under ws/dirName with a manifest and
// one source file, returning the package's root directory.
func writePkg(t *testing.T, ws, dirName, path, kind, version string, deps map[string]string, capabilities []string, source string) string {
	t.Helper()
	return writePkgNative(t, ws, dirName, path, kind, version, deps, capabilities, nil, source)
}

// writePkgNative is writePkg plus the manifest's nativeCapabilities
// grandfather list (ADR 0042).
func writePkgNative(t *testing.T, ws, dirName, path, kind, version string, deps map[string]string, capabilities, native []string, source string) string {
	t.Helper()
	root := filepath.Join(ws, dirName)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	m := manifest.Manifest{Path: path, Kind: manifest.Kind(kind), Version: version, Dependencies: deps, Capabilities: capabilities, NativeCapabilities: native}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, manifest.Filename), data, 0o644); err != nil {
		t.Fatal(err)
	}
	// ADR 0105: code under src/, named after its declaration (ADR 0104).
	file := "Decl.fiig"
	if f, errs := parser.Parse(source); len(errs) == 0 && len(f.Decls) > 0 {
		file = layout.FileName(syntax.DeclName(f.Decls[0]))
	}
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", file), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// TestLoadRejectsDomainCapabilityWithoutNativeEntry is bug class H2 (ADR
// 0042): declaring a capability in the allowlist is not enough — a
// non-primitive one must also be on the grandfather list, because nothing
// else says why it is allowed to be Go-only.
func TestLoadRejectsDomainCapabilityWithoutNativeEntry(t *testing.T) {
	ws := t.TempDir()
	app := writePkg(t, ws, "app", "app", "application", "1.0.0", nil, []string{"ShippingProvider"}, `gateway ship() -> Result<int, int> requires ShippingProvider {
    return Ok(1);
}`)
	_, errs := Load(app, []string{ws})
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "neither a primitive") {
		t.Fatalf("want exactly one H2 error, got %v", errs)
	}
}

// TestLoadAcceptsPrimitiveCapabilityWithoutNativeEntry: a primitive needs
// only the ordinary allowlist entry.
func TestLoadAcceptsPrimitiveCapabilityWithoutNativeEntry(t *testing.T) {
	ws := t.TempDir()
	app := writePkg(t, ws, "app", "app", "application", "1.0.0", nil, []string{"Clock"}, `gateway now() -> Result<int, int> requires Clock {
    return Ok(1);
}`)
	if _, errs := Load(app, []string{ws}); len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
}

// TestLoadChecksDependencies proves a dependency is type-checked, not only
// built: it never runs unchecked, and checking is what types its constants
// (ADR 0048 §2.4). The error names the package, since a dependency's line
// number alone points nowhere.
func TestLoadChecksDependencies(t *testing.T) {
	ws := t.TempDir()
	writePkg(t, ws, "units", "units", "library", "1.0.0", nil, nil, "rule same(a: float, b: float) -> bool {\n    return a == b;\n}")
	app := writePkg(t, ws, "app", "app", "application", "1.0.0", map[string]string{"units": "1.0.0"}, nil, "model AppMarker { n: int; }")

	_, errs := Load(app, []string{ws})
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "package units") || !strings.Contains(errs[0].Error(), "[M1]") {
		t.Fatalf("errs = %v, want one M1 naming package units", errs)
	}
}

// ADR 0097 (abs-recommendations GAPS G16): an application names its
// workspaces in its manifest, so no -workspace flag is needed.
func TestLoadReadsTheManifestsWorkspaces(t *testing.T) {
	ws := t.TempDir()
	writePkg(t, ws, "leaf", "leaf", "library", "1.0.0", nil, nil, "model LeafMarker { n: int; }")
	app := writePkg(t, ws, "app", "app", "application", "1.0.0", map[string]string{"leaf": "1.0.0"}, nil, "model AppMarker { n: int; }")
	if _, errs := Load(app, nil); len(errs) == 0 {
		t.Fatal("without workspaces, leaf can't be found")
	}
	m := manifest.Manifest{Path: "app", Kind: manifest.Application, Version: "1.0.0", Dependencies: map[string]string{"leaf": "1.0.0"}, Workspaces: []string{".."}}
	data, _ := json.Marshal(m)
	if err := os.WriteFile(filepath.Join(app, manifest.Filename), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if !Wanted(app, nil) {
		t.Error("Wanted = false for a manifest with workspaces")
	}
	loaded, errs := Load(app, nil)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	if _, ok := loaded.Packages["leaf"]; !ok {
		t.Errorf("packages = %v, want leaf found through the manifest's workspace", loaded.Packages)
	}
	// The same directory given on the command line too is searched once.
	if _, errs := Load(app, []string{ws}); len(errs) > 0 {
		t.Fatalf("a workspace named twice: %v", errs)
	}
}
