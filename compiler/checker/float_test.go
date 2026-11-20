package checker

import (
	"strings"
	"testing"

	"fiig-lang/compiler/syntax"
)

// ADR 0048: float, and untyped constants (§2.4).

func checkSrc(t *testing.T, srcs ...string) []Diagnostic {
	t.Helper()
	files := make([]*syntax.File, len(srcs))
	for i, src := range srcs {
		files[i] = mustParse(t, src)
	}
	return Check(files, nil)
}

func wantDiag(t *testing.T, diags []Diagnostic, rule, fragment string) {
	t.Helper()
	if len(diags) != 1 || diags[0].Rule != rule || !strings.Contains(diags[0].Msg, fragment) {
		t.Fatalf("diags = %v, want one %s containing %q", diags, rule, fragment)
	}
}

func TestFloatArithmeticAndConstantsCheck(t *testing.T) {
	diags := checkSrc(t, `model Reading {
    celsius: float
    count: int
}`, `rule fahrenheit(r: Reading) -> float {
    half = r.celsius / 2
    scale: float = 9
    offset = 32.0
    return half * 2 * scale / 5 + offset - 0.5 % 0.25
}`, `rule colder(a: Reading, b: Reading) -> bool {
    return a.celsius < b.celsius and a.celsius >= -40 and -a.celsius > 0.5
}`, `rule tally(r: Reading) -> int {
    whole: int = 3.0
    return r.count * 2 + whole
}`)
	if len(diags) != 0 {
		t.Fatalf("diags = %v, want none", diags)
	}
}

// TestNumericTypesNeverMix is ADR 0019's addendum, which §2.2 leaves
// unmodified: no implicit widening under arithmetic or comparison (§2.9).
func TestNumericTypesNeverMix(t *testing.T) {
	wantDiag(t, checkSrc(t, "rule r(n: int, x: float) -> float {\n    return n * x\n}"), "A1", "int * float mixes numeric types")
	wantDiag(t, checkSrc(t, "rule r(n: int, x: float) -> bool {\n    return n < x\n}"), "M2", "cannot compare int with float")
	// A default-typed binding is typed: `y` is a float, so `y * n` mixes.
	wantDiag(t, checkSrc(t, "rule r(n: int) -> float {\n    y = 2.5\n    return y * n\n}"), "A1", "float * int")
	wantDiag(t, checkSrc(t, "rule r(n: int) -> float {\n    return n\n}"), "A1", "got int, expected float")
	wantDiag(t, checkSrc(t, "rule r(s: string) -> int {\n    return -s\n}"), "A1", "expected int, float, or decimal")
}

// TestConstantsLandWhereTheyAreUsed is §2.4: representability is checked
// in the type the constant lands in, at compile time.
func TestConstantsLandWhereTheyAreUsed(t *testing.T) {
	wantDiag(t, checkSrc(t, "rule r() -> int {\n    return 2.5\n}"), "A1", "constant 2.5 is not a whole number")
	wantDiag(t, checkSrc(t, "rule r(n: int) -> int {\n    return n + 0.5\n}"), "A1", "constant 0.5 is not a whole number")
	wantDiag(t, checkSrc(t, "rule r() -> float {\n    return 1e308 * 10\n}"), "", "constant 1e308 * 10 does not fit in float")
	wantDiag(t, checkSrc(t, "rule r() -> int {\n    return 1 / (2 - 2)\n}"), "", "constant division by zero")
	wantDiag(t, checkSrc(t, "rule r() -> float {\n    return 1e99999\n}"), "", "exponent beyond")
	// Underflow is not an error (§2.3, added 2026-10-28).
	if diags := checkSrc(t, "rule r() -> float {\n    return 1e-300 * 1e-300\n}"); len(diags) != 0 {
		t.Fatalf("underflow: diags = %v, want none", diags)
	}
}

