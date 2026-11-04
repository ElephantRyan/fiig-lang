package runtime

import (
	"context"
	"testing"
)

// fakeDatabase is the "simulated gateway provider" capability.go's doc comment
// describes — a tiny in-memory stand-in good enough to let a Gateway's body
// actually run, nothing more. One instance is reused across a test's calls
// so inserts are visible to later queries within the same test. It
// implements TransactionalProvider too (trivially — Commit/Rollback are
// no-ops, since nothing here actually stages writes), only so that a
// workflow using `transaction Database { ... }` (checkout.fiig's
// placeOrder) has something that answers Begin; transaction_test.go is
// where commit/rollback's real effects are actually exercised, against a
// purpose-built provider that does stage writes.
type fakeDatabase struct {
	rowsByID    map[string]Value
	rowsByEmail map[string]Value

	// executeRowsAffected is what every "execute" call reports — 1 ("always
	// succeeds") unless a test overrides it to exercise the 0-rows-affected
	// path (reserve_inventory.fiig's OrderError.InventoryUnavailable).
	executeRowsAffected int64
}

func newFakeDatabase() *fakeDatabase {
	return &fakeDatabase{rowsByID: map[string]Value{}, rowsByEmail: map[string]Value{}, executeRowsAffected: 1}
}

func (db *fakeDatabase) Call(ctx context.Context, method string, args []Value) (Value, *Fault) {
	switch method {
	case "queryOne":
		// args[0] is the query text (ignored — this fake has no SQL
		// engine); args[1] is the lookup key. Both get_customer.fiig (by
		// id) and check_email_unique.fiig (by email) land here, so probe
		// both tables rather than parsing the query string.
		if len(args) < 2 {
			return nil, Faultf("queryOne: expected at least 2 args, got %d", len(args))
		}
		key := keyString(args[1])
		if row, ok := db.rowsByID[key]; ok {
			return row, nil
		}
		if row, ok := db.rowsByEmail[key]; ok {
			return row, nil
		}
		return None{}, nil
	case "execute":
		return Int(db.executeRowsAffected), nil // rows affected
	default:
		return nil, Faultf("fakeDatabase has no method %q", method)
	}
}

func (db *fakeDatabase) Begin(ctx context.Context) (Tx, *Fault) {
	return fakeTx{db}, nil
}

// fakeTx is deliberately trivial: every call just delegates straight to
// the live db, and Commit/Rollback are both no-ops. Good enough to let a
// `transaction Database { ... }` block run at all; not a demonstration of
// real isolation (see transaction_test.go for that).
type fakeTx struct{ db *fakeDatabase }

func (tx fakeTx) Call(ctx context.Context, method string, args []Value) (Value, *Fault) {
	return tx.db.Call(ctx, method, args)
}
func (tx fakeTx) Commit() *Fault   { return nil }
func (tx fakeTx) Rollback() *Fault { return nil }

func (db *fakeDatabase) insert(id, name, email string) {
	row := Record{Fields: map[string]Value{
		"id":    Str(id),
		"name":  Str(name),
		"email": Str(email),
	}}
	db.rowsByID[id] = row
	db.rowsByEmail[email] = row
}

func keyString(v Value) string {
	return v.String()
}

func customerID(s string) Value {
	return Str(s)
}

func emailAddress(s string) Value {
	return Str(s)
}

func TestGetCustomerStepViaSimulatedCapability(t *testing.T) {
	u := loadUniverse(t, "customer-service/models", "customer-service/errors", "customer-service/gateways")
	db := newFakeDatabase()
	db.insert("c1", "Ada", "ada@example.com")
	caps := NewCapabilityRegistry()
	caps.Register("Database", db)
	caps.Register("Random", NewSeededRandomProvider(1))
	in := newTestInterp(u, caps)

	v, fault := in.Call(context.Background(), "getCustomer", map[string]Value{"id": customerID("c1")})
	if fault != nil {
		t.Fatalf("Call: %v", fault)
	}
	rv := mustResult(t, v)
	if !rv.Ok {
		t.Fatalf("got Err(%s), want Ok", rv.Value)
	}
	customer := mustRecord(t, rv.Value)
	if customer.Fields["name"] != Str("Ada") {
		t.Fatalf("name = %v, want Ada", customer.Fields["name"])
	}

	v, fault = in.Call(context.Background(), "getCustomer", map[string]Value{"id": customerID("missing")})
	if fault != nil {
		t.Fatalf("Call: %v", fault)
	}
	rv = mustResult(t, v)
	if rv.Ok {
		t.Fatalf("got Ok for missing id, want Err")
	}
	ev, ok := rv.Value.(EnumValue)
	if !ok || ev.Variant != "NotFound" {
		t.Fatalf("got %s, want CustomerError.NotFound", rv.Value)
	}
}

