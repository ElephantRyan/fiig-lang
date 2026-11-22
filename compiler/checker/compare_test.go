package checker

import "testing"

// TestComparisonOperandsAreM2AndM3 covers ADR 0052 §2.3 and §2.4: == needs
// operands of one type, and ordering needs int or string on both sides.
func TestComparisonOperandsAreM2AndM3(t *testing.T) {
	point := "model Point {\n\tx: int;\n\ty: int;\n}"
	region := "enum Region {\n\tUS,\n\tEU,\n}"
	cases := []struct {
		name, src, rule string
	}{
		{"int == string", "rule f(a: int, b: string) -> bool {\n\treturn a == b;\n}", "M2"},
		{"string != bool", "rule f(a: string, b: bool) -> bool {\n\treturn a != b;\n}", "M2"},
		{"int < string", "rule f(a: int, b: string) -> bool {\n\treturn a < b;\n}", "M2"},
		{"model ordered", "rule f(a: Point, b: Point) -> bool {\n\treturn a < b;\n}", "M3"},
		{"bool ordered", "rule f(a: bool, b: bool) -> bool {\n\treturn a >= b;\n}", "M3"},
		{"enum ordered", "rule f(a: Region) -> bool {\n\treturn a > Region.US;\n}", "M3"},
		{"model ==", "rule f(a: Point, b: Point) -> bool {\n\treturn a == b;\n}", ""},
		{"enum variant", "rule f(a: Region) -> bool {\n\treturn a == Region.EU;\n}", ""},
		{"optional vs None", "rule f(a: string | None) -> bool {\n\treturn a == None;\n}", ""},
		{"optional vs member", "rule f(a: string | None, b: string) -> bool {\n\treturn a != b;\n}", ""},
		{"string <=", "rule f(a: string, b: string) -> bool {\n\treturn a <= b;\n}", ""},
		{"int literal", "rule f(a: int) -> bool {\n\treturn a > 0;\n}", ""},
	}
	for _, tc := range cases {
		got := rules(checkSources(t, point, region, tc.src))
		total := 0
		for _, n := range got {
			total += n
		}
		if tc.rule == "" {
			if total != 0 {
				t.Errorf("%s: want no diagnostics, got %v", tc.name, got)
			}
			continue
		}
		if got[tc.rule] != 1 || total != 1 {
			t.Errorf("%s: want exactly one %s, got %v", tc.name, tc.rule, got)
		}
	}
}

func TestBytesAreComparableButNotOrdered(t *testing.T) {
	eq := rules(checkSources(t, "rule f(a: bytes, b: bytes) -> bool {\n\treturn a == b;\n}"))
	if len(eq) != 0 {
		t.Errorf("bytes ==: want no diagnostics, got %v", eq)
	}
	lt := rules(checkSources(t, "rule f(a: bytes, b: bytes) -> bool {\n\treturn a < b;\n}"))
	if lt["M3"] != 1 {
		t.Errorf("bytes <: want one M3, got %v", lt)
	}
}

// TestHandlesAreNotComparableM1: a Connection at any depth, and a capability
// value, compare by identity only, so == on them is M1.
func TestHandlesAreNotComparableM1(t *testing.T) {
	session := "model Session {\n\tconnection: Connection;\n\tuser: string;\n}"
	nested := "activity same(a: Session, b: Session) -> bool {\n\treturn a == b;\n}"
	if got := rules(checkSources(t, session, nested)); got["M1"] != 1 {
		t.Errorf("Connection inside a model: want one M1, got %v", got)
	}
	provider := "activity same(merchant: Merchant) -> bool {\n\tProvider = choosePaymentProvider(merchant);\n\treturn Provider == Stripe;\n}"
	if got := rules(checkShop(t, provider)); got["M1"] != 1 {
		t.Errorf("provider == group: want one M1, got %v", got)
	}
}

// TestResultsAreNotComparableM1: a Result is an outcome to match, not data to
// inspect, so == on one is M1 at any depth (ADR 0052, 2026-10-27).
func TestResultsAreNotComparableM1(t *testing.T) {
	direct := "rule f(a: Result<int, string>, b: Result<int, string>) -> bool {\n\treturn a == b;\n}"
	if got := rules(checkSources(t, direct)); got["M1"] != 1 {
		t.Errorf("Result == Result: want one M1, got %v", got)
	}
	listed := "rule f(a: List<Result<int, string>>, b: List<Result<int, string>>) -> bool {\n\treturn a != b;\n}"
	if got := rules(checkSources(t, listed)); got["M1"] != 1 {
		t.Errorf("List<Result> !=: want one M1, got %v", got)
	}
	attempt := "model Attempt {\n\toutcome: Result<int, string>;\n}"
	nested := "rule f(a: Attempt, b: Attempt) -> bool {\n\treturn a == b;\n}"
	if got := rules(checkSources(t, attempt, nested)); got["M1"] != 1 {
		t.Errorf("Result inside a model: want one M1, got %v", got)
	}
}
