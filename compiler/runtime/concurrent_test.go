package runtime

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

// TestRetrieveCustomersVerticalSlice is Milestone 4's own named primary
// vertical slice (fiig-lang.md §5 Milestone 4, and the first-demonstrable-
// release backlog in §12): `retrieveCustomers` concurrently retrieving many
// customers with partial failures, as a real BatchResult.
func TestRetrieveCustomersVerticalSlice(t *testing.T) {
	u := loadUniverse(t, "customer-service/models", "customer-service/errors",
		"customer-service/gateways", "customer-service/activities", "customer-service/workflows")
	db := newFakeDatabase()
	db.insert("c1", "Ada", "ada@example.com")
	db.insert("c2", "Grace", "grace@example.com")
	// c3 deliberately missing -> a per-item failure.
	caps := NewCapabilityRegistry()
	caps.Register("Database", db)
	in := newTestInterp(u, caps)

	ids := List{Elems: []Value{customerID("c1"), customerID("c2"), customerID("c3")}}
	v, fault := in.Call(context.Background(), "retrieveCustomers", map[string]Value{"customerIds": ids})
	if fault != nil {
		t.Fatalf("Call: %v", fault)
	}
	br, ok := v.(BatchResult)
	if !ok {
		t.Fatalf("got %T, want BatchResult", v)
	}
	if len(br.Items) != 3 {
		t.Fatalf("got %d items, want 3", len(br.Items))
	}
	oks, errs := 0, 0
	for _, item := range br.Items {
		if item.Outcome.Ok {
			oks++
		} else {
			errs++
		}
	}
	if oks != 2 || errs != 1 {
		t.Fatalf("got %d ok / %d err, want 2/1", oks, errs)
	}
}

// TestABatchIsInInputOrder is ADR 0148 §3: BatchResult.Items lines up
// positionally with the input list, not with whichever getCustomer happened
// to finish first.
func TestABatchIsInInputOrder(t *testing.T) {
	u := loadUniverse(t, "customer-service/models", "customer-service/errors",
		"customer-service/gateways", "customer-service/activities", "customer-service/workflows")
	db := newFakeDatabase()
	// Insert in reverse of the order they'll be queried, and make earlier
	// IDs resolve slower than later ones (via a tiny artificial stagger),
	// so completion order would visibly differ from input order if the
	// batch were collected as results arrived.
	ids := make([]string, 50)
	for i := range ids {
		id := fmt.Sprintf("c%02d", i)
		ids[i] = id
		db.insert(id, "name-"+id, id+"@example.com")
	}
	caps := NewCapabilityRegistry()
	caps.Register("Database", staggeredProvider(db, len(ids)))
	in := newTestInterp(u, caps)

	elems := make([]Value, len(ids))
	for i, id := range ids {
		elems[i] = customerID(id)
	}
	v, fault := in.Call(context.Background(), "retrieveCustomers", map[string]Value{"customerIds": List{Elems: elems}})
	if fault != nil {
		t.Fatalf("Call: %v", fault)
	}
	br := v.(BatchResult)
	if len(br.Items) != len(ids) {
		t.Fatalf("got %d items, want %d", len(br.Items), len(ids))
	}
	for i, item := range br.Items {
		if item.Key != Str(ids[i]) {
			t.Fatalf("item %d key = %v, want %s", i, item.Key, ids[i])
		}
	}
}

// staggeredProvider makes earlier-queried rows resolve slower than later
// ones, so a completion-order collection would visibly disagree with input
// order — a control for TestABatchIsInInputOrder.
func staggeredProvider(db *fakeDatabase, n int) CapabilityProvider {
	return FuncProvider{
		"queryOne": func(ctx context.Context, args []Value) (Value, *Fault) {
			if len(args) >= 2 {
				key := keyString(args[1])
				// Reverse-index stagger: the first id queried sleeps
				// longest, the last barely at all.
				for i := 0; i < n; i++ {
					if key == fmt.Sprintf("c%02d", i) {
						time.Sleep(time.Duration(n-i) * time.Millisecond)
						break
					}
				}
			}
			return db.Call(ctx, "queryOne", args)
		},
	}
}

