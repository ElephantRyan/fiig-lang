package checker

import "testing"

// ADR 0129: Level.values() for an enum whose variants carry no data.
func TestEnumValuesIsForAnEnumWithoutPayloads(t *testing.T) {
	plain := "enum Level {\n    Low,\n    High\n}"
	if diags := checkSrc(t, plain, "rule all() -> List<Level> {\n    return Level.values();\n}"); len(diags) != 0 {
		t.Fatalf("diags = %v", diags)
	}
	payload := "enum Failure {\n    Missing,\n    Late(days: int)\n}"
	wantDiag(t, checkSrc(t, payload, "rule all() -> List<Failure> {\n    return Failure.values();\n}"), "", "lists variants that carry no data")
	wantDiag(t, checkSrc(t, plain, "rule all() -> int {\n    return Level.names();\n}"), "", `Level has no variant "names"`)
}

// ADR 0129: the new string and list methods type-check, and distinct and
// indexOfOrNone hold their elements to == as contains does.
func TestTheMethodBatchTypeChecks(t *testing.T) {
	src := `rule tidy(words: List<string>, n: int) -> string {
    short = words.distinct().reverse().take(3).drop(1);
    where = words.indexOfOrNone("a");
    both = words.concat(["x", "y"]).distinct();
    line = "-".repeat(n);
    return "${n}".padLeft(width: 5, fill: "0").padRight(7, "-");
}`
	if diags := checkSrc(t, src); len(diags) != 0 {
		t.Fatalf("diags = %v", diags)
	}
	wantDiag(t, checkSrc(t, "rule r(xs: List<float>) -> List<float> {\n    return xs.distinct();\n}"), "M1", "List<float>.distinct would compare floats with ==")
	wantDiag(t, checkSrc(t, "rule r(xs: List<float>) -> int | None {\n    return xs.indexOfOrNone(1.5);\n}"), "M1", "List<float>.indexOfOrNone would compare floats with ==")
	wantDiag(t, checkSrc(t, "rule r(s: string) -> string {\n    return s.padLeft(3);\n}"), "", "")
}

// ADR 0129 §2a: concat takes a list of the same element type.
func TestConcatTakesTheSameElementType(t *testing.T) {
	diags := checkSrc(t, "rule r(xs: List<string>, ns: List<int>) -> List<string> {\n    return xs.concat(ns);\n}")
	if len(diags) == 0 {
		t.Fatal("concat of List<string> and List<int> checked")
	}
}
