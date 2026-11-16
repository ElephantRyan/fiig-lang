package registry

import (
	"path/filepath"
	"testing"
)

// ADR 0079 §4: a gateway that requires a package records its path, not
// the alias its file chose, so the catalog can show which domains call
// which; a capability keeps its name.
func TestGatewayCardRecordsTheRequiredPackagesPath(t *testing.T) {
	cards, _, err := Generate(filepath.Join("..", "..", "examples", "shop-orders"))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"chargeOrder": "shop.payments", "saveOrder": "Database"}
	for _, c := range cards {
		if w, ok := want[c.Name]; ok {
			if c.Requires != w {
				t.Errorf("%s requires %q, want %q", c.Name, c.Requires, w)
			}
			delete(want, c.Name)
		}
	}
	if len(want) > 0 {
		t.Errorf("no cards for %v", want)
	}
}

// ADR 0080: an enum card shows each variant's spelling, and changing one
// is a major change, since clients send and read it.
func TestVariantSpellingIsOnTheCardAndAMajorChange(t *testing.T) {
	cards, _, err := Generate(filepath.Join("..", "..", "examples", "event-inbox"))
	if err != nil {
		t.Fatal(err)
	}
	var status *Card
	for _, c := range cards {
		if c.Name == "SubscriptionStatus" {
			status = c
		}
	}
	if status == nil || status.Variants[4].Name != "PastDue" || status.Variants[4].Text != "past_due" {
		t.Fatalf("SubscriptionStatus card: %+v", status)
	}
	old := []Variant{{Name: "PastDue", Text: "past_due"}, {Name: "Active"}}
	for _, newer := range [][]Variant{
		{{Name: "PastDue", Text: "pastdue"}, {Name: "Active"}},
		{{Name: "PastDue"}, {Name: "Active"}},
		{{Name: "PastDue", Text: "past_due"}, {Name: "Active", Text: "active"}},
	} {
		changes := variantChanges("SubscriptionStatus", old, newer)
		if len(changes) != 1 || changes[0].Bump != Major {
			t.Errorf("%+v: got %+v, want one major change", newer, changes)
		}
	}
	if changes := variantChanges("SubscriptionStatus", old, old); len(changes) != 0 {
		t.Errorf("unchanged: got %+v", changes)
	}
}
