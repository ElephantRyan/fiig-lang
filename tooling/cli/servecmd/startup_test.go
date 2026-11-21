package servecmd

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"fiig-lang/compiler/adapter"
	"fiig-lang/tooling/migrate"
	"fiig-lang/tooling/runprogram"
)

// ADR 0124: fiig serve runs an application's startup adapters before it
// listens, and refuses to start when one refuses.

var startupApp = map[string]string{
	"app/fiig.pkg.json":      `{"path": "t.preflight", "kind": "application", "version": "1.0.0"}`,
	"app/check_error.fiig":   "enum CheckError {\n    Missing\n}\n",
	"app/storage.fiig":       "config Storage {\n    ready: bool from env \"STORAGE_READY\" = true\n}\n",
	"app/storage_ready.fiig": "activity storageReady() -> bool {\n    return Storage.ready\n}\n",
	"app/verify_storage.fiig": "workflow verifyStorage() -> Result<None, CheckError> {\n" +
		"    if storageReady() {\n        return Ok(None)\n    }\n    return Err(CheckError.Missing)\n}\n",
	"app/hello.fiig":     "workflow hello() -> string {\n    return \"hi\"\n}\n",
	"app/api.fiig":       "adapter http api {\n    GET \"/hello\" -> hello()\n        produces \"application/json\"\n}\n",
	"app/preflight.fiig": "adapter startup preflight {\n    run -> verifyStorage() {\n        Ok(_) -> proceed\n        Err(_) -> halt\n    }\n}\n",
}

func startupLoops(t *testing.T) []loop {
	t.Helper()
	root := t.TempDir()
	writeTree(t, root, startupApp)
	env, _ := runprogram.NewEnvironment("")
	_, loops, _, caps, err := load(filepath.Join(root, "app"), []string{"api", "preflight"}, "", nil, nil, nil, adapter.DefaultLimits(), env, nil, migrate.Apply)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(caps.Close)
	return loops
}

func TestAStartupAdapterIsOneLoopThatRunsFirst(t *testing.T) {
	t.Setenv("STORAGE_READY", "true")
	loops := startupLoops(t)
	if len(loops) != 1 || !loops[0].startup {
		t.Fatalf("loops = %+v, want one startup loop", loops)
	}
	if err := loops[0].run(context.Background()); err != nil {
		t.Fatalf("a program whose check passes was refused: %v", err)
	}
}

func TestAFailingStartupCheckRefusesToServe(t *testing.T) {
	t.Setenv("STORAGE_READY", "false")
	loops := startupLoops(t)
	err := loops[0].run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "refusing to start") || !strings.Contains(err.Error(), "verifyStorage") {
		t.Fatalf("run = %v, want a refusal naming verifyStorage", err)
	}
}
