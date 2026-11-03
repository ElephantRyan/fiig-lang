package checker

import (
	"strings"
	"testing"
)

// ADR 0064: decimal in the checker.

func TestDecimalArithmeticAndConstantsCheck(t *testing.T) {
	diags := checkSrc(t, `rule tax(price: decimal, quantity: decimal) -> decimal {
    rate: decimal = 0.0725
    quarter: decimal = 10.0 / 40.0
    whole: decimal = 3
    long: decimal = 1.0000000000000000000000000000000000000
    return -price * quantity * rate + quarter % 0.1 - whole * (1.0 / 4.0) + long
}`, `rule cheaper(a: decimal, b: decimal) -> bool {
    return a < b and a != b and a >= 0.01 and a == 1.50
}`)
	if len(diags) != 0 {
		t.Fatalf("diags = %v, want none", diags)
	}
}

// TestDecimalConstantsMustBeExact is §7: a constant becomes decimal only
// if its exact value fits in 34 digits written in full.
func TestDecimalConstantsMustBeExact(t *testing.T) {
	wantDiag(t, checkSrc(t, "rule r() -> decimal {\n    return 1.0 / 3.0\n}"), "A1", "has no exact decimal value")
	wantDiag(t, checkSrc(t, "rule r(d: decimal) -> decimal {\n    return d * (1.0 / 3.0)\n}"), "A1", "has no exact decimal value")
	wantDiag(t, checkSrc(t, "rule r() -> decimal {\n    return 1234567890123456789012345678901234.5\n}"), "", "does not fit in decimal")
	wantDiag(t, checkSrc(t, "rule r() -> decimal {\n    return 1e34\n}"), "", "does not fit in decimal")
	wantDiag(t, checkSrc(t, "rule r() -> decimal {\n    return math.pi\n}"), "A1", "has no exact decimal value")
}

// TestDecimalDivisionIsACompileError is §3.
func TestDecimalDivisionIsACompileError(t *testing.T) {
	wantDiag(t, checkSrc(t, "rule r(total: decimal) -> decimal {\n    return total / 4\n}"), "A1", "math.divide(total, 4, places: 2, rounding: Rounding.HalfEven)")
}

// TestDecimalNeverMixes is ADR 0019's addendum applied to decimal.
func TestDecimalNeverMixes(t *testing.T) {
	wantDiag(t, checkSrc(t, "rule r(d: decimal, n: int) -> decimal {\n    return d * n\n}"), "A1", "decimal * int mixes numeric types")
	wantDiag(t, checkSrc(t, "rule r(d: decimal, x: float) -> decimal {\n    return d + x\n}"), "A1", "decimal + float mixes numeric types")
	wantDiag(t, checkSrc(t, "rule r(d: decimal) -> decimal {\n    rate = 0.0725\n    return d * rate\n}"), "A1", "decimal * float")
	wantDiag(t, checkSrc(t, "rule r(d: decimal, x: float) -> bool {\n    return d < x\n}"), "M2", "cannot compare decimal with float")
}

// TestDecimalMathFormsCheck is ADR 0064's math members, in every form
// a decimal takes.
func TestDecimalMathFormsCheck(t *testing.T) {
	diags := checkSrc(t, `rule fine(x: decimal, f: float, n: int, mode: Rounding) -> decimal {
    whole: int = math.roundToInt(x) + math.floorToInt(x) + math.truncToInt(2.5)
    back: float = math.toFloat(x) + math.round(2.5)
    shares: List<decimal> = math.allocate(10.00, parts: n, places: 2)
    m: decimal | None = math.mulOrNone(x, 1.05)
    parsed: decimal | None = "19.99".toDecimalOrNone()
    return math.round(x * 1.05, places: 2, rounding: mode) + math.divide(10.00, 3, places: 2, rounding: Rounding.HalfEven) + math.toDecimal(n) + math.toDecimal(f) + math.abs(x) + math.max(x, 2.5) + math.floorMod(x, 0.25)
}`)
	if len(diags) != 0 {
		t.Fatalf("diags = %v, want none", diags)
	}
}

func TestDecimalMathMisuse(t *testing.T) {
	cases := []struct{ src, rule, fragment string }{
		// §4: places and rounding are required and named.
		{"rule r(x: decimal) -> decimal {\n    return math.round(x, places: 2)\n}", "", `missing argument "rounding"`},
		{"rule r(x: decimal) -> decimal {\n    return math.round(x, 2, rounding: Rounding.Up)\n}", "", `"places" must be passed by name`},
		{"rule r(x: decimal) -> List<decimal> {\n    return math.allocate(x, 3, places: 2)\n}", "", "so parts and places can't be swapped"},
		// The one-argument form is float's; a decimal needs places.
		{"rule r(x: decimal) -> decimal {\n    return math.round(x)\n}", "A1", "(x: decimal, places: int, rounding: Rounding) -> decimal"},
		// §4: float has no places.
		{"rule r(x: float) -> float {\n    return math.round(x, places: 2, rounding: Rounding.Up)\n}", "A1", "math.round has no form taking (float, int, Rounding)"},
		{"rule r(x: decimal) -> decimal {\n    return math.divide(x, 3, places: 2, rounding: 2)\n}", "A1", `got int, expected Rounding`},
		// §3: an int divisor is converted by name.
		{"rule r(x: decimal, n: int) -> decimal {\n    return math.divide(x, n, places: 2, rounding: Rounding.Up)\n}", "A1", "got int, expected decimal"},
	}
	for _, c := range cases {
		wantDiag(t, checkSrc(t, c.src), c.rule, c.fragment)
	}
	// Decimal has no divOrNone, since it has no `/`; its one form is float's.
	diags := checkSrc(t, "rule r(x: decimal) -> float | None {\n    return math.divOrNone(x, x)\n}")
	if len(diags) != 2 || !strings.Contains(diags[0].Msg, `got decimal, expected float (argument "a" of math.divOrNone)`) {
		t.Errorf("divOrNone on decimals: diags = %v", diags)
	}
}
