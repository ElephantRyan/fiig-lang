package runtime

import (
	"math"
	"strings"
	"testing"

	"fiig-lang/compiler/checker"
	"fiig-lang/compiler/parser"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// ADR 0049 §2.4: the prelude math group at runtime.

// TestEveryMathFormulaIsImplemented keeps this package's bodies and
// compiler/types's table in step (§2.8).
func TestEveryMathFormulaIsImplemented(t *testing.T) {
	declared := map[string]bool{}
	for _, name := range types.MathFormulaNames() {
		declared[name] = true
		if mathFormulas[name] == nil {
			t.Errorf("math.%s is in compiler/types's table but has no body", name)
		}
	}
	for name := range mathFormulas {
		if !declared[name] {
			t.Errorf("math.%s has a body but isn't in compiler/types's table", name)
		}
	}
}

// checkClean fails t unless srcs, one declaration each, check with no
// diagnostics: universeFromSource ignores them, and a case here must be a
// program the checker accepts.
func checkClean(t *testing.T, srcs ...string) {
	t.Helper()
	files := make([]*syntax.File, len(srcs))
	for i, src := range srcs {
		f, errs := parser.Parse(src)
		if len(errs) > 0 {
			t.Fatalf("parse %q: %v", src, errs)
		}
		files[i] = f
	}
	if diags := checker.Check(files, nil); len(diags) > 0 {
		t.Fatalf("check %q: %v", srcs, diags)
	}
}

type mathCase struct {
	params, result, expr string
	args                 map[string]Value
	want                 Value  // when fault is ""
	fault                string // a fragment of the fault's message
}

func runMathCases(t *testing.T, cases []mathCase) {
	t.Helper()
	for _, c := range cases {
		src := "rule r(" + c.params + ") -> " + c.result + " {\n    return " + c.expr + "\n}"
		checkClean(t, src)
		got, fault := runEach(t, src, "r", c.args)
		switch {
		case c.fault != "":
			if fault == nil || !strings.Contains(fault.Message, c.fault) {
				t.Errorf("%s %v: got %v, %v; want fault %q", c.expr, c.args, got, fault, c.fault)
			}
		case fault != nil || !Equal(got, c.want):
			t.Errorf("%s %v = %v, %v; want %v", c.expr, c.args, got, fault, c.want)
		}
	}
}

func x(f float64) map[string]Value { return map[string]Value{"x": Float(f)} }
func n(i int64) map[string]Value   { return map[string]Value{"n": Int(i)} }

// TestMathCrossings is ADR 0048 §2.7's table, row by row, with -3.5.
func TestMathCrossings(t *testing.T) {
	toInt := func(member string, arg float64, want int64) mathCase {
		return mathCase{"x: float", "int", "math." + member + "(x)", x(arg), Int(want), ""}
	}
	runMathCases(t, []mathCase{
		toInt("floorToInt", -3.5, -4),
		toInt("ceilToInt", -3.5, -3),
		toInt("truncToInt", -3.5, -3),
		toInt("roundToInt", -3.5, -4),
		toInt("roundHalfEvenToInt", -3.5, -4),
		toInt("roundToInt", 2.5, 3),
		toInt("roundHalfEvenToInt", 2.5, 2),
		toInt("floorToInt", -9223372036854775808, math.MinInt64),
		{"x: float", "int", "math.floorToInt(x)", x(1e300), nil, "math.floorToInt(1e+300) at 2:12 does not fit in int"},
		{"x: float", "int", "math.truncToInt(x)", x(9223372036854775807), nil, "does not fit in int"},
		// Rounds to the nearest float, ties to even, and never faults.
		{"n: int", "float", "math.toFloat(n)", n(math.MaxInt64), Float(9223372036854775807), ""},
		{"n: int", "float", "math.toFloat(n) / 2", n(7), Float(3.5), ""},
	})
}

func TestMathFloatFormulas(t *testing.T) {
	runMathCases(t, []mathCase{
		{"x: float", "float", "math.sqrt(x)", x(2), Float(math.Sqrt2), ""},
		{"x: float", "float", "math.floor(x) + math.ceil(x) + math.round(x) + math.trunc(x)", x(-2.5), Float(-3 - 2 - 3 - 2), ""},
		// -0.0 never appears (ADR 0048 §2.3).
		{"x: float", "float", "math.ceil(x)", x(-0.5), Float(0), ""},
		{"x: float", "float", "math.atan2(y: x, x: 0.0)", x(1), Float(math.Pi / 2), ""},
		{"x: float", "float", "math.pow(x, 10)", x(2), Float(1024), ""},
		{"x: float", "float", "math.log10(x)", x(1000), Float(3), ""},
		{"x: float", "float", "math.sin(x * math.pi / 2)", x(1), Float(1), ""},
		// §2.3's table: an answer IEEE gives as NaN or Inf is a fault.
		{"x: float", "float", "math.sqrt(x)", x(-1), nil, "math.sqrt(-1.0) at 2:12 is not a real number"},
		{"x: float", "float", "math.log(x)", x(0), nil, "math.log(0.0) at 2:12 has no finite result"},
		{"x: float", "float", "math.exp(x)", x(1000), nil, "has no finite result"},
		{"x: float", "float", "math.asin(x)", x(2), nil, "is not a real number"},
		{"x: float", "float", "math.pow(x, 0.5)", x(-8), nil, "is not a real number"},
		// Underflow isn't a fault.
		{"x: float", "float", "math.exp(x)", x(-1000), Float(0), ""},
	})
}

// TestMathTwoForms is §2.4's int and float forms of one name.
func TestMathTwoForms(t *testing.T) {
	ints := func(a, b int64) map[string]Value { return map[string]Value{"a": Int(a), "b": Int(b)} }
	floats := func(a, b float64) map[string]Value { return map[string]Value{"a": Float(a), "b": Float(b)} }
	runMathCases(t, []mathCase{
		{"a: int, b: int", "int", "math.abs(a) + math.abs(b)", ints(-3, 4), Int(7), ""},
		{"a: float, b: float", "float", "math.abs(a) + math.abs(b)", floats(-2.5, 1), Float(3.5), ""},
		{"a: int, b: int", "int", "math.abs(a)", ints(math.MinInt64, 0), nil, "math.abs(-9223372036854775808) at 2:12 does not fit in int"},
		// Ints compare as ints: these two round to the same float.
		{"a: int, b: int", "int", "math.max(a, b)", ints(math.MaxInt64-1, math.MaxInt64), Int(math.MaxInt64), ""},
		{"a: int, b: int", "int", "math.min(a, b)", ints(math.MaxInt64, math.MaxInt64-1), Int(math.MaxInt64 - 1), ""},
		{"a: float, b: float", "float", "math.max(a, b) - math.min(a, b)", floats(-1.5, 2), Float(3.5), ""},
		// floorMod takes the divisor's sign; `%` takes the dividend's.
		{"a: int, b: int", "int", "math.floorMod(a, b)", ints(-7, 3), Int(2), ""},
		{"a: int, b: int", "int", "math.floorMod(a, b)", ints(7, -3), Int(-2), ""},
		{"a: int, b: int", "int", "math.floorMod(a, b)", ints(-6, 3), Int(0), ""},
		{"a: float, b: float", "float", "math.floorMod(a, b)", floats(-7.5, 2), Float(0.5), ""},
		{"a: float, b: float", "float", "math.floorMod(a, b)", floats(-6, 3), Float(0), ""},
		{"a: int, b: int", "int", "math.floorMod(a, b)", ints(5, 0), nil, "math.floorMod(5, 0) at 2:12 divides by zero"},
	})
}

// TestMathOrNone is ADR 0063 §2: None exactly where the operator faults.
func TestMathOrNone(t *testing.T) {
	ints := func(a, b int64) map[string]Value { return map[string]Value{"a": Int(a), "b": Int(b)} }
	floats := func(a, b float64) map[string]Value { return map[string]Value{"a": Float(a), "b": Float(b)} }
	runMathCases(t, []mathCase{
		{"a: int, b: int", "int | None", "math.addOrNone(a, b)", ints(2, 3), Int(5), ""},
		{"a: int, b: int", "int | None", "math.addOrNone(a, b)", ints(math.MaxInt64, 1), None{}, ""},
		{"a: int, b: int", "int | None", "math.subOrNone(a, b)", ints(math.MinInt64, 1), None{}, ""},
		{"a: int, b: int", "int | None", "math.mulOrNone(a, b)", ints(math.MinInt64, -1), None{}, ""},
		{"a: float, b: float", "float | None", "math.mulOrNone(a, b)", floats(1e200, 1e200), None{}, ""},
		{"a: float, b: float", "float | None", "math.addOrNone(a, b)", floats(0.5, 0.25), Float(0.75), ""},
		{"a: float, b: float", "float | None", "math.divOrNone(a, b)", floats(1, 4), Float(0.25), ""},
		{"a: float, b: float", "float | None", "math.divOrNone(a, b)", floats(1, 0), None{}, ""},
		{"a: float, b: float", "float | None", "math.divOrNone(a, b)", floats(1e300, 1e-300), None{}, ""},
	})
}

// TestMathCloseTo is ADR 0051's rule: Python's math.isclose.
func TestMathCloseTo(t *testing.T) {
	close := func(a, b, relative, absolute float64, want bool) mathCase {
		return mathCase{"a: float, b: float, r: float, e: float", "bool", "math.closeTo(a, b, relative: r, absolute: e)",
			map[string]Value{"a": Float(a), "b": Float(b), "r": Float(relative), "e": Float(absolute)}, Bool(want), ""}
	}
	bad := close(1, 1, -1e-9, 0, false)
	bad.fault = "has a negative tolerance"
	runMathCases(t, []mathCase{
		close(0.1+0.2, 0.3, 1e-9, 0, true),
		// Relative alone fails near zero; absolute alone fails for large values.
		close(1e-20, 0, 1e-9, 0, false),
		close(1e-20, 0, 1e-9, 1e-12, true),
		close(1e10, 1e10+1, 0, 1e-6, false),
		close(1e10, 1e10+1, 1e-9, 1e-6, true),
		// Symmetric, unlike NumPy's isclose.
		close(1, 1.1, 0.0909091, 0, true),
		close(1.1, 1, 0.0909091, 0, true),
		// A difference beyond float's range is not close, and not a fault.
		close(1.7e308, -1.7e308, 0.5, 0, false),
		bad,
		{"x: float", "bool", "math.isZero(x)", x(0), Bool(true), ""},
		{"x: float", "bool", "math.isZero(x)", x(5e-324), Bool(false), ""},
	})
}

// TestMathConstantsRun is §2.7: the checker's rounded value is what runs.
func TestMathConstantsRun(t *testing.T) {
	runMathCases(t, []mathCase{
		{"x: float", "float", "math.pi * x * x", x(2), Float(math.Pi * 4), ""},
		{"x: float", "float", "math.tau + x", x(0), Float(2 * math.Pi), ""},
		{"x: float", "float", "math.e", x(0), Float(math.E), ""},
		{"x: float", "float", "math.maxFloat", x(0), Float(math.MaxFloat64), ""},
		{"x: float", "float", "math.maxFloat * x", x(2), nil, "float overflow"},
		{"n: int", "int", "math.maxInt - n", n(0), Int(math.MaxInt64), ""},
		{"n: int", "int", "math.minInt + n", n(-1), nil, "integer overflow"},
	})
}

// TestFormulaCallsMath is a fiig-written formula built on a native one.
func TestFormulaCallsMath(t *testing.T) {
	srcs := []string{
		"formulas geometry {\n    formula hypot(a: float, b: float) -> float {\n        return math.sqrt(a * a + b * b)\n    }\n}",
		"use acme.geometry\n\nrule diagonal(w: float, h: float) -> float {\n    return geometry.hypot(w, h)\n}",
	}
	checkClean(t, srcs...)
	in := newTestInterp(universeFromSource(t, srcs...), nil)
	got, fault := in.Call(t.Context(), "diagonal", map[string]Value{"w": Float(3), "h": Float(4)})
	if fault != nil || got != Float(5) {
		t.Fatalf("diagonal(3, 4) = %v, %v; want 5.0", got, fault)
	}
}
