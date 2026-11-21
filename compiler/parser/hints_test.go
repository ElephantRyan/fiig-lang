package parser

import (
	"strings"
	"testing"
)

// A construct other languages have and fiig leaves out is one error that says
// what to write, not a cascade (docs/improvement-ideas.md B3).
func TestALeftOutConstructIsOneErrorThatSaysWhatToWrite(t *testing.T) {
	rule := func(body string) string { return "rule r(a: int, b: bool) -> int {\n" + body + "\n    return 1\n}" }
	cases := []struct{ name, src, fragment string }{
		{"&&", rule("    c = b && b"), "write `and`"},
		{"||", rule("    c = b || b"), "write `or`"},
		{"!", rule("    c = !b"), "write `not`"},
		{";", rule("    n = 1;"), "remove the `;`"},
		{"++", rule("    n: int = 0\n    n++"), "write `n = n + 1`"},
		{"--", rule("    n: int = 0\n    n--"), "write `n = n - 1`"},
		{"+=", rule("    n: int = 0\n    n += 1"), "no compound assignment"},
		{"=>", rule("    c = [1].map(x => x + 1)"), "there are no lambdas"},
		{"ternary", rule("    c = a > 1 ? 2 : 3"), "there is no `cond ? a : b`"},
		{"map literal", rule("    m = {\"a\": 1}"), "[\"a\": 1, \"b\": 2]"},
		{"c for", rule("    for (i = 0; i < 3; i++) {\n    }"), "`for x in xs { ... }`"},
		{"elif", "rule r(a: int) -> int {\n    if a > 1 {\n        return 1\n    } elif a > 0 {\n        return 2\n    }\n    return 3\n}", "write `else if`"},
		{"switch", rule("    switch a {\n    }"), "write a `match`"},
		{"const", "const MAX = 3", "there are no constants of their own"},
		{"import", "import foo\nrule r() -> int {\n    return 1\n}", "an import is `use acme.package`"},
		{"function", "function f() {\n}", "a declaration starts with"},
		{"try", rule("    try {\n    }"), "there is no `try`"},
	}
	for _, c := range cases {
		_, errs := Parse(c.src)
		if len(errs) != 1 || !strings.Contains(errs[0].Msg, c.fragment) {
			t.Errorf("%s: errs = %v, want one saying %q", c.name, errs, c.fragment)
		}
	}
}

// The errors before the hint are kept, and a program with none of these is
// unchanged.
func TestAHintKeepsTheErrorsBeforeIt(t *testing.T) {
	_, errs := Parse("model M {\n    a: \n}\nrule r(b: bool) -> bool {\n    return b && b\n}")
	if len(errs) < 2 || !strings.Contains(errs[len(errs)-1].Msg, "write `and`") {
		t.Errorf("errs = %v, want the model's error and then the hint", errs)
	}
	if _, errs := Parse("rule r(a: bool, b: bool) -> bool {\n    return a and not b\n}"); len(errs) != 0 {
		t.Errorf("errs = %v", errs)
	}
	// An error with none of the known constructs on its line is reported as before.
	if _, errs := Parse("rule r() -> int {\n    return )\n}"); len(errs) == 0 || strings.Contains(errs[0].Msg, "write") {
		t.Errorf("errs = %v", errs)
	}
}
