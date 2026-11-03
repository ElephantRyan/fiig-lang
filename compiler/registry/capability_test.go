package registry

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestInterfaceAndCapabilityCards checks ADR 0043's cards: an interface
// card lists its operations and, from the manifest, the groups that fulfill
// it; a capability card lists its gateways and what it provides; and an
// activity that calls a capability group directly is not pure, even though
// it calls no gateway by name.
func TestInterfaceAndCapabilityCards(t *testing.T) {
	root := t.TempDir()
	write := func(name, src string) {
		if err := os.WriteFile(filepath.Join(root, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("fiig.pkg.json", `{"path": "shop", "kind": "application", "version": "1.0.0", "provides": {"shop.PaymentProvider": "Stripe"}}`)
	write("payment_provider.fiig", "interface PaymentProvider {\n\tcharge(orderId: string) -> Result<string, string>\n}\n")
	write("stripe.fiig", "capability Stripe {\n\tgateway charge(orderId: string) -> Result<string, string>\n\t\trequires Socket\n\t\tidempotent\n\t{\n\t\treturn Ok(Socket.write(orderId))\n\t}\n}\n")
	write("pay.fiig", "activity pay(orderId: string) -> Result<string, string> {\n\treturn Stripe.charge(orderId)\n}\n")
	write("echo.fiig", "activity echo(orderId: string) -> string {\n\treturn orderId\n}\n")

	cards, fileErrs, err := Generate(root)
	if err != nil || len(fileErrs) > 0 {
		t.Fatalf("Generate: %v %+v", err, fileErrs)
	}
	byName := map[string]*Card{}
	for _, c := range cards {
		byName[c.Name] = c
	}

	iface := byName["PaymentProvider"]
	if iface == nil || iface.Kind != "interface" || len(iface.Operations) != 1 || iface.Operations[0].Name != "charge" {
		t.Fatalf("interface card: %+v", iface)
	}
	if !reflect.DeepEqual(iface.Fulfillers, []string{"Stripe"}) {
		t.Errorf("interface Fulfillers = %v, want [Stripe]", iface.Fulfillers)
	}

	group := byName["Stripe"]
	if group == nil || group.Kind != "capability" {
		t.Fatalf("capability card: %+v", group)
	}
	if op := group.Operations[0]; op.Requires != "Socket" || !op.Idempotent || op.Error != "string" {
		t.Errorf("capability operation = %+v", op)
	}
	if !reflect.DeepEqual(group.Provides, []string{"shop.PaymentProvider"}) {
		t.Errorf("capability Provides = %v", group.Provides)
	}

	if pay := byName["pay"]; pay.Pure || !reflect.DeepEqual(pay.Uses, []string{"Stripe.charge"}) {
		t.Errorf("pay: Pure=%v Uses=%v, want impure using Stripe.charge", pay.Pure, pay.Uses)
	}
	if echo := byName["echo"]; !echo.Pure {
		t.Errorf("echo should still be pure")
	}
}
