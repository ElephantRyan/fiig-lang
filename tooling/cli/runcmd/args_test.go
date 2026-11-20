package runcmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// abs-recommendations GAPS G12: arguments can come from a file or stdin,
// so a password isn't an argv entry.
func TestArgsSource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "args.json")
	if err := os.WriteFile(path, []byte(`{"password": "s3cret"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		args, file, stdin, want, err string
	}{
		{args: `{"a": 1}`, want: `{"a": 1}`},
		{args: "{}", file: path, want: `{"password": "s3cret"}`},
		{args: "{}", file: "-", stdin: `{"b": 2}`, want: `{"b": 2}`},
		{args: `{"a": 1}`, file: path, err: "both given"},
		{args: "{}", file: filepath.Join(t.TempDir(), "missing.json"), err: "-args-file"},
	}
	for _, c := range cases {
		got, err := argsSource(c.args, c.file, strings.NewReader(c.stdin))
		if c.err != "" {
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("%+v: err = %v, want %q", c, err, c.err)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("%+v: got %q, %v", c, got, err)
		}
	}
}
