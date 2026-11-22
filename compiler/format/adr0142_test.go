package format

import (
	"strings"
	"testing"
)

// ADR 0142 step 1: `fiig fmt` keeps the symbol operators as they were
// written, so a file in the new spelling stays in it. Writing `;` and the
// condition parentheses everywhere is step 3's rewrite.
func TestFormatKeepsTheSymbolOperators(t *testing.T) {
	src := "rule r(a: bool, b: bool, n: int | None) -> int {\n" +
		"    if (a && !b || b) {\n" +
		"        return n ?? 0\n" +
		"    }\n" +
		"\n" +
		"    return 1\n" +
		"}\n"
	out := mustFormat(t, src)
	if out != src {
		t.Fatalf("got:\n%s\nwant:\n%s", out, src)
	}
	old := mustFormat(t, "rule r(a: bool, b: bool) -> bool {\n    return a and not b or b\n}\n")
	if !strings.Contains(old, "return a and not b or b") {
		t.Fatalf("the old spelling changed:\n%s", old)
	}
}
