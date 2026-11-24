package runprogram

import (
	"os"
	"path/filepath"
	"testing"

	"fiig-lang/compiler/runtime"
)

// ADR 0149 §2: every command gives its interpreter the root manifest's
// concurrency default and maximum, and leaves the runtime's own without one.
func TestUseConcurrency(t *testing.T) {
	use := func(manifest string) *runtime.Interp {
		root := t.TempDir()
		if manifest != "" {
			if err := os.WriteFile(filepath.Join(root, "fiig.pkg.json"), []byte(manifest), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		in := runtime.New(nil, runtime.NewCapabilityRegistry())
		UseConcurrency(in, root)
		return in
	}
	if in := use(""); in.DefaultConcurrencyLimit != 16 || in.MaxConcurrencyLimit != 0 {
		t.Errorf("no manifest: %d, %d; want 16, 0", in.DefaultConcurrencyLimit, in.MaxConcurrencyLimit)
	}
	if in := use(`{"path": "a.app", "kind": "application", "version": "1.0.0"}`); in.DefaultConcurrencyLimit != 16 || in.MaxConcurrencyLimit != 0 {
		t.Errorf("no concurrency: %d, %d; want 16, 0", in.DefaultConcurrencyLimit, in.MaxConcurrencyLimit)
	}
	if in := use(`{"path": "a.app", "kind": "application", "version": "1.0.0", "concurrency": {"default": 4, "maximum": 8}}`); in.DefaultConcurrencyLimit != 4 || in.MaxConcurrencyLimit != 8 {
		t.Errorf("both: %d, %d; want 4, 8", in.DefaultConcurrencyLimit, in.MaxConcurrencyLimit)
	}
	if in := use(`{"path": "a.app", "kind": "application", "version": "1.0.0", "concurrency": {"maximum": 8}}`); in.DefaultConcurrencyLimit != 16 || in.MaxConcurrencyLimit != 8 {
		t.Errorf("maximum only: %d, %d; want 16, 8", in.DefaultConcurrencyLimit, in.MaxConcurrencyLimit)
	}
}
