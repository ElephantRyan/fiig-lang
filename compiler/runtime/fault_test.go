package runtime

import (
	"context"
	"testing"
	"time"

	"fiig-lang/compiler/checker"
	"fiig-lang/compiler/parser"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// universeFromSource builds a Universe from inline source directly — for
// tests exercising a fault scenario that isn't (and shouldn't be) attested
// anywhere in the real corpus, like division by zero or a non-exhaustive
// match.
func universeFromSource(t *testing.T, srcs ...string) *types.Universe {
	t.Helper()
	files := make([]*syntax.File, len(srcs))
	for i, src := range srcs {
		f, errs := parser.Parse(src)
		if len(errs) > 0 {
			t.Fatalf("parse: %v", errs)
		}
		files[i] = f
	}
	universe, conflicts := types.BuildUniverse(files)
	if len(conflicts) > 0 {
		t.Fatalf("unexpected name conflicts: %+v", conflicts)
	}
	// The checker gives constants their types (ADR 0048 §2.4), as run-cli's
	// check before running does. Its diagnostics are not this helper's
	// business: some tests run deliberately unchecked input.
	checker.Check(files, nil)
	return universe
}

func TestFaultReportsCallStack(t *testing.T) {
	u := loadUniverse(t, "customer-service/models", "customer-service/errors", "customer-service/gateways")
	caps := NewCapabilityRegistry()
	// "Database" is never registered — a real host-configuration defect.
	in := newTestInterp(u, caps)

	_, fault := in.Call(context.Background(), "getCustomer", map[string]Value{"id": customerID("c1")})
	if fault == nil {
		t.Fatal("got nil fault, want one for an unregistered capability")
	}
	if len(fault.Stack) == 0 || fault.Stack[0] != "getCustomer" {
		t.Fatalf("Stack = %v, want it to start with %q", fault.Stack, "getCustomer")
	}
}

func TestDivisionByZeroIsAFault(t *testing.T) {
	u := universeFromSource(t, "rule divide(a: int, b: int) -> int {\n    return a / b;\n}\n")
	in := newTestInterp(u, nil)

	_, fault := in.Call(context.Background(), "divide", map[string]Value{"a": Int(1), "b": Int(0)})
	if fault == nil {
		t.Fatal("got nil fault, want one for division by zero")
	}
}

func TestCancelledContextFaultsImmediately(t *testing.T) {
	u := loadUniverse(t, "customer-service/models", "customer-service/errors", "customer-service/rules", "customer-service/formulas")
	in := newTestInterp(u, nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	customer := Record{TypeName: "Customer", Fields: map[string]Value{
		"id": customerID("c1"), "name": Str("Ada"), "email": emailAddress("ada@example.com"),
	}}
	_, fault := in.Call(ctx, "renameCustomer", map[string]Value{"customer": customer, "newName": Str("Grace")})
	if fault == nil {
		t.Fatal("got nil fault, want one for an already-cancelled context")
	}
}

// TestDeadlineExceededFaultsDuringConcurrentWork: there's no fiig-level
// syntax yet for a workflow to declare its own deadline (unattested
// anywhere in the corpus, so none was invented) — but a deadline set by
// the host on the context.Context passed into Call is honored anywhere
// checkCancelled is checked, including between concurrent.go's worker
// jobs, "for free," since every eval function in this package threads ctx
// through and never calls time.Sleep directly outside in.Clock. This only
// catches a deadline *between* capability calls, not inside one already in
// flight — a FuncProvider has no ctx of its own to select against, the
// same limitation Go's own blocking calls have unless they choose to
// support cancellation themselves.
func TestDeadlineExceededFaultsDuringConcurrentWork(t *testing.T) {
	u := loadUniverse(t, "customer-service/models", "customer-service/errors",
		"customer-service/gateways", "customer-service/activities", "customer-service/workflows")
	caps := NewCapabilityRegistry()
	caps.Register("Database", FuncProvider{
		"queryOne": func(_ context.Context, args []Value) (Value, *Fault) {
			time.Sleep(5 * time.Millisecond)
			return None{}, nil
		},
	})
	in := newTestInterp(u, caps)
	in.DefaultConcurrencyLimit = 2

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
	defer cancel()

	elems := make([]Value, 50)
	for i := range elems {
		elems[i] = customerID("c")
	}
	_, fault := in.Call(ctx, "retrieveCustomers", map[string]Value{"customerIds": List{Elems: elems}})
	if fault == nil {
		t.Fatal("got nil fault, want a deadline-exceeded fault")
	}
}

// TestUnexhaustiveMatchAtRuntimeIsAFault: compiler/checker's A9 guarantees
// this never happens to a checked program, but the interpreter can run
// unchecked syntax directly (as this test does), so it needs its own
// defense — a Fault, not a panic or a silently wrong zero value.
func TestUnexhaustiveMatchAtRuntimeIsAFault(t *testing.T) {
	u := universeFromSource(t, `enum E {
    A(),
    B()
}
`, `rule r(e: E) -> int {
    return match (e) {
        E.A(_) -> 1
    }
}
`)
	in := newTestInterp(u, nil)
	_, fault := in.Call(context.Background(), "r", map[string]Value{
		"e": EnumValue{TypeName: "E", Variant: "B"},
	})
	if fault == nil {
		t.Fatal("got nil fault, want one: no arm matches E.B at runtime")
	}
}
