package checker

import "testing"

// ADR 0067: string methods in the checker.

func TestStringMethodsCheck(t *testing.T) {
	diags := checkSrc(t, `rule r(s: string, names: List<string>) -> string {
    n: int = s.length() + s.codePointCount() + s.utf8().length()
    head: string = s.slice(0, 1)
    ok: bool = s.startsWith("a") and s.endsWith("z") and s.equalsIgnoringCase("A")
    t: string = s.removePrefix("Bearer ").removeSuffix("/").trim().trimStart().trimEnd()
    u: string = s.lowercase().uppercase().replaceAll("-", "/")
    parts: List<string> = s.split(",")
    i: int | None = s.toIntOrNone()
    f: float | None = s.toFloatOrNone()
    d: decimal | None = s.toDecimalOrNone()
    return parts.join(", ")
}`)
	if len(diags) != 0 {
		t.Fatalf("diags = %v, want none", diags)
	}
}

func TestStringMethodMisuse(t *testing.T) {
	cases := []struct{ src, rule, fragment string }{
		// §5: join is for List<string>.
		{"rule r(xs: List<int>) -> string {\n    return xs.join(\",\")\n}", "A1", "List<int>.join joins strings"},
		// Arguments and results are typed by the table.
		{"rule r(s: string) -> string {\n    return s.slice(\"a\", 1)\n}", "A1", ""},
		{"rule r(s: string) -> int {\n    return s.toIntOrNone()\n}", "A1", ""},
		{"rule r(s: string) -> int {\n    return s.split(1).length()\n}", "A1", ""},
		// ADR 0065 §6: a bare call is A13.
		{"rule r(s: string) -> string {\n    s.trim()\n    return s\n}", "A13", "the result of trim() is discarded"},
		// There is no byteCount; bytes go through the encoding.
		{"rule r(s: string) -> int {\n    return s.byteCount()\n}", "K1", `string has no method "byteCount"`},
	}
	for _, c := range cases {
		wantDiag(t, checkSrc(t, c.src), c.rule, c.fragment)
	}
}