// TestConstantsAreEvaluatedExactly checks the value recorded for the
// runtime: rounded once, where it lands, with Go's integer-constant `/`.
func TestConstantsAreEvaluatedExactly(t *testing.T) {
	cases := []struct {
		expr, typ string
		want      syntax.Const
	}{
		{"0.1 + 0.2", "float", syntax.Const{Float: true, F: 0.3}},
		{"7 / 2", "float", syntax.Const{Float: true, F: 3}},
		{"7.0 / 2", "float", syntax.Const{Float: true, F: 3.5}},
		{"-7 % 3", "int", syntax.Const{Int: -1}},
		{"-7.5 % 2.0", "float", syntax.Const{Float: true, F: -1.5}},
		{"7.5 % -2.0", "float", syntax.Const{Float: true, F: 1.5}},
		{"1e3", "int", syntax.Const{Int: 1000}},
		{"-0.0", "float", syntax.Const{Float: true, F: 0}},
	}
	for _, c := range cases {
		f := mustParse(t, "rule r() -> "+c.typ+" {\n    return "+c.expr+"\n}\n")
		if diags := Check([]*syntax.File{f}, nil); len(diags) != 0 {
			t.Fatalf("%s: diags = %v", c.expr, diags)
		}
		ret := f.Decls[0].(*syntax.RuleDecl).Body.Stmts[0].(*syntax.ReturnStmt)
		got := ret.Value.(syntax.ConstRoot).Constant()
		if got == nil || *got != c.want {
			t.Errorf("%s as %s = %+v, want %+v", c.expr, c.typ, got, c.want)
		}
	}
}

// TestFloatEqualityIsM1 is ADR 0051 §2.3 / ADR 0052 §2.2: a float at any
// depth makes == a type error, and the diagnostic names the path to it.
func TestFloatEqualityIsM1(t *testing.T) {
	money := "model Money {\n    amount: float\n}"
	order := "model Order {\n    id: string\n    total: Money\n}"
	shape := "enum Shape {\n    Circle(radius: float)\n    Dot\n}"
	cases := []struct {
		params, expr, fragment string
	}{
		{"a: float, b: float", "a == b", "cannot compare float with =="},
		{"a: float, b: float", "a != 0.0", "cannot compare float with !="},
		{"a: Order, b: Order", "a == b", "Order.total.amount is float"},
		{"a: List<float>, b: List<float>", "a == b", "use math.allClose"},
		{"a: Shape, b: Shape", "a == b", "Shape.Circle.radius is float"},
		{"a: float | None, b: float | None", "a == b", "cannot compare float | None with =="},
	}
	for _, c := range cases {
		diags := checkSrc(t, money, order, shape, "rule r("+c.params+") -> bool {\n    return "+c.expr+"\n}")
		wantDiag(t, diags, "M1", c.fragment)
	}
	// Comparing a specific non-float field is the fix the message offers.
	if diags := checkSrc(t, money, order, "rule r(a: Order, b: Order) -> bool {\n    return a.id == b.id and a.total.amount <= b.total.amount\n}"); len(diags) != 0 {
		t.Fatalf("diags = %v, want none", diags)
	}
}

// TestListLiteralConstantsTakeTheElementType is §2.4 inside a list literal
// with no declared type.
func TestListLiteralConstantsTakeTheElementType(t *testing.T) {
	ok := []string{
		"rule r(x: float) -> List<float> {\n    return [1, x, 2]\n}",
		"rule r() -> float {\n    xs = [1, 2.5]\n    return xs.at(0)\n}",
	}
	for _, src := range ok {
		if diags := checkSrc(t, src); len(diags) != 0 {
			t.Errorf("%s: diags = %v, want none", src, diags)
		}
	}
	wantDiag(t, checkSrc(t, "rule r(n: int) -> int {\n    xs = [n, 2.5]\n    return n\n}"), "A1", "constant 2.5 is not a whole number")
}

// `+` on a string says to interpolate, with the string to write.
func TestPlusOnStringsSuggestsInterpolation(t *testing.T) {
	wantDiag(t, checkSrc(t, "rule r(note: string) -> string {\n    return note + \"x\"\n}"), "A1", "interpolate instead: \"${note}x\"")
}
