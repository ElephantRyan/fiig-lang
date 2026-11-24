package runtime

import (
	"context"
	"testing"
	"time"

	"fiig-lang/compiler/syntax"
)

// ADR 0149 §2: the effective limit is the block's own limit, or the
// default, capped by the application maximum.
func TestTheApplicationMaximumCapsEveryBlock(t *testing.T) {
	bare := &syntax.ConcurrentExpr{}
	limited := func(n string) *syntax.ConcurrentExpr {
		return &syntax.ConcurrentExpr{Limit: &syntax.LimitValue{Value: &syntax.IntLit{Text: n}}}
	}
	for _, c := range []struct {
		name         string
		def, maximum int
		block        *syntax.ConcurrentExpr
		want         int
	}{
		{"bare, no maximum", 16, 0, bare, 16},
		{"bare, the application's default", 4, 0, bare, 4},
		{"bare, default above maximum", 16, 8, bare, 8},
		{"own limit, no maximum", 16, 0, limited("32"), 32},
		{"own limit under maximum", 16, 8, limited("3"), 3},
		{"own limit over maximum", 16, 8, limited("32"), 8},
	} {
		in := New(nil, NewCapabilityRegistry())
		in.DefaultConcurrencyLimit, in.MaxConcurrencyLimit = c.def, c.maximum
		got, fault := in.resolveConcurrencyLimit(context.Background(), c.block, nil)
		if fault != nil || got != c.want {
			t.Errorf("%s: limit = %d, %v; want %d", c.name, got, fault, c.want)
		}
	}
}

// The cap holds while a block runs: a block asking for 20 at once under a
// maximum of 3 never has more than 3 bodies in flight.
func TestABlockRunsNoMoreThanTheMaximumAtOnce(t *testing.T) {
	u := loadUniverse(t, "customer-service/models", "customer-service/errors",
		"customer-service/gateways", "customer-service/activities", "customer-service/workflows")
	const n, maximum = 40, 3
	var active, maxActive counter
	caps := NewCapabilityRegistry()
	caps.Register("Database", FuncProvider{
		"queryOne": func(_ context.Context, args []Value) (Value, *Fault) {
			maxActive.max(active.inc())
			defer active.dec()
			time.Sleep(time.Millisecond)
			return None{}, nil
		},
	})
	in := newTestInterp(u, caps)
	in.DefaultConcurrencyLimit, in.MaxConcurrencyLimit = n, maximum
	elems := make([]Value, n)
	for i := range elems {
		elems[i] = customerID("c")
	}
	if _, fault := in.Call(context.Background(), "retrieveCustomers", map[string]Value{"customerIds": List{Elems: elems}}); fault != nil {
		t.Fatalf("Call: %v", fault)
	}
	if got := maxActive.get(); got > maximum || got == 0 {
		t.Fatalf("most bodies in flight = %d, want 1..%d", got, maximum)
	}
}
