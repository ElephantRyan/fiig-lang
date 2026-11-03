package format

import "testing"

// TestFormatSortedFormIsStable is ADR 0065 §3's keyed sort, with and
// without its modifier, beside the method form and a local named sorted
// (the word is contextual).
func TestFormatSortedFormIsStable(t *testing.T) {
	src := "rule r(orders: List<Order>) -> List<Order> {\n" +
		"    byTotal = sorted(order in orders): order.total\n" +
		"    sorted = byTotal.sorted()\n" +
		"    return sorted(order in sorted, descending): order.region\n" +
		"}\n"
	if out := mustFormat(t, src); out != src {
		t.Fatalf("got:\n%s\nwant:\n%s", out, src)
	}
}

// TestFormatMapSyntaxIsStable is ADR 0065 §5 and §7: map literals, the
// empty map, and a two-name for with a discarded key.
func TestFormatMapSyntaxIsStable(t *testing.T) {
	src := "rule r(m: Map<string, int>) -> Map<string, int> {\n" +
		"    limits = [\"small\": 10, \"large\": 500]\n" +
		"    seen: Map<string, bool> = [:]\n" +
		"    total: int = 0\n" +
		"    for _, value in m {\n" +
		"        total = total + value\n" +
		"    }\n" +
		"    return limits\n" +
		"}\n"
	if out := mustFormat(t, src); out != src {
		t.Fatalf("got:\n%s\nwant:\n%s", out, src)
	}
}
