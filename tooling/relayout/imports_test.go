package relayout

import "testing"

// ADR 0145 §1: a `use` line ends with `;`, which is not part of its path.
func TestUseLinesWithASemicolon(t *testing.T) {
	got := rewriteImports("use abs.client;\nuse abs.client as c;\n", map[string]string{"abs.client": "abs.app.absclient"})
	want := "use abs.app.absclient as client;\nuse abs.app.absclient as c;\n"
	if got != want {
		t.Errorf("rewriteImports = %q, want %q", got, want)
	}

	text := "use acme.payments as pay;\n\ncapability Stripe {\n}\n"
	got = addProvides(text, "Stripe", "acme.payments.Provider", "acme.stripe")
	want = "use acme.payments as pay;\n\ncapability Stripe provides pay.Provider {\n}\n"
	if got != want {
		t.Errorf("addProvides = %q, want %q", got, want)
	}
}
