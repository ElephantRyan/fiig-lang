package checker

import "testing"

// ADR 0099: `x or d` is x, or d when x is None.

const orModel = `model Input {
    note: string | None;
    alias: string | None;
    tags: List<string> | None;
    count: int | None;
    done: bool | None;
}`

func TestOrDefaultChecks(t *testing.T) {
	diags := checkSrc(t, `rule r(input: Input, ok: bool) -> string {
    note: string = input.note ?? "";
    name: string = input.alias ?? input.note ?? "anonymous";
    tags: List<string> = input.tags ?? [];
    n: int = input.count ?? 0;
    finished: bool = input.done ?? false;
    both: bool = ok || finished;
    return "${note}${name}${tags.length()}${n}${both}";
}`, orModel)
	if len(diags) != 0 {
		t.Fatalf("diags = %v, want none", diags)
	}
}

func TestOrDefaultMisuse(t *testing.T) {
	cases := []struct{ body, rule, fragment string }{
		{`x: string = input.note or 0`, "A1", ""},
		{`x: string = input.note or input.count`, "A1", "the default after `or` is int | None, expected string"},
		{`x: string = input.alias or false`, "A1", "the default after `or` is bool, expected string"},
		{`x: string = input.alias or input.note`, "A1", "expected string"},
		{`x: bool = 1 or true`, "A1", "expected bool (or `T | None`"},
	}
	for _, c := range cases {
		src := "rule r(input: Input) -> int {\n    " + c.body + "\n    return 0\n}"
		wantDiag(t, checkSrc(t, src, orModel), c.rule, c.fragment)
	}
}
