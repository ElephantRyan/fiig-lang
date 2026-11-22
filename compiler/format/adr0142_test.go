package format

import (
	"strings"
	"testing"
)

// ADR 0142 §5: `fiig fmt` writes the new forms: `;`, parenthesised
// conditions and loop heads, `!`/`&&`/`||`/`??`, and `return` before the
// bare expression or `match` that was a declaration's value.
func TestFormatWritesTheNewSyntax(t *testing.T) {
	src := "rule r(a: bool, xs: List<int>, n: int | None) -> int {\n" +
		"    total: int = 0\n" +
		"    for x in xs {\n" +
		"        if not a and x > 1 {\n" +
		"            total = total + x\n" +
		"        } else if a {\n" +
		"            break\n" +
		"        }\n" +
		"    }\n" +
		"    while a {\n" +
		"        continue\n" +
		"    }\n" +
		"    match n {\n" +
		"        None -> 0,\n" +
		"        given -> given + total,\n" +
		"    }\n" +
		"}\n"
	want := "rule r(a: bool, xs: List<int>, n: int | None) -> int {\n" +
		"    total: int = 0;\n" +
		"    for (x in xs) {\n" +
		"        if (!a && x > 1) {\n" +
		"            total = total + x;\n" +
		"        } else if (a) {\n" +
		"            break;\n" +
		"        }\n" +
		"    }\n" +
		"\n" +
		"    while (a) {\n" +
		"        continue;\n" +
		"    }\n" +
		"\n" +
		"    return match (n) {\n" +
		"        None -> 0,\n" +
		"        given -> given + total,\n" +
		"    }\n" +
		"}\n"
	if out := mustFormat(t, src); out != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out, want)
	}
	if out := mustFormat(t, want); out != want {
		t.Fatalf("not stable:\n%s", out)
	}
}

// An `or` is written `||` or `??` from the checker's verdict, keyed by its
// offset, and grouped where `??` meets `&&` or `||`; an `or` with no
// verdict is left and reported (ADR 0142 §5).
func TestFormatResolvesEachOr(t *testing.T) {
	src := "rule r(a: bool, b: bool, n: int | None) -> int {\n    x = n or 0 + 1\n    y = a or b\n    z = a or b\n    return x\n}\n"
	ors := map[int]bool{
		strings.Index(src, "n or"):     true,
		strings.Index(src, "a or b\n"): false,
	}
	out, errs, unknown := FormatTyped(src, ors)
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	for _, want := range []string{"x = n ?? 0 + 1;", "y = a || b;", "z = a or b;"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if len(unknown) != 1 || unknown[0].Line != 4 {
		t.Errorf("unknown = %v, want the `or` on line 4", unknown)
	}
	gsrc := "rule r(a: bool | None, b: bool, c: bool) -> bool {\n    return a or b and c\n}\n"
	grouped, _, _ := FormatTyped(gsrc, map[int]bool{strings.Index(gsrc, "a or"): true})
	if !strings.Contains(grouped, "return a ?? (b && c);") {
		t.Errorf("got:\n%s", grouped)
	}
}

// A final match whose block arm doesn't end in a value stays a statement,
// since `return match` would need every arm to give one.
func TestFormatLeavesAMatchThatCantReturn(t *testing.T) {
	src := "rule r(n: int | None) -> int {\n    match n {\n        None -> 0,\n        given -> {\n            if given > 1 {\n                return 1\n            }\n            x = given\n        },\n    }\n}\n"
	out := mustFormat(t, src)
	if !strings.Contains(out, "    match (n) {") || strings.Contains(out, "return match") {
		t.Fatalf("got:\n%s", out)
	}
}

// A match used as a value prints after its `return` or `name =`.
func TestFormatMatchAsAValue(t *testing.T) {
	src := "rule r(n: int | None) -> int {\n" +
		"    label = match (n) {\n" +
		"        None -> 0,\n" +
		"        given -> given,\n" +
		"    }\n" +
		"\n" +
		"    return match (n) {\n" +
		"        None -> label,\n" +
		"        given -> given,\n" +
		"    }\n" +
		"}\n"
	if out := mustFormat(t, src); out != src {
		t.Fatalf("got:\n%s\nwant:\n%s", out, src)
	}
}
