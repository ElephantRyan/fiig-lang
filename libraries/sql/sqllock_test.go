package sql

import (
	"context"
	"database/sql"
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
	"fiig-lang/tooling/runprogram"
)

// ADR 0125: fiig.sqllock's SqlScheduleLock, in a real SQLite database, deciding
// which replica runs a startup route that says once (ADR 0124 §6).

var sqlLockProgram = map[string]string{
	"fiig.pkg.json": `{
    "path": "sqllock.app",
    "kind": "application",
    "version": "1.0.0",
    "dependencies": { "fiig.sqllock": "1.0.0" },
    "capabilities": ["Database"],
    "nativeCapabilities": ["Database"]
}`,
	"job_error.fiig": `enum JobError {
    Broken
}`,
	"count_seed.fiig": `gateway countSeed() -> Result<None, JobError>
    requires Database
{
    Database.execute("CREATE TABLE IF NOT EXISTS seeds (n INTEGER)")
    Database.execute("INSERT INTO seeds (n) VALUES (1)")
    return Ok(None)
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
}

// sqlLockRunner plans Seeded for one replica of a deploy, with fiig.sqllock's lock
// and db as the Database.
func sqlLockRunner(t *testing.T, db *sql.DB, holder string, deploy int64) *adapter.StartupRunner {
	t.Helper()
	root := t.TempDir()
	for name, src := range fixture.Module(sqlLockProgram) {
		os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0o755)
		if err := os.WriteFile(filepath.Join(root, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	loaded, errs := loader.Load(root, nil)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	files, _, err := runprogram.LoadFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	if diags := runprogram.Check(root, files, loaded.Packages); len(diags) > 0 {
		t.Fatal(diags)
	}
	u, _ := types.BuildUniverse(files)
	lock, err := runtime.LockFactory(map[string]any{"type": "lock", "lock": "fiig.sqllock"})
	if err != nil {
		t.Fatal(err)
	}
	caps := runtime.NewCapabilityRegistry()
	caps.Register(runtime.ScheduleLockCapability, lock)
	caps.Register("Database", New(db))
	runprogram.RegisterDefaults(caps)
	t.Cleanup(caps.Close)
	in := runtime.New(u, caps)
	in.Imports = runprogram.BuildImports(files, loaded.Packages)
	in.Groups = runprogram.BuildGroups(files, loaded.Packages)
	in.Packages = loaded.Packages
	d, _ := u.Lookup("Seeded")
	runners, err := adapter.StartupRunners([]adapter.Served{{Decl: d.(*syntax.AdapterDecl)}}, u, in, holder, deploy)
	if err != nil {
		t.Fatal(err)
	}
	return runners[0]
}

func seedCount(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM seeds`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Of two replicas of one deploy, one runs the route and the other finds it
// done; a new deploy runs it again.
func TestSqlScheduleLockRunsAOnceRouteOncePerDeploy(t *testing.T) {
	db := openTestDB(t)
	var logs []string
	for _, holder := range []string{"replica-a", "replica-b"} {
		r := sqlLockRunner(t, db, holder, 11)
		r.Logf = func(format string, args ...any) { logs = append(logs, format) }
		if err := adapter.RunStartup(context.Background(), []*adapter.StartupRunner{r}); err != nil {
			t.Fatalf("%s: %v", holder, err)
		}
	}
	if n := seedCount(t, db); n != 1 {
		t.Errorf("two replicas of one deploy ran the route %d times, want 1", n)
	}
	if !strings.Contains(strings.Join(logs, "\n"), "another replica has run it for this deploy") {
		t.Errorf("logs = %q", logs)
	}
	next := sqlLockRunner(t, db, "replica-c", 12)
	if err := adapter.RunStartup(context.Background(), []*adapter.StartupRunner{next}); err != nil {
		t.Fatal(err)
	}
	if n := seedCount(t, db); n != 2 {
		t.Errorf("after a new deploy the route has run %d times, want 2", n)
	}
}

// A claim lasts until it expires, and then another caller takes it: a winner
// that died doesn't keep a deploy from starting.
func TestSqlScheduleLockClaimExpires(t *testing.T) {
	db := openTestDB(t)
	lock := sqlLockRunner(t, db, "replica-a", 21).Lock()
	claim := func(job, holder string, hold int64) bool {
		t.Helper()
		won, failure, f := lock.Claim(context.Background(), job, 21, holder, hold)
		if failure != nil || f != nil {
			t.Fatalf("claim: %v %v", failure, f)
		}
		return won
	}
	if !claim("held", "a", 60_000) {
		t.Fatal("the first claim should win")
	}
	if claim("held", "b", 60_000) {
		t.Error("a second claim while the first is held should lose")
	}
	if !claim("short", "a", 1) {
		t.Fatal("a fresh job should be claimed")
	}
	time.Sleep(5 * time.Millisecond)
	if !claim("short", "b", 60_000) {
		t.Error("a lapsed claim should be taken over")
	}
}

// A claim deletes the rows that have expired, claims and done rows alike, and
// keeps the live ones, so a done row still answers completed until its
// retention ends.
func TestSqlScheduleLockClaimPrunesExpiredRows(t *testing.T) {
	db := openTestDB(t)
	lock := sqlLockRunner(t, db, "replica-a", 31).Lock()
	ctx := context.Background()
	if _, failure, f := lock.Claim(ctx, "first", 31, "a", 60_000); failure != nil || f != nil {
		t.Fatalf("claim: %v %v", failure, f)
	}
	if failure, f := lock.Complete(ctx, "kept", 31, "a", 60_000); failure != nil || f != nil {
		t.Fatalf("complete: %v %v", failure, f)
	}
	past := time.Now().UnixMilli() - 1_000
	for _, key := range []string{"fiig:schedule:old:1", "fiig:schedule:gone:31:done"} {
		if _, err := db.Exec(`INSERT INTO fiig_locks (lock_key, holder, expires_at) VALUES (?, 'z', ?)`, key, past); err != nil {
			t.Fatal(err)
		}
	}
	if won, failure, f := lock.Claim(ctx, "next", 31, "a", 60_000); !won || failure != nil || f != nil {
		t.Fatalf("claim: %v %v %v", won, failure, f)
	}
	rows, err := db.Query(`SELECT lock_key FROM fiig_locks ORDER BY lock_key`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var k string
		rows.Scan(&k)
		keys = append(keys, k)
	}
	want := "fiig:schedule:first:31 fiig:schedule:kept:31:done fiig:schedule:next:31"
	if got := strings.Join(keys, " "); got != want {
		t.Errorf("rows after a claim = %q, want %q", got, want)
	}
	if done, failure, f := lock.Completed(ctx, "kept", 31); !done || failure != nil || f != nil {
		t.Errorf("an unexpired done row should still be completed: %v %v %v", done, failure, f)
	}
}
