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

// ADR 0124 §6: `once` and `within` sit between `run` and the arrow.
// ADR 0127: `degrade`, and a config field read from a startup route.
func TestFormatDegradeAndStartupConfigField(t *testing.T) {
	want := "adapter startup Preflight {\n" +
		"    run -> verifyStorage() {\n" +
		"        Ok(_) -> proceed\n" +
		"        Err(_) -> degrade\n" +
		"    }\n" +
		"}\n"
	got, errs := Format("adapter startup Preflight {\n run -> verifyStorage() {\n Ok(_)->proceed\n Err(_)->degrade\n }\n}")
	if len(errs) > 0 || got != want {
		t.Errorf("got:\n%s\nerrs: %v", got, errs)
	}
	wantConfig := "config Health {\n    storageDown: bool from startup \"Preflight.verifyStorage\"\n}\n"
	got, errs = Format("config Health {\n storageDown:bool   from startup \"Preflight.verifyStorage\"\n}")
	if len(errs) > 0 || got != wantConfig {
		t.Errorf("got:\n%s\nerrs: %v", got, errs)
	}
}

func TestFormatStartupOnceAndWithin(t *testing.T) {
	want := "adapter startup Preflight {\n" +
		"    run once within \"2m\" -> migrate() {\n" +
		"        _ -> halt\n" +
		"    }\n" +
		"\n" +
		"    run within \"500ms\" -> verifyStorage() {\n" +
		"        _ -> proceed\n" +
		"    }\n" +
		"}\n"
	got, errs := Format("adapter startup Preflight {\n run  once   within \"2m\"->migrate() {\n _->halt\n }\n run within \"500ms\" -> verifyStorage() {\n _->proceed\n }\n}")
	if len(errs) > 0 {
		t.Fatalf("format errors: %v", errs)
	}
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}
