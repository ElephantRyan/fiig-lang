package runtime

import (
	"context"
	"testing"
)

// TestReadCustomerWithRetrySucceedsAfterFailures exercises
// read_customer_with_retry.fiig (`return retry(attempts: 3): getCustomer(id)`)
// against a capability that fails the first two attempts and succeeds on
// the third — using NewDeterministic so the backoff between attempts never
// actually sleeps, keeping the test instant and reproducible.
func TestReadCustomerWithRetrySucceedsAfterFailures(t *testing.T) {
	u := loadUniverse(t, "customer-service/models", "customer-service/errors",
		"customer-service/gateways", "customer-service/workflows")
	var calls int
	caps := NewCapabilityRegistry()
	caps.Register("Database", FuncProvider{
		"queryOne": func(_ context.Context, args []Value) (Value, *Fault) {
			calls++
			if calls < 3 {
				return None{}, nil // getCustomer turns this into Err(NotFound)
			}
			return Record{Fields: map[string]Value{
				"id": Str("c1"), "name": Str("Ada"), "email": Str("ada@example.com"),
			}}, nil
		},
	})
	in := NewDeterministic(u, caps, 1)

	v, fault := in.Call(context.Background(), "readCustomerWithRetry", map[string]Value{"id": customerID("c1")})
	if fault != nil {
		t.Fatalf("Call: %v", fault)
	}
	rv := mustResult(t, v)
	if !rv.Ok {
		t.Fatalf("got Err(%s) after retrying, want Ok", rv.Value)
	}
	if calls != 3 {
		t.Fatalf("got %d attempts, want 3", calls)
	}

	clock := in.Clock.(*ManualClock)
	sleeps := clock.Sleeps()
	if len(sleeps) != 2 {
		t.Fatalf("got %d recorded backoff sleeps, want 2 (one between each of the 2 failed attempts)", len(sleeps))
	}
}

// TestRetryReturnsLastErrorAfterExhaustingAttempts covers the other half:
// every attempt fails, and the retry's own final value is the last Err,
// not a Fault — a typed error that was never actually recovered is still
// an ordinary Result, not a runtime defect.
func TestRetryReturnsLastErrorAfterExhaustingAttempts(t *testing.T) {
	u := loadUniverse(t, "customer-service/models", "customer-service/errors",
		"customer-service/gateways", "customer-service/workflows")
	caps := NewCapabilityRegistry()
	caps.Register("Database", FuncProvider{
		"queryOne": func(_ context.Context, args []Value) (Value, *Fault) {
			return None{}, nil
		},
	})
	in := NewDeterministic(u, caps, 1)

	v, fault := in.Call(context.Background(), "readCustomerWithRetry", map[string]Value{"id": customerID("missing")})
	if fault != nil {
		t.Fatalf("Call: %v", fault)
	}
	rv := mustResult(t, v)
	if rv.Ok {
		t.Fatalf("got Ok, want Err after exhausting every attempt")
	}
	ev, ok := rv.Value.(EnumValue)
	if !ok || ev.Variant != "NotFound" {
		t.Fatalf("got %s, want CustomerError.NotFound", rv.Value)
	}

	clock := in.Clock.(*ManualClock)
	if len(clock.Sleeps()) != 2 {
		t.Fatalf("got %d backoff sleeps, want 2 (between attempts 1-2 and 2-3, none after the last)", len(clock.Sleeps()))
	}
}
