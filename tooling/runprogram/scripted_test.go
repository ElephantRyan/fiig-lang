package runprogram

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCapabilitiesFileExpandsEnv: a deployed capabilities file names its
// paths and secrets as `${NAME}`, and an unset one is an error.
func TestCapabilitiesFileExpandsEnv(t *testing.T) {
	path := filepath.Join(t.TempDir(), "caps.json")
	if err := os.WriteFile(path, []byte(`{"Database": {"type": "scripted", "responses": {"name": ["${FIIG_TEST_NAME}"]}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FIIG_TEST_NAME", "Ada")
	if _, err := LoadCapabilitiesFile(path, nil); err != nil {
		t.Fatalf("set: %v", err)
	}
	got, err := expandEnv(map[string]any{"dsn": "file:${FIIG_TEST_NAME}.db", "list": []any{"${FIIG_TEST_NAME}"}})
	if err != nil {
		t.Fatal(err)
	}
	m := got.(map[string]any)
	if m["dsn"] != "file:Ada.db" || m["list"].([]any)[0] != "Ada" {
		t.Fatalf("expanded = %v", got)
	}
	os.Unsetenv("FIIG_TEST_UNSET")
	if _, err := expandEnv("${FIIG_TEST_UNSET}"); err == nil || !strings.Contains(err.Error(), "FIIG_TEST_UNSET is not set") {
		t.Fatalf("unset: err = %v", err)
	}
}
