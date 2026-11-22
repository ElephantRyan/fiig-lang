package configcmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fiig-lang/compiler/loader"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
	"fiig-lang/internal/fixture"
	"fiig-lang/tooling/runprogram"
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
		pkg, bare, d, err := resolveEntry(name, root, loaded.Packages, modulePath, nil)
		if err != nil || pkg == nil || bare != "ordersApi" {
			t.Fatalf("%s: resolved to %v %q %v", name, pkg, bare, err)
		}
		if _, isAdapter := d.(*syntax.AdapterDecl); !isAdapter {
			t.Errorf("%s resolved to %T, want an adapter", name, d)
		}
	}
	// A dependency's adapter, by its package path.
	if _, _, _, err := resolveEntry("shop.payments.paymentsApi", root, loaded.Packages, modulePath, nil); err != nil {
		t.Errorf("shop.payments.paymentsApi should resolve: %v", err)
	}
	for _, name := range []string{"orders.nope", "nothere.ordersApi", "ordersApi"} {
		if _, _, _, err := resolveEntry(name, root, loaded.Packages, modulePath, nil); err == nil {
			t.Errorf("%s should not resolve", name)
		}
	}
}

// ADR 0112 open question 4: a bare entry the root doesn't declare is found
// in the module's folder packages when exactly one declares it, and an
// ambiguous one names every spelling that would work.
var foldersModule = map[string]string{
	"fiig.pkg.json": `{"path": "t.app", "kind": "application", "version": "1.0.0"}`,
	"src/orders/OrdersApi.fiig": `adapter http ordersApi {
    GET "/orders/{id}" -> getOrder(id: string)
        produces "application/json"
}`,
	"src/orders/GetOrder.fiig": `workflow getOrder(id: string) -> string {
    return regionOf(id);
}`,
	"src/orders/RegionOf.fiig": `activity regionOf(id: string) -> string {
    return "${id}@${OrderSettings.region}";
}`,
	"src/orders/OrderSettings.fiig": `wire OrderSettings {
    region: string from env "ORDER_REGION"
}`,
	"src/orders/Health.fiig": `workflow health() -> string {
    return "orders";
}`,
	"src/billing/Health.fiig": `workflow health() -> string {
    return "billing";
}`,
	"src/billing/Invoice.fiig": `workflow invoice() -> string {
    return "invoice";
}`,
}

func TestABareEntryIsFoundInAFolderPackage(t *testing.T) {
	dir := t.TempDir()
	for name, src := range fixture.Module(foldersModule) {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	loaded, errs := loader.Load(dir, nil)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	var own []*types.Package
	for _, m := range loaded.Module {
		own = append(own, m.Package)
	}
	files, _, err := runprogram.LoadFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	if diags := runprogram.Check(dir, files, loaded.Packages); len(diags) > 0 {
		t.Fatal(diags)
	}
	root, _ := types.BuildUniverse(files)
	modulePath := loaded.Manifest.Path

	for name, want := range map[string]string{
		"ordersApi": "t.app.orders", "orders.ordersApi": "t.app.orders", "t.app.orders.ordersApi": "t.app.orders",
		"invoice": "t.app.billing", "orders.health": "t.app.orders", "billing.health": "t.app.billing",
	} {
		pkg, _, _, err := resolveEntry(name, root, loaded.Packages, modulePath, own)
		if err != nil || pkg == nil || pkg.Path != want {
			t.Errorf("%s: resolved to %v, %v; want package %s", name, pkg, err, want)
		}
	}

	// The adapter found by its bare name reaches its package's config.
	pkg, bare, d, err := resolveEntry("ordersApi", root, loaded.Packages, modulePath, own)
	if err != nil {
		t.Fatal(err)
	}
	if _, isAdapter := d.(*syntax.AdapterDecl); !isAdapter {
		t.Fatalf("ordersApi resolved to %T, want an adapter", d)
	}
	program := runprogram.Program{Universe: root, Imports: runprogram.BuildImports(files, loaded.Packages), Groups: runprogram.BuildGroups(files, loaded.Packages)}
	vars, err := runprogram.Variables(runprogram.ReachableConfigFrom(program, []runprogram.Entry{{Package: pkg, Name: bare}}), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(vars) != 1 || vars[0].Name != "ORDER_REGION" {
		t.Errorf("ordersApi's variables = %+v, want ORDER_REGION alone", vars)
	}

	_, _, _, err = resolveEntry("health", root, loaded.Packages, modulePath, own)
	if err == nil || !strings.Contains(err.Error(), "name one: billing.health, orders.health") {
		t.Errorf("an ambiguous bare name: got %v, want both spellings listed", err)
	}
	_, _, _, err = resolveEntry("nothing", root, loaded.Packages, modulePath, own)
	if err == nil || !strings.Contains(err.Error(), "folder packages") {
		t.Errorf("a missing name: got %v", err)
	}
}
