package checker

import "testing"

// ADR 0142 §3: `!`, `&&` and `||` are bool operators, and `??` is the
// default for a `T | None`. Both spellings check until step 2 (§5).
func TestSymbolOperatorsCheck(t *testing.T) {
	diags := checkSrc(t, `rule r(input: Input, ok: bool) -> string {
    note: string = input.note ?? "";
    name: string = input.alias ?? input.note ?? "anonymous";
    tags: List<string> = input.tags ?? [];
    finished: bool = input.done ?? false;
    both: bool = (ok || finished) && !ok;
    if (both && !(note == "")) {
        return name;
    }
    for (tag in tags) {
        if (tag == "x") {
            return tag;
        }
    }
    return "${note}${tags.length()}";
}`, orModel)
	if len(diags) != 0 {
		t.Fatalf("diags = %v, want none", diags)
	}
}

func TestSymbolOperatorMisuse(t *testing.T) {
	cases := []struct{ body, rule, fragment string }{
		{`x: string = input.note || ""`, "A1", "`||` is bool only"},
		{`x: int = 3 ?? 4`, "A1", "`??` gives a `T | None` a default"},
		{`x: string = input.note ?? 0`, "A1", ""},
		{`x: bool = !3`, "A1", ""},
		{`x: bool = 1 && true`, "A1", ""},
	}
	for _, c := range cases {
		src := "rule r(input: Input) -> int {\n    " + c.body + "\n    return 0\n}"
		wantDiag(t, checkSrc(t, src, orModel), c.rule, c.fragment)
	}
}

// ADR 0142 §2: a match used as a value has arms of one type, and an arm
// block that doesn't end with `return` gives it no value.
func TestMatchAsAValueMisuse(t *testing.T) {
	cases := []struct{ body, rule, fragment string }{
		{"x = match (input.count) {\n        None -> 0\n        n -> \"many\"\n    }", "A1", "expected int"},
		{"x = match (input.count) {\n        None -> 0\n        n -> {\n            y = n\n        }\n    }", "A1", "gives it no value"},
		{"return match (input.count) {\n        None -> \"none\"\n        n -> 1\n    }", "A1", "expected int"},
	}
	for _, c := range cases {
		src := "rule r(input: Input) -> int {\n    " + c.body + "\n    return 0\n}"
		wantDiag(t, checkSrc(t, src, orModel), c.rule, c.fragment)
	}
}