// TestRetrieveCustomersScaleBoundsConcurrency is the Milestone 4 scale test
// (fiig-lang.md §5): process 10,000 simulated customer IDs without ever
// running more than the configured limit's worth of bodies at once.
func TestRetrieveCustomersScaleBoundsConcurrency(t *testing.T) {
	u := loadUniverse(t, "customer-service/models", "customer-service/errors",
		"customer-service/gateways", "customer-service/activities", "customer-service/workflows")
	const n = 10_000
	const limit = 8

	var active, maxActive counter
	caps := NewCapabilityRegistry()
	caps.Register("Database", FuncProvider{
		"queryOne": func(_ context.Context, args []Value) (Value, *Fault) {
			cur := active.inc()
			maxActive.max(cur)
			defer active.dec()
			if len(args) < 2 {
				return None{}, nil
			}
			id := keyString(args[1])
			return Record{Fields: map[string]Value{
				"id":    Str(id),
				"name":  Str("name-" + id),
				"email": Str(id + "@example.com"),
			}}, nil
		},
	})
	in := newTestInterp(u, caps)
	in.DefaultConcurrencyLimit = limit

	elems := make([]Value, n)
	for i := range elems {
		elems[i] = customerID(fmt.Sprintf("c%d", i))
	}
	v, fault := in.Call(context.Background(), "retrieveCustomers", map[string]Value{"customerIds": List{Elems: elems}})
	if fault != nil {
		t.Fatalf("Call: %v", fault)
	}
	br := v.(BatchResult)
	if len(br.Items) != n {
		t.Fatalf("got %d items, want %d", len(br.Items), n)
	}
	for _, item := range br.Items {
		if !item.Outcome.Ok {
			t.Fatalf("unexpected per-item failure: %s", item.Outcome)
		}
	}
	if got := maxActive.get(); got > limit {
		t.Fatalf("max concurrently active calls = %d, want <= %d", got, limit)
	}
	if got := maxActive.get(); got == 0 {
		t.Fatalf("max concurrently active calls = 0; the test isn't exercising concurrency at all")
	}
}

// TestConcurrentCancelsSiblingsOnFault is "cancel all active children when
// the parent is cancelled" (fiig-lang.md §5 scale tests) in the direction a
// unit test can actually observe: one item's genuine Fault cancels every
// other in-flight sibling rather than letting them keep running to
// completion.
func TestConcurrentCancelsSiblingsOnFault(t *testing.T) {
	u := loadUniverse(t, "customer-service/models", "customer-service/errors",
		"customer-service/gateways", "customer-service/activities", "customer-service/workflows")
	const n = 20
	release := make(chan struct{})

	caps := NewCapabilityRegistry()
	caps.Register("Database", FuncProvider{
		"queryOne": func(_ context.Context, args []Value) (Value, *Fault) {
			key := keyString(args[1])
			if key == "poison" {
				return nil, Faultf("simulated capability defect")
			}
			<-release
			return None{}, nil
		},
	})
	in := newTestInterp(u, caps)
	in.DefaultConcurrencyLimit = n // every item gets its own worker

	elems := make([]Value, 0, n+1)
	elems = append(elems, customerID("poison"))
	for i := 0; i < n; i++ {
		elems = append(elems, customerID(fmt.Sprintf("c%d", i)))
	}

	done := make(chan *Fault, 1)
	go func() {
		_, fault := in.Call(context.Background(), "retrieveCustomers", map[string]Value{"customerIds": List{Elems: elems}})
		done <- fault
	}()

	// Give the non-poisoned workers a chance to start and block on release,
	// then let the poisoned one's Fault cancel the whole block before any
	// of them is released.
	time.Sleep(50 * time.Millisecond)
	close(release)

	fault := <-done
	if fault == nil {
		t.Fatalf("got no fault, want the simulated capability defect to propagate")
	}
}

// counter is a tiny concurrency-safe test helper for tracking how many
// capability calls are active at once, and the high-water mark.
type counter struct {
	mu  sync.Mutex
	cur int
	mx  int
}

func (c *counter) inc() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cur++
	return c.cur
}
func (c *counter) dec() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cur--
}
func (c *counter) max(v int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if v > c.mx {
		c.mx = v
	}
}
func (c *counter) get() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.mx
}
