package configcmd

import (
	"path/filepath"
	"testing"

	"fiig-lang/compiler/loader"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// abs-recommendations G44: an entry may name an adapter in a folder package,
// in full or relative to the module, as `fiig serve` does.
func TestAnEntryNamesAFolderPackagesAdapter(t *testing.T) {
	examples, err := filepath.Abs(filepath.Join("..", "..", "..", "examples"))
	if err != nil {
		t.Fatal(err)
	}
	loaded, errs := loader.Load(filepath.Join(examples, "shop-orders"), []string{examples})
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	root, _ := types.BuildUniverse(nil)
	modulePath := loaded.Manifest.Path

	for _, name := range []string{"orders.ordersApi", modulePath + ".orders.ordersApi"} {
		pkg, bare, d, ok := resolveEntry(name, root, loaded.Packages, modulePath)
		if !ok || pkg == nil || bare != "ordersApi" {
			t.Fatalf("%s: resolved to %v %q %v", name, pkg, bare, ok)
		}
		if _, isAdapter := d.(*syntax.AdapterDecl); !isAdapter {
			t.Errorf("%s resolved to %T, want an adapter", name, d)
		}
	}
	// A dependency's adapter, by its package path.
	if _, _, _, ok := resolveEntry("shop.payments.paymentsApi", root, loaded.Packages, modulePath); !ok {
		t.Error("shop.payments.paymentsApi should resolve")
	}
	for _, name := range []string{"orders.nope", "nothere.ordersApi", "ordersApi"} {
		if _, _, _, ok := resolveEntry(name, root, loaded.Packages, modulePath); ok {
			t.Errorf("%s should not resolve", name)
		}
	}
}
