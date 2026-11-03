package checker

import "testing"

// ADR 0065 §1-§2 and §6's A13 widening in the checker.

func TestListQueriesCheck(t *testing.T) {
	diags := checkSrc(t, `rule r(xs: List<int>, ds: List<decimal>, fs: List<float>, names: List<string>) -> bool {
    head: int = xs.first() + xs.last()
    maybe: int | None = xs.firstOrNone()
    other: decimal | None = ds.lastOrNone()
    up: List<decimal> = ds.sorted()
    down: List<float> = fs.sortedDescending()
    byName: List<string> = names.sorted()
    return xs.contains(3) and ds.contains(1.5) and math.containsClose(fs, 0.3, relative: 1e-9, absolute: 0.0) and math.allClose(fs, down, relative: 1e-9, absolute: 0.0)
}`)
	if len(diags) != 0 {
		t.Fatalf("diags = %v, want none", diags)
	}
}

func TestListQueryMisuse(t *testing.T) {
	cases := []struct{ src, rule, fragment string }{
		// §1: contains needs ==, so not floats, Results, or handles.
		{"rule r(fs: List<float>) -> bool {\n    return fs.contains(0.3)\n}", "M1", "use math.containsClose"},
		{"rule r(rs: List<Result<int, string>>, x: Result<int, string>) -> bool {\n    return rs.contains(x)\n}", "M1", "holds a Result"},
		// §2: sorting needs an ordered element.
		{"rule r(bs: List<bool>) -> List<bool> {\n    return bs.sorted()\n}", "M3", "List<bool>.sorted needs an ordered element type"},
		{"rule r(xs: List<int>) -> int {\n    return xs.first(0)\n}", "", "List.first takes 0 argument(s), got 1"},
		{"rule r(xs: List<int>) -> bool {\n    return xs.contains(\"a\")\n}", "A1", ""},
		// The float counterparts' tolerances are named (ADR 0051).
		{"rule r(fs: List<float>) -> bool {\n    return math.containsClose(fs, 0.3, 1e-9, absolute: 0.0)\n}", "", `"relative" must be passed by name`},
		// == on a float list names its counterpart.
		{"rule r(a: List<float>, b: List<float>) -> bool {\n    return a == b\n}", "M1", "use math.allClose"},
	}
	for _, c := range cases {
		wantDiag(t, checkSrc(t, c.src), c.rule, c.fragment)
	}
	// A float a field below the element: no counterpart reaches inside a
	// model (open question 3), so the message points to a loop.
	wantDiag(t, checkSrc(t, "rule r(rs: List<Reading>, x: Reading) -> bool {\n    return rs.contains(x)\n}", "model Reading {\n    value: float\n}"), "M1", "List<Reading>.contains compares elements with ==, and Reading.value is float")
}

// TestDiscardedMethodResultIsA13 is §6: every closed-table method is pure,
// so a bare call does nothing.
func TestDiscardedMethodResultIsA13(t *testing.T) {
	for _, stmt := range []string{"xs.sorted()", "xs.first()", "name.isBlank()", "name.utf8()"} {
		src := "rule r(xs: List<int>, name: string) -> int {\n    " + stmt + "\n    return 0\n}"
		wantDiag(t, checkSrc(t, src), "A13", "methods return a new value and change nothing")
	}
}
