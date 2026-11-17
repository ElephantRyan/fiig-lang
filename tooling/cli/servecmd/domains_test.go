package servecmd

import (
	"bytes"
	"context"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"fiig-lang/compiler/adapter"
	"fiig-lang/compiler/runtime"
	"fiig-lang/tooling/runprogram"
)

// ADR 0079: examples/shop-orders, a monolith of two domains, and
// examples/shop-orders-split, the same application once Payments is its
// own service.

// recorder is a Database that keeps each statement it's given, by the
// name its caps-file entry gives it, so a test can see which domain's
// writes went to which database (§6a).
type recorder struct {
	mu   sync.Mutex
	seen map[string][]string
}

func (r *recorder) factory() runtime.FactoryRegistry {
	return runtime.FactoryRegistry{"recorder": func(cfg map[string]any) (runtime.CapabilityProvider, error) {
		name, _ := cfg["name"].(string)
		return runtime.FuncProvider{"execute": func(_ context.Context, args []runtime.Value) (runtime.Value, *runtime.Fault) {
			r.mu.Lock()
			defer r.mu.Unlock()
			sql := string(args[0].(runtime.Str))
			r.seen[name] = append(r.seen[name], strings.Fields(sql)[2]) // the table
			return runtime.None{}, nil
		}}, nil
	}}
}

func examplesDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", "..", "..", "examples"))
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func post(t *testing.T, h http.Handler, path, body string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code, strings.TrimSpace(rec.Body.String())
}

