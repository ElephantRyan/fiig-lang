package format

import "testing"

// ADR 0071 §1: a config field prints on one line, its default after `=`.
func TestFormatConfigDecl(t *testing.T) {
	want := "wire Sessions {\n" +
		"    // Seconds a login lasts.\n" +
		"    maxAge: int from env \"SESSION_MAX_AGE\" = 24 * 60 * 60\n" +
		"    exportToken: Secret from env \"ACCOUNTS_EXPORT_TOKEN\"\n" +
		"}\n"
	got, errs := Format("config  Sessions {\n    // Seconds a login lasts.\n    maxAge:int from env \"SESSION_MAX_AGE\"=24*60*60\n  exportToken : Secret from env \"ACCOUNTS_EXPORT_TOKEN\"\n}")
	if len(errs) > 0 {
		t.Fatalf("format errors: %v", errs)
	}
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}
