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

// ADR 0112: schedule adapters, claimed through fiig.redis's lock (or a
// local one), on a fake clock.

var scheduleProgram = map[string]string{
	"fiig.pkg.json": `{
    "path": "scheduletest.jobs",
    "kind": "application",
    "version": "1.0.0",
    "dependencies": { "fiig.redis": "1.0.0" },
    "capabilities": ["Socket"]
}`,
	"job_error.fiig": `enum JobError {
    Busy
    Broken
}`,
	"count_tick.fiig": `use fiig.redis.Redis

gateway countTick(firedAt: int) -> Result<None, JobError>
    requires Redis
{
    match Redis.send(["INCR", "ticks"]) {
        Ok(_) -> {
            return Ok(None)
        }
        Err(_) -> {
            return Err(JobError.Broken)
        }
    }
}`,
	"note_tick.fiig": `activity noteTick(firedAt: int) -> Result<None, JobError> {
    return countTick(firedAt)
}`,
	// A firing on the hour fails with Busy, logged and carried past; one at
	// 13:00 is Broken, which halts.
	"tick.fiig": `workflow tick(firedAt: int) -> Result<None, JobError> {
    noteTick(firedAt)?
    if firedAt % 3600000 == 0 {
        if firedAt % 86400000 == 46800000 {
            return Err(JobError.Broken)
        }
        return Err(JobError.Busy)
    }
    return Ok(None)
}`,
	// ADR 0124 §6: a startup route that runs once per deploy, counted.
	"count_seed.fiig": `use fiig.redis.Redis

gateway countSeed() -> Result<None, JobError>
    requires Redis
{
    match Redis.send(["INCR", "seeds"]) {
        Ok(_) -> {
            return Ok(None)
        }
        Err(_) -> {
            return Err(JobError.Broken)
        }
    }
}`,
	"note_seed.fiig": `activity noteSeed() -> Result<None, JobError> {
    return countSeed()
}`,
	"seed.fiig": `workflow seed() -> Result<None, JobError> {
    noteSeed()?
    return Ok(None)
}`,
	"seeded.fiig": `adapter startup Seeded {
    run once -> seed() {
        Ok(_) -> proceed
        Err(_) -> halt
    }
}`,
	"spin.fiig": `workflow spin() -> Result<None, JobError> {
    while true {
    }
    return Ok(None)
}`,
	"broken.fiig": `workflow broken(firedAt: int) -> Result<None, JobError> {
    zero = firedAt - firedAt
    n = firedAt / zero
    if n > 0 {
        return Ok(None)
    }
    return Ok(None)
}`,
	"faulter.fiig": `adapter schedule Faulter {
    every "15m" -> broken(firedAt: int) {
        _ -> halt
    }
}`,
	"jobs.fiig": `adapter schedule Jobs {
    every "15m" -> tick(firedAt: int) {
        Ok(_) -> done
        Err(JobError.Busy) -> done
        Err(JobError.Broken) -> halt
    }
}`,
	"spinner.fiig": `adapter schedule Spinner {
    every "1s" -> spin() {
        _ -> done
    }
}`,
}

// fakeClock starts at a time and moves only when a runner waits; it ends
// the run after stops waits.
type fakeClock struct {
	mu    sync.Mutex
	now   time.Time
	waits int
	stops int
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Wait(ctx context.Context, d time.Duration) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.waits == c.stops {
		return false
	}
	c.waits++
	c.now = c.now.Add(d)
	return true
}

// scheduleRunner loads scheduleProgram and returns the runner of adapter,
// with lock bound as ScheduleLock and the given holder.
func scheduleRunner(t *testing.T, redis *fakeRedis, lock map[string]any, adapterName, holder string) *adapter.ScheduleRunner {
	t.Helper()
	factory := runtime.FactoryRegistry{"lock": runtime.LockFactory, "local": runtime.LocalLockFactory}[lock["type"].(string)]
	p, err := factory(lock)
	if err != nil {
		t.Fatal(err)
	}
	return scheduleRunnerWithLock(t, redis, p, adapterName, holder)
}

