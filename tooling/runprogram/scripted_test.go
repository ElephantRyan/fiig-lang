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
	var problems []string
	got := expandEnv(map[string]any{"dsn": "file:${FIIG_TEST_NAME}.db", "list": []any{"${FIIG_TEST_NAME}"}}, os.LookupEnv, "caps.json", &problems)
	m := got.(map[string]any)
	if len(problems) != 0 || m["dsn"] != "file:Ada.db" || m["list"].([]any)[0] != "Ada" {
		t.Fatalf("expanded = %v, %v", got, problems)
	}
	os.Unsetenv("FIIG_TEST_UNSET")
	if _, err := LoadCapabilitiesFile(path, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"Database": {"type": "scripted", "dsn": "${FIIG_TEST_UNSET}", "user": "${FIIG_TEST_UNSET_TOO}"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	// ADR 0071 §6: every unset variable is reported, with where it is.
	_, problems, err := LoadCapabilitiesFileWith(path, nil, os.LookupEnv)
	want := "FIIG_TEST_UNSET is unset; caps.json Database.dsn" + "|" + "FIIG_TEST_UNSET_TOO is unset; caps.json Database.user"
	if err != nil || strings.Join(problems, "|") != want {
		t.Fatalf("problems = %q, %v", problems, err)
	}
}
