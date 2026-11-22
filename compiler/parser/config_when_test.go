package parser

import (
	"testing"

	"fiig-lang/compiler/syntax"
)

// ADR 0122: a config may say when it is present; `when` is contextual.
func TestParseConfigWhen(t *testing.T) {
	f, errs := Parse("wire Oidc when (SignInConfig.authMode != \"abs\" && !Flags.off) {\n    issuer: string from env \"OIDC_ISSUER\"\n}")
	if len(errs) != 0 {
		t.Fatalf("errs = %v", errs)
	}
	d := f.Decls[0].(*syntax.ConfigDecl)
	if d.Name != "Oidc" || d.When == nil || len(d.Fields) != 1 {
		t.Fatalf("decl = %+v", d)
	}
	paren, ok := d.When.(*syntax.ParenExpr)
	if !ok {
		t.Fatalf("when = %T, want the condition in its parentheses", d.When)
	}
	if _, ok := paren.Inner.(*syntax.BinaryExpr); !ok {
		t.Fatalf("when = %T, want the and of two conditions", paren.Inner)
	}
}

func TestWhenIsStillAName(t *testing.T) {
	// A config with no condition has none, and a field or a parameter named
	// when is still a name.
	f, errs := Parse("wire C {\n    when: bool from env \"WHEN\" = false\n}")
	if len(errs) != 0 || f.Decls[0].(*syntax.ConfigDecl).When != nil {
		t.Fatalf("a field named when: %v", errs)
	}
	if _, errs := Parse("rule r(when: int) -> int {\n    return when;\n}"); len(errs) != 0 {
		t.Fatalf("when as a name: %v", errs)
	}
}
