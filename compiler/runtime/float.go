package runtime

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"fiig-lang/compiler/syntax"
)

// Float is the language's binary64 type (ADR 0048). It is never NaN, never
// infinite, and never -0.0: every operation that would produce a
// non-finite value faults instead (§2.3), and a zero is always +0.0, so two
// Floats that compare equal behave identically everywhere.
type Float float64

func (Float) isValue() {}

// String is the shortest decimal that reads back as the same float, always
// with a `.` or an exponent so a whole float doesn't print as an int:
// `3.0`, `0.1`, `1e+21`. Python's repr does the same.
func (f Float) String() string {
	s := strconv.FormatFloat(float64(f), 'g', -1, 64)
	if !strings.ContainsAny(s, ".e") {
		s += ".0"
	}
	return s
}

// NewFloat is f as a fiig float: false when f is NaN or infinite, which
// no Float holds, and +0.0 when f is -0.0. A boundary decoding a float
// from outside reports false through its own failure channel (ADR 0048
// §2.3); it is the input's problem, not the program's.
func NewFloat(f float64) (Float, bool) {
	f, ok := finite(f)
	return Float(f), ok
}

// finite normalizes -0.0 to 0.0 and reports whether f is a value the
// language admits.
func finite(f float64) (float64, bool) {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return f, false
	}
	if f == 0 {
		f = 0
	}
	return f, true
}

// floatArith is `+ - * / %` on two floats. `%` is truncated, taking the
// dividend's sign, as on int (§2.8). The caller has already rejected a zero
// divisor; ok is false when the result overflows.
func floatArith(op syntax.BinaryOp, l, r float64) (float64, bool) {
	var out float64
	switch op {
	case syntax.OpAdd:
		out = l + r
	case syntax.OpSub:
		out = l - r
	case syntax.OpMul:
		out = l * r
	case syntax.OpDiv:
		out = l / r
	case syntax.OpMod:
		out = math.Mod(l, r)
	}
	return finite(out)
}

// constValue is the value the checker gave a constant expression (ADR 0048
// §2.4, ADR 0064 §7), or false when the checker never typed this node.
func constValue(e syntax.Expr) (Value, bool) {
	root, ok := e.(syntax.ConstRoot)
	if !ok {
		return nil, false
	}
	c := root.Constant()
	if c == nil {
		return nil, false
	}
	if c.Float {
		return Float(c.F), true
	}
	if c.Dec != nil {
		return Decimal{*c.Dec}, true
	}
	return Int(c.Int), true
}

// jsonFloat decodes a JSON number as a float, rounding to the nearest
// binary64. A number beyond float's range, such as 1e400, is the input's
// problem rather than the program's, so it is an error and not a fault
// (ADR 0048 §2.3's boundary table).
func jsonFloat(text string) (Value, error) {
	f, err := strconv.ParseFloat(text, 64)
	if v, ok := NewFloat(f); err == nil && ok {
		return v, nil
	}
	return nil, fmt.Errorf("number %s is beyond float's range", text)
}
