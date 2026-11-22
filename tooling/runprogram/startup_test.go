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
    Missing,
    Slow,
}`,
	"storage_ok.fiig": `workflow storageOk() -> Result<None, CheckError> {
    return Ok(None);
}`,
	"storage_missing.fiig": `workflow storageMissing() -> Result<None, CheckError> {
    return Err(CheckError.Missing);
}`,
	"cache_slow.fiig": `workflow cacheSlow() -> Result<None, CheckError> {
    return Err(CheckError.Slow);
}`,
	"faulty.fiig": `workflow faulty() -> Result<None, CheckError> {
    zero = 1 - 1;
    n = 1 / zero;
    if (n > 0) {
        return Ok(None);
    }
    return Ok(None);
}`,
	"hung.fiig": `workflow hung() -> Result<None, CheckError> {
    while (true) {
    }
    return Ok(None);
}`,
	// Every check passes or is tolerated: a slow cache is only logged.
	"good.fiig": `adapter startup Good {
    run -> storageOk() {
        Ok(_) -> proceed,
        Err(_) -> halt,
    }
    run -> cacheSlow() {
        Ok(_) -> proceed,
        Err(CheckError.Slow) -> proceed,
        Err(CheckError.Missing) -> halt,
    }
}`,
	// ADR 0127: a failed check the process starts after, and says so.
	"degrading.fiig": `adapter startup Degrading {
    run -> cacheSlow() {
        Ok(_) -> proceed,
        Err(CheckError.Missing) -> halt,
        Err(CheckError.Slow) -> degrade,
    }
}`,
	"degrading_once.fiig": `adapter startup DegradingOnce {
    run once -> cacheSlow() {
        Ok(_) -> proceed,
        Err(CheckError.Missing) -> halt,
        Err(CheckError.Slow) -> degrade,
    }
}`,
	// ADR 0124 §6: a route's own deadline, and a route one replica runs.
	"tight.fiig": `adapter startup Tight {
    run within "100ms" -> hung() {
        _ -> proceed,
    }
}`,
	"once_ok.fiig": `adapter startup OnceOk {
    run once -> storageOk() {
        Ok(_) -> proceed,
        Err(_) -> halt,
    }
}`,
	"once.fiig": `adapter startup Once {
    run once -> storageMissing() {
        Ok(_) -> proceed,
        Err(_) -> halt,
    }
}`,
	// Three different ways to refuse, which one report must all name.
	"bad.fiig": `adapter startup Bad {
    run -> storageMissing() {
        Ok(_) -> proceed,
        Err(_) -> halt,
    }
    run -> faulty() {
        _ -> proceed,
    }
    run -> hung() {
        _ -> proceed,
    }
}`,
}

// onceLock is a ScheduleLock that records its calls and answers from a
// script: a claim is granted from its grantAt-th call on (0 never), and a
// completion is seen from its doneAt-th Completed call on (0 never).
type onceLock struct {
	mu      sync.Mutex
	grantAt int
	doneAt  int
	failure error
	claims  []string
	asked   int
	done    []string
}

func (*onceLock) Call(context.Context, string, []runtime.Value) (runtime.Value, *runtime.Fault) {
	return nil, runtime.Faultf("not called by a program")
}

func (l *onceLock) Claim(_ context.Context, job string, firedAt int64, holder string, hold int64) (bool, error, *runtime.Fault) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.claims = append(l.claims, fmt.Sprintf("%s|%d|%s|%d", job, firedAt, holder, hold))
	return l.grantAt > 0 && len(l.claims) >= l.grantAt, l.failure, nil
}

func (l *onceLock) Complete(_ context.Context, job string, firedAt int64, holder string, retain int64) (error, *runtime.Fault) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.done = append(l.done, fmt.Sprintf("%s|%d|%s|%d", job, firedAt, holder, retain))
	return nil, nil
}

func (l *onceLock) Completed(context.Context, string, int64) (bool, error, *runtime.Fault) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.asked++
	return l.doneAt > 0 && l.asked >= l.doneAt, nil, nil
}

func (*onceLock) Describe() string { return "a test lock" }

// quick makes a runner's polling instant, and its slack small.
func quick(r *adapter.StartupRunner) {
	r.Poll = time.Millisecond
	r.Slack = 20 * time.Millisecond
}

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
	return adapter.StartupRunners(served, u, in, "replica-1", 7)
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

// ADR 0127: a route that says degrade lets the process start, says so, and is
// reported as degraded; one that proceeds is not.
func TestStartupDegradeStartsAndIsReported(t *testing.T) {
	var logs logLines
	runners := startupRunners(t, "Degrading", "Good")
	for _, r := range runners {
		r.Logf = logs.Logf
	}
	if err := adapter.RunStartup(context.Background(), runners); err != nil {
		t.Fatalf("a degraded start was refused: %v", err)
	}
	degraded := map[string]bool{}
	for _, r := range runners {
		_, key := r.Route()
		degraded[key] = r.Degraded()
	}
	if !degraded["Degrading.cacheSlow"] || degraded["Good.storageOk"] || degraded["Good.cacheSlow"] {
		t.Errorf("degraded = %v, want only Degrading.cacheSlow", degraded)
	}
	if got := logs.all(); !strings.Contains(got, "Degrading run -> cacheSlow: starting degraded, for Err(CheckError.Slow)") {
		t.Errorf("logs = %q", got)
	}
}

// ADR 0127 §3: a once route that degraded is not recorded as done, so the
// replicas waiting on it find out for themselves.
func TestStartupDegradedOnceRouteIsNotRecordedDone(t *testing.T) {
	lock := &onceLock{grantAt: 1}
	runners, err := startupRunnersWith(t, lock, "DegradingOnce")
	if err != nil {
		t.Fatal(err)
	}
	if err := adapter.RunStartup(context.Background(), runners); err != nil {
		t.Fatal(err)
	}
	if !runners[0].Degraded() {
		t.Error("the route did not end degraded")
	}
	if len(lock.done) != 0 {
		t.Errorf("a degraded route was recorded as done: %v", lock.done)
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

// §6: a `once` route is claimed for the deploy, and a replica that loses the
// claim to a replica that has finished starts without running it.
func TestStartupOnceRunsOnlyWhereItIsClaimed(t *testing.T) {
	var logs logLines
	lock := &onceLock{doneAt: 1}
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
	// The claim is for deploy 7, held for the deadline and the slack.
	if want := "Once run -> storageMissing|7|replica-1|60000"; len(lock.claims) != 1 || lock.claims[0] != want {
		t.Errorf("claims = %v, want [%s]", lock.claims, want)
	}
	if !strings.Contains(logs.all(), "another replica has run it for this deploy") {
		t.Errorf("logs = %q", logs.all())
	}
	if len(lock.done) != 0 {
		t.Errorf("a replica that did not run it recorded %v", lock.done)
	}
	// Claimed here: it runs, and its mapping halts, so nothing is recorded.
	lock.grantAt, lock.doneAt = 1, 0
	err = adapter.RunStartup(context.Background(), runners)
	if err == nil || !strings.Contains(err.Error(), "its mapping says halt") {
		t.Errorf("err = %v, want the claimed route to run and halt", err)
	}
	if len(lock.done) != 0 {
		t.Errorf("a route that halted was recorded as done: %v", lock.done)
	}
}

// §6: a route that proceeds is recorded, so the replicas waiting on it may
// start, and is recorded for the deploy it ran in.
func TestStartupOnceRecordsACompletion(t *testing.T) {
	lock := &onceLock{grantAt: 1}
	runners, err := startupRunnersWith(t, lock, "OnceOk")
	if err != nil {
		t.Fatal(err)
	}
	if err := adapter.RunStartup(context.Background(), runners); err != nil {
		t.Fatal(err)
	}
	// Retained for seven days.
	if want := "OnceOk run -> storageOk|7|replica-1|604800000"; len(lock.done) != 1 || lock.done[0] != want {
		t.Errorf("done = %v, want [%s]", lock.done, want)
	}
}

// §6: a replica that loses the claim waits for the winner to finish, polling,
// and starts when it has; it does not run the route.
func TestStartupOnceWaitsForTheWinner(t *testing.T) {
	var logs logLines
	lock := &onceLock{doneAt: 4}
	runners, err := startupRunnersWith(t, lock, "Once")
	if err != nil {
		t.Fatal(err)
	}
	runners[0].Logf = logs.Logf
	quick(runners[0])
	if err := adapter.RunStartup(context.Background(), runners); err != nil {
		t.Fatalf("a replica waiting on a winner that finished was refused: %v", err)
	}
	if lock.asked != 4 || len(lock.claims) != 4 {
		t.Errorf("asked %d times and claimed %d times, want 4 of each", lock.asked, len(lock.claims))
	}
	if !strings.Contains(logs.all(), "another replica has run it for this deploy") {
		t.Errorf("logs = %q", logs.all())
	}
}

// §6: when the winner dies without finishing, its claim lapses and a waiting
// replica's next claim wins; it runs the route itself.
func TestStartupOnceTakesOverFromAWinnerThatDied(t *testing.T) {
	lock := &onceLock{grantAt: 3}
	runners, err := startupRunnersWith(t, lock, "Once")
	if err != nil {
		t.Fatal(err)
	}
	quick(runners[0])
	err = adapter.RunStartup(context.Background(), runners)
	if err == nil || !strings.Contains(err.Error(), "its mapping says halt") {
		t.Errorf("err = %v, want the replica that took over to run the route", err)
	}
	if len(lock.claims) != 3 {
		t.Errorf("claims = %v, want three", lock.claims)
	}
}

// §6: a winner that never finishes and never lets go is waited on for its
// deadline and slack, twice, and then the start is refused.
func TestStartupOnceGivesUpWaiting(t *testing.T) {
	lock := &onceLock{}
	runners, err := startupRunnersWith(t, lock, "Once")
	if err != nil {
		t.Fatal(err)
	}
	quick(runners[0])
	runners[0].Deadline = 10 * time.Millisecond
	err = adapter.RunStartup(context.Background(), runners)
	if err == nil || !strings.Contains(err.Error(), "another replica claimed it, and it was not finished within 60ms") {
		t.Errorf("err = %v", err)
	}
}

// §6: a claim that can't be made is a refusal: whether the route has run is
// not known.
func TestStartupOnceRefusesWhenTheClaimFails(t *testing.T) {
	lock := &onceLock{grantAt: 1, failure: errors.New("redis is down")}
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
	lock := &onceLock{}
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

// seededRunner plans the Seeded adapter of scheduleProgram for one replica of
// a deploy, with fiig.redis's lock bound and redis as its server.
func seededRunner(t *testing.T, redis *fakeRedis, holder string, deploy int64) *adapter.StartupRunner {
	t.Helper()
	root := t.TempDir()
	for name, src := range fixture.Module(scheduleProgram) {
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
	lock, err := runtime.LockFactory(map[string]any{"type": "lock", "lock": "fiig.redis"})
	if err != nil {
		t.Fatal(err)
	}
	caps := runtime.NewCapabilityRegistry()
	caps.Register(runtime.ScheduleLockCapability, lock)
	RegisterDefaults(caps)
	t.Cleanup(caps.Close)
	in := runtime.New(u, caps)
	in.Imports = BuildImports(files, loaded.Packages)
	in.Groups = BuildGroups(files, loaded.Packages)
	in.Packages = loaded.Packages
	d, _ := u.Lookup("Seeded")
	runners, err := adapter.StartupRunners([]adapter.Served{{Decl: d.(*syntax.AdapterDecl)}}, u, in, holder, deploy)
	if err != nil {
		t.Fatal(err)
	}
	lp := runners[0].Lock().(*runtime.LockProvider)
	pkg, group, _ := lp.Resolve()
	env := map[string]string{"REDIS_URL": redis.url()}
	lookup := func(name string) (string, bool) { v, ok := env[name]; return v, ok }
	program := Program{Universe: u, Imports: in.Imports, Groups: in.Groups}
	if problems := in.LoadConfig(ReachableConfigFrom(program, []Entry{{Name: "Seeded"}, {Package: pkg, Name: group}}), lookup); len(problems) > 0 {
		t.Fatal(problems)
	}
	return runners[0]
}

// §6, through fiig.redis's own lock: of two replicas of one deploy, one runs
// the route and the other finds it done and does not; a new deploy runs it
// again.
func TestStartupOncePerDeployThroughRedis(t *testing.T) {
	redis := startFakeRedis(t, "pw")
	var logs logLines
	for _, holder := range []string{"replica-a", "replica-b"} {
		r := seededRunner(t, redis, holder, 11)
		r.Logf = logs.Logf
		if err := adapter.RunStartup(context.Background(), []*adapter.StartupRunner{r}); err != nil {
			t.Fatalf("%s: %v", holder, err)
		}
	}
	if n := redis.counter("seeds"); n != 1 {
		t.Errorf("two replicas of one deploy ran the route %d times, want 1", n)
	}
	if !strings.Contains(logs.all(), "another replica has run it for this deploy") {
		t.Errorf("logs = %q", logs.all())
	}
	next := seededRunner(t, redis, "replica-c", 12)
	if err := adapter.RunStartup(context.Background(), []*adapter.StartupRunner{next}); err != nil {
		t.Fatal(err)
	}
	if n := redis.counter("seeds"); n != 2 {
		t.Errorf("after a new deploy the route has run %d times, want 2", n)
	}
}

// §6: a once route with no ScheduleLock bound is found before anything runs.
func TestStartupOnceNeedsAScheduleLock(t *testing.T) {
	_, err := startupRunnersWith(t, nil, "Once")
	if err == nil || !strings.Contains(err.Error(), "binds no ScheduleLock to claim it with") {
		t.Errorf("err = %v", err)
	}
}
