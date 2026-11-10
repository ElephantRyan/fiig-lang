package format

import "testing"

// ADR 0073 §7: keyword field names and `from json` names print as written.
func TestFormatJsonFieldNames(t *testing.T) {
	want := "model Page {\n" +
		"    limit: int\n" +
		"    contentType: string from json \"content-type\"\n" +
		"}\n"
	got, errs := Format("model Page {\n  limit:int\n    contentType : string  from json \"content-type\"\n}")
	if len(errs) > 0 {
		t.Fatalf("format errors: %v", errs)
	}
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}
