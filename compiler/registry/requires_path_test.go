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
