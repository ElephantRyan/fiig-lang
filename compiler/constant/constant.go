// Package constant evaluates untyped constant expressions (ADR 0048 §2.4):
// number literals, the prelude math group's constants (`math.pi`), and
// `-`, parentheses, and `+ - * / %` over them. A
// constant has an exact value and a kind but no type; the checker gives it
// a type where it lands, and rounds it once, there. Go's constants work the
// same way (go/constant), and so does the one wart ADR 0048 names: `7 / 2`
// is integer division even in a float context, because both operands are
// integer constants.
package constant

import (
	"fmt"
	"math"
	"math/big"
	"strings"

	"fiig-lang/compiler/lexer"
	"fiig-lang/compiler/syntax"
)

// Kind is what a constant's literals were: all integers, or at least one
// fraction or exponent. It decides `/` and `%` and the default type.
type Kind int

const (
	Int Kind = iota
	Float
)

// Value is an exact constant. Irrational marks a value computed from an
// irrational constant (`math.pi`), whose Rat is only an approximation: it
// rounds to a float like any constant, but has no exact decimal value.
type Value struct {
	Kind       Kind
	Rat        *big.Rat
	Irrational bool
}

// Error is a constant expression that has no value: a division by zero, or
// a literal whose exponent is too large to evaluate exactly.
type Error struct {
	At  lexer.Pos
	Msg string
}

func (e *Error) Error() string { return e.Msg }

// maxExponent bounds a literal's decimal exponent. Exact arithmetic on
// 1e1000000000 would build a billion-digit integer; nothing a program means
// is anywhere near this, and binary64 itself ends at about 1e308.
const maxExponent = 10000

// Eval returns e's value when e is a constant expression. ok is false when
// it is not one, and err is set when it is one with no value. A subexpression
// with an error makes the whole expression an error, reported once.
func Eval(e syntax.Expr) (v Value, ok bool, err *Error) {
	switch e := e.(type) {
	case *syntax.IntLit:
		n, valid := parseInt(e.Text)
		if !valid {
			return Value{}, true, &Error{At: e.Start, Msg: fmt.Sprintf("invalid integer literal %s", e.Text)}
		}
		return Value{Kind: Int, Rat: new(big.Rat).SetInt(n)}, true, nil
	case *syntax.FloatLit:
		if exp := exponent(e.Text); exp > maxExponent || exp < -maxExponent {
			return Value{}, true, &Error{At: e.Start, Msg: fmt.Sprintf("float literal %s has an exponent beyond ±%d", e.Text, maxExponent)}
		}
		r, valid := new(big.Rat).SetString(e.Text)
		if !valid {
			return Value{}, true, &Error{At: e.Start, Msg: fmt.Sprintf("invalid float literal %s", e.Text)}
		}
		return Value{Kind: Float, Rat: r}, true, nil
	case *syntax.ParenExpr:
		return Eval(e.Inner)
	case *syntax.FieldAccess:
		// `math` is reserved in every scope (ADR 0049 §2.5), so `math.pi`
		// is always the prelude constant.
		if t, ok := e.Target.(*syntax.Ident); ok && t.Name == "math" {
			if v, ok := MathConstant(e.Field); ok {
				return v, true, nil
			}
		}
	case *syntax.NegExpr:
		v, ok, err := Eval(e.Operand)
		if !ok || err != nil {
			return v, ok, err
		}
		return Value{Kind: v.Kind, Rat: new(big.Rat).Neg(v.Rat), Irrational: v.Irrational}, true, nil
	case *syntax.BinaryExpr:
		switch e.Op {
		case syntax.OpAdd, syntax.OpSub, syntax.OpMul, syntax.OpDiv, syntax.OpMod:
		default:
			return Value{}, false, nil
		}
		l, lok, lerr := Eval(e.Left)
		if !lok {
			return Value{}, false, nil
		}
		r, rok, rerr := Eval(e.Right)
		if !rok {
			return Value{}, false, nil
		}
		if lerr != nil {
			return Value{}, true, lerr
		}
		if rerr != nil {
			return Value{}, true, rerr
		}
		return arith(e, l, r)
	}
	return Value{}, false, nil
}

func arith(e *syntax.BinaryExpr, l, r Value) (Value, bool, *Error) {
	kind := Int
	if l.Kind == Float || r.Kind == Float {
		kind = Float
	}
	out := new(big.Rat)
	switch e.Op {
	case syntax.OpAdd:
		out.Add(l.Rat, r.Rat)
	case syntax.OpSub:
		out.Sub(l.Rat, r.Rat)
	case syntax.OpMul:
		out.Mul(l.Rat, r.Rat)
	case syntax.OpDiv, syntax.OpMod:
		if r.Rat.Sign() == 0 {
			return Value{}, true, &Error{At: e.Start, Msg: "constant division by zero"}
		}
		quo := new(big.Rat).Quo(l.Rat, r.Rat)
		if kind == Int || e.Op == syntax.OpMod {
			// Truncated toward zero, so the remainder takes the dividend's
			// sign on both kinds (ADR 0048 §2.8).
			quo.SetInt(new(big.Int).Quo(quo.Num(), quo.Denom()))
		}
		if e.Op == syntax.OpDiv {
			out = quo
		} else {
			out.Sub(l.Rat, new(big.Rat).Mul(r.Rat, quo))
		}
	}
	return Value{Kind: kind, Rat: out, Irrational: l.Irrational || r.Irrational}, true, nil
}

// ToInt is v as an int: it must be a whole number within int64. The
// returned problem completes "constant <expr> ...".
func ToInt(v Value) (n int64, problem string) {
	if !v.Rat.IsInt() {
		return 0, "is not a whole number, so it is not an int"
	}
	if !v.Rat.Num().IsInt64() {
		return 0, "does not fit in int (-9223372036854775808 to 9223372036854775807)"
	}
	return v.Rat.Num().Int64(), ""
}

// ToFloat is v rounded once to the nearest binary64. Overflow is an error
// (ADR 0048 §2.3: there is no Inf); underflow rounds toward 0.0, which is
// never -0.0.
func ToFloat(v Value) (f float64, problem string) {
	f, _ = v.Rat.Float64()
	if math.IsInf(f, 0) {
		return 0, "does not fit in float (about ±1.8e308)"
	}
	if f == 0 {
		f = 0
	}
	return f, ""
}

func parseInt(text string) (*big.Int, bool) {
	if len(text) > 2 && text[0] == '0' && (text[1] == 'x' || text[1] == 'X') {
		return new(big.Int).SetString(text[2:], 16)
	}
	return new(big.Int).SetString(text, 10)
}

// exponent is a float literal's written exponent, or 0. The digits are
// scanned with a cap, so a hostile exponent can't overflow the scan.
func exponent(text string) int {
	i := strings.IndexAny(text, "eE")
	if i < 0 {
		return 0
	}
	digits := text[i+1:]
	sign := 1
	if digits != "" && (digits[0] == '+' || digits[0] == '-') {
		if digits[0] == '-' {
			sign = -1
		}
		digits = digits[1:]
	}
	n := 0
	for _, d := range digits {
		n = n*10 + int(d-'0')
		if n > maxExponent {
			return sign * (maxExponent + 1)
		}
	}
	return sign * n
}
