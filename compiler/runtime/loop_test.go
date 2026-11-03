package runtime

import (
	"context"
	"testing"

	"fiig-lang/compiler/parser"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// orderModel is declared in every loop test source so Record values built
// here line up with the program's own model (ADR 0053).
const loopModel = "model Order {\n\tamount: int\n}\n\n"

// runLoopProgram parses loopModel plus src, then calls rule name with args.
func runLoopProgram(t *testing.T, src, name string, args map[string]Value) Value {
	t.Helper()
	var files []*syntax.File
	for _, s := range []string{loopModel, src} {
		f, errs := parser.Parse(s)
		if len(errs) > 0 {
			t.Fatalf("parse: %v", errs)
		}
		files = append(files, f)
	}
	universe, conflicts := types.BuildUniverse(files)
	if len(conflicts) > 0 {
		t.Fatalf("name conflicts: %+v", conflicts)
	}
	v, fault := newTestInterp(universe, nil).Call(context.Background(), name, args)
	if fault != nil {
		t.Fatalf("Call %s: %v", name, fault)
	}
	return v
}

// orderList builds a List<Order> of records with the given amounts.
func orderList(amounts ...int64) List {
	elems := make([]Value, len(amounts))
	for i, a := range amounts {
		elems[i] = Record{TypeName: "Order", Fields: map[string]Value{"amount": Int(a)}}
	}
	return List{Elems: elems}
}

// intList builds a List of plain ints.
func intList(ns ...int64) List {
	elems := make([]Value, len(ns))
	for i, n := range ns {
		elems[i] = Int(n)
	}
	return List{Elems: elems}
}

func TestForLoopSumsAFieldOfEachModel(t *testing.T) {
	src := "rule orderTotal(orders: List<Order>) -> int {\n" +
		"\ttotal: int = 0\n" +
		"\tfor order in orders {\n" +
		"\t\ttotal = total + order.amount\n" +
		"\t}\n" +
		"\treturn total\n}\n"
	v := runLoopProgram(t, src, "orderTotal", map[string]Value{"orders": orderList(1, 2, 3)})
	if v != Int(6) {
		t.Fatalf("orderTotal = %v, want 6", v)
	}
}

func TestForLoopOverEmptyListKeepsInitializer(t *testing.T) {
	src := "rule orderTotal(orders: List<Order>) -> int {\n" +
		"\ttotal: int = 7\n" +
		"\tfor order in orders {\n" +
		"\t\ttotal = total + order.amount\n" +
		"\t}\n" +
		"\treturn total\n}\n"
	v := runLoopProgram(t, src, "orderTotal", map[string]Value{"orders": orderList()})
	if v != Int(7) {
		t.Fatalf("orderTotal over empty list = %v, want 7", v)
	}
}

func TestReturnFromInsideLoopExitsTheDeclaration(t *testing.T) {
	src := "rule firstNegative(orders: List<Order>) -> int {\n" +
		"\tfor order in orders {\n" +
		"\t\tif order.amount < 0 {\n" +
		"\t\t\treturn order.amount\n" +
		"\t\t}\n" +
		"\t}\n" +
		"\treturn 0\n}\n"
	v := runLoopProgram(t, src, "firstNegative", map[string]Value{"orders": orderList(5, -2, 7, -9)})
	if v != Int(-2) {
		t.Fatalf("firstNegative = %v, want -2", v)
	}
}

func TestBreakStopsTheLoop(t *testing.T) {
	src := "rule sumUntilNegative(orders: List<Order>) -> int {\n" +
		"\ttotal: int = 0\n" +
		"\tfor order in orders {\n" +
		"\t\tif order.amount < 0 {\n" +
		"\t\t\tbreak\n" +
		"\t\t}\n" +
		"\t\ttotal = total + order.amount\n" +
		"\t}\n" +
		"\treturn total\n}\n"
	v := runLoopProgram(t, src, "sumUntilNegative", map[string]Value{"orders": orderList(1, 2, -1, 5)})
	if v != Int(3) {
		t.Fatalf("sumUntilNegative = %v, want 3", v)
	}
}

func TestContinueSkipsOneElement(t *testing.T) {
	src := "rule sumNonNegative(orders: List<Order>) -> int {\n" +
		"\ttotal: int = 0\n" +
		"\tfor order in orders {\n" +
		"\t\tif order.amount < 0 {\n" +
		"\t\t\tcontinue\n" +
		"\t\t}\n" +
		"\t\ttotal = total + order.amount\n" +
		"\t}\n" +
		"\treturn total\n}\n"
	v := runLoopProgram(t, src, "sumNonNegative", map[string]Value{"orders": orderList(1, -1, 2)})
	if v != Int(3) {
		t.Fatalf("sumNonNegative = %v, want 3", v)
	}
}

// TestLabeledBreakExitsOuterLoop distinguishes the two targets: an
// unlabeled inner break would count 5 (inner loop exits after one step, outer
// keeps going), while `break outer` stops everything at 3.
func TestLabeledBreakExitsOuterLoop(t *testing.T) {
	src := "rule countPairs(xs: List<int>, ys: List<int>) -> int {\n" +
		"\tcount: int = 0\n" +
		"\touter: for x in xs {\n" +
		"\t\tfor y in ys {\n" +
		"\t\t\tcount = count + 1\n" +
		"\t\t\tif x == 2 {\n" +
		"\t\t\t\tbreak outer\n" +
		"\t\t\t}\n" +
		"\t\t}\n" +
		"\t}\n" +
		"\treturn count\n}\n"
	v := runLoopProgram(t, src, "countPairs", map[string]Value{
		"xs": intList(1, 2, 3),
		"ys": intList(10, 20),
	})
	if v != Int(3) {
		t.Fatalf("countPairs = %v, want 3 (labeled break)", v)
	}
}
