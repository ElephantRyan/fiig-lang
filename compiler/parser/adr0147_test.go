package parser

import "testing"

// ADR 0147: a comma is followed by another item, in every comma list. One
// before `)`, `]`, `}`, or `>` is an error at the comma, and the parse goes
// on as if it weren't there, so the formatter can drop it.
func TestNoCommaBeforeAClosingBracket(t *testing.T) {
	for _, src := range []string{
		`enum Region {
    US,
    EU,
}
`,
		`rule r(a: int, b: int,) -> int {
    return a;
}
`,
		`rule r() -> List<int> {
    return [1, 2,];
}
`,
		`rule r(x: int | None) -> int {
    return match (x) {
        None -> 0,
        n -> n,
    }
}
`,
	} {
		_, errs := Parse(src)
		if len(errs) != 1 || !IsTrailingComma(errs[0]) {
			t.Errorf("%q: errors %v, want one trailing comma", src, errs)
		}
	}
	for _, src := range []string{
		`enum Region {
    US,
    EU
}
`,
		`enum Region { US, EU }
`,
		`rule r(x: int | None) -> int {
    return match (x) {
        None -> 0,
        n -> n
    }
}
`,
	} {
		if _, errs := Parse(src); len(errs) > 0 {
			t.Errorf("%q: %v", src, errs)
		}
	}
}

// The error points at the comma and names the bracket after it.
func TestTrailingCommaMessage(t *testing.T) {
	_, errs := Parse("rule r(a: int,) -> int {\n    return a;\n}\n")
	if len(errs) != 1 {
		t.Fatalf("errors %v", errs)
	}
	want := "a comma is followed by another item, and nothing follows this one before `)`; remove it (ADR 0147)"
	if errs[0].Msg != want || errs[0].At.Line != 1 || errs[0].At.Column != 14 {
		t.Errorf("got %s %q", errs[0].At, errs[0].Msg)
	}
}
