package capability

import (
	"reflect"
	"testing"
)

func TestPrimitiveSetIsClosedAndExact(t *testing.T) {
	want := []string{"Clock", "FileSystem", "Random", "Session", "Settings", "Socket"}
	if got := Primitives(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Primitives() = %v, want %v", got, want)
	}
	for _, domain := range []string{"Database", "EmailSender", "PaymentGateway", "ShippingProvider", "HttpClient", "SqlConnection"} {
		if IsPrimitive(domain) {
			t.Errorf("%s must not be a primitive: ADR 0042 admits only what fiig cannot express", domain)
		}
	}
}

// TestTlsSocketIsNotACapability: ADR 0060 folded TLS into Socket.
func TestTlsSocketIsNotACapability(t *testing.T) {
	if IsPrimitive("TlsSocket") {
		t.Fatal("TlsSocket must not be a primitive; TLS is Socket.connectTls/startTls (ADR 0060)")
	}
}
