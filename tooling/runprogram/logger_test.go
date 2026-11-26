package runprogram

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fiig-lang/compiler/logging"
)

// ADR 0150 §3a: the flags win, else FIIG_LOG_FORMAT and FIIG_LOG_LEVEL,
// else text at info; a bad value is a startup problem.
func TestReadLogger(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "fiig.pkg.json"), []byte(`{"path": "hearsay", "kind": "application", "version": "0.4.0"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	envFile := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(envFile, []byte("FIIG_LOG_LEVEL=error\nFIIG_LOG_FORMAT=json\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	none, _ := NewEnvironment("")
	fromFile, err := NewEnvironment(envFile)
	if err != nil {
		t.Fatal(err)
	}
	if l, problems := ReadLogger(none, root, "", ""); len(problems) != 0 || l.Level() != logging.Info {
		t.Errorf("defaults: level %v, %v", l.Level(), problems)
	}
	if l, problems := ReadLogger(fromFile, root, "", ""); len(problems) != 0 || l.Level() != logging.Error {
		t.Errorf("from the env file: level %v, %v", l.Level(), problems)
	}
	if l, problems := ReadLogger(fromFile, root, "", "debug"); len(problems) != 0 || l.Level() != logging.Debug {
		t.Errorf("the flag wins: level %v, %v", l.Level(), problems)
	}
	_, problems := ReadLogger(none, root, "logfmt", "loud")
	joined := strings.Join(problems, "; ")
	if len(problems) != 2 || !strings.Contains(joined, `-log-format: "logfmt" isn't a log format`) || !strings.Contains(joined, `-log-level: "loud" isn't a log level`) {
		t.Errorf("problems = %v", problems)
	}
}
