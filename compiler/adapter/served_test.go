package adapter

import (
	"net/http"
	"strings"
	"testing"

	"fiig-lang/compiler/parser"
	"fiig-lang/compiler/runtime"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// ADR 0079 §6: several adapters on one listener.

var twoDomainsWorkflows = []string{
	`workflow listOrders() -> string {
    return "orders";
}`,
	`workflow showOrder(id: string) -> string {
    return "order ${id}";
}`,
	`workflow showPayment(id: string) -> string {
    return "payment ${id}";
}`,
	`workflow deletePayment(id: string) -> string {
    return "deleted ${id}";
}`,
}

// withWorkflows is twoDomainsWorkflows and then adapters.
func withWorkflows(adapters ...string) []string {
	return append(append([]string{}, twoDomainsWorkflows...), adapters...)
}

// servedFor parses srcs, every one an adapter or workflows, and serves
// every adapter in them on one handler.
func servedFor(t *testing.T, srcs ...string) ([]Served, *types.Universe, *runtime.Interp) {
	t.Helper()
	var files []*syntax.File
	var served []Served
	for _, src := range srcs {
		f, errs := parser.Parse(src)
		if len(errs) > 0 {
			t.Fatalf("parse: %v", errs)
		}
		files = append(files, f)
		for _, d := range f.Decls {
			if a, ok := d.(*syntax.AdapterDecl); ok {
				served = append(served, Served{Decl: a})
			}
		}
	}
	u, conflicts := types.BuildUniverse(files)
	if len(conflicts) > 0 {
		t.Fatalf("conflicts: %v", conflicts)
	}
	return served, u, runtime.New(u, runtime.NewCapabilityRegistry())
}

func TestSeveralAdaptersShareOneHandler(t *testing.T) {
	served, u, in := servedFor(t, withWorkflows(
		"adapter http ordersApi {\n    GET \"/orders\" -> listOrders()\n    GET \"/orders/{id}\" -> showOrder(id: string)\n}",
		"adapter http paymentsApi {\n    GET \"/payments/{id}\" -> showPayment(id: string)\n    DELETE \"/payments/{id}\" -> deletePayment(id: string)\n}",
	)...)
	h, err := HTTPHandlerFor(served, u, in, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{"/orders": `"orders"`, "/orders/7": `"order 7"`, "/payments/9": `"payment 9"`} {
		if code, _, body := serve(h, newGet(path)); code != 200 || strings.TrimSpace(body) != want {
			t.Errorf("GET %s: got %d %s, want %s", path, code, body, want)
		}
	}

	// One CORS policy: a preflight answers with the methods of every
	// adapter's routes on the path.
	policy, err := ParseOrigins([]string{"https://shop.example"})
	if err != nil {
		t.Fatal(err)
	}
	withCORS, err := WithCORSFor(h, served, policy)
	if err != nil {
		t.Fatal(err)
	}
	code, header, _ := serve(withCORS, preflight("/payments/9", "https://shop.example", "DELETE", ""))
	if code != http.StatusNoContent || header.Get("Access-Control-Allow-Methods") != "GET, DELETE" {
		t.Errorf("preflight: got %d, methods %q", code, header.Get("Access-Control-Allow-Methods"))
	}
}

// TestConflictingRoutesStopServing: the same route in two adapters, or two
// patterns that would serve the same requests, is an error naming both
// adapters' routes, before anything is served.
func TestConflictingRoutesStopServing(t *testing.T) {
	cases := []struct {
		name, payments, want string
	}{
		{"same route", "adapter http paymentsApi {\n    GET \"/orders/{id}\" -> showPayment(id: string)\n}", `routes GET "/orders/{id}" in adapter ordersApi and GET "/orders/{id}" in adapter paymentsApi serve the same requests`},
		{"same requests", "adapter http paymentsApi {\n    GET \"/orders/{ref}\" -> showPayment(id: ref)\n}", `route GET "/orders/{ref}" in adapter paymentsApi conflicts with another route`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			served, u, in := servedFor(t, withWorkflows(
				"adapter http ordersApi {\n    GET \"/orders/{id}\" -> showOrder(id: string)\n}",
				tc.payments,
			)...)
			_, err := HTTPHandlerFor(served, u, in, DefaultLimits())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

func newGet(path string) *http.Request {
	r, _ := http.NewRequest(http.MethodGet, path, nil)
	return r
}