// scheduleRunnerWithLock is scheduleRunner with a lock provider built
// already, such as a test's own.
func scheduleRunnerWithLock(t *testing.T, redis *fakeRedis, p runtime.CapabilityProvider, adapterName, holder string) *adapter.ScheduleRunner {
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
	caps := runtime.NewCapabilityRegistry()
	caps.Register(runtime.ScheduleLockCapability, p)
	RegisterDefaults(caps)
	t.Cleanup(caps.Close)
	in := runtime.New(u, caps)
	in.Imports = BuildImports(files, loaded.Packages)
	in.Groups = BuildGroups(files, loaded.Packages)
	in.Packages = loaded.Packages
	d, _ := u.Lookup(adapterName)
	runners, err := adapter.ScheduleRunners([]adapter.Served{{Decl: d.(*syntax.AdapterDecl)}}, u, in, holder)
	if err != nil {
		t.Fatal(err)
	}
	entries := []Entry{{Name: adapterName}}
	if lp, ok := runners[0].Lock().(*runtime.LockProvider); ok {
		pkg, group, _ := lp.Resolve()
		entries = append(entries, Entry{Package: pkg, Name: group})
	}
	env := map[string]string{"REDIS_URL": redis.url()}
	lookup := func(name string) (string, bool) { v, ok := env[name]; return v, ok }
	program := Program{Universe: u, Imports: in.Imports, Groups: in.Groups}
	if problems := in.LoadConfig(ReachableConfigFrom(program, entries), lookup); len(problems) > 0 {
		t.Fatal(problems)
	}
	return runners[0]
}

type logLines struct {
	mu    sync.Mutex
	lines []string
}

func (l *logLines) Logf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *logLines) all() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.lines, "\n")
}

func on(r *adapter.ScheduleRunner, c *fakeClock, l *logLines) *adapter.ScheduleRunner {
	r.Now, r.Wait, r.Logf = c.Now, c.Wait, l.Logf
	return r
}

// §4: two replicas computing the same firings run each of them once
// between them, and a Busy firing is logged and carried past.
func TestScheduleRunsEachFiringOnce(t *testing.T) {
	redis := startFakeRedis(t, "pw")
	lock := map[string]any{"type": "lock", "lock": "fiig.redis"}
	start := time.Date(2026, 11, 20, 10, 50, 0, 0, time.UTC)
	var logs logLines
	for _, holder := range []string{"replica-a", "replica-b"} {
		// 10:50 -> 11:00 (Busy), 11:15, 11:30, 11:45.
		clock := &fakeClock{now: start, stops: 4}
		r := on(scheduleRunner(t, redis, lock, "Jobs", holder), clock, &logs)
		if err := r.Run(context.Background()); err != nil {
			t.Fatalf("%s: %v", holder, err)
		}
	}
	if n := redis.counter("ticks"); n != 4 {
		t.Errorf("two replicas ran %d firings, want 4", n)
	}
	if got := logs.all(); strings.Count(got, "failed with JobError.Busy") != 1 || !strings.Contains(got, "2026-11-20T11:00:00Z") {
		t.Errorf("logs = %q", got)
	}
}

// §4: a local lock runs every firing; §2: Broken halts.
func TestScheduleHaltsOnHalt(t *testing.T) {
	redis := startFakeRedis(t, "pw")
	clock := &fakeClock{now: time.Date(2026, 11, 20, 12, 40, 0, 0, time.UTC), stops: 10}
	var logs logLines
	r := on(scheduleRunner(t, redis, map[string]any{"type": "local"}, "Jobs", "only"), clock, &logs)
	var halt *adapter.Halt
	if err := r.Run(context.Background()); !errors.As(err, &halt) || !strings.Contains(halt.Element, "13:00:00Z") || !strings.Contains(halt.Reason, "halt") {
		t.Fatalf("got %v", err)
	}
	// 12:45 and 13:00.
	if n := redis.counter("ticks"); n != 2 {
		t.Errorf("ran %d firings, want 2", n)
	}
}

