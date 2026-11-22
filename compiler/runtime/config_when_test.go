package runtime

import (
	"context"
	"strings"
	"testing"

	"fiig-lang/compiler/syntax"
)

// ADR 0122: a config with a `when` is read only when its condition, evaluated
// at startup from other config, holds.

var whenSources = []string{`wire SignInConfig {
    authMode: string from env "AUTH_MODE" = "both";
    mirror: bool from env "MIRROR" = false;
}`, `wire Oidc when (SignInConfig.authMode != "abs") {
    issuer: string from env "OIDC_ISSUER";
    secret: Secret from env "OIDC_SECRET";
}`, `wire Mirror when (SignInConfig.mirror && SignInConfig.authMode == "both") {
    target: string from env "MIRROR_TARGET" = "replica";
}`, `activity issuer() -> string {
    return Oidc.issuer;
}`, `activity target() -> string {
    return Mirror.target;
}`, `activity mode() -> string {
    return SignInConfig.authMode;
}`}

func whenInterp(t *testing.T, partial bool, env map[string]string) (*Interp, []string) {
	t.Helper()
	u := universeFromSource(t, whenSources...)
	source := func(name, when string) ConfigSource {
		d, _ := u.Lookup(name)
		return ConfigSource{Decl: d.(*syntax.ConfigDecl), Universe: u, WhenText: when}
	}
	// The conditional configs come first on purpose: the loader reads the
	// unconditional ones before them, whatever order it is given.
	sources := []ConfigSource{
		source("Oidc", `SignInConfig.authMode != "abs"`),
		source("Mirror", `SignInConfig.mirror and SignInConfig.authMode == "both"`),
		source("SignInConfig", ""),
	}
	in := New(u, NewCapabilityRegistry())
	lookup := func(name string) (string, bool) {
		v, ok := env[name]
		return v, ok
	}
	if partial {
		return in, in.LoadTestConfig(sources, lookup)
	}
	return in, in.LoadConfig(sources, lookup)
}

func TestAConfigWhoseWhenIsFalseIsNotRequired(t *testing.T) {
	in, problems := whenInterp(t, false, map[string]string{"AUTH_MODE": "abs"})
	if problems != nil {
		t.Fatalf("a password-only deployment with no OIDC settings: %v", problems)
	}
	_, fault := in.Call(context.Background(), "issuer", nil)
	if fault == nil || !strings.Contains(fault.Message, "config Oidc isn't set in this deployment") ||
		!strings.Contains(fault.Message, `SignInConfig.authMode != "abs"`) {
		t.Fatalf("reading it faulted with %v, want a message naming the config and its condition", fault)
	}
	// What it was given anyway is ignored, not read.
	in, problems = whenInterp(t, false, map[string]string{"AUTH_MODE": "abs", "OIDC_ISSUER": "https://idp.example", "OIDC_SECRET": "s"})
	if problems != nil {
		t.Fatal(problems)
	}
	if _, fault := in.Call(context.Background(), "issuer", nil); fault == nil {
		t.Error("a config whose when is false was read because its variables were set")
	}
}

func TestAConfigWhoseWhenHoldsIsRequiredAsAlways(t *testing.T) {
	_, problems := whenInterp(t, false, map[string]string{"AUTH_MODE": "both"})
	joined := strings.Join(problems, "\n")
	for _, want := range []string{"OIDC_ISSUER is unset", "OIDC_SECRET is unset"} {
		if !strings.Contains(joined, want) {
			t.Errorf("problems = %q, want %q", joined, want)
		}
	}
	// The default AUTH_MODE is both, so an unset one is the same.
	if _, problems := whenInterp(t, false, nil); len(problems) != 2 {
		t.Errorf("with defaults the problems are %v, want the two OIDC variables", problems)
	}

	in, problems := whenInterp(t, false, map[string]string{"OIDC_ISSUER": "https://idp.example", "OIDC_SECRET": "s3cret"})
	if problems != nil {
		t.Fatal(problems)
	}
	if got, fault := in.Call(context.Background(), "issuer", nil); fault != nil || got.String() != "https://idp.example" {
		t.Fatalf("issuer = %v, %v", got, fault)
	}
}

func TestAConditionCanUseAndAndTheOtherConfigsValues(t *testing.T) {
	in, problems := whenInterp(t, false, map[string]string{"AUTH_MODE": "both", "MIRROR": "true", "OIDC_ISSUER": "i", "OIDC_SECRET": "s"})
	if problems != nil {
		t.Fatal(problems)
	}
	if got, fault := in.Call(context.Background(), "target", nil); fault != nil || got.String() != "replica" {
		t.Fatalf("a present config's default: %v, %v", got, fault)
	}
	// mirror on but the mode isn't both: absent.
	in, problems = whenInterp(t, false, map[string]string{"AUTH_MODE": "abs", "MIRROR": "true"})
	if problems != nil {
		t.Fatal(problems)
	}
	if _, fault := in.Call(context.Background(), "target", nil); fault == nil || !strings.Contains(fault.Message, "config Mirror isn't set") {
		t.Fatalf("target = %v, want a fault naming Mirror", fault)
	}
}

// A condition that can't be evaluated because the config it reads didn't
// load is that config's problem, not a second one about the condition.
func TestAProblemInWhatAConditionReadsIsReportedOnce(t *testing.T) {
	// Mirror's condition reads mirror, which is not a bool here; Oidc's
	// doesn't, so its variables are set to leave nothing else to report.
	_, problems := whenInterp(t, false, map[string]string{"MIRROR": "maybe", "OIDC_ISSUER": "i", "OIDC_SECRET": "s"})
	if len(problems) != 1 || !strings.Contains(problems[0], "MIRROR") {
		t.Fatalf("problems = %v, want only the MIRROR one", problems)
	}
}

// `fiig test` loads config partially and a test fakes what it reads (ADR
// 0091 §5), so a conditional config is present there.
func TestATestSeesAConditionalConfigAsPresent(t *testing.T) {
	in, problems := whenInterp(t, true, map[string]string{"AUTH_MODE": "abs"})
	if problems != nil {
		t.Fatal(problems)
	}
	if len(in.ConfigAbsent) != 0 {
		t.Errorf("a test's config has absent entries: %v", in.ConfigAbsent)
	}
}
