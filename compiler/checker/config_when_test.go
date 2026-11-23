package checker

import "testing"

// ADR 0122: a config's `when`, and V2.

const (
	signInConfig = `wire SignInConfig {
    authMode: string from env "AUTH_MODE" = "both";
    mirror: bool from env "MIRROR" = false;
    retries: int from env "RETRIES" = 3;
    flavour: Flavour from env "FLAVOUR" = Flavour.Plain;
}`
	flavourEnum = `enum Flavour {
    Plain,
    Spicy
}`
)

func TestAConfigWhenIsChecked(t *testing.T) {
	for _, when := range []string{
		`SignInConfig.authMode != "abs"`,
		`SignInConfig.authMode == "oidc" or SignInConfig.authMode == "both"`,
		`not SignInConfig.mirror`,
		`SignInConfig.mirror and SignInConfig.retries == 3`,
		`(SignInConfig.authMode != "abs") and not SignInConfig.mirror`,
		`SignInConfig.flavour == Flavour.Spicy`,
		`SignInConfig.mirror == true`,
	} {
		diags := checkSrc(t, signInConfig, flavourEnum, "wire Oidc when "+when+" {\n    issuer: string from env \"OIDC_ISSUER\"\n}")
		if len(diags) != 0 {
			t.Errorf("when %s: %v, want none", when, diags)
		}
	}
}

func TestAWhenOutsideTheSubsetIsV2(t *testing.T) {
	cases := []struct{ when, fragment string }{
		{`SignInConfig.retries + 1 == 4`, "the operator +"},
		{`SignInConfig.retries > 2`, "the operator >"},
		{`"${SignInConfig.authMode}x" == "bothx"`, "an interpolated string"},
		{`mode == "abs"`, "the name mode"},
		{`Other.thing == 1`, "Other.thing"},
		{`SignInConfig.authMode.length() == 4`, "this expression"},
		{`Oidc.issuer == "x"`, "its own fields"},
	}
	for _, c := range cases {
		wantDiag(t, checkSrc(t, signInConfig, flavourEnum, "wire Oidc when "+c.when+" {\n    issuer: string from env \"OIDC_ISSUER\"\n}"), "V2", c.fragment)
	}
}

func TestAWhenIsABool(t *testing.T) {
	wantDiag(t, checkSrc(t, signInConfig, flavourEnum, "wire Oidc when (SignInConfig.retries) {\n    issuer: string from env \"OIDC_ISSUER\";\n}"), "V2", "is int, not a bool")
	wantDiag(t, checkSrc(t, signInConfig, flavourEnum, "wire Oidc when (SignInConfig.authMode) {\n    issuer: string from env \"OIDC_ISSUER\";\n}"), "V2", "is string, not a bool")
}

func TestAWhenNamesAFieldThatExistsAndComparesLikeTypes(t *testing.T) {
	wantDiag(t, checkSrc(t, signInConfig, flavourEnum, "wire Oidc when (SignInConfig.nope == \"x\") {\n    issuer: string from env \"OIDC_ISSUER\";\n}"), "", `config SignInConfig has no field "nope"`)
	// A string against an int is the ordinary type error.
	diags := checkSrc(t, signInConfig, flavourEnum, "wire Oidc when (SignInConfig.retries == \"3\") {\n    issuer: string from env \"OIDC_ISSUER\";\n}")
	if len(diags) == 0 {
		t.Error("comparing an int with a string was accepted")
	}
}

// A condition reads config that has none of its own, so there is no chain to
// order and no absent config for a condition to read.
func TestAWhenCantReadConfigThatHasAWhen(t *testing.T) {
	gated := "wire Gated when (SignInConfig.mirror) {\n    token: string from env \"GATED_TOKEN\";\n}"
	diags := checkSrc(t, signInConfig, flavourEnum, gated, "wire Next when (Gated.token == \"x\") {\n    other: string from env \"NEXT\";\n}")
	wantDiag(t, diags, "V2", "Gated, which has a `when` of its own")
}

// The fields of a config with a `when` read as they always have.
func TestAConfigWithAWhenIsReadLikeAnyOther(t *testing.T) {
	diags := checkSrc(t, signInConfig, flavourEnum, "wire Oidc when (SignInConfig.authMode != \"abs\") {\n    issuer: string from env \"OIDC_ISSUER\";\n}", "activity issuer() -> string {\n    return Oidc.issuer;\n}")
	if len(diags) != 0 {
		t.Fatalf("diags = %v, want none", diags)
	}
}
