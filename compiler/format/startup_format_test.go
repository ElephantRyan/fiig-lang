package format

import "testing"

// ADR 0124: a startup route is `run -> workflow() { ... }`, with no path.
func TestFormatStartupAdapter(t *testing.T) {
	want := "adapter startup Preflight {\n" +
		"    run -> verifyStorage() {\n" +
		"        Ok(_) -> proceed\n" +
		"        Err(StorageError.Missing) -> halt\n" +
		"    }\n" +
		"}\n"
	got, errs := Format("adapter   startup  Preflight {\n run->verifyStorage()  {\n Ok(_)->proceed\n Err(StorageError.Missing)->halt\n }\n}")
	if len(errs) > 0 {
		t.Fatalf("format errors: %v", errs)
	}
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	again, errs := Format(got)
	if len(errs) > 0 || again != got {
		t.Errorf("formatting isn't stable: %v\n%s", errs, again)
	}
}
