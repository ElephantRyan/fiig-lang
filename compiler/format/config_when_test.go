package format

import "testing"

// ADR 0122: a config's `when` is written after its name, and the
// condition's spacing is the formatter's.
func TestFormatConfigWhen(t *testing.T) {
	want := "config Oidc when SignInConfig.authMode != \"abs\" and Flags.on {\n" +
		"    issuer: string from env \"OIDC_ISSUER\"\n" +
		"}\n"
	got, errs := Format("config   Oidc  when SignInConfig.authMode!=\"abs\"   and   Flags.on {\n  issuer:string from env \"OIDC_ISSUER\"\n}")
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
