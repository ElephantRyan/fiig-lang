package servecmd

import (
	"context"
	"fmt"
	"net/http/httptest"
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
	"app/check_error.fiig":   "enum CheckError {\n    Missing,\n}\n",
	"app/storage.fiig":       "wire Storage {\n    ready: bool from env \"STORAGE_READY\" = true;\n}\n",
	"app/storage_ready.fiig": "activity storageReady() -> bool {\n    return Storage.ready;\n}\n",
	"app/verify_storage.fiig": "workflow verifyStorage() -> Result<None, CheckError> {\n" +
		"    if (storageReady()) {\n        return Ok(None);\n    }\n    return Err(CheckError.Missing);\n}\n",
	"app/hello.fiig":     "workflow hello() -> string {\n    return \"hi\";\n}\n",
	"app/api.fiig":       "adapter http api {\n    GET \"/hello\" -> hello()\n        produces \"application/json\";\n}\n",
	"app/preflight.fiig": "adapter startup preflight {\n    run -> verifyStorage() {\n        Ok(_) -> proceed,\n        Err(_) -> halt,\n    }\n}\n",
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

// §6: -check loads with migrations skipped, and can tell its startup loop to
// run a once route without claiming it.
func TestACheckRunLoadsWithoutMigratingAndCanIgnoreOnce(t *testing.T) {
	t.Setenv("STORAGE_READY", "true")
	root := t.TempDir()
	writeTree(t, root, startupApp)
	env, _ := runprogram.NewEnvironment("")
	_, loops, _, caps, err := load(filepath.Join(root, "app"), []string{"preflight"}, "", nil, nil, nil, adapter.DefaultLimits(), env, nil, migrate.Skip)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(caps.Close)
	if len(loops) != 1 || !loops[0].startup || loops[0].ignoreOnce == nil {
		t.Fatalf("loops = %+v, want one startup loop with ignoreOnce", loops)
	}
	loops[0].ignoreOnce()
	if err := loops[0].run(context.Background()); err != nil {
		t.Fatal(err)
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

// ADR 0127: a route whose mapping says degrade lets the process start, and a
// config field that reads the route is true from then on, here through a route.
var degradedApp = map[string]string{
	"app/fiig.pkg.json":    `{"path": "t.degraded", "kind": "application", "version": "1.0.0"}`,
	"app/check_error.fiig": "enum CheckError {\n    Missing,\n    Slow,\n}\n",
	"app/cache_state.fiig": "wire CacheState {\n    degraded: bool from startup \"preflight.verifyCache\";\n}\n",
	"app/verify_cache.fiig": "workflow verifyCache() -> Result<None, CheckError> {\n" +
		"    return Err(CheckError.Slow);\n}\n",
	"app/cache_is_degraded.fiig": "activity cacheIsDegraded() -> bool {\n    return CacheState.degraded;\n}\n",
	"app/status.fiig":            "workflow status() -> string {\n    if (cacheIsDegraded()) {\n        return \"degraded\";\n    }\n    return \"ok\";\n}\n",
	"app/api.fiig":               "adapter http api {\n    GET \"/status\" -> status()\n        produces \"application/json\";\n}\n",
	"app/preflight.fiig":         "adapter startup preflight {\n    run -> verifyCache() {\n        Ok(_) -> proceed,\n        Err(CheckError.Slow) -> degrade,\n        Err(CheckError.Missing) -> halt,\n    }\n}\n",
}

func TestADegradedStartupIsReadThroughConfig(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, degradedApp)
	env, _ := runprogram.NewEnvironment("")
	handler, loops, _, caps, err := load(filepath.Join(root, "app"), []string{"api", "preflight"}, "", nil, nil, nil, adapter.DefaultLimits(), env, nil, migrate.Apply)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(caps.Close)
	status := func() string {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest("GET", "/status", nil))
		return strings.TrimSpace(rec.Body.String())
	}
	if got := status(); got != `"ok"` {
		t.Fatalf("before the checks ran, /status = %s, want not degraded", got)
	}
	var logs []string
	loops[0].setLog(func(format string, args ...any) { logs = append(logs, fmt.Sprintf(format, args...)) })
	if err := loops[0].run(context.Background()); err != nil {
		t.Fatalf("a degraded start was refused: %v", err)
	}
	if got := status(); got != `"degraded"` {
		t.Errorf("after a degraded start, /status = %s", got)
	}
	if !strings.Contains(strings.Join(logs, "\n"), "starting degraded") {
		t.Errorf("logs = %q, want the degraded start logged", logs)
	}
}

// ADR 0111 open question 4, as amended 2026-11-21: a stream setting out of
// bounds stops fiig serve before it starts, in the one report.
func TestABadStreamSettingRefusesToServe(t *testing.T) {
	t.Setenv("STORAGE_READY", "true")
	t.Setenv("STREAM_BATCH_SIZE", "5000")
	root := t.TempDir()
	writeTree(t, root, startupApp)
	env, _ := runprogram.NewEnvironment("")
	_, _, _, _, err := load(filepath.Join(root, "app"), []string{"api"}, "", nil, nil, nil, adapter.DefaultLimits(), env, nil, migrate.Apply)
	if err == nil || !strings.Contains(err.Error(), "STREAM_BATCH_SIZE") {
		t.Errorf("err = %v, want a problem naming STREAM_BATCH_SIZE", err)
	}
}

// ADR 0112 open question 3, as amended 2026-11-21: so does a jitter out of
// bounds.
func TestABadScheduleJitterRefusesToServe(t *testing.T) {
	t.Setenv("STORAGE_READY", "true")
	t.Setenv("SCHEDULE_JITTER_MS", "1h")
	root := t.TempDir()
	writeTree(t, root, startupApp)
	env, _ := runprogram.NewEnvironment("")
	_, _, _, _, err := load(filepath.Join(root, "app"), []string{"api"}, "", nil, nil, nil, adapter.DefaultLimits(), env, nil, migrate.Apply)
	if err == nil || !strings.Contains(err.Error(), "SCHEDULE_JITTER_MS") {
		t.Errorf("err = %v, want a problem naming SCHEDULE_JITTER_MS", err)
	}
}
