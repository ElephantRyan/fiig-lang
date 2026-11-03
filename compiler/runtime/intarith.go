package runtime

import (
	"math"

	"fiig-lang/compiler/syntax"
)

// intArith applies an int operator, reporting false when the true result
// doesn't fit in int64 (ADR 0063 §1). For `/` the only such case is
// MinInt64 / -1; `%` can't overflow, since MinInt64 % -1 is 0, the true
// result. The caller has already rejected a zero divisor.
func intArith(op syntax.BinaryOp, l, r int64) (int64, bool) {
	switch op {
	case syntax.OpAdd:
		s := l + r
		// Overflow happened exactly when both operands share a sign the
		// sum doesn't.
		return s, (l >= 0) != (r >= 0) || (s >= 0) == (l >= 0)
	case syntax.OpSub:
		d := l - r
		return d, (l >= 0) == (r >= 0) || (d >= 0) == (l >= 0)
	case syntax.OpMul:
		if l == 0 || r == 0 {
			return 0, true
		}
		p := l * r
		if (l == -1 && r == math.MinInt64) || (r == -1 && l == math.MinInt64) || p/r != l {
			return p, false
		}
		return p, true
	case syntax.OpDiv:
		if l == math.MinInt64 && r == -1 {
			return 0, false
		}
		return l / r, true
	case syntax.OpMod:
		return l % r, true
	}
	return 0, false
}

// intNeg is unary `-`; MinInt64 is its one overflowing input.
func intNeg(n int64) (int64, bool) {
	return -n, n != math.MinInt64
}
