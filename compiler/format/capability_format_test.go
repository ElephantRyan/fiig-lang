package format

import "testing"

// TestFormatInterfaceAndCapabilityAreStable checks that ADR 0043's two new
// declarations print canonically and reach a fixed point after one pass: an
// interface's members one per line, a capability group's gateways one level
// in with a blank line between them.
func TestFormatInterfaceAndCapabilityAreStable(t *testing.T) {
	for name, src := range map[string]string{
		"interface": "interface PaymentProvider {\n" +
			"    charge(orderId: OrderId, amount: int) -> Result<PaymentReference, PaymentError>\n" +
			"    refund(reference: PaymentReference) -> Result<None, PaymentError>\n" +
			"}\n",
		"capability": "use acme.http.Client\n" +
			"\n" +
			"capability PayrollDataApi {\n" +
			"    gateway getEmployee(id: EmployeeId) -> Result<Employee, PayrollError> requires Client {\n" +
			"        return Client.send(id)\n" +
			"    }\n" +
			"\n" +
			"    // stubs are cached upstream, so a retry is safe\n" +
			"    gateway getPayStubs(id: EmployeeId) -> Result<List<PayStub>, PayrollError>\n" +
			"            requires Client\n" +
			"            idempotent {\n" +
			"        return Client.send(id)\n" +
			"    }\n" +
			"}\n",
	} {
		t.Run(name, func(t *testing.T) {
			once, errs := Format(src)
			if len(errs) > 0 {
				t.Fatalf("format errors: %v", errs)
			}
			if once != src {
				t.Errorf("formatted output differs from canonical input:\n%s\nwant:\n%s", once, src)
			}
			twice, _ := Format(once)
			if twice != once {
				t.Errorf("formatter is not idempotent:\n%s\nvs:\n%s", once, twice)
			}
		})
	}
}
