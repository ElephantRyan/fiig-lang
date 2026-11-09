package registry

import (
	"strings"
	"testing"

	"fiig-lang/compiler/semver"
)

func changeByName(changes []Change, name string) *Change {
	for i := range changes {
		if changes[i].Name == name {
			return &changes[i]
		}
	}
	return nil
}

func TestDiffNoChangeIsPatch(t *testing.T) {
	cards := []*Card{{Name: "getCustomer", Kind: "gateway", Params: []Param{{Name: "id", Type: "CustomerId"}}, Returns: "Result<Customer, CustomerError>", Requires: "Database"}}
	bump, changes := Diff(cards, cards)
	if bump != Patch {
		t.Fatalf("want Patch, got %s (changes: %+v)", bump, changes)
	}
	if len(changes) != 0 {
		t.Fatalf("want no changes, got %+v", changes)
	}
}

func TestDiffAdditionIsMinor(t *testing.T) {
	old := []*Card{{Name: "getCustomer", Kind: "gateway"}}
	newer := []*Card{{Name: "getCustomer", Kind: "gateway"}, {Name: "deleteCustomer", Kind: "gateway"}}
	bump, changes := Diff(old, newer)
	if bump != Minor {
		t.Fatalf("want Minor, got %s", bump)
	}
	c := changeByName(changes, "deleteCustomer")
	if c == nil || c.Bump != Minor {
		t.Fatalf("want a Minor addition for deleteCustomer, got %+v", changes)
	}
}

func TestDiffRemovalIsMajor(t *testing.T) {
	old := []*Card{{Name: "getCustomer", Kind: "gateway"}, {Name: "deleteCustomer", Kind: "gateway"}}
	newer := []*Card{{Name: "getCustomer", Kind: "gateway"}}
	bump, changes := Diff(old, newer)
	if bump != Major {
		t.Fatalf("want Major, got %s", bump)
	}
	c := changeByName(changes, "deleteCustomer")
	if c == nil || c.Bump != Major {
		t.Fatalf("want a Major removal for deleteCustomer, got %+v", changes)
	}
}

func TestDiffParamChangeIsMajor(t *testing.T) {
	old := []*Card{{Name: "getCustomer", Kind: "gateway", Params: []Param{{Name: "id", Type: "CustomerId"}}}}
	newer := []*Card{{Name: "getCustomer", Kind: "gateway", Params: []Param{{Name: "id", Type: "string"}}}}
	bump, _ := Diff(old, newer)
	if bump != Major {
		t.Fatalf("want Major for a parameter type change, got %s", bump)
	}
}

func TestDiffParamRenameIsMajor(t *testing.T) {
	// Fiig supports named-argument calls (docs/decisions and the
	// name:Type convention), so renaming a parameter breaks any call
	// site using it by name even though the type didn't change.
	old := []*Card{{Name: "getCustomer", Kind: "gateway", Params: []Param{{Name: "id", Type: "CustomerId"}}}}
	newer := []*Card{{Name: "getCustomer", Kind: "gateway", Params: []Param{{Name: "customerId", Type: "CustomerId"}}}}
	bump, _ := Diff(old, newer)
	if bump != Major {
		t.Fatalf("want Major for a parameter rename, got %s", bump)
	}
}

func TestDiffGatewayLosesIdempotentIsMajor(t *testing.T) {
	old := []*Card{{Name: "getCustomer", Kind: "gateway", Idempotent: true}}
	newer := []*Card{{Name: "getCustomer", Kind: "gateway", Idempotent: false}}
	bump, _ := Diff(old, newer)
	if bump != Major {
		t.Fatalf("want Major for losing idempotent, got %s", bump)
	}
}

func TestDiffGatewayGainsIdempotentIsMinor(t *testing.T) {
	old := []*Card{{Name: "getCustomer", Kind: "gateway", Idempotent: false}}
	newer := []*Card{{Name: "getCustomer", Kind: "gateway", Idempotent: true}}
	bump, _ := Diff(old, newer)
	if bump != Minor {
		t.Fatalf("want Minor for gaining idempotent, got %s", bump)
	}
}

func TestDiffModelFieldRemovedIsMajorAddedIsMinor(t *testing.T) {
	old := []*Card{{Name: "Customer", Kind: "model", Fields: []Param{{Name: "id", Type: "CustomerId"}, {Name: "name", Type: "string"}}}}
	newer := []*Card{{Name: "Customer", Kind: "model", Fields: []Param{{Name: "id", Type: "CustomerId"}, {Name: "email", Type: "string"}}}}
	bump, changes := Diff(old, newer)
	if bump != Major {
		t.Fatalf("want Major (name removed), got %s: %+v", bump, changes)
	}
	var sawRemoved, sawAdded bool
	for _, c := range changes {
		if c.Bump == Major {
			sawRemoved = true
		}
		if c.Bump == Minor {
			sawAdded = true
		}
	}
	if !sawRemoved || !sawAdded {
		t.Fatalf("want both a removal and an addition, got %+v", changes)
	}
}

func TestDiffKindChangeIsMajor(t *testing.T) {
	old := []*Card{{Name: "x", Kind: "rule"}}
	newer := []*Card{{Name: "x", Kind: "activity"}}
	bump, changes := Diff(old, newer)
	if bump != Major || len(changes) != 1 {
		t.Fatalf("want a single Major kind-change, got %s: %+v", bump, changes)
	}
}

