package schemacheck

import (
	"testing"

	"fiig-lang/compiler/parser"
	"fiig-lang/compiler/syntax"
)

func parse(t *testing.T, src string) *syntax.File {
	t.Helper()
	f, errs := parser.Parse(src)
	if len(errs) > 0 {
		t.Fatalf("parse: %v", errs)
	}
	return f
}

// Only a literal is a query to check, in a gateway, a capability group's
// gateway, and a private helper.
func TestQueriesAreTheLiteralDatabaseCalls(t *testing.T) {
	gateway := parse(t, `gateway find(id: string) -> int
    requires Database
{
    Database.queryOne("SELECT id FROM account WHERE id = ?", id);
    Database.execute("UPDATE account SET seen = 1");
    Database.queryMany("SELECT id FROM account");
    return 1;
}`)
	interpolated := parse(t, `gateway built(table: string) -> int
    requires Database
{
    Database.queryOne("SELECT id FROM ${table}");
    return 1;
}`)
	helper := parse(t, `rule shape() -> int {
    return 1;
}

private {
    cleanup() -> int {
        return Database.execute("DELETE FROM account");
    }
}`)
	group := parse(t, `capability Ledger {
    gateway record(entry: string) -> int
        requires Database
    {
        return Database.execute("INSERT INTO ledger (entry) VALUES (?)", entry);
    }
}`)
	got := Queries([]*syntax.File{gateway, interpolated, helper, group}, []string{"a.fiig", "b.fiig", "c.fiig", "d.fiig"})
	want := map[string]string{
		"SELECT id FROM account WHERE id = ?":   "a.fiig",
		"UPDATE account SET seen = 1":           "a.fiig",
		"SELECT id FROM account":                "a.fiig",
		"DELETE FROM account":                   "c.fiig",
		"INSERT INTO ledger (entry) VALUES (?)": "d.fiig",
	}
	if len(got) != len(want) {
		t.Fatalf("queries = %v, want %d", got, len(want))
	}
	for _, q := range got {
		if want[q.SQL] != q.File {
			t.Errorf("query %q in %q, want %q", q.SQL, q.File, want[q.SQL])
		}
		if q.At.Line == 0 {
			t.Errorf("query %q has no position", q.SQL)
		}
	}
}

func TestSchemaProblemsAreTheOnesTheSchemaCauses(t *testing.T) {
	for msg, want := range map[string]bool{
		"SQL logic error: no such table: sync_states (1)":     true,
		"SQL logic error: no such column: usernme (1)":        true,
		"table rating has no column named notes":              true,
		"ambiguous column name: user_id":                      true,
		`SQL logic error: near "RETURNING": syntax error (1)`: false,
		"SQL logic error: no such function: now_utc (1)":      false,
	} {
		if got := isSchemaProblem(msg); got != want {
			t.Errorf("isSchemaProblem(%q) = %v, want %v", msg, got, want)
		}
	}
}
