package checker

import (
	"testing"

	"fiig-lang/compiler/syntax"
)

// orderModel is the shared model for the for-loop tests (ADR 0053): a
// list of Orders iterated by the loop variable, reading a field of each.
const orderModel = "model Order {\n\tamount: int\n}"

// checkLoop parses orderModel and src and runs the checker over both.
func checkLoop(t *testing.T, src string) []Diagnostic {
	t.Helper()
	return Check([]*syntax.File{mustParse(t, orderModel), mustParse(t, src)}, nil)
}

func TestForLoopSumIsClean(t *testing.T) {
	src := "rule orderTotal(orders: List<Order>) -> int {\n" +
		"\ttotal: int = 0\n" +
		"\tfor order in orders {\n" +
		"\t\ttotal = total + order.amount\n" +
		"\t}\n" +
		"\treturn total\n}"
	if diags := checkLoop(t, src); len(diags) != 0 {
		t.Errorf("unexpected diagnostics for a valid loop: %+v", diags)
	}
}

func TestBreakAndContinueOutsideLoopAreL1(t *testing.T) {
	src := "rule bad(n: int) -> int {\n\tbreak\n\treturn n\n}"
	if got := rules(checkLoop(t, src))["L1"]; got != 1 {
		t.Errorf("got %d L1 diagnostics for a bare break, want 1", got)
	}
	src = "rule bad(n: int) -> int {\n\tcontinue\n\treturn n\n}"
	if got := rules(checkLoop(t, src))["L1"]; got != 1 {
		t.Errorf("got %d L1 diagnostics for a bare continue, want 1", got)
	}
}

func TestJumpAcrossConcurrentBoundaryIsL1(t *testing.T) {
	src := "rule bad(orders: List<Order>) -> int {\n" +
		"\tfor order in orders {\n" +
		"\t\tconcurrent(other in orders): {\n" +
		"\t\t\tbreak\n" +
		"\t\t}\n" +
		"\t}\n" +
		"\treturn 0\n}"
	if got := rules(checkLoop(t, src))["L1"]; got != 1 {
		t.Errorf("got %d L1 diagnostics for break inside concurrent, want 1", got)
	}
}

func TestUnknownLabelIsL2(t *testing.T) {
	src := "rule bad(orders: List<Order>) -> int {\n" +
		"\tfor order in orders {\n" +
		"\t\tbreak nowhere\n" +
		"\t}\n" +
		"\treturn 0\n}"
	if got := rules(checkLoop(t, src))["L2"]; got != 1 {
		t.Errorf("got %d L2 diagnostics, want 1", got)
	}
}

func TestLabeledBreakToEnclosingLoopIsClean(t *testing.T) {
	src := "rule find(outerList: List<Order>, innerList: List<Order>) -> int {\n" +
		"\tfound: int = 0\n" +
		"\touter: for a in outerList {\n" +
		"\t\tfor b in innerList {\n" +
		"\t\t\tif b.amount == a.amount {\n" +
		"\t\t\t\tfound = 1\n" +
		"\t\t\t\tbreak outer\n" +
		"\t\t\t}\n" +
		"\t\t}\n" +
		"\t}\n" +
		"\treturn found\n}"
	if diags := checkLoop(t, src); len(diags) != 0 {
		t.Errorf("unexpected diagnostics for labeled break: %+v", diags)
	}
}

func TestDuplicateLabelIsL3(t *testing.T) {
	src := "rule bad(orders: List<Order>) -> int {\n" +
		"\touter: for a in orders {\n" +
		"\t\touter: for b in orders {\n" +
		"\t\t\tbreak outer\n" +
		"\t\t}\n" +
		"\t}\n" +
		"\treturn 0\n}"
	if got := rules(checkLoop(t, src))["L3"]; got != 1 {
		t.Errorf("got %d L3 diagnostics, want 1", got)
	}
}

func TestForOverNonListIsI1(t *testing.T) {
	src := "rule bad(n: int) -> int {\n\tfor x in n {\n\t\tbreak\n\t}\n\treturn 0\n}"
	if got := rules(checkLoop(t, src))["I1"]; got != 1 {
		t.Errorf("got %d I1 diagnostics, want 1", got)
	}
}

func TestReassigningImmutableBindingIsR2(t *testing.T) {
	src := "rule bad(n: int) -> int {\n\ttotal = 1\n\ttotal = 2\n\treturn total\n}"
	if got := rules(checkLoop(t, src))["R2"]; got != 1 {
		t.Errorf("got %d R2 diagnostics, want 1", got)
	}
}

func TestReassigningLoopVariableIsR2(t *testing.T) {
	src := "rule bad(orders: List<Order>) -> int {\n" +
		"\tfor order in orders {\n" +
		"\t\torder = order\n" +
		"\t}\n" +
		"\treturn 0\n}"
	if got := rules(checkLoop(t, src))["R2"]; got != 1 {
		t.Errorf("got %d R2 diagnostics, want 1", got)
	}
}

func TestReassignmentTypeMismatchIsReported(t *testing.T) {
	src := "rule bad(orders: List<Order>) -> int {\n" +
		"\ttotal: int = 0\n" +
		"\tfor order in orders {\n" +
		"\t\ttotal = order\n" +
		"\t}\n" +
		"\treturn total\n}"
	if len(checkLoop(t, src)) == 0 {
		t.Error("reassigning an int local to a model value produced no diagnostics")
	}
}

func TestTypedRedeclarationIsR1(t *testing.T) {
	src := "rule bad(n: int) -> int {\n\ttotal: int = 0\n\ttotal: int = 1\n\treturn total\n}"
	if got := rules(checkLoop(t, src))["R1"]; got != 1 {
		t.Errorf("got %d R1 diagnostics, want 1", got)
	}
}

func TestReassigningInsideConcurrentBodyIsR3(t *testing.T) {
	src := "rule bad(orders: List<Order>) -> int {\n" +
		"\ttotal: int = 0\n" +
		"\tconcurrent(order in orders): {\n" +
		"\t\ttotal = total + order.amount\n" +
		"\t}\n" +
		"\treturn total\n}"
	if got := rules(checkLoop(t, src))["R3"]; got != 1 {
		t.Errorf("got %d R3 diagnostics, want 1", got)
	}
}
