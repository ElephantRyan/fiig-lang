package runtime

import (
	"context"
	"strings"
	"testing"

	"fiig-lang/compiler/syntax"
)

// ADR 0071 §1, §3, §4: reading config before the program starts.

var configSources = []string{`wire Settings {
    maxAge: int from env "MAX_AGE" = 24 * 60 * 60
    ratio: float from env "RATIO" = 1
    rate: decimal from env "RATE" = 1.50
    name: string from env "NAME"
    verbose: bool from env "VERBOSE" = false
    level: Level from env "LEVEL" = Level.Info
    region: string | None from env "REGION"
    key: Secret from env "API_KEY"
}`, `enum Level {
    Debug
    Info
}`, `activity show() -> string {
    return "${Settings.maxAge} ${Settings.ratio} ${Settings.rate} ${Settings.name} ${Settings.verbose} ${Settings.level} ${Settings.region}";
}`, `activity key() -> Secret {
    return Settings.key;
}`, `activity allowed(token: string) -> bool {
    return Settings.key.matches(token);
}`}

func configInterp(t *testing.T, env map[string]string) (*Interp, []string) {
	t.Helper()
	u := universeFromSource(t, configSources...)
	d, _ := u.Lookup("Settings")
	in := New(u, NewCapabilityRegistry())
	problems := in.LoadConfig([]ConfigSource{{Decl: d.(*syntax.ConfigDecl), Universe: u}}, func(name string) (string, bool) {
		v, ok := env[name]
		return v, ok
	})
	return in, problems
}

func TestConfigDefaultsAndValues(t *testing.T) {
	in, problems := configInterp(t, map[string]string{"NAME": "accounts", "API_KEY": "hunter2", "REGION": ""})
	if problems != nil {
		t.Fatal(problems)
	}
	got, fault := in.Call(context.Background(), "show", nil)
	if fault != nil || got.String() != "86400 1.0 1.50 accounts false Level.Info None" {
		t.Fatalf("defaults: %v %v", got, fault)
	}
	in, problems = configInterp(t, map[string]string{
		"NAME": "x", "API_KEY": "k", "MAX_AGE": "-5", "RATIO": "2.5", "RATE": "0.10",
		"VERBOSE": "true", "LEVEL": "Debug", "REGION": "eu",
	})
	if problems != nil {
		t.Fatal(problems)
	}
	if got, _ := in.Call(context.Background(), "show", nil); got.String() != "-5 2.5 0.10 x true Level.Debug eu" {
		t.Fatalf("values: %v", got)
	}
}

func TestSecretIsShownByNameAndMatches(t *testing.T) {
	in, _ := configInterp(t, map[string]string{"NAME": "x", "API_KEY": "hunter2"})
	got, _ := in.Call(context.Background(), "key", nil)
	if got.String() != "Secret(API_KEY)" || EncodeJSON(got) != "Secret(API_KEY)" {
		t.Fatalf("secret printed as %v / %v", got, EncodeJSON(got))
	}
	for token, want := range map[string]bool{"hunter2": true, "hunter3": false, "hunter": false, "": false} {
		got, _ := in.Call(context.Background(), "allowed", map[string]Value{"token": Str(token)})
		if got != Bool(want) {
			t.Errorf("matches(%q) = %v, want %v", token, got, want)
		}
	}
}

// ADR 0126: toSecret gives a Secret that prints as its name and matches like
// one from config.
func TestToSecretPrintsByNameAndMatches(t *testing.T) {
	got, f := evalBuiltinMethod(Str("hunter2"), "toSecret", []Value{Str("abs.serviceToken")})
	if f != nil {
		t.Fatal(f)
	}
	if got.String() != "Secret(abs.serviceToken)" || EncodeJSON(got) != "Secret(abs.serviceToken)" {
		t.Fatalf("printed as %v / %v", got, EncodeJSON(got))
	}
	if m, _ := evalBuiltinMethod(got, "matches", []Value{Str("hunter2")}); m != Bool(true) {
		t.Errorf("matches = %v", m)
	}
	if v, _ := evalBuiltinMethod(got, "reveal", nil); v != Str("hunter2") {
		t.Errorf("reveal = %v", v)
	}
}

func TestEveryConfigProblemIsReportedAtOnce(t *testing.T) {
	_, problems := configInterp(t, map[string]string{
		"MAX_AGE": "86400s", "RATIO": "NaN", "RATE": "1,5", "VERBOSE": "yes", "LEVEL": "verbose",
	})
	want := []string{
		`MAX_AGE="86400s" isn't an int; config Settings.maxAge`,
		`RATIO="NaN" isn't a float; config Settings.ratio`,
		`RATE="1,5" isn't a decimal; config Settings.rate`,
		`NAME is unset; config Settings.name needs a string`,
		`VERBOSE="yes" isn't a bool (true or false); config Settings.verbose`,
		`LEVEL="verbose" isn't a Level (Debug, Info); config Settings.level`,
		`API_KEY is unset; config Settings.key needs a Secret`,
	}
	if strings.Join(problems, "\n") != strings.Join(want, "\n") {
		t.Fatalf("problems:\n%s\nwant:\n%s", strings.Join(problems, "\n"), strings.Join(want, "\n"))
	}
}

func TestRenamedVariablesAndPackagesAreNamed(t *testing.T) {
	u := universeFromSource(t, configSources...)
	d, _ := u.Lookup("Settings")
	in := New(u, NewCapabilityRegistry())
	src := ConfigSource{Decl: d.(*syntax.ConfigDecl), Universe: u, Package: "acme.settings", Rename: map[string]string{"API_KEY": "ACME_KEY"}}
	problems := in.LoadConfig([]ConfigSource{src}, func(name string) (string, bool) {
		if name == "NAME" {
			return "x", true
		}
		return "", false
	})
	if len(problems) != 1 || problems[0] != "ACME_KEY is unset; config Settings.key (acme.settings; API_KEY renamed in fiig.pkg.json) needs a Secret" {
		t.Fatalf("problems = %q", problems)
	}
}
