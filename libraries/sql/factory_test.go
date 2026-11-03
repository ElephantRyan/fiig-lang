package sql

import (
	"context"
	"testing"

	"fiig-lang/compiler/runtime"
)

// TestFactoryBuildsAWorkingProviderFromDeclarativeConfig proves the
// "capability provider configuration" deliverable end to end: a plain
// JSON-shaped config blob, with no Go code naming SQLite anywhere in this
// package, produces a real, working CapabilityProvider against a real
// database — the only thing specific to SQLite is the test's own driver
// import (modernc.org/sqlite's blank import in provider_test.go already
// registers "sqlite" with database/sql; Factory itself never imports it).
func TestFactoryBuildsAWorkingProviderFromDeclarativeConfig(t *testing.T) {
	// A DSN unique to this test — not the same "file::memory:?cache=shared"
	// every other test in this package shares, since nothing here can
	// reach the underlying *sql.DB (the registry only hands back the
	// CapabilityProvider interface) to Close it when the test ends, and a
	// shared in-memory database stays alive for as long as any connection
	// to it does.
	config := map[string]map[string]any{
		"Database": {"type": "sql", "driver": "sqlite", "dsn": "file:factory_test_db?mode=memory&cache=shared"},
	}
	reg, err := runtime.LoadCapabilities(config, runtime.FactoryRegistry{"sql": Factory})
	if err != nil {
		t.Fatalf("LoadCapabilities: %v", err)
	}

	provider, ok := reg.Lookup("Database")
	if !ok {
		t.Fatal("Database provider not registered")
	}
	// The registry only hands back the plain CapabilityProvider interface
	// — reach the underlying *sql.DB through the one capability call this
	// test actually needs, proving it's a real, live connection, not a
	// type assertion back to *Provider.
	if _, fault := provider.Call(context.Background(), "execute", []runtime.Value{
		runtime.Str("CREATE TABLE customers (id TEXT PRIMARY KEY, name TEXT, email TEXT)"),
	}); fault != nil {
		t.Fatalf("Call: %v", fault)
	}
	if _, fault := provider.Call(context.Background(), "execute", []runtime.Value{
		runtime.Str("INSERT INTO customers (id, name, email) VALUES (?, ?, ?)"),
		runtime.Str("c1"), runtime.Str("Ada"), runtime.Str("ada@example.com"),
	}); fault != nil {
		t.Fatalf("Call: %v", fault)
	}
	v, fault := provider.Call(context.Background(), "queryOne", []runtime.Value{
		runtime.Str("SELECT id, name, email FROM customers WHERE id = ?"), runtime.Str("c1"),
	})
	if fault != nil {
		t.Fatalf("Call: %v", fault)
	}
	rec, ok := v.(runtime.Record)
	if !ok || rec.Fields["name"] != runtime.Str("Ada") {
		t.Fatalf("got %#v, want the row just inserted", v)
	}
}

func TestFactoryRequiresDriverAndDSN(t *testing.T) {
	if _, err := Factory(map[string]any{"dsn": "file::memory:"}); err == nil {
		t.Fatal("got nil error, want one: missing \"driver\"")
	}
	if _, err := Factory(map[string]any{"driver": "sqlite"}); err == nil {
		t.Fatal("got nil error, want one: missing \"dsn\"")
	}
}

func TestFactoryRejectsUnregisteredDriver(t *testing.T) {
	_, err := Factory(map[string]any{"driver": "no-such-driver", "dsn": "whatever"})
	if err == nil {
		t.Fatal("got nil error, want one: no database/sql driver registered under that name")
	}
}
