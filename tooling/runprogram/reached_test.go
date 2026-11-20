package runprogram

import (
	"path/filepath"
	"testing"

	"fiig-lang/compiler/loader"
	"fiig-lang/compiler/types"
)

// abs-recommendations G51: `fiig config <adapter>` lists the bindings of the
// packages that adapter reaches, not every package's.
func TestReachedPackagesAreWhatTheEntriesRunCodeIn(t *testing.T) {
	examples, err := filepath.Abs(filepath.Join("..", "..", "examples"))
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(examples, "shop-orders")
	loaded, errs := loader.Load(root, []string{examples})
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	files, _, err := LoadFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := types.BuildUniverse(files)
	program := Program{Universe: u, Imports: BuildImports(files, loaded.Packages), Groups: BuildGroups(files, loaded.Packages)}

	orders, payments := loaded.Packages["shop.orders.orders"], loaded.Packages["shop.payments"]
	if orders == nil || payments == nil {
		t.Fatalf("packages = %v, want shop.orders.orders and shop.payments", loaded.Packages)
	}

	// The orders adapter charges through Payments, so reaches both.
	got := ReachedPackages(program, []Entry{{Package: orders, Name: "ordersApi"}}, loaded.Packages)
	if !got["shop.orders.orders"] || !got["shop.payments"] {
		t.Errorf("ordersApi reaches %v, want shop.orders.orders and shop.payments", got)
	}

	// Payments' own adapter never reaches back into Orders.
	got = ReachedPackages(program, []Entry{{Package: payments, Name: "paymentsApi"}}, loaded.Packages)
	if !got["shop.payments"] || got["shop.orders.orders"] {
		t.Errorf("paymentsApi reaches %v, want shop.payments and not shop.orders.orders", got)
	}

	// Nothing named, nothing reached.
	if got := ReachedPackages(program, nil, loaded.Packages); len(got) != 0 {
		t.Errorf("no entries reach %v", got)
	}
}
