package checker

import "testing"

// ADR 0130: literal patterns.

func TestLiteralPatternsCheck(t *testing.T) {
	cases := []struct {
		name string
		srcs []string
	}{
		{"int with a catch-all", []string{"rule r(code: int) -> string {\n    return match (code) {\n        200 -> \"ok\",\n        404 -> \"missing\",\n        -1 -> \"unset\",\n        0x10 -> \"hex\",\n        _ -> \"other\"\n    }\n}"}},
		{"a binding catches the rest", []string{"rule r(code: int) -> int {\n    return match (code) {\n        200 -> 1,\n        other -> other\n    }\n}"}},
		{"string", []string{"rule r(name: string) -> int {\n    return match (name) {\n        \"ACME\" -> 1,\n        \"Globex\" -> 2,\n        _ -> 0\n    }\n}"}},
		{"bool, both arms", []string{"rule r(flag: bool) -> int {\n    return match (flag) {\n        true -> 1,\n        false -> 2\n    }\n}"}},
		{"optional string", []string{"rule r(name: string | None) -> int {\n    return match (name) {\n        \"ACME\" -> 1,\n        None -> 0,\n        other -> 2\n    }\n}"}},
		{"inside a constructor", []string{
			"enum Failure {\n    Rejected(status: int),\n    Unknown\n}",
			"rule r(x: Result<int, Failure>) -> int {\n    return match (x) {\n        Err(Failure.Rejected(500)) -> 1,\n        Err(Failure.Rejected(_)) -> 2,\n        Err(Failure.Unknown) -> 3,\n        Ok(_) -> 0\n    }\n}",
		}},
	}
	for _, c := range cases {
		if diags := checkSrc(t, c.srcs...); len(diags) != 0 {
			t.Errorf("%s: diags = %v", c.name, diags)
		}
	}
}

func TestLiteralPatternMistakes(t *testing.T) {
	cases := []struct{ name, src, rule, fragment string }{
		{"int with no catch-all", "rule r(code: int) -> int {\n    return match (code) {\n        200 -> 1,\n        404 -> 2\n    }\n}", "A9", "every other value (add a `_` arm)"},
		{"string with no catch-all", "rule r(name: string) -> int {\n    return match (name) {\n        \"a\" -> 1\n    }\n}", "A9", "every other value"},
		{"bool missing false", "rule r(flag: bool) -> int {\n    return match (flag) {\n        true -> 1\n    }\n}", "A9", "missing case(s): false"},
		{"optional with no None arm", "rule r(name: string | None) -> int {\n    return match (name) {\n        \"a\" -> 1,\n        other -> 2\n    }\n}", "A9", "None"},
		{"int pattern on a string", "rule r(name: string) -> int {\n    return match (name) {\n        5 -> 1,\n        _ -> 0\n    }\n}", "A1", "the pattern 5 is int, and the subject is string"},
		{"string pattern on an int", "rule r(n: int) -> int {\n    return match (n) {\n        \"a\" -> 1,\n        _ -> 0\n    }\n}", "A1", `the pattern "a" is string, and the subject is int`},
		{"int pattern on a float", "rule r(x: float) -> int {\n    return match (x) {\n        1 -> 1,\n        _ -> 0\n    }\n}", "A1", "has no literal pattern"},
		{"int pattern on a decimal", "rule r(x: decimal) -> int {\n    return match (x) {\n        1 -> 1,\n        _ -> 0\n    }\n}", "A1", "has no literal pattern"},
	}
	for _, c := range cases {
		wantDiag(t, checkSrc(t, c.src), c.rule, c.fragment)
	}
}

// `expect x is Err(Invalid("score", _))`: a literal in an `is` pattern is
// checked against the type it meets (ADR 0091 open question 6).
func TestLiteralPatternInAnIsExpectation(t *testing.T) {
	files := func(test string) []Diagnostic {
		diags, _ := CheckSources(map[string]string{
			"errors/rating_error.fiig": "enum RatingError {\n    Invalid(field: string, reason: string)\n}\n",
			"rules/rate.fiig":          "rule rate(score: int) -> Result<int, RatingError> {\n    return Err(RatingError.Invalid(\"score\", \"range\"));\n}\n",
			"tests/rejects.fiig":       test,
		}, nil, "", nil)
		return diags
	}
	if diags := files("test rejects {\n    expect rate(11) is Err(RatingError.Invalid(\"score\", _));\n}\n"); len(diags) != 0 {
		t.Fatalf("diags = %v", diags)
	}
	if diags := files("test rejects {\n    expect rate(11) is Err(RatingError.Invalid(5, _));\n}\n"); len(diags) == 0 {
		t.Fatal("an int pattern against a string field should be refused")
	}
}
