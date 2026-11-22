package checker

import "testing"

// ADR 0065 §3: sorted(x in xs): key.

const sortedModels = "model Order {\n    id: string;\n    total: decimal;\n    region: string;\n    note: string | None;\n    rush: bool;\n}"

func TestSortedFormCheck(t *testing.T) {
	diags := checkSrc(t, `rule r(orders: List<Order>, sorted: int) -> List<Order> {
    byTotal: List<Order> = sorted(order in orders): order.total;
    newest = sorted(order in byTotal): order.region descending;
    computed = sorted(order in orders): math.abs(order.total) * 2;
    return newest;
}`, sortedModels)
	if len(diags) != 0 {
		t.Fatalf("diags = %v, want none", diags)
	}
	// `sorted` is contextual: a declaration named sorted is still called by
	// its bare name, and parses as a call.
	diags = checkSrc(t, "rule sorted(n: int) -> int {\n    return n;\n}", "activity a(n: int) -> int {\n    return sorted(n);\n}")
	if len(diags) != 0 {
		t.Fatalf("call to a rule named sorted: diags = %v, want none", diags)
	}
}

func TestSortedFormMisuse(t *testing.T) {
	cases := []struct{ src, rule, fragment string }{
		{"rule r(orders: List<Order>) -> List<Order> {\n    return sorted(order in orders): order.note;\n}", "M3", "decide where None goes"},
		{"rule r(orders: List<Order>) -> List<Order> {\n    return sorted(order in orders): order.rush;\n}", "M3", "a sorted key must be int, float, decimal, or string, got bool"},
		{"rule r(order: Order) -> List<Order> {\n    return sorted(o in order): o.total;\n}", "A1", "sorted takes a List, got Order"},
		{"rule r(orders: List<Order>) -> List<Order> {\n    sorted(order in orders): order.total;\n    return orders;\n}", "A13", "the result of sorted(...) is discarded"},
	}
	for _, c := range cases {
		wantDiag(t, checkSrc(t, c.src, sortedModels), c.rule, c.fragment)
	}
	// The key may call only what a rule may, even inside an activity.
	diags := checkSrc(t, `activity a(orders: List<Order>) -> List<Order> {
    return sorted(order in orders): priceOf(order.id);
}`, "gateway priceOf(id: string) -> int\n    requires Database\n{\n    return 1;\n}", sortedModels)
	wantDiag(t, diags, "B5", `a sorted key may not call gateway "priceOf"; it may call what the enclosing declaration may, but nothing with an effect`)
}
