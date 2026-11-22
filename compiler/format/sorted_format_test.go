package format

import "testing"

// TestFormatSortedFormIsStable is ADR 0065 §3's keyed sort, with and
// without its modifier, beside the method form and a local named sorted
// (the word is contextual).
func TestFormatSortedFormIsStable(t *testing.T) {
	src := "rule r(orders: List<Order>) -> List<Order> {\n" +
		"    byTotal = sorted(order in orders): order.total;\n" +
		"    sorted = byTotal.sorted();\n" +
		"    return sorted(order in sorted): order.region descending then order.total;\n" +
		"}\n"
	if out := mustFormat(t, src); out != src {
		t.Fatalf("got:\n%s\nwant:\n%s", out, src)
	}
}

// TestFormatRewritesHeaderDescending is ADR 0093 §4's migration: the old
// header modifier is printed on the key.
func TestFormatRewritesHeaderDescending(t *testing.T) {
	src := "rule r(orders: List<Order>) -> List<Order> {\n" +
		"    return sorted(order in orders, descending): order.total;\n" +
		"}\n"
	want := "rule r(orders: List<Order>) -> List<Order> {\n" +
		"    return sorted(order in orders): order.total descending;\n" +
		"}\n"
	if out := mustFormat(t, src); out != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out, want)
	}
}

// TestFormatListFormsAreStable is ADR 0093 §1's forms, nested and inside
// call arguments, beside calls and locals that share their names.
func TestFormatListFormsAreStable(t *testing.T) {
	src := "rule r(ratings: List<Rating>) -> int {\n" +
		"    recommended = filter(r in ratings): r.recommended;\n" +
		"    groups = groupBy(r in recommended): r.itemId;\n" +
		"    ids = map(r in ratings): r.itemId;\n" +
		"    count = count(r in ratings): r.score > 3;\n" +
		"    max = math.max(sum(r in ratings): r.score, max(r in ratings): r.updatedAt);\n" +
		"    return minOrNone(r in filter(r in ratings): any(x in ratings): x.score == r.score): r.score;\n" +
		"}\n"
	if out := mustFormat(t, src); out != src {
		t.Fatalf("got:\n%s\nwant:\n%s", out, src)
	}
}

// TestFormatMapSyntaxIsStable is ADR 0065 §5 and §7: map literals, the
// empty map, and a two-name for with a discarded key.
func TestFormatMapSyntaxIsStable(t *testing.T) {
	src := "rule r(m: Map<string, int>) -> Map<string, int> {\n" +
		"    limits = [\"small\": 10, \"large\": 500];\n" +
		"    seen: Map<string, bool> = [:];\n" +
		"    total: int = 0;\n" +
		"    for (_, value in m) {\n" +
		"        total = total + value;\n" +
		"    }\n" +
		"\n" +
		"    return limits;\n" +
		"}\n"
	if out := mustFormat(t, src); out != src {
		t.Fatalf("got:\n%s\nwant:\n%s", out, src)
	}
}
