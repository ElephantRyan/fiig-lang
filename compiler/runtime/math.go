package runtime

import (
	"context"
	"fmt"
	"math"
	"strings"

	"fiig-lang/compiler/decimal"
	"fiig-lang/compiler/lexer"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// ADR 0049 §2.4: the prelude math group's formulas. Their names and forms
// are declared in compiler/types's table, which the checker types calls
// against; this file supplies only bodies, and
// TestEveryMathFormulaIsImplemented keeps the two in step. The checker has
// picked each call's form, so a body with an int, a float, or a decimal
// form dispatches on its first argument's value.
//
// A float result is finite (ADR 0048 §2.3): a body whose IEEE answer is
// NaN or infinite faults, through mathCall's floatResult.

// mathCall is one call in progress: its name and arguments, for fault
// messages, and where it is.
type mathCall struct {
	member string
	args   []Value
	at     lexer.Pos
}

func (c mathCall) faultf(format string, a ...any) *Fault {
	parts := make([]string, len(c.args))
	for i, v := range c.args {
		parts[i] = fmt.Sprint(v)
	}
	call := fmt.Sprintf("math.%s(%s)", c.member, strings.Join(parts, ", "))
	return Faultf("%s at %s %s", call, c.at, fmt.Sprintf(format, a...))
}

// floatResult is r as a fiig float, or the fault for a result that has
// none: NaN for an argument outside the formula's domain, an infinity for
// a pole or an overflow.
func (c mathCall) floatResult(r float64) (Value, *Fault) {
	if math.IsNaN(r) {
		return nil, c.faultf("is not a real number")
	}
	if f, ok := NewFloat(r); ok {
		return f, nil
	}
	return nil, c.faultf("has no finite result")
}

// toInt is r, already rounded to a whole number, as an int (ADR 0048 §2.7).
func (c mathCall) toInt(r float64) (Value, *Fault) {
	// int's range is [-2^63, 2^63), and both ends are exact floats.
	if r < -(1<<63) || r >= 1<<63 {
		return nil, c.faultf("does not fit in int (ADR 0048 §2.7)")
	}
	return Int(r), nil
}

// decimalResult is d, or the fault for a result over decimal's cap.
func (c mathCall) decimalResult(d decimal.Decimal, ok bool) (Value, *Fault) {
	if !ok {
		return nil, c.faultf("does not fit in decimal (34 digits written in full)")
	}
	return Decimal{d}, nil
}

type mathFunc func(c mathCall) (Value, *Fault)

func floatArg(c mathCall, i int) float64 { return float64(c.args[i].(Float)) }

func decimalArg(c mathCall, i int) decimal.Decimal { return c.args[i].(Decimal).Decimal }

// roundingArg is a `Rounding` argument as compiler/decimal's mode; the
// enum's variants are declared in the same order.
func roundingArg(c mathCall, i int) decimal.Rounding {
	variant := c.args[i].(EnumValue).Variant
	for mode, name := range decimal.RoundingNames {
		if name == variant {
			return decimal.Rounding(mode)
		}
	}
	panic("unknown Rounding variant " + variant)
}

// placesArg is a places argument. An int far outside any decimal's places
// rounds the same as the nearest bound compiler/decimal applies anyway.
func placesArg(c mathCall, i int) int {
	return int(max(min(int64(c.args[i].(Int)), 1<<20), -(1 << 20)))
}

func floatFunc(f func(float64) float64) mathFunc {
	return func(c mathCall) (Value, *Fault) { return c.floatResult(f(floatArg(c, 0))) }
}

// toIntFunc is a `*ToInt` crossing: round on a float, mode on a decimal
// (ADR 0064 §6 keeps the names rather than taking a rounding argument).
func toIntFunc(round func(float64) float64, mode decimal.Rounding) mathFunc {
	return func(c mathCall) (Value, *Fault) {
		if d, isDecimal := c.args[0].(Decimal); isDecimal {
			n, ok := d.Int64(mode)
			if !ok {
				return nil, c.faultf("does not fit in int (ADR 0064 §6)")
			}
			return Int(n), nil
		}
		return c.toInt(round(floatArg(c, 0)))
	}
}

// orNone is an `OrNone` formula (ADR 0063 §2): the operator's result, or
// None where the operator would fault.
func orNone(op syntax.BinaryOp) mathFunc {
	return func(c mathCall) (Value, *Fault) {
		if l, isInt := c.args[0].(Int); isInt {
			r := c.args[1].(Int)
			if op == syntax.OpDiv && r == 0 {
				return None{}, nil
			}
			v, ok := intArith(op, int64(l), int64(r))
			if !ok {
				return None{}, nil
			}
			return Int(v), nil
		}
		if l, isDecimal := c.args[0].(Decimal); isDecimal {
			v, fault := decimalArith(&syntax.BinaryExpr{Op: op}, l, c.args[1].(Decimal))
			if fault != nil {
				return None{}, nil
			}
			return v, nil
		}
		l, r := floatArg(c, 0), floatArg(c, 1)
		if op == syntax.OpDiv && r == 0 {
			return None{}, nil
		}
		v, ok := floatArith(op, l, r)
		if !ok {
			return None{}, nil
		}
		return Float(v), nil
	}
}

// pick is min, or max when max is set, in either form. Ints are compared
// as ints, since two near maxInt can round to the same float.
func pick(max bool) mathFunc {
	return func(c mathCall) (Value, *Fault) {
		var lessOrEqual bool
		if l, isInt := c.args[0].(Int); isInt {
			lessOrEqual = l <= c.args[1].(Int)
		} else if l, isDecimal := c.args[0].(Decimal); isDecimal {
			lessOrEqual = l.Cmp(decimalArg(c, 1)) <= 0
		} else {
			lessOrEqual = floatArg(c, 0) <= floatArg(c, 1)
		}
		if lessOrEqual != max || Equal(c.args[0], c.args[1]) {
			return c.args[0], nil
		}
		return c.args[1], nil
	}
}

var mathFormulas = map[string]mathFunc{
	// ADR 0048 §2.7's crossings; the table there gives -3.5 for each.
	"floorToInt":         toIntFunc(math.Floor, decimal.Floor),
	"ceilToInt":          toIntFunc(math.Ceil, decimal.Ceiling),
	"truncToInt":         toIntFunc(math.Trunc, decimal.Down),
	"roundToInt":         toIntFunc(math.Round, decimal.HalfUp),
	"roundHalfEvenToInt": toIntFunc(math.RoundToEven, decimal.HalfEven),
	// Every int64 and every decimal is inside binary64's range, so this
	// rounds (ties to even) and never faults.
	"toFloat": func(c mathCall) (Value, *Fault) {
		if d, isDecimal := c.args[0].(Decimal); isDecimal {
			return Float(d.Float64()), nil
		}
		return Float(float64(c.args[0].(Int))), nil
	},
	// ADR 0064 §6: an int is exact; a float is the shortest decimal that
	// converts back to it, and faults outside decimal's range.
	"toDecimal": func(c mathCall) (Value, *Fault) {
		if n, isInt := c.args[0].(Int); isInt {
			return Decimal{decimal.FromInt(int64(n))}, nil
		}
		return c.decimalResult(decimal.FromFloat(floatArg(c, 0)))
	},

	"floor": floatFunc(math.Floor),
	"ceil":  floatFunc(math.Ceil),
	"round": func(c mathCall) (Value, *Fault) {
		if d, isDecimal := c.args[0].(Decimal); isDecimal {
			return c.decimalResult(d.Round(placesArg(c, 1), roundingArg(c, 2)))
		}
		return c.floatResult(math.Round(floatArg(c, 0)))
	},
	"trunc": floatFunc(math.Trunc),
	"sqrt":  floatFunc(math.Sqrt),
	"exp":   floatFunc(math.Exp),
	"log":   floatFunc(math.Log),
	"log10": floatFunc(math.Log10),
	"sin":   floatFunc(math.Sin),
	"cos":   floatFunc(math.Cos),
	"tan":   floatFunc(math.Tan),
	"asin":  floatFunc(math.Asin),
	"acos":  floatFunc(math.Acos),
	"atan":  floatFunc(math.Atan),
	"atan2": func(c mathCall) (Value, *Fault) {
		return c.floatResult(math.Atan2(floatArg(c, 0), floatArg(c, 1)))
	},
	"pow": func(c mathCall) (Value, *Fault) {
		return c.floatResult(math.Pow(floatArg(c, 0), floatArg(c, 1)))
	},

	"abs": func(c mathCall) (Value, *Fault) {
		if n, isInt := c.args[0].(Int); isInt {
			if n >= 0 {
				return n, nil
			}
			v, ok := intNeg(int64(n))
			if !ok {
				return nil, c.faultf("does not fit in int (ADR 0063)")
			}
			return Int(v), nil
		}
		if d, isDecimal := c.args[0].(Decimal); isDecimal {
			if d.Sign() < 0 {
				return Decimal{d.Neg()}, nil
			}
			return d, nil
		}
		return Float(math.Abs(floatArg(c, 0))), nil
	},
	"min": pick(false),
	"max": pick(true),
	// floorMod's remainder takes the divisor's sign, where `%`'s takes the
	// dividend's (ADR 0048 §2.8).
	"floorMod": func(c mathCall) (Value, *Fault) {
		if l, isInt := c.args[0].(Int); isInt {
			r := c.args[1].(Int)
			if r == 0 {
				return nil, c.faultf("divides by zero")
			}
			m := l % r
			if m != 0 && (m < 0) != (r < 0) {
				m += r
			}
			return m, nil
		}
		if l, isDecimal := c.args[0].(Decimal); isDecimal {
			r := decimalArg(c, 1)
			if r.Sign() == 0 {
				return nil, c.faultf("divides by zero")
			}
			m := l.Rem(r)
			if m.Sign() != 0 && m.Sign() != r.Sign() {
				return c.decimalResult(m.Add(r))
			}
			return Decimal{m}, nil
		}
		l, r := floatArg(c, 0), floatArg(c, 1)
		if r == 0 {
			return nil, c.faultf("divides by zero")
		}
		m := math.Mod(l, r)
		if m != 0 && (m < 0) != (r < 0) {
			m += r
		}
		return c.floatResult(m)
	},

	"addOrNone": orNone(syntax.OpAdd),
	"subOrNone": orNone(syntax.OpSub),
	"mulOrNone": orNone(syntax.OpMul),
	"divOrNone": orNone(syntax.OpDiv),

	// ADR 0064 §3: the exact quotient, rounded once.
	"divide": func(c mathCall) (Value, *Fault) {
		b := decimalArg(c, 1)
		if b.Sign() == 0 {
			return nil, c.faultf("divides by zero")
		}
		return c.decimalResult(decimal.Quo(decimalArg(c, 0), b, placesArg(c, 2), roundingArg(c, 3)))
	},
	// ADR 0064 §3a: shares that add back to exactly the amount.
	"allocate": func(c mathCall) (Value, *Fault) {
		shares, problem := decimal.Allocate(decimalArg(c, 0), int64(c.args[1].(Int)), placesArg(c, 2))
		if problem != "" {
			return nil, c.faultf("%s", problem)
		}
		elems := make([]Value, len(shares))
		for i, d := range shares {
			elems[i] = Decimal{d}
		}
		return List{Elems: elems}, nil
	},

	"closeTo": func(c mathCall) (Value, *Fault) {
		relative, absolute, f := tolerances(c)
		if f != nil {
			return nil, f
		}
		return Bool(isClose(floatArg(c, 0), floatArg(c, 1), relative, absolute)), nil
	},
	// ADR 0065 §1: closeTo's rule where == is refused for floats.
	"containsClose": func(c mathCall) (Value, *Fault) {
		relative, absolute, f := tolerances(c)
		if f != nil {
			return nil, f
		}
		x := floatArg(c, 1)
		for _, e := range c.args[0].(List).Elems {
			if isClose(float64(e.(Float)), x, relative, absolute) {
				return Bool(true), nil
			}
		}
		return Bool(false), nil
	},
	"allClose": func(c mathCall) (Value, *Fault) {
		relative, absolute, f := tolerances(c)
		if f != nil {
			return nil, f
		}
		if am, ok := c.args[0].(Map); ok {
			// Same keys, each value close (ADR 0065 §1).
			bm := c.args[1].(Map)
			close := am.Len() == bm.Len()
			am.Each(func(k, v Value) bool {
				bv, ok := bm.Get(k)
				close = ok && isClose(float64(v.(Float)), float64(bv.(Float)), relative, absolute)
				return close
			})
			return Bool(close), nil
		}
		a, b := c.args[0].(List).Elems, c.args[1].(List).Elems
		if len(a) != len(b) {
			return Bool(false), nil
		}
		for i := range a {
			if !isClose(float64(a[i].(Float)), float64(b[i].(Float)), relative, absolute) {
				return Bool(false), nil
			}
		}
		return Bool(true), nil
	},
	"isZero": func(c mathCall) (Value, *Fault) {
		return Bool(floatArg(c, 0) == 0), nil
	},
}

// tolerances is a closeness call's relative and absolute arguments, its
// last two.
func tolerances(c mathCall) (relative, absolute float64, f *Fault) {
	relative, absolute = floatArg(c, len(c.args)-2), floatArg(c, len(c.args)-1)
	if relative < 0 || absolute < 0 {
		return 0, 0, c.faultf("has a negative tolerance; relative and absolute must be at least 0.0")
	}
	return relative, absolute, nil
}

// isClose is Python's math.isclose (ADR 0051): symmetric, and the larger of
// the two tolerances wins. A difference too large for a float is not close.
func isClose(a, b, relative, absolute float64) bool {
	limit := math.Max(relative*math.Max(math.Abs(a), math.Abs(b)), absolute)
	return math.Abs(a-b) <= limit
}

// callMath calls `math.member(args)`. The checker picked the call's form
// and landed every constant argument in it, so the form here is the one
// whose parameters the arguments bind to and whose types the values have.
func (in *Interp) callMath(ctx context.Context, fa *syntax.FieldAccess, args []syntax.Arg, env *Env) (Value, bool, *Fault) {
	forms, ok := types.MathFormula(fa.Field)
	body := mathFormulas[fa.Field]
	if !ok || body == nil {
		return nil, false, Faultf("the prelude math group has no formula %q", fa.Field)
	}
	vals, earlyReturn, fault := in.evalArgsPositional(ctx, args, env)
	if fault != nil || earlyReturn {
		return nil, earlyReturn, fault
	}
	argNames := make([]string, len(args))
	for i, a := range args {
		argNames[i] = a.Name
	}
	for _, form := range forms {
		at, ok := types.BindMathArgs(form.Params, argNames)
		if !ok {
			continue
		}
		call := mathCall{member: fa.Field, args: make([]Value, len(at)), at: fa.Start}
		fits := true
		for i, ai := range at {
			call.args[i] = vals[ai]
			fits = fits && valueHasType(vals[ai], form.Params[i].Type)
		}
		if fits {
			v, f := body(call)
			return v, false, f
		}
	}
	return nil, false, Faultf("math.%s has no form taking these arguments", fa.Field)
}

// valueHasType is whether v is a value of a math parameter's type.
func valueHasType(v Value, t types.Type) bool {
	switch t := t.(type) {
	case types.Unknown:
		return true
	case types.Primitive:
		switch t.Name {
		case "int":
			_, ok := v.(Int)
			return ok
		case "float":
			_, ok := v.(Float)
			return ok
		case "decimal":
			_, ok := v.(Decimal)
			return ok
		}
	case types.Named:
		e, ok := v.(EnumValue)
		return ok && e.TypeName == t.Name
	case types.Generic:
		if m, ok := v.(Map); ok && t.Name == "Map" {
			fits := true
			m.Each(func(k, val Value) bool {
				fits = valueHasType(val, t.Args[1])
				return fits
			})
			return fits
		}
		l, ok := v.(List)
		if !ok || t.Name != "List" {
			return false
		}
		for _, e := range l.Elems {
			if !valueHasType(e, t.Args[0]) {
				return false
			}
		}
		return true
	}
	return false
}
