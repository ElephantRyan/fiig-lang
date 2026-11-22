package runprogram

import (
	"strings"
	"testing"

	"fiig-lang/compiler/parser"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// ADR 0122: `fiig config` lists a conditional config's variables with their
// condition, and the config that condition reads, which nothing else may read.
func TestAConditionalConfigIsListedWithItsConditionAndWhatItReads(t *testing.T) {
	srcs := []string{
		"wire SignInConfig {\n    authMode: string from env \"AUTH_MODE\" = \"both\";\n}",
		"wire Oidc when (SignInConfig.authMode != \"abs\") {\n    issuer: string from env \"OIDC_ISSUER\";\n    audience: string | None from env \"OIDC_AUDIENCE\";\n}",
		"activity issuer() -> string {\n    return Oidc.issuer;\n}",
	}
	var files []*syntax.File
	for _, src := range srcs {
		f, errs := parser.Parse(src)
		if len(errs) != 0 {
			t.Fatal(errs)
		}
		files = append(files, f)
	}
	u, _ := types.BuildUniverse(files)

	// The activity reads only Oidc, but its condition reads SignInConfig, so
	// both are needed to start.
	sources := ReachableConfig(Program{Universe: u}, []string{"issuer"})
	var names []string
	for _, s := range sources {
		names = append(names, s.Decl.Name)
	}
	if got := strings.Join(names, ","); got != "Oidc,SignInConfig" {
		t.Fatalf("issuer reaches %q, want Oidc and the config its when reads", got)
	}

	vars, err := Variables(sources, "")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, v := range vars {
		got[v.Name] = v.Requirement
	}
	want := map[string]string{
		"AUTH_MODE":     `= "both"`,
		"OIDC_ISSUER":   `required when SignInConfig.authMode != "abs"`,
		"OIDC_AUDIENCE": `optional when SignInConfig.authMode != "abs"`,
	}
	for name, req := range want {
		if got[name] != req {
			t.Errorf("%s is %q, want %q", name, got[name], req)
		}
	}
}