func TestCreateCustomerWorkflowComposesGatewaysAndRules(t *testing.T) {
	// create_customer.fiig: validate -> check uniqueness -> insert, each a
	// `?`-propagated call through a workflow calling a rule then two gateways.
	u := loadUniverse(t,
		"customer-service/models", "customer-service/errors",
		"customer-service/rules", "customer-service/formulas", "customer-service/activities", "customer-service/gateways", "customer-service/workflows")
	db := newFakeDatabase()
	db.insert("existing", "Taken", "taken@example.com")
	caps := NewCapabilityRegistry()
	caps.Register("Database", db)
	caps.Register("Random", NewSeededRandomProvider(1))
	in := newTestInterp(u, caps)

	newCustomer := Record{TypeName: "NewCustomer", Fields: map[string]Value{
		"name":  Str("Ada"),
		"email": emailAddress("ada@example.com"),
	}}
	v, fault := in.Call(context.Background(), "createCustomer", map[string]Value{"input": newCustomer})
	if fault != nil {
		t.Fatalf("Call: %v", fault)
	}
	rv := mustResult(t, v)
	if !rv.Ok {
		t.Fatalf("got Err(%s), want Ok", rv.Value)
	}
	customer := mustRecord(t, rv.Value)
	if customer.Fields["name"] != Str("Ada") {
		t.Fatalf("got %s", customer)
	}

	// Blank name -> validateNewCustomer's Err propagates through the `?`.
	blank := Record{TypeName: "NewCustomer", Fields: map[string]Value{
		"name":  Str(""),
		"email": emailAddress("x@example.com"),
	}}
	v, fault = in.Call(context.Background(), "createCustomer", map[string]Value{"input": blank})
	if fault != nil {
		t.Fatalf("Call: %v", fault)
	}
	rv = mustResult(t, v)
	if rv.Ok {
		t.Fatalf("got Ok for blank name, want Err")
	}

	// Email already in use -> checkEmailUnique's Err propagates.
	taken := Record{TypeName: "NewCustomer", Fields: map[string]Value{
		"name":  Str("Bob"),
		"email": emailAddress("taken@example.com"),
	}}
	v, fault = in.Call(context.Background(), "createCustomer", map[string]Value{"input": taken})
	if fault != nil {
		t.Fatalf("Call: %v", fault)
	}
	rv = mustResult(t, v)
	if rv.Ok {
		t.Fatalf("got Ok for taken email, want Err")
	}
	ev, ok := rv.Value.(EnumValue)
	if !ok || ev.Variant != "EmailAlreadyInUse" {
		t.Fatalf("got %s, want CustomerError.EmailAlreadyInUse", rv.Value)
	}
}

func TestCheckoutWorkflowNestedMatchAndCatchAllResultBinding(t *testing.T) {
	// checkout.fiig: nested match statements, and the one corpus case where
	// a bare catch-all name pattern ("found") matches against a Result
	// subject and must bind to its Ok payload (match.go's doc comment).
	u := loadUniverse(t,
		"customer-service/models", "customer-service/errors",
		"customer-service/rules", "customer-service/formulas", "customer-service/activities", "customer-service/gateways", "customer-service/workflows",
		"order-processing/models", "order-processing/errors",
		"order-processing/rules", "order-processing/activities", "order-processing/gateways", "order-processing/workflows")
	db := newFakeDatabase()
	caps := NewCapabilityRegistry()
	caps.Register("Database", db)
	caps.Register("Random", NewSeededRandomProvider(1))
	caps.Register("PaymentGateway", FuncProvider{
		"charge": func(_ context.Context, args []Value) (Value, *Fault) {
			return Result{Ok: true, Value: Str("ref-1")}, nil
		},
	})
	in := newTestInterp(u, caps)

	newCustomer := Record{TypeName: "NewCustomer", Fields: map[string]Value{
		"name":  Str("Ada"),
		"email": emailAddress("ada@example.com"),
	}}
	v, fault := in.Call(context.Background(), "checkout", map[string]Value{
		"orderId": Str("o1"),
		"input":   newCustomer,
		"total":   Int(500),
	})
	if fault != nil {
		t.Fatalf("Call: %v", fault)
	}
	rv := mustResult(t, v)
	if !rv.Ok {
		t.Fatalf("got Err(%s), want Ok", rv.Value)
	}
}

