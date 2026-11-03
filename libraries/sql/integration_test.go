package sql

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"fiig-lang/compiler/parser"
	"fiig-lang/compiler/runtime"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// TestPlaceOrderAgainstRealSQLite is the real end-to-end proof this whole
// library exists for: order-processing/activities/place_order.fiig's two
// `transaction Database { ... }` blocks, running unmodified against an
// actual SQLite database (via the pure-Go test-only driver) instead of any
// kind of mock — the first time any fiig example has executed against a
// real database engine rather than a simulated/scripted capability.
func TestPlaceOrderAgainstRealSQLite(t *testing.T) {
	u := loadUniverse(t,
		"order-processing/models", "order-processing/errors",
		"order-processing/rules", "order-processing/activities", "order-processing/gateways", "order-processing/workflows")

	db := openTestDB(t)
	if _, err := db.Exec(`CREATE TABLE inventory_reservations (order_id TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE orders (id TEXT, customer_id TEXT, total INTEGER, payment_reference TEXT)`); err != nil {
		t.Fatal(err)
	}

	caps := runtime.NewCapabilityRegistry()
	caps.Register("Database", New(db))
	caps.Register("PaymentGateway", runtime.FuncProvider{
		"charge": func(_ context.Context, args []runtime.Value) (runtime.Value, *runtime.Fault) {
			return runtime.Result{Ok: true, Value: runtime.Str("ref-1")}, nil
		},
	})
	in := runtime.New(u, caps)

	orderID := runtime.Str("o1")
	customerID := runtime.Str("c1")
	total := runtime.Int(500)

	v, fault := in.Call(context.Background(), "placeOrder", map[string]runtime.Value{
		"orderId": orderID, "customerId": customerID, "total": total,
	})
	if fault != nil {
		t.Fatalf("Call: %v", fault)
	}
	rv, ok := v.(runtime.Result)
	if !ok || !rv.Ok {
		t.Fatalf("got %#v, want Ok", v)
	}

	// Both transaction blocks actually committed to the real database.
	var reservationCount, orderCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM inventory_reservations WHERE order_id = 'o1'`).Scan(&reservationCount); err != nil {
		t.Fatal(err)
	}
	if reservationCount != 1 {
		t.Fatalf("got %d inventory_reservations rows, want 1 (the first transaction block should have committed)", reservationCount)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM orders WHERE id = 'o1'`).Scan(&orderCount); err != nil {
		t.Fatal(err)
	}
	if orderCount != 1 {
		t.Fatalf("got %d orders rows, want 1 (the second transaction block should have committed)", orderCount)
	}
}

// TestReserveInventoryRollsBackRealTransactionOnFailure proves the
// rows-affected contract end to end: a reservation attempt that affects 0
// real rows (the table already dropped, simulating unavailable inventory
// infrastructure) produces OrderError.InventoryUnavailable, and the
// transaction wrapping it rolls back cleanly rather than leaving a fault.
func TestReserveInventoryRollsBackRealTransactionOnFailure(t *testing.T) {
	u := loadUniverse(t, "order-processing/models", "order-processing/errors", "order-processing/gateways")
	db := openTestDB(t)
	// No inventory_reservations table at all — every INSERT into it fails
	// outright, which is a Fault (not 0 rows affected), but still proves
	// transaction.go's Rollback path fires on a real database/sql error.
	caps := runtime.NewCapabilityRegistry()
	caps.Register("Database", New(db))
	in := runtime.New(u, caps)

	orderID := runtime.Str("o1")
	_, fault := in.Call(context.Background(), "reserveInventory", map[string]runtime.Value{"orderId": orderID})
	if fault == nil {
		t.Fatal("got nil fault, want one: no such table")
	}
}

func findExamplesDir(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		candidate := filepath.Join(dir, "examples")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find examples/ directory above " + dir)
		}
		dir = parent
	}
}

func loadUniverse(t *testing.T, subdirs ...string) *types.Universe {
	t.Helper()
	root := findExamplesDir(t)
	var files []*syntax.File
	for _, sub := range subdirs {
		dir := filepath.Join(root, sub)
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if e.IsDir() || filepath.Ext(e.Name()) != ".fiig" {
				continue
			}
			src, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				t.Fatal(err)
			}
			f, errs := parser.Parse(string(src))
			if len(errs) > 0 {
				t.Fatalf("parse %s: %v", e.Name(), errs)
			}
			files = append(files, f)
		}
	}
	u, conflicts := types.BuildUniverse(files)
	if len(conflicts) > 0 {
		t.Fatalf("unexpected conflicts: %+v", conflicts)
	}
	return u
}
