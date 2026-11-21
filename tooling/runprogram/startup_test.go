package runprogram

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fiig-lang/compiler/adapter"
	"fiig-lang/compiler/loader"
	"fiig-lang/compiler/runtime"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
	"fiig-lang/internal/fixture"
)

// ADR 0124: startup adapters. Every route runs at once before anything
// serves, and one report names every failure.

var startupProgram = map[string]string{
	"fiig.pkg.json": `{
    "path": "startuptest.app",
    "kind": "application",
    "version": "1.0.0"
}`,
	"check_error.fiig": `enum CheckError {
    Missing
    Slow
}`,
	"storage_ok.fiig": `workflow storageOk() -> Result<None, CheckError> {
    return Ok(None)
}`,
	"storage_missing.fiig": `workflow storageMissing() -> Result<None, CheckError> {
    return Err(CheckError.Missing)
}`,
	"cache_slow.fiig": `workflow cacheSlow() -> Result<None, CheckError> {
    return Err(CheckError.Slow)
}`,
	"faulty.fiig": `workflow faulty() -> Result<None, CheckError> {
    zero = 1 - 1
    n = 1 / zero
    if n > 0 {
        return Ok(None)
    }
    return Ok(None)
}`,
	"hung.fiig": `workflow hung() -> Result<None, CheckError> {
    while true {
    }
    return Ok(None)
}`,
	// Every check passes or is tolerated: a slow cache is only logged.
	"good.fiig": `adapter startup Good {
    run -> storageOk() {
        Ok(_) -> proceed
        Err(_) -> halt
    }
    run -> cacheSlow() {
        Ok(_) -> proceed
        Err(CheckError.Slow) -> proceed
        Err(CheckError.Missing) -> halt
    }
}`,
	// Three different ways to refuse, which one report must all name.
	"bad.fiig": `adapter startup Bad {
    run -> storageMissing() {
        Ok(_) -> proceed
        Err(_) -> halt
    }
    run -> faulty() {
        _ -> proceed
    }
    run -> hung() {
        _ -> proceed
    }
}`,
}

func startupRunners(t *testing.T, adapterNames ...string) []*adapter.StartupRunner {
	t.Helper()
	root := t.TempDir()
	for name, src := range fixture.Module(startupProgram) {
		os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0o755)
		if err := os.WriteFile(filepath.Join(root, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	loaded, errs := loader.Load(root, nil)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	files, _, err := LoadFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	if diags := Check(root, files, loaded.Packages); len(diags) > 0 {
		t.Fatal(diags)
	}
	u, _ := types.BuildUniverse(files)
	caps := runtime.NewCapabilityRegistry()
	RegisterDefaults(caps)
	t.Cleanup(caps.Close)
	in := runtime.New(u, caps)
	in.Imports = BuildImports(files, loaded.Packages)
	in.Groups = BuildGroups(files, loaded.Packages)
	in.Packages = loaded.Packages
	var served []adapter.Served
	for _, name := range adapterNames {
		d, _ := u.Lookup(name)
		served = append(served, adapter.Served{Decl: d.(*syntax.AdapterDecl)})
	}
	runners, err := adapter.StartupRunners(served, u, in)
	if err != nil {
		t.Fatal(err)
	}
	return runners
}

func TestStartupProceedsWhenNothingHalts(t *testing.T) {
	var logs logLines
	runners := startupRunners(t, "Good")
	if len(runners) != 2 {
		t.Fatalf("Good has %d routes, want 2", len(runners))
	}
	for _, r := range runners {
		r.Logf = logs.Logf
	}
	if err := adapter.RunStartup(context.Background(), runners); err != nil {
		t.Fatalf("a program whose checks all proceed was refused: %v", err)
	}
	// A failure the mapping tolerates is still said (ADR 0124 §1).
	if got := logs.all(); !strings.Contains(got, "Good run -> cacheSlow failed with CheckError.Slow, and its mapping lets the process start") {
		t.Errorf("logs = %q", got)
	}
}

// §2: a halt, a fault, and a deadline each refuse the start, and the one
// report names all of them.
func TestStartupRefusesAndNamesEveryFailure(t *testing.T) {
	runners := startupRunners(t, "Good", "Bad")
	for _, r := range runners {
		r.Deadline = 200 * time.Millisecond
	}
	err := adapter.RunStartup(context.Background(), runners)
	if err == nil {
		t.Fatal("a program with three failing checks started")
	}
	msg := err.Error()
	for _, want := range []string{
		"3 startup check(s) failed",
		"Bad run -> storageMissing: its mapping says halt, for Err(CheckError.Missing)",
		"Bad run -> faulty: it faulted",
		"Bad run -> hung: it did not finish within 200ms",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("the report is missing %q:\n%s", want, msg)
		}
	}
	// What passed is not in the report.
	if strings.Contains(msg, "storageOk") || strings.Contains(msg, "cacheSlow") {
		t.Errorf("the report names a check that proceeded:\n%s", msg)
	}
}

// §2: a fault halts even when the mapping says proceed for every outcome:
// a check that crashed has not said the thing is fine.
func TestStartupFaultHaltsEvenUnderAProceedWildcard(t *testing.T) {
	runners := startupRunners(t, "Bad")
	var faulty *adapter.StartupRunner
	for _, r := range runners {
		if strings.HasSuffix(r.Name(), "faulty") {
			faulty = r
		}
	}
	if err := faulty.Check(context.Background()); err == nil || !strings.Contains(err.Error(), "it faulted") {
		t.Fatalf("Check = %v, want a fault to halt", err)
	}
}
