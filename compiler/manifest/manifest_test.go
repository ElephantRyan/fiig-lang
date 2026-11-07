package manifest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseValid(t *testing.T) {
	m, errs := Parse([]byte(`{
		"path": "company.time",
		"kind": "library",
		"version": "2.4.0",
		"dependencies": {"standard.time": "1.0.0"}
	}`))
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if m.Path != "company.time" || m.Kind != Library || m.Version != "2.4.0" {
		t.Fatalf("got %+v", m)
	}
	if m.Dependencies["standard.time"] != "1.0.0" {
		t.Fatalf("got dependencies %+v", m.Dependencies)
	}
}

func TestValidateRejectsBadKind(t *testing.T) {
	_, errs := Parse([]byte(`{"path": "company.time", "kind": "sdk", "version": "2.4.0"}`))
	if len(errs) != 1 {
		t.Fatalf("got %d errors, want 1: %v", len(errs), errs)
	}
}

func TestValidateRejectsBadPath(t *testing.T) {
	_, errs := Parse([]byte(`{"path": "2company", "kind": "library", "version": "2.4.0"}`))
	if len(errs) != 1 {
		t.Fatalf("got %d errors, want 1: %v", len(errs), errs)
	}
}

func TestValidateRejectsBadVersion(t *testing.T) {
	_, errs := Parse([]byte(`{"path": "company.time", "kind": "library", "version": "2.4"}`))
	if len(errs) != 1 {
		t.Fatalf("got %d errors, want 1: %v", len(errs), errs)
	}
}

func TestValidateRejectsBadDependencyVersion(t *testing.T) {
	_, errs := Parse([]byte(`{
		"path": "company.time", "kind": "library", "version": "2.4.0",
		"dependencies": {"standard.time": "latest"}
	}`))
	if len(errs) != 1 {
		t.Fatalf("got %d errors, want 1: %v", len(errs), errs)
	}
}

func TestValidateCollectsEveryProblem(t *testing.T) {
	_, errs := Parse([]byte(`{"path": "", "kind": "sdk", "version": "x"}`))
	if len(errs) != 3 {
		t.Fatalf("got %d errors, want 3: %v", len(errs), errs)
	}
}

func TestParseRejectsInvalidJSON(t *testing.T) {
	_, errs := Parse([]byte(`not json`))
	if len(errs) != 1 {
		t.Fatalf("got %d errors, want 1: %v", len(errs), errs)
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	data := []byte(`{"path": "company.time", "kind": "library", "version": "2.4.0"}`)
	if err := os.WriteFile(filepath.Join(dir, Filename), data, 0o644); err != nil {
		t.Fatal(err)
	}
	m, errs := Load(dir)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if m.Path != "company.time" {
		t.Fatalf("got %+v", m)
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, errs := Load(t.TempDir())
	if len(errs) != 1 {
		t.Fatalf("got %d errors, want 1: %v", len(errs), errs)
	}
}

func TestParseCapabilities(t *testing.T) {
	m, errs := Parse([]byte(`{
		"path": "acme.billing", "kind": "application", "version": "1.0.0",
		"capabilities": ["Database", "PaymentGateway"]
	}`))
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(m.Capabilities) != 2 || m.Capabilities[0] != "Database" || m.Capabilities[1] != "PaymentGateway" {
		t.Fatalf("got %+v", m.Capabilities)
	}
}

func TestValidateRejectsBadCapabilityName(t *testing.T) {
	_, errs := Parse([]byte(`{
		"path": "acme.billing", "kind": "application", "version": "1.0.0",
		"capabilities": ["some.Database"]
	}`))
	if len(errs) != 1 {
		t.Fatalf("got %d errors, want 1: %v", len(errs), errs)
	}
}

func TestParseDeprecations(t *testing.T) {
	m, errs := Parse([]byte(`{
		"path": "company.time", "kind": "library", "version": "2.4.0",
		"deprecations": {"addBusinessDays": {"since": "2.0.0", "message": "use addWorkingDays instead"}}
	}`))
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	dep, ok := m.Deprecations["addBusinessDays"]
	if !ok || dep.Since != "2.0.0" || dep.Message != "use addWorkingDays instead" {
		t.Fatalf("got %+v", m.Deprecations)
	}
}

func TestValidateRejectsBadDeprecationKey(t *testing.T) {
	_, errs := Parse([]byte(`{
		"path": "company.time", "kind": "library", "version": "2.4.0",
		"deprecations": {"company.time.addBusinessDays": {"since": "2.0.0"}}
	}`))
	if len(errs) != 1 {
		t.Fatalf("got %d errors, want 1: %v", len(errs), errs)
	}
}

func TestValidateRejectsBadDeprecationSince(t *testing.T) {
	_, errs := Parse([]byte(`{
		"path": "company.time", "kind": "library", "version": "2.4.0",
		"deprecations": {"addBusinessDays": {"since": "soon"}}
	}`))
	if len(errs) != 1 {
		t.Fatalf("got %d errors, want 1: %v", len(errs), errs)
	}
}

func TestParseOrigin(t *testing.T) {
	m, errs := Parse([]byte(`{
		"path": "acme.billingClient", "kind": "library", "version": "1.0.0",
		"origin": "https://github.com/acme/billing-client"
	}`))
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if m.Origin != "https://github.com/acme/billing-client" {
		t.Fatalf("got %q", m.Origin)
	}
}

func TestValidateNativeCapabilities(t *testing.T) {
	base := Manifest{Path: "app", Kind: Application, Version: "1.0.0"}

	ok := base
	ok.NativeCapabilities = []string{"Database", "EmailSender"}
	if errs := ok.Validate(); len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}

	prim := base
	prim.NativeCapabilities = []string{"Socket"}
	if errs := prim.Validate(); len(errs) != 1 {
		t.Fatalf("a primitive in nativeCapabilities must be rejected, got %v", errs)
	}

	bad := base
	bad.NativeCapabilities = []string{"not a name"}
	if errs := bad.Validate(); len(errs) != 1 {
		t.Fatalf("a malformed native capability must be rejected, got %v", errs)
	}
}

// ADR 0071 §5: only an application renames, and only its dependencies'
// variables.
func TestEnvironmentRenames(t *testing.T) {
	good := `{"path": "checkout.app", "kind": "application", "version": "1.0.0",
		"dependencies": {"acme.pgmini": "1.2.0"},
		"environment": {"acme.pgmini": {"PG_URL": "DATABASE_URL"}}}`
	if _, errs := Parse([]byte(good)); len(errs) != 0 {
		t.Fatalf("good: %v", errs)
	}
	for src, want := range map[string]string{
		`{"path": "acme.lib", "kind": "library", "version": "1.0.0", "dependencies": {"acme.pgmini": "1.2.0"}, "environment": {"acme.pgmini": {"PG_URL": "DATABASE_URL"}}}`:  "environment renames are an application's to make",
		`{"path": "a.app", "kind": "application", "version": "1.0.0", "environment": {"acme.pgmini": {"PG_URL": "DATABASE_URL"}}}`:                                           `renames variables of "acme.pgmini", which isn't a dependency`,
		`{"path": "a.app", "kind": "application", "version": "1.0.0", "dependencies": {"acme.pgmini": "1.2.0"}, "environment": {"acme.pgmini": {"PG-URL": "DATABASE_URL"}}}`: "must both be environment variable names",
	} {
		_, errs := Parse([]byte(src))
		found := false
		for _, e := range errs {
			found = found || strings.Contains(e.Error(), want)
		}
		if !found {
			t.Errorf("%s: errs = %v, want %q", src, errs, want)
		}
	}
}
