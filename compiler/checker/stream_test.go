package checker

import (
	"strings"
	"testing"

	"fiig-lang/compiler/parser"
)

// ADR 0111: stream routes, bug classes I1-I4.

const streamModels = `model OrderPlaced {
    orderId: string;
    quantity: int;
    lines: List<string>;
    weight: float;
}`

const streamErrors = `enum OrderError {
    OutOfStock(sku: string),
    Invalid(reason: string)
}`

const placeOrder = `workflow placeOrder(order: OrderPlaced) -> Result<None, OrderError> {
    return Ok(None);
}`

const placeRaw = `workflow placeRaw(payload: bytes) -> Result<None, OrderError> {
    return Ok(None);
}`

func checkStream(t *testing.T, adapter string) []Diagnostic {
	t.Helper()
	return checkSrc(t, streamModels, streamErrors, placeOrder, placeRaw, adapter)
}

func TestStreamRoutesCheck(t *testing.T) {
	good := []string{
		`adapter stream Intake {
    source "orders" group "intake" from earliest key orderId -> placeOrder(order: OrderPlaced) {
        Ok(_) -> ack,
        Err(OrderError.OutOfStock(_)) -> retry(attempts: 5) else deadLetter "orders.failed",
        Err(OrderError.Invalid(reason)) -> deadLetter "orders.invalid"
    }
}`,
		`adapter stream Intake {
    source "orders" group "intake" from earliest key quantity -> placeOrder(order: OrderPlaced) {
        Ok(_) -> ack,
        Err(_) -> halt
    }
}`,
		`adapter stream Raw {
    source "raw" group "intake" from earliest -> placeRaw(payload: bytes) {
        _ -> ack
    }
}`,
	}
	for _, src := range good {
		if diags := checkStream(t, src); len(diags) > 0 {
			t.Errorf("diags = %v for\n%s", diags, src)
		}
	}
}

func TestStreamRouteBugClasses(t *testing.T) {
	cases := []struct{ name, route, rule, fragment string }{
		{"no key", `source "orders" group "intake" from earliest -> placeOrder(order: OrderPlaced) { _ -> ack }`, "I1", "names the field that identifies an element"},
		{"key isn't a field", `source "orders" group "intake" from earliest key id -> placeOrder(order: OrderPlaced) { _ -> ack }`, "I1", `key "id" isn't a field of OrderPlaced`},
		{"key text can't carry", `source "orders" group "intake" from earliest key lines -> placeOrder(order: OrderPlaced) { _ -> ack }`, "I1", `key "lines" is List<string>`},
		{"key is a float", `source "orders" group "intake" from earliest key weight -> placeOrder(order: OrderPlaced) { _ -> ack }`, "I1", `a float is never a key`},
		{"key on bytes", `source "raw" group "intake" from earliest key id -> placeRaw(payload: bytes) { _ -> ack }`, "I1", "has no fields, so the route has no key"},
		{"no mapping", `source "orders" group "intake" from earliest key orderId -> placeOrder(order: OrderPlaced)`, "I2", "has no mapping"},
		{"missing variant", `source "orders" group "intake" from earliest key orderId -> placeOrder(order: OrderPlaced) {
            Ok(_) -> ack
            Err(OrderError.Invalid(_)) -> halt
        }`, "I2", "doesn't say what happens to Err(OutOfStock)"},
		{"missing Ok", `source "orders" group "intake" from earliest key orderId -> placeOrder(order: OrderPlaced) { Err(_) -> halt }`, "I2", "doesn't say what happens to Ok"},
		{"no attempts", `source "orders" group "intake" from earliest key orderId -> placeOrder(order: OrderPlaced) {
            Ok(_) -> ack
            Err(_) -> retry(attempts: 0)
        }`, "I2", "the limit is at least 1"},
		{"two arguments", `source "orders" group "intake" from earliest key orderId -> placeOrder(order: OrderPlaced, extra: int) { _ -> ack }`, "I3", "binds exactly one argument"},
	}
	cases = append(cases,
		struct{ name, route, rule, fragment string }{"no group", `source "orders" from earliest key orderId -> placeOrder(order: OrderPlaced) { _ -> ack }`, "I8", "names no consumer group"},
		struct{ name, route, rule, fragment string }{"no start", `source "orders" group "intake" key orderId -> placeOrder(order: OrderPlaced) { _ -> ack }`, "I8", "doesn't say where a new group starts"},
	)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			diags := checkStream(t, "adapter stream Intake {\n    "+c.route+"\n}")
			found := false
			for _, d := range diags {
				found = found || (d.Rule == c.rule && strings.Contains(d.Msg, c.fragment))
			}
			if !found {
				t.Errorf("diags = %v, want %s containing %q", diags, c.rule, c.fragment)
			}
		})
	}
}

func TestStreamElementMustBeAModelOrBytes(t *testing.T) {
	diags := checkSrc(t, streamErrors, `workflow count(n: int) -> Result<None, OrderError> {
    return Ok(None);
}`, `adapter stream Counts {
    source "counts" group "intake" from earliest -> count(n: int) { _ -> ack }
}`)
	wantDiag(t, diags, "I4", `the element "n" is int`)
}

func TestStreamMappingTakesOnlyDispositions(t *testing.T) {
	_, errs := parser.Parse(`adapter stream Intake {
    source "orders" group "intake" from earliest key orderId -> placeOrder(order: OrderPlaced) {
        Ok(_) -> 200
    }
}`)
	if len(errs) == 0 || !strings.Contains(errs[0].Msg, "expected 'ack', 'retry', 'halt', or 'deadLetter'") {
		t.Errorf("errs = %v", errs)
	}
}