// §3: a run that reaches the next firing is stopped and logged, and the
// next firing runs.
func TestScheduleOverrunIsStopped(t *testing.T) {
	redis := startFakeRedis(t, "pw")
	clock := &fakeClock{now: time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC), stops: 2}
	var logs logLines
	r := on(scheduleRunner(t, redis, map[string]any{"type": "local"}, "Spinner", "only"), clock, &logs)
	if err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := logs.all(); strings.Count(got, "overran into the next firing") != 2 {
		t.Errorf("logs = %q", got)
	}
}

// Review fix 4 (§2, as amended): a fault fails its firing only, is logged,
// and the next firing runs; the mapping's halt is never reached, since a
// faulted run has no result to match.
func TestScheduleFaultSkipsTheFiring(t *testing.T) {
	redis := startFakeRedis(t, "pw")
	clock := &fakeClock{now: time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC), stops: 2}
	var logs logLines
	r := on(scheduleRunner(t, redis, map[string]any{"type": "local"}, "Faulter", "only"), clock, &logs)
	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("got %v, want the run to carry on past both faults", err)
	}
	if got := logs.all(); strings.Count(got, "faulted, so it was skipped") != 2 {
		t.Errorf("logs = %q", got)
	}
}

// recordingLock claims every firing and remembers how long each was held.
type recordingLock struct {
	mu    sync.Mutex
	holds []int64
}

func (l *recordingLock) Call(context.Context, string, []runtime.Value) (runtime.Value, *runtime.Fault) {
	return nil, nil
}

func (l *recordingLock) Claim(_ context.Context, _ string, _ int64, _ string, holdMillis int64) (bool, error, *runtime.Fault) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.holds = append(l.holds, holdMillis)
	return true, nil, nil
}

func (l *recordingLock) Complete(context.Context, string, int64, string, int64) (error, *runtime.Fault) {
	return nil, nil
}

func (l *recordingLock) Completed(context.Context, string, int64) (bool, error, *runtime.Fault) {
	return false, nil, nil
}

func (l *recordingLock) Describe() string { return "a test" }

// Review (ADR 0112 §4): a claim is held to the next firing but never less
// than a minute, so an every "1s" job's claims don't expire while a
// replica with a late clock could still claim them.
func TestScheduleClaimsAreHeldAMinute(t *testing.T) {
	redis := startFakeRedis(t, "pw")
	lock := &recordingLock{}
	clock := &fakeClock{now: time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC), stops: 1}
	r := on(scheduleRunnerWithLock(t, redis, lock, "Spinner", "only"), clock, &logLines{})
	if err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(lock.holds) != 1 || lock.holds[0] != 60000 {
		t.Errorf("holds = %v, want [60000]", lock.holds)
	}
}

// §4: with no lock bound, the adapter doesn't start.
func TestScheduleNeedsALock(t *testing.T) {
	root := t.TempDir()
	for name, src := range fixture.Module(scheduleProgram) {
		os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0o755)
		os.WriteFile(filepath.Join(root, name), []byte(src), 0o644)
	}
	loaded, _ := loader.Load(root, nil)
	files, _, _ := LoadFiles(root)
	u, _ := types.BuildUniverse(files)
	caps := runtime.NewCapabilityRegistry()
	in := runtime.New(u, caps)
	in.Packages = loaded.Packages
	d, _ := u.Lookup("Jobs")
	_, err := adapter.ScheduleRunners([]adapter.Served{{Decl: d.(*syntax.AdapterDecl)}}, u, in, "x")
	if err == nil || !strings.Contains(err.Error(), "binds no ScheduleLock, so every replica would run each job") {
		t.Errorf("err = %v", err)
	}
}
