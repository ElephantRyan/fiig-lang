package format

import "testing"

// ADR 0130: a literal pattern prints as written, and formatting is stable.
func TestFormatLiteralPatterns(t *testing.T) {
	want := "rule describe(code: int, name: string, on: bool) -> string {\n" +
		"    return match (code) {\n" +
		"        200 -> \"ok\"\n" +
		"        -1 -> \"unset\"\n" +
		"        _ -> \"other\"\n" +
		"    }\n" +
		"}\n"
	src := "rule describe(code: int, name: string, on: bool) -> string {\n match code {\n  200->\"ok\"\n  -1  ->\"unset\"\n  _->\"other\"\n }\n}"
	got, errs := Format(src)
	if len(errs) > 0 || got != want {
		t.Fatalf("got:\n%s\nerrs: %v", got, errs)
	}
	again, errs := Format(got)
	if len(errs) > 0 || again != got {
		t.Errorf("formatting isn't stable:\n%s", again)
	}
	strs := "rule r(name: string, on: bool) -> int {\n" +
		"    return match (name) {\n" +
		"        \"A\\\"B\" -> 1\n" +
		"        \"line\\nbreak\" -> 2\n" +
		"        _ -> 0\n" +
		"    }\n" +
		"}\n"
	if got, errs := Format(strs); len(errs) > 0 || got != strs {
		t.Errorf("string patterns: got:\n%s\nerrs: %v", got, errs)
	}
	bools := "rule r(on: bool) -> int {\n    return match (on) {\n        true -> 1\n        false -> 2\n    }\n}\n"
	if got, errs := Format(bools); len(errs) > 0 || got != bools {
		t.Errorf("bool patterns: got:\n%s\nerrs: %v", got, errs)
	}
}
