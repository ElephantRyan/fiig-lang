package checker

import (
	"strings"
	"testing"

	"fiig-lang/compiler/syntax"
)

// ADR 0049 §2.6/§2.8: method calls are checked against the receiver
// type's table, whatever expression the receiver is.

func TestListMethodsAreTyped(t *testing.T) {
	order := mustParse(t, `model Order {
    id: string
    lines: List<int>
}
`)
	f := mustParse(t, `rule methods(order: Order) -> int {
    n: int = order.lines.length();
    first: int = order.lines.at(0);
    head: List<int> = order.lines.slice(0, n);
    return match (order.lines.atOrNone(5)) {
        None -> {
            return n;
        }
        x -> {
            return x + first + head.length();
        }
    }
}
`)
	if diags := Check([]*syntax.File{order, f}, nil); len(diags) != 0 {
		t.Fatalf("want a clean build, got %v", diags)
	}

	bad := mustParse(t, `rule bad(xs: List<string>) -> int {
    s: int = xs.at(0);
    return xs.at("0").isBlank();
}
`)
	// Three A1s: at() on a List<string> gives a string, not an int; its
	// index must be an int; and isBlank() gives a bool, not the int the
	// rule returns.
	if got := Check([]*syntax.File{bad}, nil); len(got) != 3 {
		t.Fatalf("want 3 type mismatches, got %v", got)
	}
}

func TestMethodNotInTableIsK1(t *testing.T) {
	cases := map[string]string{
		`42.isBlank()`:         `int has no methods`,
		`name.reverse()`:       `string has no method "reverse"`,
		`customer.name.size()`: `string has no method "size"`,
		`xs.reversed()`:        `List has no method "reversed"`,
		`customer.isValid()`:   `Customer has no methods`,
		`maybe.isBlank()`:      `has no methods`,
	}
	for call, want := range cases {
		customer := mustParse(t, `model Customer {
    name: string
}
`)
		f := mustParse(t, `rule r(name: string, customer: Customer, xs: List<int>, maybe: string | None) -> bool {
    x = `+call+`
    return true
}
`)
		diags := Check([]*syntax.File{customer, f}, nil)
		if len(diags) != 1 || diags[0].Rule != "K1" || !strings.Contains(diags[0].Msg, want) {
			t.Errorf("%s: diags %v, want one K1 containing %q", call, diags, want)
		}
	}
}
