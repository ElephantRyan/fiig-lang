package runprogram

import (
	"context"
	"strings"
	"testing"

	"fiig-lang/compiler/runtime"
)

// ADR 0085 §3b: com.degeneratedevelopment.postgres's per-connection statement cache, against
// the fake server in postgres_test.go, which keeps prepared statements by
// name as Postgres does.

func pgExecute(p runtime.CapabilityProvider, sql string, args ...runtime.Value) *runtime.Fault {
	_, f := p.Call(context.Background(), "execute", append([]runtime.Value{runtime.Str(sql)}, args...))
	return f
}

// A statement is parsed once per connection, under a name, and then only
// bound.
func TestPostgresPreparesAStatementOnce(t *testing.T) {
	server := &fakePostgres{t: t, auth: authTrust, handle: pets}
	in, _ := pgInterp(t, pgSettings(server.listen(t), ""))
	for range 3 {
		v, f := in.Call(context.Background(), "findPet", map[string]runtime.Value{"name": runtime.Str("Rex")})
		if f != nil || v.(runtime.Record).Fields["name"] != runtime.Value(runtime.Str("Rex")) {
			t.Fatalf("findPet = %v, %v", v, f)
		}
	}
	p, _ := in.Capabilities.Lookup("Database")
	if f := pgExecute(p, "INSERT INTO t VALUES (?, ?)", runtime.Int(1), runtime.Int(2)); f != nil {
		t.Fatal(f)
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	if got := strings.Join(server.parses, ","); got != "fiig_s0,fiig_s1" {
		t.Errorf("parsed %s, want each statement once by name", got)
	}
	if len(server.queries) != 4 || server.queries[2] != "SELECT name, weight, born FROM pets WHERE name = $1" {
		t.Errorf("queries = %q", server.queries)
	}
}

// The least recently used statement is closed to make room; a statement
// that fails to parse isn't kept; size 0 parses every statement unnamed.
func TestPostgresStatementCacheIsBounded(t *testing.T) {
	server := &fakePostgres{t: t, auth: authTrust, handle: func(sql string, params []pgParam) pgReply {
		if strings.HasPrefix(sql, "BAD") {
			return pets(sql, params)
		}
		return pgReply{tag: "INSERT 0 1"}
	}}
	settings := pgSettings(server.listen(t), "")
	settings["statementCacheSize"] = "2"
	in, _ := pgInterp(t, settings)
	p, _ := in.Capabilities.Lookup("Database")
	for _, sql := range []string{"INSERT a", "INSERT b", "INSERT a", "INSERT c", "INSERT a", "INSERT b"} {
		if f := pgExecute(p, sql); f != nil {
			t.Fatal(f)
		}
	}
	for range 2 {
		if f := pgExecute(p, "BAD"); f == nil {
			t.Fatal("BAD succeeded")
		}
	}
	server.mu.Lock()
	// a and b fill it; c closes b (a was used since), b closes c, and the
	// first BAD closes a, then isn't kept, so the second closes nothing.
	got, closes := strings.Join(server.parses, ","), server.closes
	server.mu.Unlock()
	if got != "fiig_s0,fiig_s1,fiig_s2,fiig_s3,fiig_s4,fiig_s5" || closes != 3 {
		t.Errorf("parsed %s with %d closes, want a, b, c, b, BAD, BAD by name and 3 closes", got, closes)
	}

	off := &fakePostgres{t: t, auth: authTrust, handle: func(string, []pgParam) pgReply { return pgReply{tag: "INSERT 0 1"} }}
	settings = pgSettings(off.listen(t), "")
	settings["statementCacheSize"] = "0"
	in, _ = pgInterp(t, settings)
	p, _ = in.Capabilities.Lookup("Database")
	for range 2 {
		if f := pgExecute(p, "INSERT a"); f != nil {
			t.Fatal(f)
		}
	}
	off.mu.Lock()
	defer off.mu.Unlock()
	if got := strings.Join(off.parses, ","); got != "," || off.closes != 0 {
		t.Errorf("parsed %q with %d closes, want the unnamed statement twice", got, off.closes)
	}
}

func TestPostgresStatementCacheSizeSetting(t *testing.T) {
	for value, fragment := range map[string]string{"many": "must be a number", "-1": "must be 0 or more"} {
		settings := pgSettings("5432", "")
		settings["statementCacheSize"] = value
		in, _ := pgInterp(t, settings)
		p, _ := in.Capabilities.Lookup("Database")
		if f := pgExecute(p, "SELECT 1"); f == nil || !strings.Contains(f.Error(), fragment) {
			t.Errorf("statementCacheSize %s: fault = %v, want %q", value, f, fragment)
		}
	}
}

// A cached statement the server refuses as changed (0A000) or unknown
// (26000) is parsed again under its name and run once more. Inside a
// transaction the error stands, since the server would refuse the second
// try, and the statement is parsed again on its next use.
func TestPostgresPreparesAChangedStatementAgain(t *testing.T) {
	const numbered = "INSERT INTO t VALUES ($1, $2)"
	stale := map[string]bool{}
	server := &fakePostgres{t: t, auth: authTrust, handle: pets}
	server.bound = func(sql string) *pgReply {
		if stale[sql] {
			stale[sql] = false
			return &pgReply{errCode: "0A000", errMsg: "cached plan must not change result type"}
		}
		return nil
	}
	in, pool := pgInterp(t, pgSettings(server.listen(t), ""))
	p, _ := in.Capabilities.Lookup("Database")
	insert := []runtime.Value{runtime.Int(1), runtime.Int(2)}
	if f := pgExecute(p, "INSERT INTO t VALUES (?, ?)", insert...); f != nil {
		t.Fatal(f)
	}
	stale[numbered] = true
	if f := pgExecute(p, "INSERT INTO t VALUES (?, ?)", insert...); f != nil {
		t.Fatalf("after a schema change: %v", f)
	}
	if f := pgExecute(p, "DEALLOCATE ALL"); f != nil {
		t.Fatal(f)
	}
	if f := pgExecute(p, "INSERT INTO t VALUES (?, ?)", insert...); f != nil {
		t.Fatalf("after DEALLOCATE ALL: %v", f)
	}
	server.mu.Lock()
	if got := strings.Join(server.parses, ","); got != "fiig_s0,fiig_s0,fiig_s1,fiig_s0" {
		t.Errorf("parsed %s, want the insert, again after each refusal, and DEALLOCATE ALL", got)
	}
	server.parses = nil
	server.mu.Unlock()

	stale[numbered] = true
	tx, f := pool.Begin(context.Background(), "")
	if f != nil {
		t.Fatal(f)
	}
	if _, f := tx.Call(context.Background(), "execute", append([]runtime.Value{runtime.Str("INSERT INTO t VALUES (?, ?)")}, insert...)); f == nil || !strings.Contains(f.Error(), "0A000") {
		t.Fatalf("in a transaction: %v, want the 0A000", f)
	}
	tx.Rollback()
	if f := pgExecute(p, "INSERT INTO t VALUES (?, ?)", insert...); f != nil {
		t.Fatal(f)
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	if got := strings.Join(server.parses, ","); got != "fiig_s2,fiig_s3,fiig_s0" || server.accepted != 1 {
		t.Errorf("parsed %s on %d connections, want BEGIN, ROLLBACK, then the insert again by its name, on one", got, server.accepted)
	}
}
