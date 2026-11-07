package runprogram

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fiig-lang/compiler/loader"
	"fiig-lang/compiler/types"
)

// ADR 0071 §3, the owner's per-vertical case: checkout-app and
// checkout-clientvertical share checkout.core from one commit, and the
// vertical, whose manifest has only acme.stripe, neither needs Square's key
// nor can import Square.
func TestAVerticalProvesWhatItDoesNotContain(t *testing.T) {
	examples, err := filepath.Abs(filepath.Join("..", "..", "examples"))
	if err != nil {
		t.Fatal(err)
	}
	vars := func(app, entry string) string {
		root := filepath.Join(examples, app)
		loaded, errs := loader.Load(root, []string{examples})
		if len(errs) > 0 {
			t.Fatalf("%s: %v", app, errs)
		}
		files, _, err := LoadFiles(root)
		if err != nil {
			t.Fatal(err)
		}
		if diags := Check(root, files, loaded.Packages); len(diags) > 0 {
			t.Fatalf("%s: %v", app, diags)
		}
		u, _ := types.BuildUniverse(files)
		p := Program{Universe: u, Imports: BuildImports(files, loaded.Packages), Groups: BuildGroups(files, loaded.Packages), Renames: Renames(root)}
		list, err := Variables(ReachableConfig(p, []string{entry}), "")
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, v := range list {
			names = append(names, v.Name)
		}
		return strings.Join(names, ",")
	}
	if got := vars("checkout-app", "takePayment"); got != "SQUARE_ACCESS_TOKEN,STRIPE_API_KEY" {
		t.Errorf("checkout-app reads %q, want both providers' keys", got)
	}
	// Its own Stripe account, under the name its manifest renames it to.
	if got := vars("checkout-clientvertical", "takeClientPayment"); got != "CLIENTVERTICAL_STRIPE_API_KEY" {
		t.Errorf("checkout-clientvertical reads %q, want only Stripe's key, renamed", got)
	}

	// And it can't reach Square at all: the import is an error. A copy, so
	// the corpus other packages' tests read never changes.
	root := t.TempDir()
	if err := os.CopyFS(root, os.DirFS(filepath.Join(examples, "checkout-clientvertical"))); err != nil {
		t.Fatal(err)
	}
	sneak := filepath.Join(root, "rules", "sneak_square.fiig")
	src := "use acme.payments\nuse acme.square.Square\nuse checkout.core\n\nrule sneakSquare(merchant: core.PayingMerchant) -> payments.PaymentProvider {\n    return Square\n}\n"
	if err := os.WriteFile(sneak, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, errs := loader.Load(root, []string{examples})
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	files, _, _ := LoadFiles(root)
	diags := Check(root, files, loaded.Packages)
	found := false
	for _, d := range diags {
		found = found || strings.Contains(d.Msg, `"acme.square.Square" isn't a dependency of this package`)
	}
	if !found {
		t.Fatalf("importing Square in the vertical: %v", diags)
	}
}