func TestDiffEnumVariantChanges(t *testing.T) {
	notFound := Variant{Name: "NotFound", Fields: []Param{{Name: "id", Type: "string"}}}
	old := []*Card{{Name: "CustomerError", Kind: "enum", Variants: []Variant{notFound}}}
	newer := []*Card{{Name: "CustomerError", Kind: "enum", Variants: []Variant{notFound, {Name: "Invalid", Fields: []Param{{Name: "reason", Type: "string"}}}}}}
	bump, _ := Diff(old, newer)
	if bump != Minor {
		t.Fatalf("want Minor for a new enum variant, got %s", bump)
	}
	plain := []*Card{{Name: "Region", Kind: "enum", Variants: []Variant{{Name: "US"}}}}
	plainer := []*Card{{Name: "Region", Kind: "enum", Variants: []Variant{{Name: "US"}, {Name: "EU"}}}}
	if bump, _ := Diff(plain, plainer); bump != Minor {
		t.Fatalf("want Minor for a new payload-free variant, got %s", bump)
	}
}

// ADR 0072 §2: an enum's first payload turns `"US"` into
// `{"variant": "US"}` for every client.
func TestDiffEnumFirstPayloadIsMajor(t *testing.T) {
	old := []*Card{{Name: "Region", Kind: "enum", Variants: []Variant{{Name: "US"}}}}
	newer := []*Card{{Name: "Region", Kind: "enum", Variants: []Variant{{Name: "US"}, {Name: "Other", Fields: []Param{{Name: "name", Type: "string"}}}}}}
	bump, changes := Diff(old, newer)
	if bump != Major {
		t.Fatalf("want Major for a first payload, got %s: %+v", bump, changes)
	}
}

func TestDiffAdapterRouteChanges(t *testing.T) {
	old := []*Card{{Name: "httpApi", Kind: "adapter", Routes: []string{"GET /customers/{id}"}}}
	newer := []*Card{{Name: "httpApi", Kind: "adapter", Routes: []string{"GET /customers/{customerId}"}}}
	bump, changes := Diff(old, newer)
	if bump != Major {
		t.Fatalf("want Major for a changed route path, got %s: %+v", bump, changes)
	}
}

func TestDiffDependenciesIgnored(t *testing.T) {
	old := []*Card{{Name: "placeOrder", Kind: "workflow", Dependencies: []string{"chargePayment"}}}
	newer := []*Card{{Name: "placeOrder", Kind: "workflow", Dependencies: []string{"chargePayment", "sendReceipt"}}}
	bump, changes := Diff(old, newer)
	if bump != Patch || len(changes) != 0 {
		t.Fatalf("want Dependencies-only changes ignored, got %s: %+v", bump, changes)
	}
}

func TestCheckBumpAcceptsSufficientBump(t *testing.T) {
	old := mustVersion(t, "1.0.0")
	newer := mustVersion(t, "2.0.0")
	if err := CheckBump(old, newer, Major); err != nil {
		t.Fatalf("want no error, got %v", err)
	}
}

func TestCheckBumpRejectsInsufficientBump(t *testing.T) {
	old := mustVersion(t, "1.0.0")
	newer := mustVersion(t, "1.0.1")
	if err := CheckBump(old, newer, Major); err == nil {
		t.Fatal("want an error: a patch bump doesn't satisfy a required major bump")
	}
}

func TestCheckBumpAllowsMoreThanRequired(t *testing.T) {
	old := mustVersion(t, "1.0.0")
	newer := mustVersion(t, "2.0.0")
	if err := CheckBump(old, newer, Minor); err != nil {
		t.Fatalf("want no error: over-bumping is fine, got %v", err)
	}
}

func TestCheckBumpRejectsNonIncreasingVersion(t *testing.T) {
	old := mustVersion(t, "1.0.0")
	newer := mustVersion(t, "1.0.0")
	if err := CheckBump(old, newer, Patch); err == nil {
		t.Fatal("want an error: new version must be greater than old")
	}
}

func mustVersion(t *testing.T, s string) semver.Version {
	t.Helper()
	v, err := semver.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// ADR 0071 §5: a release that reads a new environment variable is
// reported, Major when every deployment must now set it.
func TestDiffReportsNewEnvironmentVariables(t *testing.T) {
	old := []*Card{{Name: "Stripe", Kind: "config", Environment: []EnvVar{{Name: "STRIPE_API_KEY", Type: "Secret", Secret: true}}}}
	newer := []*Card{
		{Name: "Stripe", Kind: "config", Environment: []EnvVar{
			{Name: "STRIPE_API_KEY", Type: "Secret", Secret: true},
			{Name: "AWS_SECRET_ACCESS_KEY", Type: "Secret", Secret: true},
			{Name: "STRIPE_TIMEOUT", Type: "int", Default: "10"},
		}},
		{Name: "Audit", Kind: "config", Environment: []EnvVar{{Name: "AUDIT_URL", Type: "string"}}},
	}
	bump, changes := Diff(old, newer)
	if bump != Major {
		t.Fatalf("bump = %v, want major", bump)
	}
	var msgs []string
	for _, c := range changes {
		msgs = append(msgs, c.Bump.String()+": "+c.Msg)
	}
	got := strings.Join(msgs, "\n")
	for _, want := range []string{
		"major: now reads environment variable AWS_SECRET_ACCESS_KEY (Secret), which every deployment must set",
		"minor: now reads environment variable STRIPE_TIMEOUT (int, not required)",
		"major: now reads environment variable AUDIT_URL (string), which every deployment must set",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("changes missing %q:\n%s", want, got)
		}
	}
}
