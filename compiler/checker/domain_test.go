package checker

import (
	"strings"
	"testing"

	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// paymentsDomain is a domain package with a workflow, for ADR 0079.
func paymentsDomain(t *testing.T) types.Packages {
	t.Helper()
	pkg := buildDep(t, "shop.payments", nil, nil,
		"enum PaymentError {\n\tDeclined\n}",
		"model Receipt {\n\tid: string\n}",
		"workflow takePayment(reference: string) -> Result<Receipt, PaymentError> {\n\treturn Ok(Receipt(id: reference));\n}",
	)
	return types.Packages{"shop.payments": pkg}
}

// TestGatewayCallsAnotherPackagesWorkflow is ADR 0079 §1: a gateway that
// requires an imported package may call that package's workflows.
func TestGatewayCallsAnotherPackagesWorkflow(t *testing.T) {
	app := paymentsDomain(t)
	gw := mustParse(t, "use shop.payments\n\n"+
		"gateway charge(order: string) -> Result<string, string>\n\trequires payments\n{\n"+
		"\treturn match (payments.takePayment(reference: order)) {\n\t\tOk(receipt) -> Ok(receipt.id)\n\t\tErr(failure) -> Err(\"declined\")\n\t}\n}")
	if diags := Check([]*syntax.File{gw}, app); len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %+v", diags)
	}

	aliased := mustParse(t, "use shop.payments as pay\n\n"+
		"gateway charge(order: string) -> Result<pay.Receipt, pay.PaymentError>\n\trequires pay\n{\n\treturn pay.takePayment(reference: order);\n}")
	if diags := Check([]*syntax.File{aliased}, app); len(diags) != 0 {
		t.Fatalf("aliased: unexpected diagnostics: %+v", diags)
	}
}

// TestForeignWorkflowNeedsTheGatewaysRequires is ADR 0079 §1's refusals: a
// gateway requiring something else is B4, an activity or workflow is B8.
func TestForeignWorkflowNeedsTheGatewaysRequires(t *testing.T) {
	app := paymentsDomain(t)
	cases := []struct {
		name, src, rule, want string
	}{
		{"gateway requiring Database", "use shop.payments\n\ngateway charge(order: string) -> Result<payments.Receipt, payments.PaymentError>\n\trequires Database\n{\n\treturn payments.takePayment(reference: order);\n}", "B4", "only if it requires payments"},
		{"activity", "use shop.payments\n\nactivity charge(order: string) -> Result<payments.Receipt, payments.PaymentError> {\n\treturn payments.takePayment(reference: order);\n}", "B8", "through a gateway that requires payments"},
		{"workflow", "use shop.payments\n\nworkflow charge(order: string) -> Result<payments.Receipt, payments.PaymentError> {\n\treturn payments.takePayment(reference: order);\n}", "B8", "through a gateway that requires payments"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			diags := Check([]*syntax.File{mustParse(t, tc.src)}, app)
			if len(diags) != 1 || diags[0].Rule != tc.rule || !strings.Contains(diags[0].Msg, tc.want) {
				t.Fatalf("want one %s containing %q, got %+v", tc.rule, tc.want, diags)
			}
		})
	}
}

// TestOwnWorkflowStaysB4 is ADR 0079 §2: within a package a gateway still
// may not call a workflow, and the message says where one may be called.
func TestOwnWorkflowStaysB4(t *testing.T) {
	files := parseAll(t,
		"workflow ship(order: string) -> string {\n\treturn order;\n}",
		"gateway notify(order: string) -> string\n\trequires Socket\n{\n\treturn ship(order);\n}",
	)
	diags := Check(files, nil)
	if len(diags) != 1 || diags[0].Rule != "B4" || !strings.Contains(diags[0].Msg, "another package's gateway") {
		t.Fatalf("want one B4 naming ADR 0079 §2, got %+v", diags)
	}
}

// TestRequiredPackageIsOneThing is ADR 0010 under ADR 0079: a gateway
// requiring a package uses no capability (P7) and isn't called inside a
// transaction on one (C4).
func TestRequiredPackageIsOneThing(t *testing.T) {
	app := paymentsDomain(t)
	usesSocket := mustParse(t, "use shop.payments\n\n"+
		"gateway charge(order: string) -> Result<payments.Receipt, payments.PaymentError>\n\trequires payments\n{\n"+
		"\tconnection = Socket.connect(order, 443);\n\treturn payments.takePayment(reference: order);\n}")
	if diags := Check([]*syntax.File{usesSocket}, app); rules(diags)["P7"] != 1 {
		t.Errorf("want one P7: a gateway requiring a package may not also use Socket; got %+v", diags)
	}
	inTransaction := parseAll(t,
		"use shop.payments\n\ngateway charge(order: string) -> Result<payments.Receipt, payments.PaymentError>\n\trequires payments\n{\n\treturn payments.takePayment(reference: order);\n}",
		"use shop.payments\n\nactivity checkout(order: string) -> Result<None, string> {\n\ttransaction Database {\n\t\tcharge(order);\n\t}\n\treturn Ok(None);\n}",
	)
	if got := rules(Check(inTransaction, app))["C4"]; got != 1 {
		t.Errorf("got %d C4, want 1: another domain's gateway can't join a Database transaction", got)
	}
}
