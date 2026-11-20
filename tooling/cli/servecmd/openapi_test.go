package servecmd

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// `fiig openapi` on the monolith: one document for both adapters, with
// another package's model (kernel.Money) described, not left empty.
func TestOpenAPIForTheMonolith(t *testing.T) {
	examples := examplesDir(t)
	var out bytes.Buffer
	if err := writeOpenAPI(&out, filepath.Join(examples, "shop-orders"), []string{"shop.orders.orders.ordersApi", "shop.payments.paymentsApi"}, []string{examples}); err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out.String())
	}
	paths := doc["paths"].(map[string]any)
	for _, p := range []string{"/orders/{order}", "/payments/{reference}"} {
		if _, ok := paths[p]; !ok {
			t.Errorf("no %s in %v", p, paths)
		}
	}
	if title := doc["info"].(map[string]any)["title"]; title != "shop.orders.orders.ordersApi, shop.payments.paymentsApi" {
		t.Errorf("title: %v", title)
	}
	body, _ := json.Marshal(paths["/orders/{order}"].(map[string]any)["post"].(map[string]any)["requestBody"])
	if !strings.Contains(string(body), `"amount":{"format":"decimal","type":"number"}`) || !strings.Contains(string(body), `"currency":{"type":"string"}`) {
		t.Errorf("Money isn't described: %s", body)
	}
	receipt, _ := json.Marshal(paths["/payments/{reference}"])
	if !strings.Contains(string(receipt), `"refundable":{"type":"boolean"}`) {
		t.Errorf("Payments' Receipt isn't described: %s", receipt)
	}
}

func TestOpenAPIRefusesWhatServeWould(t *testing.T) {
	examples := examplesDir(t)
	var out bytes.Buffer
	err := writeOpenAPI(&out, filepath.Join(examples, "shop-orders"), []string{"nope"}, []string{examples})
	if err == nil || !strings.Contains(err.Error(), `no adapter named "nope"`) {
		t.Errorf("got %v", err)
	}
}
