package sql

import (
	"context"
	"strings"
	"testing"

	"fiig-lang/compiler/checker"
	"fiig-lang/compiler/parser"
	"fiig-lang/compiler/runtime"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// ADR 0087 against real SQLite: a call that opts in gets each conflict as
// a DatabaseConflict, one that doesn't still faults, and inside a
// transaction a handled conflict leaves the transaction usable.

var conflictSources = []string{
	`gateway addPerson(id: string, name: string | None, age: int, team: string | None) -> Result<int, DatabaseConflict>
    requires Database
{
    return Database.execute("INSERT INTO people (id, name, age, team) VALUES (?, ?, ?, ?)", id, name, age, team);
}`,
	`gateway addPersonUnhandled(id: string) -> int
    requires Database
{
    return Database.execute("INSERT INTO people (id, name, age) VALUES (?, 'x', 1)", id);
}`,
	`gateway claimId(id: string) -> Result<string | None, DatabaseConflict>
    requires Database
{
    return Database.queryOne("INSERT INTO people (id, name, age) VALUES (?, 'x', 1) RETURNING id", id);
}`,
	`gateway countPeople() -> int | None
    requires Database
{
    return Database.queryOne("SELECT COUNT(*) FROM people");
}`,
	`activity addAllSkippingTaken(ids: List<string>) -> int {
    added: int = 0;
    transaction Database {
        for (id in ids) {
            match (addPerson(id, "x", 1, None)) {
                Ok(_) -> {
                    added = added + 1;
                }
                Err(_) -> None
            }
        }
    }
    return added;
}`,
}

func conflictInterp(t *testing.T) *runtime.Interp {
	t.Helper()
	files := make([]*syntax.File, len(conflictSources))
	for i, src := range conflictSources {
		f, errs := parser.Parse(src)
		if len(errs) > 0 {
			t.Fatalf("parse: %v", errs)
		}
		files[i] = f
	}
	if diags := checker.Check(files, nil); len(diags) > 0 {
		t.Fatalf("check: %v", diags)
	}
	u, _ := types.BuildUniverse(files)
	db := openTestDB(t)
	for _, stmt := range []string{
		`PRAGMA foreign_keys = ON`,
		`CREATE TABLE teams (id TEXT PRIMARY KEY)`,
		`CREATE TABLE people (id TEXT PRIMARY KEY, name TEXT NOT NULL, age INTEGER CHECK (age >= 0), team TEXT REFERENCES teams (id))`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { db.Exec("DROP TABLE people"); db.Exec("DROP TABLE teams") })
	caps := runtime.NewCapabilityRegistry()
	caps.Register("Database", New(db))
	return runtime.New(u, caps)
}

func conflictOf(t *testing.T, v runtime.Value) (string, string) {
	t.Helper()
	r := v.(runtime.Result)
	if r.Ok {
		return "", ""
	}
	e := r.Value.(runtime.EnumValue)
	if e.TypeName != "DatabaseConflict" {
		t.Fatalf("Err(%v), want a DatabaseConflict", e)
	}
	return e.Variant, string(e.Fields[0].(runtime.Str))
}

func TestEachConflictIsAValue(t *testing.T) {
	in := conflictInterp(t)
	call := func(id string, name runtime.Value, age int64, team runtime.Value) runtime.Value {
		v, f := in.Call(context.Background(), "addPerson", map[string]runtime.Value{"id": runtime.Str(id), "name": name, "age": runtime.Int(age), "team": team})
		if f != nil {
			t.Fatalf("addPerson: %v", f)
		}
		return v
	}
	if v := call("p1", runtime.Str("Ada"), 30, runtime.None{}); !runtime.Equal(v, runtime.Result{Ok: true, Value: runtime.Int(1)}) {
		t.Fatalf("first insert = %v", v)
	}
	cases := []struct {
		v               runtime.Value
		variant, naming string
	}{
		{call("p1", runtime.Str("Bob"), 1, runtime.None{}), "Unique", "people.id"},
		{call("p2", runtime.None{}, 1, runtime.None{}), "NotNull", "people.name"},
		{call("p3", runtime.Str("Cy"), -1, runtime.None{}), "Check", ""},
		{call("p4", runtime.Str("Di"), 1, runtime.Str("no-such-team")), "ForeignKey", ""},
	}
	for _, tc := range cases {
		variant, name := conflictOf(t, tc.v)
		if variant != tc.variant || !strings.Contains(name, tc.naming) {
			t.Errorf("got %s(%q), want %s naming %q", variant, name, tc.variant, tc.naming)
		}
	}
	claimed, f := in.Call(context.Background(), "claimId", map[string]runtime.Value{"id": runtime.Str("p1")})
	if f != nil {
		t.Fatal(f)
	}
	if variant, _ := conflictOf(t, claimed); variant != "Unique" {
		t.Errorf("INSERT ... RETURNING a taken id = %v, want Unique", claimed)
	}
	if _, f := in.Call(context.Background(), "addPersonUnhandled", map[string]runtime.Value{"id": runtime.Str("p1")}); f == nil || !strings.Contains(f.Error(), "UNIQUE") {
		t.Errorf("a call that didn't opt in: fault = %v, want the conflict as a fault", f)
	}
}

func TestAHandledConflictKeepsTheTransaction(t *testing.T) {
	in := conflictInterp(t)
	ids := runtime.List{Elems: []runtime.Value{runtime.Str("a"), runtime.Str("a"), runtime.Str("b")}}
	v, f := in.Call(context.Background(), "addAllSkippingTaken", map[string]runtime.Value{"ids": ids})
	if f != nil {
		t.Fatal(f)
	}
	if v != runtime.Value(runtime.Int(2)) {
		t.Fatalf("added %v, want 2", v)
	}
	n, _ := in.Call(context.Background(), "countPeople", nil)
	if n != runtime.Value(runtime.Int(2)) {
		t.Fatalf("people after commit = %v, want 2: the rows around the handled conflict committed", n)
	}
}
