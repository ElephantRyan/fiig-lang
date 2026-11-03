package checker

import (
	"testing"

	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// ADR 0043: interfaces, capability groups, and provider values.

// paymentsSrc is one package's worth of payment code: an interface, two
// capability groups that provide it, the error and merchant types they use,
// and a third group for payroll data that a gateway reaches through
// `requires`.
var paymentsSrc = []string{
	"enum PaymentError {\n\tDeclined\n}",
	"enum Region {\n\tEU\n\tUS\n}",
	"model Merchant {\n\tregion: Region\n}",
	"interface PaymentProvider {\n\tcharge(orderId: string, amount: int) -> Result<string, PaymentError>\n}",
	"capability Stripe {\n\tgateway charge(orderId: string, amount: int) -> Result<string, PaymentError>\n\t\trequires Socket\n\t{\n\t\treturn Ok(orderId)\n\t}\n}",
	"capability Square {\n\tgateway charge(orderId: string, amount: int) -> Result<string, PaymentError>\n\t\trequires Socket\n\t{\n\t\treturn Ok(orderId)\n\t}\n}",
	"capability PayrollDataApi {\n\tgateway getEmployee(id: string) -> Result<string, PaymentError>\n\t\trequires Socket\n\t{\n\t\treturn Ok(id)\n\t}\n}",
	"rule choosePaymentProvider(merchant: Merchant) -> PaymentProvider {\n\tif merchant.region == Region.EU {\n\t\treturn Square\n\t}\n\treturn Stripe\n}",
}

func checkShop(t *testing.T, extra ...string) []Diagnostic {
	t.Helper()
	files := parseAll(t, append(append([]string{}, paymentsSrc...), extra...)...)
	return CheckPackage(files, nil, "shop", map[string]string{
		"shop.PaymentProvider": "Stripe",
	})
}

// checkShopBoth is checkShop with both Stripe and Square providing the
// interface. One manifest maps an interface to one group, and Stripe and
// Square would ship as two packages; here they share one, so each is bound
// separately against the same universe.
func checkShopBoth(t *testing.T, extra ...string) []Diagnostic {
	t.Helper()
	files := parseAll(t, append(append([]string{}, paymentsSrc...), extra...)...)
	universe, _ := types.BuildUniverse(files)
	own := &types.Package{Universe: universe}
	for _, g := range []string{"Stripe", "Square"} {
		if errs := types.BindProvides("shop", own, nil, map[string]string{"shop.PaymentProvider": g}); len(errs) > 0 {
			t.Fatalf("binding %s: %v", g, errs)
		}
	}
	var out []Diagnostic
	for _, f := range files {
		out = append(out, CheckFile(f, universe, nil)...)
	}
	return out
}

func TestProviderSelectionAndCapabilityGroupsCheckClean(t *testing.T) {
	diags := checkShopBoth(t,
		"activity processPayment(merchant: Merchant, orderId: string, amount: int) -> Result<string, PaymentError> {\n\tProvider = choosePaymentProvider(merchant)\n\treturn Provider.charge(orderId: orderId, amount: amount)\n}",
		"activity refundAll(orderId: string) -> Result<string, PaymentError> {\n\tProviders = [Stripe, Square]\n\tfor Candidate in Providers {\n\t\tcharged = Candidate.charge(orderId: orderId, amount: 1)?\n\t}\n\treturn Ok(orderId)\n}",
		"activity loadPayView(id: string) -> Result<string, PaymentError> {\n\treturn PayrollDataApi.getEmployee(id)\n}",
		"gateway fetchEmployee(id: string) -> Result<string, PaymentError>\n\trequires PayrollDataApi\n{\n\treturn PayrollDataApi.getEmployee(id)\n}",
		"activity chargeWith(Provider: PaymentProvider, orderId: string) -> Result<string, PaymentError> {\n\treturn Provider.charge(orderId: orderId, amount: 5)\n}",
	)
	if len(diags) != 0 {
		t.Errorf("unexpected diagnostics: %+v", diags)
	}
}

func TestReturningAGroupThatDoesNotProvideTheInterfaceIsA1(t *testing.T) {
	// Only Stripe is bound as a provider, so returning Square is a mismatch.
	diags := checkShop(t)
	if got := rules(diags)["A1"]; got != 1 {
		t.Errorf("got %d A1 diagnostics, want 1 (Square provides nothing): %+v", got, diags)
	}
}

func TestProvidesCompletenessAndSignatureAreP1AndP2(t *testing.T) {
	files := parseAll(t,
		"enum PaymentError {\n\tDeclined\n}",
		"interface PaymentProvider {\n\tcharge(orderId: string, amount: int) -> Result<string, PaymentError>\n\trefund(reference: string) -> Result<None, PaymentError>\n}",
		"capability Stripe {\n\tgateway charge(orderId: string, amount: string) -> Result<string, PaymentError>\n\t\trequires Socket\n\t{\n\t\treturn Ok(orderId)\n\t}\n}",
	)
	diags := CheckPackage(files, nil, "shop", map[string]string{"shop.PaymentProvider": "Stripe"})
	got := rules(diags)
	if got["P1"] != 1 || got["P2"] != 1 {
		t.Errorf("got P1=%d P2=%d, want 1 each (missing refund, amount: string): %+v", got["P1"], got["P2"], diags)
	}
}

func TestRuleInvokingAProviderIsP3(t *testing.T) {
	diags := checkShopBoth(t,
		"rule chargeInRule(Provider: PaymentProvider) -> Result<string, PaymentError> {\n\treturn Provider.charge(orderId: \"o\", amount: 1)\n}",
	)
	if got := rules(diags)["P3"]; got != 1 {
		t.Errorf("got %d P3 diagnostics, want 1: %+v", got, diags)
	}
}

func TestRuleCallingACapabilityGroupIsB1(t *testing.T) {
	diags := checkShopBoth(t,
		"rule lookupInRule(id: string) -> Result<string, PaymentError> {\n\treturn PayrollDataApi.getEmployee(id)\n}",
	)
	if got := rules(diags)["B1"]; got != 1 {
		t.Errorf("got %d B1 diagnostics, want 1: %+v", got, diags)
	}
}

func TestCapabilityGroupCycleIsP5(t *testing.T) {
	diags := Check(parseAll(t,
		"capability Ping {\n\tgateway ping(x: int) -> int\n\t\trequires Pong\n\t{\n\t\treturn Pong.pong(x)\n\t}\n}",
		"capability Pong {\n\tgateway pong(x: int) -> int\n\t\trequires Ping\n\t{\n\t\treturn Ping.ping(x)\n\t}\n}",
	), nil)
	if got := rules(diags)["P5"]; got != 2 {
		t.Errorf("got %d P5 diagnostics, want 2 (one per group on the cycle): %+v", got, diags)
	}
}

func TestCapabilityInAModelIsP6(t *testing.T) {
	diags := checkShopBoth(t,
		"model MerchantSettings {\n\tprovider: PaymentProvider\n\tbackups: List<PaymentProvider>\n}",
	)
	if got := rules(diags)["P6"]; got != 2 {
		t.Errorf("got %d P6 diagnostics, want 2: %+v", got, diags)
	}
}

func TestGatewayUsingACapabilityItDoesNotRequireIsP7(t *testing.T) {
	diags := checkShopBoth(t,
		"gateway chargeDirect(orderId: string) -> Result<string, PaymentError>\n\trequires Socket\n{\n\treturn Stripe.charge(orderId: orderId, amount: 1)\n}",
		"gateway chargeVia(Provider: PaymentProvider) -> Result<string, PaymentError>\n\trequires Socket\n{\n\treturn Provider.charge(orderId: \"o\", amount: 1)\n}",
	)
	if got := rules(diags)["P7"]; got != 2 {
		t.Errorf("got %d P7 diagnostics, want 2: %+v", got, diags)
	}
}

func TestCaseRulesAreN1N2N3(t *testing.T) {
	diags := checkShopBoth(t,
		"capability payroll {\n\tgateway Fetch(id: string) -> string\n\t\trequires Socket\n\t{\n\t\treturn id\n\t}\n}",
		"enum Status {\n\tactive\n}",
		"activity mislabeled(merchant: Merchant) -> int {\n\tprovider = choosePaymentProvider(merchant)\n\tTotal = 1\n\treturn Total\n}",
	)
	got := rules(diags)
	if got["N1"] != 1 || got["N2"] != 2 || got["N3"] != 2 {
		t.Errorf("got N1=%d N2=%d N3=%d, want 1, 2, 2: %+v", got["N1"], got["N2"], got["N3"], diags)
	}
}

func TestAliasCollisionsAreG4(t *testing.T) {
	param := mustParse(t, "use company.time as when\n\nrule noop(when: int) -> int {\n\treturn when\n}")
	if got := rules(Check([]*syntax.File{param}, nil))["G4"]; got != 2 {
		t.Errorf("alias vs parameter: got %d G4 diagnostics, want 2 (both sites)", got)
	}
	decl := parseAll(t,
		"use company.time as shipOrder\n\nrule noop(x: int) -> int {\n\treturn x\n}",
		"rule shipOrder(x: int) -> int {\n\treturn x\n}",
	)
	if got := rules(Check(decl, nil))["G4"]; got != 1 {
		t.Errorf("alias vs declaration: got %d G4 diagnostics, want 1", got)
	}
	capitalized := checkShopBoth(t,
		"activity shadowed(merchant: Merchant) -> int {\n\tMerchant = choosePaymentProvider(merchant)\n\treturn 1\n}",
	)
	if got := rules(capitalized)["G4"]; got != 1 {
		t.Errorf("capitalized binding vs model: got %d G4 diagnostics, want 1: %+v", got, capitalized)
	}
}

// buildDep builds one dependency package from sources, binding provides.
func buildDep(t *testing.T, path string, deps types.Packages, provides map[string]string, srcs ...string) *types.Package {
	t.Helper()
	files := parseAll(t, srcs...)
	pkg, diags := BuildPackage(files, deps)
	diags = append(diags, BindProvides(path, pkg.Universe, files, deps, provides)...)
	if len(diags) > 0 {
		t.Fatalf("building %s: %+v", path, diags)
	}
	return pkg
}

// TestCapabilityGroupsAcrossPackages is ADR 0043's layering end to end in
// the checker: acme.http's Client group, acme.payments' interface, acme.stripe
// whose Stripe group requires the imported Client and provides the
// interface, and an application selecting Stripe by path-tail import.
func TestCapabilityGroupsAcrossPackages(t *testing.T) {
	httpPkg := buildDep(t, "acme.http", nil, nil,
		"capability Client {\n\tgateway send(body: string) -> Result<string, string>\n\t\trequires Socket\n\t{\n\t\treturn Ok(body)\n\t}\n}",
		"gateway loose(body: string) -> string\n\trequires Socket\n{\n\treturn body\n}",
	)
	paymentsPkg := buildDep(t, "acme.payments", nil, nil,
		"interface PaymentProvider {\n\tcharge(orderId: string) -> Result<string, string>\n}",
	)
	stripeDeps := types.Packages{"acme.http": httpPkg, "acme.payments": paymentsPkg}
	stripePkg := buildDep(t, "acme.stripe", stripeDeps, map[string]string{"acme.payments.PaymentProvider": "Stripe"},
		"use acme.http.Client\n\ncapability Stripe {\n\tgateway charge(orderId: string) -> Result<string, string>\n\t\trequires Client\n\t{\n\t\treturn Client.send(orderId)\n\t}\n}",
	)
	app := types.Packages{"acme.http": httpPkg, "acme.payments": paymentsPkg, "acme.stripe": stripePkg}

	clean := mustParse(t, "use acme.payments\nuse acme.stripe.Stripe\n\n"+
		"rule chooseProvider(region: string) -> payments.PaymentProvider {\n\treturn Stripe\n}")
	clean2 := mustParse(t, "use acme.stripe.Stripe\n\n"+
		"activity pay(orderId: string) -> Result<string, string> {\n\treturn Stripe.charge(orderId)\n}")
	if diags := Check([]*syntax.File{clean, clean2}, app); len(diags) != 0 {
		t.Errorf("unexpected diagnostics: %+v", diags)
	}

	looseCall := mustParse(t, "use acme.http\n\nactivity bypass(body: string) -> string {\n\treturn http.loose(body)\n}")
	looseImport := mustParse(t, "use acme.http.loose\n\nrule noop(x: int) -> int {\n\treturn x\n}")
	if got := rules(Check([]*syntax.File{looseCall, looseImport}, app))["P4"]; got != 2 {
		t.Errorf("got %d P4 diagnostics, want 2 (call and import of a loose gateway)", got)
	}

	badCase := mustParse(t, "use acme.http as Http\nuse acme.http.Client as client\n\nrule noop(x: int) -> int {\n\treturn x\n}")
	if got := rules(Check([]*syntax.File{badCase}, app))["N1"]; got != 2 {
		t.Errorf("got %d N1 diagnostics, want 2 (capitalized package alias, lowercase group alias)", got)
	}
}
