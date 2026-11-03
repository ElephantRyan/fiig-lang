package runtime

import (
	"fmt"

	"fiig-lang/compiler/decimal"
	"fiig-lang/compiler/syntax"
)

// Decimal is the language's decimal type (ADR 0064): exact, at most 34
// digits written in full, and keeping its scale for display. == and
// ordering compare values, so 1.50 equals 1.5 though they print
// differently (§5).
type Decimal struct{ decimal.Decimal }

func (Decimal) isValue() {}

// decimalArith is `+ - * %` on two decimals; `/` is a compile error (§3),
// so it is reached only by an unchecked program. The caller has already
// rejected a zero divisor.
func decimalArith(e *syntax.BinaryExpr, l, r Decimal) (Value, *Fault) {
	var out decimal.Decimal
	ok := true
	switch e.Op {
	case syntax.OpAdd:
		out, ok = l.Add(r.Decimal)
	case syntax.OpSub:
		out, ok = l.Sub(r.Decimal)
	case syntax.OpMul:
		out, ok = l.Mul(r.Decimal)
	case syntax.OpMod:
		out = l.Rem(r.Decimal)
	default:
		return nil, Faultf("%s is not defined on decimal; use math.divide", e.Op)
	}
	if !ok {
		return nil, Faultf("decimal overflow at %s: %s %s %s does not fit in decimal (34 digits written in full)", e.Start, l, e.Op, r)
	}
	return Decimal{out}, nil
}

// jsonDecimal decodes a JSON number as a decimal, exactly and keeping its
// scale. A number with no decimal of 34 digits written in full, such as
// 1e6000, is the input's problem rather than the program's, so it is an
// error and not a fault (ADR 0064 §8).
func jsonDecimal(text string) (Value, error) {
	if d, ok := decimal.Parse(text); ok {
		return Decimal{d}, nil
	}
	return nil, fmt.Errorf("number %s does not fit in decimal (34 digits written in full)", text)
}
