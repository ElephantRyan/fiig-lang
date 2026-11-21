package sql

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fiig-lang/compiler/parser"
	"fiig-lang/compiler/runtime"
	"fiig-lang/compiler/syntax"
	"fiig-lang/tooling/schemacheck"
)

// ADR 0131: queries are prepared against the schema the migrations make, in a
// real SQLite database.

func schemaRoot(t *testing.T, migrations map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, text := range migrations {
		p := filepath.Join(root, "migrations", name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func queriesOf(t *testing.T, sqls ...string) []schemacheck.Query {
	t.Helper()
	var files []*syntax.File
	var paths []string
	for i, q := range sqls {
		f, errs := parser.Parse("gateway g" + string(rune('a'+i)) + "() -> int\n    requires Database\n{\n    return Database.execute(\"" + q + "\")\n}")
		if len(errs) > 0 {
			t.Fatal(errs)
		}
		files = append(files, f)
		paths = append(paths, "src/G"+string(rune('a'+i))+".fiig")
	}
	return schemacheck.Queries(files, paths)
}

func TestACheckFindsWhatTheSchemaLacks(t *testing.T) {
	root := schemaRoot(t, map[string]string{
		"0001_accounts.sql": "CREATE TABLE account (id TEXT PRIMARY KEY, name TEXT NOT NULL)",
		"0002_seen.sql":     "ALTER TABLE account ADD COLUMN seen_at INTEGER",
	})
	res, err := schemacheck.Check(context.Background(), root, nil, queriesOf(t,
		"SELECT id, name, seen_at FROM account WHERE id = ?",
		"INSERT INTO account (id, name) VALUES (?, ?)",
		"SELECT id, nmae FROM account",
		"DELETE FROM accounts WHERE id = ?",
		"UPDATE account SET seen_at = ? WHERE id = ?",
	), runtime.FactoryRegistry{"sql": Factory})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Diagnostics) != 2 {
		t.Fatalf("diagnostics = %v, want the misspelt column and table", res.Diagnostics)
	}
	var all []string
	for _, d := range res.Diagnostics {
		all = append(all, d.String())
	}
	got := strings.Join(all, "\n")
	for _, want := range []string{"[Q4]", "no such column: nmae", "no such table: accounts", "src/Gc.fiig", "src/Gd.fiig"} {
		if !strings.Contains(got, want) {
			t.Errorf("diagnostics = %s\nwant one containing %q", got, want)
		}
	}
	if res.Note != "" {
		t.Errorf("note = %q", res.Note)
	}
}

// SQL SQLite can't prepare is counted, not reported: it may be Postgres's.
func TestAQuerySQLiteCannotPrepareIsNotAnError(t *testing.T) {
	root := schemaRoot(t, map[string]string{"0001_init.sql": "CREATE TABLE t (id INTEGER PRIMARY KEY)"})
	res, err := schemacheck.Check(context.Background(), root, nil, queriesOf(t,
		"SELECT id FROM t",
		"SELECT id FROM t WHERE id = ANY(?)::int[] FOR UPDATE SKIP LOCKED",
	), runtime.FactoryRegistry{"sql": Factory})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Diagnostics) != 0 || !strings.Contains(res.Note, "1 of 2 queries use SQL that SQLite can't prepare") {
		t.Errorf("diagnostics = %v, note = %q", res.Diagnostics, res.Note)
	}
}

// With no migrations there is no schema in files, and nothing is flagged.
func TestAProgramWithoutMigrationsIsNotChecked(t *testing.T) {
	res, err := schemacheck.Check(context.Background(), t.TempDir(), nil, queriesOf(t, "SELECT id FROM anything"), runtime.FactoryRegistry{"sql": Factory})
	if err != nil || len(res.Diagnostics) != 0 || !strings.Contains(res.Note, "has no migrations") {
		t.Errorf("res = %+v, err = %v", res, err)
	}
}

// Migrations SQLite can't run (Postgres-only) leave the queries unchecked.
func TestMigrationsSQLiteCannotRunLeaveTheQueriesUnchecked(t *testing.T) {
	root := schemaRoot(t, map[string]string{"0001_init.sql": "CREATE TABLE t (id UUID PRIMARY KEY DEFAULT gen_random_uuid()); CREATE EXTENSION pgcrypto"})
	res, err := schemacheck.Check(context.Background(), root, nil, queriesOf(t, "SELECT id FROM t"), runtime.FactoryRegistry{"sql": Factory})
	if err != nil || len(res.Diagnostics) != 0 || !strings.Contains(res.Note, "the migrations don't apply to SQLite") {
		t.Errorf("res = %+v, err = %v", res, err)
	}
}
