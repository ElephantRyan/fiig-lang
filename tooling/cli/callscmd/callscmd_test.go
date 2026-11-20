package callscmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ADR 0106: fiig calls lists each callee's kind and the capabilities it
// reaches, through activities to their gateways.
func TestCallsOfAWorkflow(t *testing.T) {
	examples, err := filepath.Abs(filepath.Join("..", "..", "..", "examples"))
	if err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	file := filepath.Join(examples, "order-processing", "workflows", "place_ship_and_notify_customer.fiig")
	if code := Run("fiig calls", []string{"-root", examples, file}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	got := out.String()
	for _, want := range []string{
		"notifyShipped      activity    effects: Database, EmailSender",
		"placeAndShipOrder  activity    effects: Database, PaymentGateway, ShippingProvider",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output:\n%s\nwant a line %q", got, want)
		}
	}
	// A rule calls only pure things.
	out.Reset()
	rule := filepath.Join(examples, "getting-started", "activities", "record_rating.fiig")
	if _, err := os.Stat(rule); err == nil {
		Run("fiig calls", []string{"-root", examples, rule}, &out, &errOut)
		if !strings.Contains(out.String(), "pure") {
			t.Errorf("recordRating's calls:\n%s\nwant a pure rule among them", out.String())
		}
	}
}
