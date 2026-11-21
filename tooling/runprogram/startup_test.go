package runprogram

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
	// ADR 0124 §6: a route's own deadline, and a route one replica runs.
	"tight.fiig": `adapter startup Tight {
    run within "100ms" -> hung() {
        _ -> proceed
    }
}`,
	"once.fiig": `adapter startup Once {
    run once -> storageMissing() {
        Ok(_) -> proceed
        Err(_) -> halt
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

// onceLock is a ScheduleLock that records its claims.
type onceLock struct {
	mu      sync.Mutex
	grant   bool
	failure error
	claims  []string
}

func (*onceLock) Call(context.Context, string, []runtime.Value) (runtime.Value, *runtime.Fault) {
	return nil, runtime.Faultf("not called by a program")
}

func (l *onceLock) Claim(_ context.Context, job string, firedAt int64, holder string, hold int64) (bool, error, *runtime.Fault) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.claims = append(l.claims, fmt.Sprintf("%s|%d|%s|%d", job, firedAt, holder, hold))
	return l.grant, l.failure, nil
}

func (*onceLock) Describe() string { return "a test lock" }

func startupRunners(t *testing.T, adapterNames ...string) []*adapter.StartupRunner {
	t.Helper()
	runners, err := startupRunnersWith(t, nil, adapterNames...)
	if err != nil {
		t.Fatal(err)
	}
	return runners
}

// startupRunnersWith plans the named adapters with lock bound as the
// ScheduleLock, or none when it is nil, and returns what planning said.
func startupRunnersWith(t *testing.T, lock runtime.ScheduleLock, adapterNames ...string) ([]*adapter.StartupRunner, error) {
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
	if lock != nil {
		caps.Register(runtime.ScheduleLockCapability, lock)
	}
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
	return adapter.StartupRunners(served, u, in, "replica-1")
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

// §6: a route's `within` is its deadline, in place of the 30 s default.
func TestStartupWithinIsTheRoutesDeadline(t *testing.T) {
	runners := startupRunners(t, "Tight")
	if runners[0].Deadline != 100*time.Millisecond {
		t.Fatalf("Deadline = %s, want 100ms", runners[0].Deadline)
	}
	err := adapter.RunStartup(context.Background(), runners)
	if err == nil || !strings.Contains(err.Error(), "Tight run -> hung: it did not finish within 100ms") {
		t.Errorf("err = %v", err)
	}
	if got := startupRunners(t, "Good")[0].Deadline; got != adapter.StartupDeadline {
		t.Errorf("a route with no within has Deadline %s, want %s", got, adapter.StartupDeadline)
	}
}

// §6: a `once` route is claimed, and a replica that loses the claim starts
// without running it.
func TestStartupOnceRunsOnlyWhereItIsClaimed(t *testing.T) {
	var logs logLines
	lock := &onceLock{grant: false}
	runners, err := startupRunnersWith(t, lock, "Once")
	if err != nil {
		t.Fatal(err)
	}
	runners[0].Logf = logs.Logf
	if got := runners[0].Lock(); got == nil {
		t.Fatal("a once route has no lock")
	}
	// Claimed elsewhere: storageMissing would halt, and it didn't run.
	if err := adapter.RunStartup(context.Background(), runners); err != nil {
		t.Fatalf("a replica that lost the claim was refused: %v", err)
	}
	if want := "Once run -> storageMissing|0|replica-1|600000"; len(lock.claims) != 1 || lock.claims[0] != want {
		t.Errorf("claims = %v, want [%s]", lock.claims, want)
	}
	if !strings.Contains(logs.all(), "another replica has claimed it") {
		t.Errorf("logs = %q", logs.all())
	}
	// Claimed here: it runs, and its mapping halts.
	lock.grant = true
	err = adapter.RunStartup(context.Background(), runners)
	if err == nil || !strings.Contains(err.Error(), "its mapping says halt") {
		t.Errorf("err = %v, want the claimed route to run and halt", err)
	}
}

// §6: a claim that can't be made is a refusal: whether the route has run is
// not known.
func TestStartupOnceRefusesWhenTheClaimFails(t *testing.T) {
	lock := &onceLock{grant: true, failure: errors.New("redis is down")}
	runners, err := startupRunnersWith(t, lock, "Once")
	if err != nil {
		t.Fatal(err)
	}
	err = adapter.RunStartup(context.Background(), runners)
	if err == nil || !strings.Contains(err.Error(), "it could not be claimed (redis is down)") {
		t.Errorf("err = %v", err)
	}
}

// §6: `-check` runs a once route in the process that asks, claiming nothing.
func TestStartupIgnoreOnceRunsWithoutClaiming(t *testing.T) {
	lock := &onceLock{grant: false}
	runners, err := startupRunnersWith(t, lock, "Once")
	if err != nil {
		t.Fatal(err)
	}
	runners[0].IgnoreOnce = true
	err = adapter.RunStartup(context.Background(), runners)
	if err == nil || !strings.Contains(err.Error(), "its mapping says halt") {
		t.Errorf("err = %v, want the route to run", err)
	}
	if len(lock.claims) != 0 {
		t.Errorf("claims = %v, want none", lock.claims)
	}
}

// §6: a once route with no ScheduleLock bound is found before anything runs.
func TestStartupOnceNeedsAScheduleLock(t *testing.T) {
	_, err := startupRunnersWith(t, nil, "Once")
	if err == nil || !strings.Contains(err.Error(), "binds no ScheduleLock to claim it with") {
		t.Errorf("err = %v", err)
	}
}
