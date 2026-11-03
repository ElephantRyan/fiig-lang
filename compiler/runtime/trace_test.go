package runtime

import (
	"context"
	"testing"
)

func spanByName(spans []Span, name string) (Span, bool) {
	for _, s := range spans {
		if s.Name == name {
			return s, true
		}
	}
	return Span{}, false
}

func TestTracingRecordsNestedCallSpans(t *testing.T) {
	// createCustomer (workflow) -> registerCustomer (activity) ->
	// validateNewCustomer (activity), checkEmailUnique (gateway),
	// insertCustomer (gateway) — a real 3-level call tree from the actual
	// corpus, not a synthetic one.
	u := loadUniverse(t,
		"customer-service/models", "customer-service/errors",
		"customer-service/rules", "customer-service/formulas", "customer-service/activities", "customer-service/gateways", "customer-service/workflows")
	db := newFakeDatabase()
	caps := NewCapabilityRegistry()
	caps.Register("Database", db)
	in := newTestInterp(u, caps)
	rec := &Recorder{}
	in.Tracer = rec

	newCustomer := Record{TypeName: "NewCustomer", Fields: map[string]Value{
		"name": Str("Ada"), "email": emailAddress("ada@example.com"),
	}}
	_, fault := in.Call(context.Background(), "createCustomer", map[string]Value{"input": newCustomer})
	if fault != nil {
		t.Fatalf("Call: %v", fault)
	}

	spans := rec.Spans()
	top, ok := spanByName(spans, "createCustomer")
	if !ok {
		t.Fatalf("no span for createCustomer; got %+v", spans)
	}
	if top.Kind != SpanWorkflow || top.ParentID != 0 {
		t.Fatalf("createCustomer span = %+v, want a root workflow span", top)
	}
	if top.Outcome != OutcomeOk {
		t.Fatalf("createCustomer outcome = %s, want ok", top.Outcome)
	}

	register, ok := spanByName(spans, "registerCustomer")
	if !ok {
		t.Fatalf("no span for registerCustomer; got %+v", spans)
	}
	if register.Kind != SpanActivity || register.ParentID != top.ID {
		t.Fatalf("registerCustomer span = %+v, want an activity span under createCustomer (%d)", register, top.ID)
	}

	for _, name := range []string{"validateNewCustomer", "checkEmailUnique", "insertCustomer"} {
		child, ok := spanByName(spans, name)
		if !ok {
			t.Fatalf("no span for %s; got %+v", name, spans)
		}
		if child.ParentID != register.ID {
			t.Fatalf("%s.ParentID = %d, want %d (registerCustomer's span)", name, child.ParentID, register.ID)
		}
		if !child.End.After(child.Start) && !child.End.Equal(child.Start) {
			t.Fatalf("%s span has no valid duration: %+v", name, child)
		}
	}

	validate, _ := spanByName(spans, "validateNewCustomer")
	if validate.Kind != SpanActivity {
		t.Fatalf("validateNewCustomer.Kind = %s, want activity", validate.Kind)
	}
	checkEmail, _ := spanByName(spans, "checkEmailUnique")
	if checkEmail.Kind != SpanGateway {
		t.Fatalf("checkEmailUnique.Kind = %s, want gateway", checkEmail.Kind)
	}
}

