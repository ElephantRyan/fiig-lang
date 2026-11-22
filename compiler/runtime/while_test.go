package runtime

import (
	"context"
	"strings"
	"testing"
	"time"

	"fiig-lang/compiler/parser"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// ADR 0059: `while` re-evaluates its condition before every iteration and
// shares for's break/continue/label handling.

func TestWhileRunsUntilConditionIsFalse(t *testing.T) {
	src := "rule countdown(n: int) -> int {\n" +
		"\tleft: int = n;\n" +
		"\tsteps: int = 0;\n" +
		"\twhile (left > 0) {\n" +
		"\t\tleft = left - 1;\n" +
		"\t\tsteps = steps + 1;\n" +
		"\t}\n" +
		"\treturn steps;\n}\n"
	if v := runLoopProgram(t, src, "countdown", map[string]Value{"n": Int(5)}); v != Int(5) {
		t.Fatalf("countdown(5) = %v, want 5", v)
	}
	if v := runLoopProgram(t, src, "countdown", map[string]Value{"n": Int(0)}); v != Int(0) {
		t.Fatalf("countdown(0) = %v, want 0 (condition false before the first iteration)", v)
	}
}

// TestWhileRunsPastTheCallDepthLimit is the point of ADR 0059: an iteration
// count recursion could not reach (ADR 0054's 1000-frame cap).
func TestWhileRunsPastTheCallDepthLimit(t *testing.T) {
	src := "rule countdown(n: int) -> int {\n" +
		"\tleft: int = n;\n" +
		"\twhile (left > 0) {\n" +
		"\t\tleft = left - 1;\n" +
		"\t}\n" +
		"\treturn left;\n}\n"
	if v := runLoopProgram(t, src, "countdown", map[string]Value{"n": Int(50000)}); v != Int(0) {
		t.Fatalf("countdown(50000) = %v, want 0", v)
	}
}

func TestWhileTrueWithBreakAndContinue(t *testing.T) {
	src := "rule sumOdd(ceiling: int) -> int {\n" +
		"\ti: int = 0;\n" +
		"\ttotal: int = 0;\n" +
		"\twhile (true) {\n" +
		"\t\ti = i + 1;\n" +
		"\t\tif (i > ceiling) {\n" +
		"\t\t\tbreak;\n" +
		"\t\t}\n" +
		"\t\tif (i % 2 == 0) {\n" +
		"\t\t\tcontinue;\n" +
		"\t\t}\n" +
		"\t\ttotal = total + i;\n" +
		"\t}\n" +
		"\treturn total;\n}\n"
	if v := runLoopProgram(t, src, "sumOdd", map[string]Value{"ceiling": Int(6)}); v != Int(9) {
		t.Fatalf("sumOdd(6) = %v, want 9", v)
	}
}

func TestReturnFromInsideWhileExitsTheDeclaration(t *testing.T) {
	src := "rule firstOver(ceiling: int) -> int {\n" +
		"\ti: int = 0;\n" +
		"\twhile (true) {\n" +
		"\t\ti = i + 3;\n" +
		"\t\tif (i > ceiling) {\n" +
		"\t\t\treturn i;\n" +
		"\t\t}\n" +
		"\t}\n" +
		"\treturn 0;\n}\n"
	if v := runLoopProgram(t, src, "firstOver", map[string]Value{"ceiling": Int(10)}); v != Int(12) {
		t.Fatalf("firstOver(10) = %v, want 12", v)
	}
}

// TestLabeledBreakFromWhileExitsOuterFor: an unlabeled break would only
// leave the while, and the for would go on to count every element.
func TestLabeledBreakFromWhileExitsOuterFor(t *testing.T) {
	src := "rule count(xs: List<int>) -> int {\n" +
		"\tcount: int = 0;\n" +
		"\touter: for (x in xs) {\n" +
		"\t\twhile (true) {\n" +
		"\t\t\tcount = count + 1;\n" +
		"\t\t\tif (x == 2) {\n" +
		"\t\t\t\tbreak outer;\n" +
		"\t\t\t}\n" +
		"\t\t\tbreak;\n" +
		"\t\t}\n" +
		"\t}\n" +
		"\treturn count;\n}\n"
	if v := runLoopProgram(t, src, "count", map[string]Value{"xs": intList(1, 2, 3)}); v != Int(2) {
		t.Fatalf("count = %v, want 2 (labeled break)", v)
	}
}

// TestWhileTrueStopsAtDeadline is §4: no iteration cap, the call's deadline
// is what stops a loop that never ends.
func TestWhileTrueStopsAtDeadline(t *testing.T) {
	src := "rule spin(n: int) -> int {\n" +
		"\ti: int = n;\n" +
		"\twhile (true) {\n" +
		"\t\ti = i + 1;\n" +
		"\t}\n" +
		"\treturn i;\n}\n"
	f, errs := parser.Parse(src)
	if len(errs) > 0 {
		t.Fatalf("parse: %v", errs)
	}
	universe, conflicts := types.BuildUniverse([]*syntax.File{f})
	if len(conflicts) > 0 {
		t.Fatalf("name conflicts: %+v", conflicts)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, fault := newTestInterp(universe, nil).Call(ctx, "spin", map[string]Value{"n": Int(0)})
	if fault == nil || !strings.Contains(fault.Error(), "cancelled") {
		t.Fatalf("got fault %v, want a cancellation fault", fault)
	}
}