// TestCheckoutWorkflowReinterpretsOutOfStock exercises checkout.fiig's
// other match arm: a bare zero-field enum variant constructed in an
// ordinary expression position (`CheckoutError.OutOfStock`), not as a
// match pattern — a code path TestCheckoutWorkflowNestedMatchAndCatchAllResultBinding
// never reaches, and dormant until now (evalFieldAccess had no case for a
// Target naming an enum type; it tried, and failed, to look "CheckoutError"
// up as a local variable).
func TestCheckoutWorkflowReinterpretsOutOfStock(t *testing.T) {
	u := loadUniverse(t,
		"customer-service/models", "customer-service/errors",
		"customer-service/rules", "customer-service/formulas", "customer-service/activities", "customer-service/gateways", "customer-service/workflows",
		"order-processing/models", "order-processing/errors",
		"order-processing/rules", "order-processing/activities", "order-processing/gateways", "order-processing/workflows")
	db := newFakeDatabase()
	db.executeRowsAffected = 0 // reserveInventory's insert never "succeeds"
	caps := NewCapabilityRegistry()
	caps.Register("Database", db)
	caps.Register("Random", NewSeededRandomProvider(1))
	in := newTestInterp(u, caps)

	newCustomer := Record{TypeName: "NewCustomer", Fields: map[string]Value{
		"name":  Str("Ada"),
		"email": emailAddress("ada@example.com"),
	}}
	v, fault := in.Call(context.Background(), "checkout", map[string]Value{
		"orderId": Str("o1"),
		"input":   newCustomer,
		"total":   Int(500),
	})
	if fault != nil {
		t.Fatalf("Call: %v", fault)
	}
	rv := mustResult(t, v)
	if rv.Ok {
		t.Fatalf("got Ok(%s), want Err(CheckoutError.OutOfStock)", rv.Value)
	}
	ev, ok := rv.Value.(EnumValue)
	if !ok || ev.TypeName != "CheckoutError" || ev.Variant != "OutOfStock" {
		t.Fatalf("got %s, want CheckoutError.OutOfStock", rv.Value)
	}
}

// TestReserveInventoryRowsAffectedContract exercises reserve_inventory.fiig
// against the real `execute` contract (rows affected, an int) rather than
// the boolean the gateway used to assume — 0 rows affected means the
// reservation didn't happen, per OrderError.InventoryUnavailable.
func TestReserveInventoryRowsAffectedContract(t *testing.T) {
	u := loadUniverse(t, "order-processing/models", "order-processing/errors", "order-processing/gateways")
	orderID := Str("o1")

	caps := NewCapabilityRegistry()
	caps.Register("Database", FuncProvider{
		"execute": func(_ context.Context, args []Value) (Value, *Fault) { return Int(1), nil },
	})
	in := newTestInterp(u, caps)
	v, fault := in.Call(context.Background(), "reserveInventory", map[string]Value{"orderId": orderID})
	if fault != nil {
		t.Fatalf("Call: %v", fault)
	}
	if rv := mustResult(t, v); !rv.Ok {
		t.Fatalf("got Err(%s), want Ok for 1 row affected", rv.Value)
	}

	caps = NewCapabilityRegistry()
	caps.Register("Database", FuncProvider{
		"execute": func(_ context.Context, args []Value) (Value, *Fault) { return Int(0), nil },
	})
	in = newTestInterp(u, caps)
	v, fault = in.Call(context.Background(), "reserveInventory", map[string]Value{"orderId": orderID})
	if fault != nil {
		t.Fatalf("Call: %v", fault)
	}
	rv := mustResult(t, v)
	if rv.Ok {
		t.Fatalf("got Ok, want Err for 0 rows affected")
	}
	ev, ok := rv.Value.(EnumValue)
	if !ok || ev.Variant != "InventoryUnavailable" {
		t.Fatalf("got %s, want OrderError.InventoryUnavailable", rv.Value)
	}
}