// TestMonolithServesBothDomains is §6 and §6a: one listener serves
// Orders' adapter and Payments', Orders reaches Payments through a
// gateway (§1), and each domain writes to its own Database.
func TestMonolithServesBothDomains(t *testing.T) {
	examples := examplesDir(t)
	r := &recorder{seen: map[string][]string{}}
	caps := writeFile(t, "caps.json", `{
		"Database": {"type": "recorder", "name": "orders"},
		"packages": {"shop.payments": {"Database": {"type": "recorder", "name": "payments"}}}
	}`)
	env, _ := runprogram.NewEnvironment("")
	h, bindings, _, err := load(filepath.Join(examples, "shop-orders"), []string{"ordersApi", "shop.payments.paymentsApi"}, caps, []string{examples}, r.factory(), nil, adapter.DefaultLimits(), env, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(bindings, "\n"); got != "shop.payments: Database (its own binding)" {
		t.Errorf("bindings: got %q", got)
	}

	if code, body := post(t, h, "/orders/o-1", `{"amount": 42.50, "currency": "EUR"}`); code != 201 || body != `{"order":"o-1","charge":{"id":"pay-o-1","refundable":true}}` {
		t.Errorf("order: got %d %s", code, body)
	}
	if code, body := post(t, h, "/orders/o-2", `{"amount": 5000, "currency": "EUR"}`); code != 422 || body != `{"variant":"Declined","reason":"over the limit for one payment"}` {
		t.Errorf("declined order: got %d %s", code, body)
	}
	if code, body := post(t, h, "/payments/p-1", `{"amount": 700, "currency": "EUR"}`); code != 200 || !strings.Contains(body, `"refundable":false`) {
		t.Errorf("Payments' own route: got %d %s", code, body)
	}
	want := map[string][]string{"orders": {"orders"}, "payments": {"charges", "charges"}}
	if !equalSeen(r.seen, want) {
		t.Errorf("writes: got %v, want %v (each domain in its own database)", r.seen, want)
	}
}

// TestDomainSharesTheDatabaseWithoutABinding is §6a's fallback: without a
// "packages" entry Payments uses the process-wide Database, and the
// startup listing says so.
func TestDomainSharesTheDatabaseWithoutABinding(t *testing.T) {
	examples := examplesDir(t)
	r := &recorder{seen: map[string][]string{}}
	caps := writeFile(t, "caps.json", `{"Database": {"type": "recorder", "name": "orders"}}`)
	env, _ := runprogram.NewEnvironment("")
	h, bindings, _, err := load(filepath.Join(examples, "shop-orders"), []string{"ordersApi"}, caps, []string{examples}, r.factory(), nil, adapter.DefaultLimits(), env, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(bindings, "\n"); got != "shop.payments: Database (shared with the application)" {
		t.Errorf("bindings: got %q", got)
	}
	if code, body := post(t, h, "/orders/o-1", `{"amount": 10, "currency": "EUR"}`); code != 201 {
		t.Fatalf("order: got %d %s", code, body)
	}
	if want := map[string][]string{"orders": {"charges", "orders"}}; !equalSeen(r.seen, want) {
		t.Errorf("writes: got %v, want %v", r.seen, want)
	}
}

// TestServeRefusesWhatItCantServe: a dependency the root doesn't have,
// an adapter named twice, and a caps-file binding for a package that
// isn't a dependency (§6a, like ADR 0071 §5's stale rename).
func TestServeRefusesWhatItCantServe(t *testing.T) {
	examples := examplesDir(t)
	root := filepath.Join(examples, "shop-orders")
	env, _ := runprogram.NewEnvironment("")
	cases := []struct {
		names []string
		caps  string
		want  string
	}{
		{[]string{"ordersApi", "shop.billing.billingApi"}, `{}`, "doesn't depend on a package shop.billing"},
		{[]string{"ordersApi", "shop.payments.nope"}, `{}`, `package shop.payments has no adapter named "nope"`},
		{[]string{"ordersApi", "ordersApi"}, `{}`, "named twice"},
		{[]string{"ordersApi"}, `{"packages": {"shop.billing": {"Database": {"type": "recorder"}}}}`, "binds for package shop.billing, which the application doesn't depend on"},
	}
	for _, tc := range cases {
		r := &recorder{seen: map[string][]string{}}
		_, _, _, err := load(root, tc.names, writeFile(t, "caps.json", tc.caps), []string{examples}, r.factory(), nil, adapter.DefaultLimits(), env, nil)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%v %s: got %v, want an error containing %q", tc.names, tc.caps, err, tc.want)
		}
	}
}

// TestSplitChangesOnlyTheGateway is §3 and §7: shop-orders-split is
// shop-orders with Payments moved out. Every file but the gateway and
// the deployment's own (manifest, lockfile, caps file, Payments' URL) is
// byte for byte the same, and it answers as the monolith does, against
// Payments served alone.
func TestSplitChangesOnlyTheGateway(t *testing.T) {
	examples := examplesDir(t)
	monolith, split := filepath.Join(examples, "shop-orders"), filepath.Join(examples, "shop-orders-split")
	changed := map[string]bool{
		"gateways/charge_order.fiig":   true,
		"fiig.pkg.json":                true,
		"fiig.lock.json":               true,
		"caps.json":                    true,
		"config/payments_service.fiig": true,
	}
	a, b := filesUnder(t, monolith), filesUnder(t, split)
	for name := range changed {
		delete(a, name)
		delete(b, name)
	}
	for name, content := range a {
		other, ok := b[name]
		if !ok {
			t.Errorf("%s is in shop-orders but not shop-orders-split", name)
		} else if !bytes.Equal(content, other) {
			t.Errorf("%s differs between shop-orders and shop-orders-split; only the gateway should", name)
		}
		delete(b, name)
	}
	for name := range b {
		t.Errorf("%s is only in shop-orders-split", name)
	}

	r := &recorder{seen: map[string][]string{}}
	env, _ := runprogram.NewEnvironment("")
	payments, _, _, err := load(filepath.Join(examples, "shop-payments"), []string{"paymentsApi"}, writeFile(t, "caps.json", `{"Database": {"type": "recorder", "name": "payments"}}`), []string{examples}, r.factory(), nil, adapter.DefaultLimits(), env, nil)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(payments)
	defer server.Close()
	t.Setenv("PAYMENTS_URL", server.URL)
	orders, _, _, err := load(split, []string{"ordersApi"}, writeFile(t, "caps.json", `{"Database": {"type": "recorder", "name": "orders"}, "Socket": {"type": "socket"}}`), []string{examples}, r.factory(), nil, adapter.DefaultLimits(), env, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ order, body, want string }{
		{"o-1", `{"amount": 42.50, "currency": "EUR"}`, `201 {"order":"o-1","charge":{"id":"pay-o-1","refundable":true}}`},
		{"o-2", `{"amount": 5000, "currency": "EUR"}`, `422 {"variant":"Declined","reason":"over the limit for one payment"}`},
		{"o-3", `{"amount": 0, "currency": "EUR"}`, `422 {"variant":"InvalidAmount"}`},
	} {
		code, body := post(t, orders, "/orders/"+tc.order, tc.body)
		if got := strings.TrimSpace(strconv.Itoa(code) + " " + body); got != tc.want {
			t.Errorf("%s: got %s, want %s", tc.order, got, tc.want)
		}
	}
	if want := map[string][]string{"orders": {"orders"}, "payments": {"charges"}}; !equalSeen(r.seen, want) {
		t.Errorf("writes: got %v, want %v", r.seen, want)
	}

	server.Close()
	if code, body := post(t, orders, "/orders/o-4", `{"amount": 5, "currency": "EUR"}`); code != 503 || !strings.Contains(body, "Unavailable") {
		t.Errorf("Payments down: got %d %s, want 503 Unavailable", code, body)
	}
}

func filesUnder(t *testing.T, root string) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		content, err := os.ReadFile(path)
		out[filepath.ToSlash(rel)] = content
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func equalSeen(got, want map[string][]string) bool {
	if len(got) != len(want) {
		return false
	}
	for name, tables := range want {
		g := append([]string(nil), got[name]...)
		sort.Strings(g)
		if strings.Join(g, ",") != strings.Join(tables, ",") {
			return false
		}
	}
	return true
}
