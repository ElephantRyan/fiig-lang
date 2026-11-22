package servecmd

import (
	"fiig-lang/tooling/migrate"
	"os"
	"path/filepath"
	"testing"

	"fiig-lang/compiler/adapter"
	"fiig-lang/internal/fixture"
	"fiig-lang/tooling/runprogram"
)

// ADR 0079 §6a: a binding follows the workflow that's running, not the
// gateway's own package. Payments calls an audit library's gateway, which
// requires Database; it writes to Payments' database, because Payments'
// workflow is what it runs for.
var followsProgram = map[string]string{
	"app/fiig.pkg.json": `{"path": "t.app", "kind": "application", "version": "1.0.0",
		"dependencies": {"t.pay": "1.0.0"}, "capabilities": ["Database"], "nativeCapabilities": ["Database"]}`,
	"app/charge.fiig":   "use t.pay;\n\ngateway charge(id: string) -> string\n    requires pay\n{\n    return pay.takePayment(id: id);\n}\n",
	"app/checkout.fiig": "workflow checkout(id: string) -> string {\n    return payFor(id);\n}\n",
	"app/pay_step.fiig": "activity payFor(id: string) -> string {\n    return charge(id: id);\n}\n",
	"app/api.fiig":      "adapter http api {\n    POST \"/checkout/{id}\" -> checkout(id: string)\n        produces \"application/json\";\n}\n",

	"pay/fiig.pkg.json": `{"path": "t.pay", "kind": "library", "version": "1.0.0",
		"dependencies": {"t.audit": "1.0.0"}, "capabilities": ["Database"], "nativeCapabilities": ["Database"]}`,
	"pay/take_payment.fiig":   "workflow takePayment(id: string) -> string {\n    payStep(id);\n    return \"paid ${id}\";\n}\n",
	"pay/pay_step.fiig":       "activity payStep(id: string) -> None {\n    return recordPayment(id: id);\n}\n",
	"pay/record_payment.fiig": "use t.audit.Audit;\n\ngateway recordPayment(id: string) -> None\n    requires Audit\n{\n    Audit.write(id: id);\n    return None;\n}\n",

	"audit/fiig.pkg.json": `{"path": "t.audit", "kind": "library", "version": "1.0.0",
		"capabilities": ["Database"], "nativeCapabilities": ["Database"]}`,
	"audit/audit.fiig": "capability Audit {\n    gateway write(id: string) -> None\n        requires Database\n    {\n        Database.execute(\"INSERT INTO audit (id) VALUES (?)\", id);\n        return None;\n    }\n}\n",
}

// TestServingADependencyTheRootNeverImports is §6 for an application
// whose own code never names the domain it serves: the adapter's route
// types and the workflow's names resolve in the domain's package.
func TestServingADependencyTheRootNeverImports(t *testing.T) {
	workspace := t.TempDir()
	program := map[string]string{
		"app/fiig.pkg.json": `{"path": "t.app", "kind": "application", "version": "1.0.0", "dependencies": {"t.pay": "1.0.0"}}`,
		"app/health.fiig":   "workflow health() -> string {\n    return \"ok\";\n}\n",
		"app/api.fiig":      "adapter http api {\n    GET \"/health\" -> health()\n        produces \"application/json\";\n}\n",

		"pay/fiig.pkg.json": `{"path": "t.pay", "kind": "library", "version": "1.0.0", "dependencies": {"t.kernel": "1.0.0"}}`,
		"pay/quote.fiig":    "use t.kernel;\n\nworkflow quote(id: string, amount: kernel.Money) -> string {\n    return \"${id}: ${amount.amount} ${amount.currency}\";\n}\n",
		"pay/pay_api.fiig":  "use t.kernel;\n\nadapter http payApi {\n    POST \"/pay/{id}\" -> quote(id: string, amount: kernel.Money)\n        consumes \"application/json\"\n        produces \"application/json\";\n}\n",

		"kernel/fiig.pkg.json": `{"path": "t.kernel", "kind": "library", "version": "1.0.0"}`,
		"kernel/money.fiig":    "model Money {\n    amount: decimal;\n    currency: string;\n}\n",
	}
	writeTree(t, workspace, program)
	env, _ := runprogram.NewEnvironment("")
	h, _, _, _, err := load(filepath.Join(workspace, "app"), []string{"api", "t.pay.payApi"}, "", []string{workspace}, nil, nil, adapter.DefaultLimits(), env, nil, migrate.Apply)
	if err != nil {
		t.Fatal(err)
	}
	if code, body := post(t, h, "/pay/p-1", `{"amount": 9.50, "currency": "EUR"}`); code != 200 || body != `"p-1: 9.50 EUR"` {
		t.Errorf("got %d %s", code, body)
	}
}

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, src := range fixture.Module(files) {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestBindingFollowsTheRunningWorkflow(t *testing.T) {
	workspace := t.TempDir()
	for name, src := range fixture.Module(followsProgram) {
		path := filepath.Join(workspace, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	r := &recorder{seen: map[string][]string{}}
	caps := writeFile(t, "caps.json", `{
		"Database": {"type": "recorder", "name": "app"},
		"packages": {"t.pay": {"Database": {"type": "recorder", "name": "pay"}}}
	}`)
	env, _ := runprogram.NewEnvironment("")
	h, _, _, _, err := load(filepath.Join(workspace, "app"), []string{"api"}, caps, []string{workspace}, r.factory(), nil, adapter.DefaultLimits(), env, nil, migrate.Apply)
	if err != nil {
		t.Fatal(err)
	}
	if code, body := post(t, h, "/checkout/c-1", ""); code != 200 || body != `"paid c-1"` {
		t.Fatalf("checkout: got %d %s", code, body)
	}
	if want := map[string][]string{"pay": {"audit"}}; !equalSeen(r.seen, want) {
		t.Errorf("writes: got %v, want %v: the audit library ran for t.pay's workflow", r.seen, want)
	}
}
