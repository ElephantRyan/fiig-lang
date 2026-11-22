package format

import (
	"strings"
	"testing"

	"fiig-lang/compiler/parser"
	"fiig-lang/compiler/syntax"
)

// ADR 0132: `application`, with named `serve` blocks.
func TestFormatApplication(t *testing.T) {
	want := "use t.app.ratings\n" +
		"use t.app.sync\n" +
		"\n" +
		"application MyApp {\n" +
		"    serve web {\n" +
		"        ratings.ratingsApi\n" +
		"        ratings.adminApi\n" +
		"    }\n" +
		"\n" +
		"    serve worker {\n" +
		"        sync.syncSchedule\n" +
		"    }\n" +
		"}\n"
	src := "use t.app.ratings\nuse t.app.sync\n\napplication   MyApp{\n serve web{ ratings.ratingsApi, ratings.adminApi }\n  serve worker {\n sync.syncSchedule\n}\n}"
	got, errs := Format(src)
	if len(errs) > 0 || got != want {
		t.Fatalf("got:\n%s\nerrs: %v", got, errs)
	}
	if again, errs := Format(got); len(errs) > 0 || again != got {
		t.Errorf("formatting isn't stable:\n%s", again)
	}
	single := "application MyApp {\n    serve {\n        api\n    }\n}\n"
	if got, errs := Format(single); len(errs) > 0 || got != single {
		t.Errorf("an unnamed block: got:\n%s\nerrs: %v", got, errs)
	}
}

func TestApplicationParses(t *testing.T) {
	f, errs := parser.Parse("use t.app.ratings\n\napplication MyApp {\n    serve web {\n        ratings.ratingsApi, healthApi\n    }\n    serve {\n    }\n}\n")
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	app, ok := f.Decls[0].(*syntax.ApplicationDecl)
	if !ok || app.Name != "MyApp" || len(app.Serves) != 2 {
		t.Fatalf("decl = %#v", f.Decls[0])
	}
	b := app.Serves[0]
	if b.Role != "web" || len(b.Adapters) != 2 || strings.Join(b.Adapters[0].Path, ".") != "ratings.ratingsApi" || strings.Join(b.Adapters[1].Path, ".") != "healthApi" {
		t.Errorf("web block = %+v", b)
	}
	if app.Serves[1].Role != "" || len(app.Serves[1].Adapters) != 0 {
		t.Errorf("unnamed block = %+v", app.Serves[1])
	}
}

// `application` and `serve` are contextual: elsewhere they are names.
func TestApplicationAndServeAreOrdinaryNamesElsewhere(t *testing.T) {
	src := "rule r(application: int, serve: int) -> int {\n    return application + serve\n}\n"
	if _, errs := parser.Parse(src); len(errs) > 0 {
		t.Errorf("errs = %v", errs)
	}
}

func TestApplicationMistakesAreOneError(t *testing.T) {
	_, errs := parser.Parse("application MyApp {\n    run api\n}\n")
	if len(errs) == 0 || !strings.Contains(errs[0].Msg, "expected `serve` in an application") {
		t.Errorf("errs = %v", errs)
	}
}
