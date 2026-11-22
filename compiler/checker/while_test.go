package checker

import (
	"testing"

	"fiig-lang/compiler/syntax"
)

// ADR 0059: `while` checks its condition like `if` and shares for's
// break/continue/label rules.

func TestWhileCountdownIsClean(t *testing.T) {
	src := "rule countdown(n: int) -> int {\n" +
		"\tleft: int = n;\n" +
		"\tsteps: int = 0;\n" +
		"\twhile (left > 0) {\n" +
		"\t\tleft = left - 1;\n" +
		"\t\tsteps = steps + 1;\n" +
		"\t}\n" +
		"\treturn steps;\n}"
	if diags := checkLoop(t, src); len(diags) != 0 {
		t.Errorf("unexpected diagnostics for a valid while: %+v", diags)
	}
}

func TestWhileConditionMustBeBoolIsA1(t *testing.T) {
	src := "rule bad(n: int) -> int {\n" +
		"\twhile (n) {\n" +
		"\t\tbreak;\n" +
		"\t}\n" +
		"\treturn n;\n}"
	if got := rules(checkLoop(t, src))["A1"]; got != 1 {
		t.Errorf("got %d A1 diagnostics for an int condition, want 1", got)
	}
}

func TestBreakInsideWhileIsNotL1(t *testing.T) {
	src := "rule spin(n: int) -> int {\n" +
		"\twhile (true) {\n" +
		"\t\tif (n > 0) {\n" +
		"\t\t\tcontinue;\n" +
		"\t\t}\n" +
		"\t\tbreak;\n" +
		"\t}\n" +
		"\treturn n;\n}"
	if diags := checkLoop(t, src); len(diags) != 0 {
		t.Errorf("unexpected diagnostics for break/continue in while: %+v", diags)
	}
}

func TestLabeledBreakFromWhileToEnclosingForIsClean(t *testing.T) {
	src := "rule find(orders: List<Order>) -> int {\n" +
		"\tfound: int = 0;\n" +
		"\touter: for (order in orders) {\n" +
		"\t\twhile (order.amount > found) {\n" +
		"\t\t\tfound = found + 1;\n" +
		"\t\t\tbreak outer;\n" +
		"\t\t}\n" +
		"\t}\n" +
		"\treturn found;\n}"
	if diags := checkLoop(t, src); len(diags) != 0 {
		t.Errorf("unexpected diagnostics for labeled break out of while: %+v", diags)
	}
}

func TestWhileLabelRepeatingEnclosingForIsL3(t *testing.T) {
	src := "rule bad(orders: List<Order>) -> int {\n" +
		"\tscan: for (order in orders) {\n" +
		"\t\tscan: while (true) {\n" +
		"\t\t\tbreak scan;\n" +
		"\t\t}\n" +
		"\t}\n" +
		"\treturn 0;\n}"
	if got := rules(checkLoop(t, src))["L3"]; got != 1 {
		t.Errorf("got %d L3 diagnostics, want 1", got)
	}
}

func TestBreakInConcurrentInsideWhileIsL1(t *testing.T) {
	src := "rule bad(orders: List<Order>) -> int {\n" +
		"\twhile (true) {\n" +
		"\t\tconcurrent(other in orders): {\n" +
		"\t\t\tbreak;\n" +
		"\t\t}\n" +
		"\t}\n" +
		"\treturn 0;\n}"
	if got := rules(checkLoop(t, src))["L1"]; got != 1 {
		t.Errorf("got %d L1 diagnostics for break inside concurrent, want 1", got)
	}
}

// TestActivityCycleThroughWhileBodyIsB9: B9's call graph comes from
// syntax.BodyCallNames, which must walk into a while's condition and body
// or a cycle hidden inside one goes unreported.
func TestActivityCycleThroughWhileBodyIsB9(t *testing.T) {
	model := mustParse(t, "model NewCustomer {\n\tname: string\n}")
	inBody := mustParse(t, "activity inBody(input: NewCustomer) -> NewCustomer {\n"+
		"\twhile (true) {\n\t\treturn inCond(input);\n\t}\n\treturn input;\n}")
	inCond := mustParse(t, "activity inCond(input: NewCustomer) -> NewCustomer {\n"+
		"\twhile (inBody(input) == input) {\n\t\tbreak;\n\t}\n\treturn input;\n}")
	if got := rules(Check([]*syntax.File{model, inBody, inCond}, nil)); got["B9"] != 2 {
		t.Fatalf("want 2 B9 (inBody, inCond), got %v", got)
	}
}
