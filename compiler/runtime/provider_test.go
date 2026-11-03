package runtime

import (
	"context"
	"testing"

	"fiig-lang/compiler/checker"
	"fiig-lang/compiler/parser"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// ADR 0043 end to end at runtime.

func parseSources(t *testing.T, srcs ...string) []*syntax.File {
	t.Helper()
	var files []*syntax.File
	for _, src := range srcs {
		f, errs := parser.Parse(src)
		if len(errs) > 0 {
			t.Fatalf("parse: %v\n%s", errs, src)
		}
		files = append(files, f)
	}
	return files
}

func buildProviderPackage(t *testing.T, path string, deps types.Packages, provides map[string]string, srcs ...string) *types.Package {
	t.Helper()
	files := parseSources(t, srcs...)
	pkg, diags := checker.BuildPackage(files, deps)
	diags = append(diags, checker.BindProvides(path, pkg.Universe, files, deps, provides)...)
	if len(diags) > 0 {
		t.Fatalf("building %s: %+v", path, diags)
	}
	return pkg
}

// TestProviderDispatchAcrossPackages runs ADR 0043's layering for real: an
// application rule picks Stripe or Square by region, an activity invokes
// whichever it got through a capitalized provider binding, each group's
// gateway requires acme.http's Client group, and Client's gateway is the
// only thing that touches a host-backed primitive. A local capability group
// is called alongside, and a list of providers is iterated.
func TestProviderDispatchAcrossPackages(t *testing.T) {
	httpPkg := buildProviderPackage(t, "acme.http", nil, nil,
		"capability Client {\n\tgateway send(body: string) -> Result<bytes, SocketError>\n\t\trequires Socket\n\t{\n\t\tconnection = Socket.connectTls(\"pay.example\", 443)?\n\t\tSocket.write(connection, body.utf8())?\n\t\treturn Socket.read(connection)\n\t}\n}",
	)
	paymentsPkg := buildProviderPackage(t, "acme.payments", nil, nil,
		"interface PaymentProvider {\n\tcharge(orderId: string) -> Result<bytes, SocketError>\n}",
	)
	providerDeps := types.Packages{"acme.http": httpPkg, "acme.payments": paymentsPkg}
	provides := map[string]string{"acme.payments.PaymentProvider": "Stripe"}
	stripePkg := buildProviderPackage(t, "acme.stripe", providerDeps, provides,
		"use acme.http.Client\n\ncapability Stripe {\n\tgateway charge(orderId: string) -> Result<bytes, SocketError>\n\t\trequires Client\n\t{\n\t\treturn Client.send(\"stripe:${orderId}\")\n\t}\n}",
	)
	squarePkg := buildProviderPackage(t, "acme.square", providerDeps, map[string]string{"acme.payments.PaymentProvider": "Square"},
		"use acme.http.Client\n\ncapability Square {\n\tgateway charge(orderId: string) -> Result<bytes, SocketError>\n\t\trequires Client\n\t{\n\t\treturn Client.send(\"square:${orderId}\")\n\t}\n}",
	)
	packages := types.Packages{"acme.payments": paymentsPkg, "acme.stripe": stripePkg, "acme.square": squarePkg}

	files := parseSources(t,
		"use acme.payments\nuse acme.stripe.Stripe\nuse acme.square.Square\n\n"+
			"rule choosePaymentProvider(region: string) -> payments.PaymentProvider {\n\tif region == \"EU\" {\n\t\treturn Square\n\t}\n\treturn Stripe\n}",
		"activity pay(region: string, orderId: string) -> Result<bytes, SocketError> {\n\tProvider = choosePaymentProvider(region)\n\treturn Provider.charge(orderId: orderId)\n}",
		"use acme.stripe.Stripe\nuse acme.square.Square\n\n"+
			"activity chargeEverywhere(orderId: string) -> Result<bytes, SocketError> {\n\tlast: bytes = <<>>\n\tfor Candidate in [Stripe, Square] {\n\t\tlast = Candidate.charge(orderId: orderId)?\n\t}\n\treturn Ok(last)\n}",
		"capability Ledger {\n\tgateway record(entry: string) -> Result<None, SocketError>\n\t\trequires Socket\n\t{\n\t\tconnection = Socket.connect(\"ledger\", 7000)?\n\t\treturn Socket.write(connection, entry.utf8())\n\t}\n}",
		"activity log(entry: string) -> Result<None, SocketError> {\n\treturn Ledger.record(entry)\n}",
	)
	if diags := checker.Check(files, packages); len(diags) > 0 {
		t.Fatalf("checker diagnostics: %+v", diags)
	}

	universe, _ := types.BuildUniverse(files)
	// One scripted peer per call below, in call order: the payment peers
	// echo "sent <body>", the ledger only takes its entry.
	pay := func(body string) *SocketPeer {
		return &SocketPeer{Host: "pay.example", Port: 443, Steps: []SocketStep{
			{StartTLS: true}, {Expect: []byte(body)}, {Send: []byte("sent " + body)},
		}}
	}
	script := &ScriptedSocket{Peers: []*SocketPeer{
		pay("square:o1"), pay("stripe:o2"), pay("stripe:o3"), pay("square:o3"),
		{Host: "ledger", Port: 7000, Steps: []SocketStep{{Expect: []byte("e1")}}},
	}}
	caps := NewCapabilityRegistry()
	caps.Register("Socket", SocketProvider{Backend: script})
	in := NewDeterministic(universe, caps, 1)
	in.Imports = map[string]map[string]*types.Package{}
	in.Groups = map[string]map[string]*types.GroupRef{}
	for _, f := range files {
		for _, d := range f.Decls {
			in.Imports[syntax.DeclName(d)] = checker.ResolveFileImports(f, packages)
			in.Groups[syntax.DeclName(d)] = checker.ResolveFileGroups(f, packages)
		}
	}

	for _, tc := range []struct {
		decl string
		args map[string]Value
		want string
	}{
		{"pay", map[string]Value{"region": Str("EU"), "orderId": Str("o1")}, "sent square:o1"},
		{"pay", map[string]Value{"region": Str("US"), "orderId": Str("o2")}, "sent stripe:o2"},
		{"chargeEverywhere", map[string]Value{"orderId": Str("o3")}, "sent square:o3"},
		{"log", map[string]Value{"entry": Str("e1")}, ""},
	} {
		got, fault := in.Call(context.Background(), tc.decl, tc.args)
		if fault != nil {
			t.Fatalf("%s(%v): fault %v", tc.decl, tc.args, fault)
		}
		want := Value(None{})
		if tc.want != "" {
			want = Bytes{B: []byte(tc.want)}
		}
		if !Equal(got, Result{Ok: true, Value: want}) {
			t.Errorf("%s(%v) = %s, want Ok(%s)", tc.decl, tc.args, got, want)
		}
	}
	if err := script.Done(); err != nil {
		t.Fatal(err)
	}
}
