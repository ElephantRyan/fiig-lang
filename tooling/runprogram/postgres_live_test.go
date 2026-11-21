package runprogram

import (
	"context"
	"os"
	"testing"
	"time"

	"fiig-lang/compiler/runtime"
)

// ADR 0085 against a real Postgres, named by the variables
// examples/relay-notes/caps.postgres.json reads: PGHOST, PGPORT, PGUSER,
// PGPASSWORD, PGDATABASE, and PGSSLMODE (disable when unset). Skipped
// without PGHOST.

func livePostgres(t *testing.T, extra map[string]any) runtime.CapabilityProvider {
	t.Helper()
	host := os.Getenv("PGHOST")
	if host == "" {
		t.Skip("PGHOST and the other PG* variables name a Postgres to test against")
	}
	settings := map[string]any{"host": host, "user": os.Getenv("PGUSER"), "password": os.Getenv("PGPASSWORD"),
		"database": os.Getenv("PGDATABASE"), "sslmode": "disable", "applicationName": "fiig-live-test"}
	if port := os.Getenv("PGPORT"); port != "" {
		settings["port"] = port
	}
	if mode := os.Getenv("PGSSLMODE"); mode != "" {
		settings["sslmode"] = mode
	}
	for k, v := range extra {
		settings[k] = v
	}
	in, _ := pgInterp(t, settings)
	p, _ := in.Capabilities.Lookup("Database")
	return p
}

func liveCount(t *testing.T, p runtime.CapabilityProvider, sql string, args ...runtime.Value) int64 {
	t.Helper()
	v, f := p.Call(context.Background(), "queryOne", append([]runtime.Value{runtime.Str(sql)}, args...))
	if f != nil {
		t.Fatal(f)
	}
	return int64(v.(runtime.Record).Fields["n"].(runtime.Int))
}

// ADR 0084 §3a: past its deadline, a statement is cancelled on the server,
// not left running there.
func TestLivePostgresCancelsAStatementPastItsDeadline(t *testing.T) {
	p := livePostgres(t, nil)
	const sleep = "SELECT pg_sleep(10)"
	ctx, stop := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer stop()
	start := time.Now()
	if _, f := p.Call(ctx, "execute", []runtime.Value{runtime.Str(sleep)}); f == nil {
		t.Fatal("execute past its deadline succeeded")
	}
	if took := time.Since(start); took > 1500*time.Millisecond {
		t.Errorf("execute took %v past a 500 ms deadline", took)
	}
	running := int64(-1)
	for range 20 {
		running = liveCount(t, p, "SELECT count(*) AS n FROM pg_stat_activity WHERE query = ? AND state = 'active'", runtime.Str(sleep))
		if running == 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if running != 0 {
		t.Errorf("%d backends still run %s two seconds after the deadline", running, sleep)
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("the statement stopped only after %v", took)
	}
}

// The same inside a transaction: the block's pinned connection is
// cancelled and discarded when the block ends.
func TestLivePostgresCancelsAStatementInATransaction(t *testing.T) {
	p := livePostgres(t, nil)
	const sleep = "SELECT pg_sleep(9)"
	tx, f := p.(*runtime.DriverProvider).Begin(context.Background(), "")
	if f != nil {
		t.Fatal(f)
	}
	ctx, stop := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer stop()
	if _, f := tx.Call(ctx, "execute", []runtime.Value{runtime.Str(sleep)}); f == nil {
		t.Fatal("execute past its deadline succeeded")
	}
	tx.Rollback()
	running := int64(-1)
	for range 20 {
		running = liveCount(t, p, "SELECT count(*) AS n FROM pg_stat_activity WHERE query = ? AND state = 'active'", runtime.Str(sleep))
		if running == 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if running != 0 {
		t.Errorf("%d backends still run %s two seconds after the deadline", running, sleep)
	}
}
