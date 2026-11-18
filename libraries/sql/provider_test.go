package sql

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite" // test-only driver — isolated to this module's own go.mod, never the core fiig-lang module's.

	"fiig-lang/compiler/decimal"
	"fiig-lang/compiler/runtime"
)

// openTestDB opens a real, in-memory SQLite database (via a pure-Go
// driver — no cgo) with a tiny customers table, proving Provider against
// an actual SQL engine rather than a mock. "file::memory:?cache=shared"
// plus a single open connection keeps one shared in-memory database alive
// across every connection database/sql's pool might otherwise open.
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file::memory:?cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE TABLE customers (id TEXT PRIMARY KEY, name TEXT, email TEXT)`); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestQueryOneReturnsRowOrNone(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.Exec(`INSERT INTO customers (id, name, email) VALUES ('c1', 'Ada', 'ada@example.com')`); err != nil {
		t.Fatal(err)
	}
	p := New(db)

	v, fault := p.Call(context.Background(), "queryOne", []runtime.Value{
		runtime.Str("SELECT id, name, email FROM customers WHERE id = ?"), runtime.Str("c1"),
	})
	if fault != nil {
		t.Fatalf("Call: %v", fault)
	}
	rec, ok := v.(runtime.Record)
	if !ok {
		t.Fatalf("got %T, want Record", v)
	}
	if rec.Fields["name"] != runtime.Str("Ada") {
		t.Fatalf("name = %v, want Ada", rec.Fields["name"])
	}

	v, fault = p.Call(context.Background(), "queryOne", []runtime.Value{
		runtime.Str("SELECT id, name, email FROM customers WHERE id = ?"), runtime.Str("missing"),
	})
	if fault != nil {
		t.Fatalf("Call: %v", fault)
	}
	if _, isNone := v.(runtime.None); !isNone {
		t.Fatalf("got %s, want None for no matching row", v)
	}
}

func TestQueryManyReturnsAllMatchingRows(t *testing.T) {
	db := openTestDB(t)
	for _, row := range [][3]string{{"c1", "Ada", "ada@example.com"}, {"c2", "Grace", "grace@example.com"}} {
		if _, err := db.Exec(`INSERT INTO customers (id, name, email) VALUES (?, ?, ?)`, row[0], row[1], row[2]); err != nil {
			t.Fatal(err)
		}
	}
	p := New(db)
	v, fault := p.Call(context.Background(), "queryMany", []runtime.Value{runtime.Str("SELECT id, name, email FROM customers ORDER BY id")})
	if fault != nil {
		t.Fatalf("Call: %v", fault)
	}
	list, ok := v.(runtime.List)
	if !ok || len(list.Elems) != 2 {
		t.Fatalf("got %#v, want a 2-element List", v)
	}
	first := list.Elems[0].(runtime.Record)
	if first.Fields["name"] != runtime.Str("Ada") {
		t.Fatalf("got %s", first)
	}
}

func TestExecuteReturnsRowsAffected(t *testing.T) {
	db := openTestDB(t)
	p := New(db)

	v, fault := p.Call(context.Background(), "execute", []runtime.Value{
		runtime.Str("INSERT INTO customers (id, name, email) VALUES (?, ?, ?)"),
		runtime.Str("c1"), runtime.Str("Ada"), runtime.Str("ada@example.com"),
	})
	if fault != nil {
		t.Fatalf("Call: %v", fault)
	}
	if v != runtime.Int(1) {
		t.Fatalf("got %s, want Int(1)", v)
	}

	v, fault = p.Call(context.Background(), "execute", []runtime.Value{runtime.Str("DELETE FROM customers WHERE id = 'missing'")})
	if fault != nil {
		t.Fatalf("Call: %v", fault)
	}
	if v != runtime.Int(0) {
		t.Fatalf("got %s, want Int(0) rows affected", v)
	}
}

func TestValueWrapperArgumentUnwraps(t *testing.T) {
	db := openTestDB(t)
	p := New(db)
	customerID := runtime.Str("c1")

	_, fault := p.Call(context.Background(), "execute", []runtime.Value{
		runtime.Str("INSERT INTO customers (id, name, email) VALUES (?, ?, ?)"),
		customerID, runtime.Str("Ada"), runtime.Str("ada@example.com"),
	})
	if fault != nil {
		t.Fatalf("Call: %v", fault)
	}
	v, fault := p.Call(context.Background(), "queryOne", []runtime.Value{
		runtime.Str("SELECT id, name, email FROM customers WHERE id = ?"), customerID,
	})
	if fault != nil {
		t.Fatalf("Call: %v", fault)
	}
	if _, isNone := v.(runtime.None); isNone {
		t.Fatalf("got None, want the row inserted with the unwrapped CustomerId value")
	}
}

func TestTransactionCommits(t *testing.T) {
	db := openTestDB(t)
	p := New(db)

	tx, fault := p.Begin(context.Background(), "")
	if fault != nil {
		t.Fatalf("Begin: %v", fault)
	}
	_, fault = tx.Call(context.Background(), "execute", []runtime.Value{
		runtime.Str("INSERT INTO customers (id, name, email) VALUES (?, ?, ?)"),
		runtime.Str("c1"), runtime.Str("Ada"), runtime.Str("ada@example.com"),
	})
	if fault != nil {
		t.Fatalf("Call: %v", fault)
	}
	// Visible inside the transaction before commit.
	v, fault := tx.Call(context.Background(), "queryOne", []runtime.Value{
		runtime.Str("SELECT id, name, email FROM customers WHERE id = ?"), runtime.Str("c1"),
	})
	if fault != nil {
		t.Fatalf("Call: %v", fault)
	}
	if _, isNone := v.(runtime.None); isNone {
		t.Fatal("got None, want the uncommitted write visible inside its own transaction")
	}

	if fault := tx.Commit(); fault != nil {
		t.Fatalf("Commit: %v", fault)
	}
	// Visible outside the transaction after commit, through the plain Provider.
	v, fault = p.Call(context.Background(), "queryOne", []runtime.Value{
		runtime.Str("SELECT id, name, email FROM customers WHERE id = ?"), runtime.Str("c1"),
	})
	if fault != nil {
		t.Fatalf("Call: %v", fault)
	}
	if _, isNone := v.(runtime.None); isNone {
		t.Fatal("got None, want the committed write visible outside the transaction")
	}
}

func TestTransactionRollsBack(t *testing.T) {
	db := openTestDB(t)
	p := New(db)

	tx, fault := p.Begin(context.Background(), "")
	if fault != nil {
		t.Fatalf("Begin: %v", fault)
	}
	_, fault = tx.Call(context.Background(), "execute", []runtime.Value{
		runtime.Str("INSERT INTO customers (id, name, email) VALUES (?, ?, ?)"),
		runtime.Str("c1"), runtime.Str("Ada"), runtime.Str("ada@example.com"),
	})
	if fault != nil {
		t.Fatalf("Call: %v", fault)
	}
	if fault := tx.Rollback(); fault != nil {
		t.Fatalf("Rollback: %v", fault)
	}

	v, fault := p.Call(context.Background(), "queryOne", []runtime.Value{
		runtime.Str("SELECT id, name, email FROM customers WHERE id = ?"), runtime.Str("c1"),
	})
	if fault != nil {
		t.Fatalf("Call: %v", fault)
	}
	if _, isNone := v.(runtime.None); !isNone {
		t.Fatalf("got %s, want None: the insert should have been rolled back", v)
	}
}

// TestProviderSatisfiesRuntimeInterfaces is a compile-time-ish check that
// Provider is actually usable as compiler/runtime expects it to be —
// exactly what a `caps.Register("Database", sql.New(db))` call site needs.
func TestProviderSatisfiesRuntimeInterfaces(t *testing.T) {
	var _ runtime.CapabilityProvider = (*Provider)(nil)
	var _ runtime.TransactionalProvider = (*Provider)(nil)
}

// TestFloatColumnsRoundTrip is ADR 0048 §1 item 3: a REAL column decodes to
// a float instead of faulting, and a float binds as a parameter.
func TestFloatColumnsRoundTrip(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.Exec(`CREATE TABLE readings (id TEXT PRIMARY KEY, celsius REAL)`); err != nil {
		t.Fatal(err)
	}
	p := New(db)
	if _, fault := p.Call(context.Background(), "execute", []runtime.Value{
		runtime.Str("INSERT INTO readings (id, celsius) VALUES (?, ?)"), runtime.Str("r1"), runtime.Float(-12.75),
	}); fault != nil {
		t.Fatalf("execute: %v", fault)
	}
	v, fault := p.Call(context.Background(), "queryOne", []runtime.Value{
		runtime.Str("SELECT celsius FROM readings WHERE id = ?"), runtime.Str("r1"),
	})
	if fault != nil {
		t.Fatalf("queryOne: %v", fault)
	}
	if got := v.(runtime.Record).Fields["celsius"]; got != runtime.Float(-12.75) {
		t.Fatalf("celsius = %v, want -12.75", got)
	}
}

// TestDecimalBindsAsText is ADR 0064 §8: a decimal is stored as TEXT at its
// scale, so 1.50 comes back as "1.50", not the float 1.5. A row has no
// declared types, so it reads back as a string.
func TestDecimalBindsAsText(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.Exec(`CREATE TABLE weights (id TEXT PRIMARY KEY, kilograms TEXT)`); err != nil {
		t.Fatal(err)
	}
	d, _ := decimal.Parse("1.50")
	p := New(db)
	if _, fault := p.Call(context.Background(), "execute", []runtime.Value{
		runtime.Str("INSERT INTO weights (id, kilograms) VALUES (?, ?)"), runtime.Str("w1"), runtime.Decimal{Decimal: d},
	}); fault != nil {
		t.Fatalf("execute: %v", fault)
	}
	v, fault := p.Call(context.Background(), "queryOne", []runtime.Value{
		runtime.Str("SELECT kilograms, typeof(kilograms) AS kind FROM weights WHERE id = ?"), runtime.Str("w1"),
	})
	if fault != nil {
		t.Fatalf("queryOne: %v", fault)
	}
	row := v.(runtime.Record)
	if row.Fields["kilograms"] != runtime.Str("1.50") || row.Fields["kind"] != runtime.Str("text") {
		t.Fatalf("row = %v, want kilograms 1.50 stored as text", row)
	}
}