func TestTracingRecordsErrAndFaultOutcomes(t *testing.T) {
	u := loadUniverse(t, "customer-service/models", "customer-service/errors", "customer-service/gateways")
	db := newFakeDatabase()
	caps := NewCapabilityRegistry()
	caps.Register("Database", db)
	in := newTestInterp(u, caps)
	rec := &Recorder{}
	in.Tracer = rec

	// A typed Err — not a Fault.
	_, fault := in.Call(context.Background(), "getCustomer", map[string]Value{"id": customerID("missing")})
	if fault != nil {
		t.Fatalf("Call: %v", fault)
	}
	span, ok := spanByName(rec.Spans(), "getCustomer")
	if !ok {
		t.Fatal("no span for getCustomer")
	}
	if span.Outcome != OutcomeErr {
		t.Fatalf("Outcome = %s, want err (a typed CustomerError.NotFound)", span.Outcome)
	}
	if span.Detail == "" {
		t.Fatal("Detail is empty, want the Err value's rendering")
	}

	// A genuine Fault — no capability registered for a different name
	// wouldn't apply here, so use an interp with no Database registered
	// instead to force one.
	rec2 := &Recorder{}
	in2 := newTestInterp(u, NewCapabilityRegistry())
	in2.Tracer = rec2
	_, fault = in2.Call(context.Background(), "getCustomer", map[string]Value{"id": customerID("c1")})
	if fault == nil {
		t.Fatal("got nil fault, want one for an unregistered capability")
	}
	span2, ok := spanByName(rec2.Spans(), "getCustomer")
	if !ok {
		t.Fatal("no span for getCustomer")
	}
	if span2.Outcome != OutcomeFault {
		t.Fatalf("Outcome = %s, want fault", span2.Outcome)
	}
}

func TestTracingConcurrentBlockSpanParentsChildren(t *testing.T) {
	u := loadUniverse(t, "customer-service/models", "customer-service/errors",
		"customer-service/gateways", "customer-service/workflows")
	db := newFakeDatabase()
	db.insert("c1", "Ada", "ada@example.com")
	db.insert("c2", "Grace", "grace@example.com")
	caps := NewCapabilityRegistry()
	caps.Register("Database", db)
	in := newTestInterp(u, caps)
	rec := &Recorder{}
	in.Tracer = rec

	ids := List{Elems: []Value{customerID("c1"), customerID("c2")}}
	_, fault := in.Call(context.Background(), "retrieveCustomers", map[string]Value{"customerIds": ids})
	if fault != nil {
		t.Fatalf("Call: %v", fault)
	}

	spans := rec.Spans()
	top, ok := spanByName(spans, "retrieveCustomers")
	if !ok {
		t.Fatal("no span for retrieveCustomers")
	}
	concurrentSpan, ok := spanByName(spans, "concurrent(customerId)")
	if !ok {
		t.Fatalf("no span for the concurrent block; got %+v", spans)
	}
	if concurrentSpan.Kind != SpanConcurrent || concurrentSpan.ParentID != top.ID {
		t.Fatalf("concurrent span = %+v, want Kind=concurrent, ParentID=%d", concurrentSpan, top.ID)
	}

	getCustomerCount := 0
	for _, s := range spans {
		if s.Name == "getCustomer" {
			getCustomerCount++
			if s.ParentID != concurrentSpan.ID {
				t.Fatalf("getCustomer span %+v has ParentID %d, want the concurrent block's %d", s, s.ParentID, concurrentSpan.ID)
			}
		}
	}
	if getCustomerCount != 2 {
		t.Fatalf("got %d getCustomer spans, want 2 (one per concurrent item)", getCustomerCount)
	}
}

func TestNoTracerMeansNoOverheadPath(t *testing.T) {
	// Tracer left nil (the default from New/newTestInterp) — this is
	// mostly a documentation-by-test that the zero value works with no
	// setup, not a performance assertion.
	u := loadUniverse(t, "customer-service/models", "customer-service/rules", "customer-service/formulas")
	in := newTestInterp(u, nil)
	if in.Tracer != nil {
		t.Fatal("expected a nil Tracer by default")
	}
	customer := Record{TypeName: "Customer", Fields: map[string]Value{
		"id": customerID("c1"), "name": Str("Ada"), "email": emailAddress("ada@example.com"),
	}}
	if _, fault := in.Call(context.Background(), "renameCustomer", map[string]Value{"customer": customer, "newName": Str("Grace")}); fault != nil {
		t.Fatalf("Call: %v", fault)
	}
}
