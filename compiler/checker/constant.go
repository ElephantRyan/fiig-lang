package checker

import (
	"strings"

	"fiig-lang/compiler/constant"
	"fiig-lang/compiler/decimal"
	"fiig-lang/compiler/format"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// ADR 0048 §2.4: number literals are untyped constants. A constant
// expression is evaluated exactly (compiler/constant), takes its type from
// where it lands, is range-checked there, and the value is recorded on the
// expression's root for the runtime.

var (
	floatType   = types.Primitive{Name: "float"}
	decimalType = types.Primitive{Name: "decimal"}
)

// isConstant reports whether e is a constant expression.
func isConstant(e syntax.Expr) bool {
	_, ok, _ := constant.Eval(e)
	return ok
}

// landConstant types e against want when e is a constant expression, and
// reports false otherwise. The type is the numeric member of want that
// suits the constant's kind, or the kind's default (int for an integer
// constant, float for a fractional one) when want names no number. A value
// that doesn't fit the type is a compile error, here, rather than a fault
// at runtime.
func (c *checker) landConstant(e syntax.Expr, want types.Type) (types.Type, bool) {
	v, ok, err := constant.Eval(e)
	if !ok {
		return nil, false
	}
	if err != nil {
		c.report(err.At, "", "%s", err.Msg)
		return types.Unknown{}, true
	}
	target := constantTarget(v.Kind, want)
	start, _ := e.(syntax.Spanned).Bounds()
	var landed syntax.Const
	var problem string
	switch target {
	case "int":
		landed.Int, problem = constant.ToInt(v)
	case "decimal":
		if v.Irrational {
			problem = "is irrational, so it has no exact decimal value and is not a decimal"
			break
		}
		var d decimal.Decimal
		d, problem = decimal.FromRat(v.Rat, constantScale(e))
		landed.Dec = &d
	default:
		landed.Float = true
		landed.F, problem = constant.ToFloat(v)
	}
	if problem != "" {
		code := ""
		if target == "int" && !v.Rat.IsInt() || target == "decimal" && strings.Contains(problem, "no exact") {
			code = "A1"
		}
		c.report(start, code, "constant %s %s", format.Expr(e), problem)
	} else {
		e.(syntax.ConstRoot).SetConstant(&landed)
	}
	return types.Primitive{Name: target}, true
}

// constantTarget picks the type a constant of kind lands as in want.
func constantTarget(kind constant.Kind, want types.Type) string {
	def := "int"
	if kind == constant.Float {
		def = "float"
	}
	var numeric []string
	members := []types.Type{want}
	if u, ok := want.(types.Union); ok {
		members = u.Members
	}
	for _, m := range members {
		if p, ok := m.(types.Primitive); ok && isNumeric(p) {
			if p.Name == def {
				return def
			}
			numeric = append(numeric, p.Name)
		}
	}
	if len(numeric) > 0 {
		return numeric[0]
	}
	return def
}

func isNumeric(t types.Type) bool {
	p, ok := t.(types.Primitive)
	return ok && (p.Name == "int" || p.Name == "float" || p.Name == "decimal")
}

// constantScale is the scale a constant lands with as a decimal (ADR 0064
// §7): a literal's, through any parentheses or `-`, or -1 for a computed
// constant, which takes the smallest scale that holds it exactly.
func constantScale(e syntax.Expr) int {
	switch e := e.(type) {
	case *syntax.IntLit:
		return 0
	case *syntax.FloatLit:
		return decimal.LiteralScale(e.Text)
	case *syntax.ParenExpr:
		return constantScale(e.Inner)
	case *syntax.NegExpr:
		return constantScale(e.Operand)
	}
	return -1
}

// operands types a binary expression's two sides. A constant side takes the
// other side's type, so `x * 2` multiplies two floats when x is one; two
// constants (only a comparison reaches here with both, since constant
// arithmetic is folded whole) land together, as float if either is
// fractional.
func (c *checker) operands(e *syntax.BinaryExpr, env map[string]types.Type) (types.Type, types.Type) {
	lv, lconst, _ := constant.Eval(e.Left)
	rv, rconst, _ := constant.Eval(e.Right)
	switch {
	case lconst && rconst:
		want := types.Type(intType)
		if lv.Kind == constant.Float || rv.Kind == constant.Float {
			want = floatType
		}
		lt, _ := c.landConstant(e.Left, want)
		rt, _ := c.landConstant(e.Right, want)
		return lt, rt
	case lconst:
		rt := c.inferExpr(e.Right, env)
		lt, _ := c.landConstant(e.Left, rt)
		return lt, rt
	case rconst:
		lt := c.inferExpr(e.Left, env)
		rt, _ := c.landConstant(e.Right, lt)
		return lt, rt
	}
	return c.inferExpr(e.Left, env), c.inferExpr(e.Right, env)
}

// arithmetic is the type of `+ - * / %` over lt and rt: both int or both
// float, never mixed (ADR 0019's addendum, ADR 0048 §2.2).
func (c *checker) arithmetic(e *syntax.BinaryExpr, lt, rt types.Type) types.Type {
	for i, t := range []types.Type{lt, rt} {
		if _, unknown := t.(types.Unknown); unknown || isNumeric(t) {
			continue
		}
		side := e.Left
		if i == 1 {
			side = e.Right
		}
		start, _ := side.(syntax.Spanned).Bounds()
		c.report(start, "A1", "type mismatch: got %s, expected int, float, or decimal", t)
		return types.Unknown{}
	}
	if isNumeric(lt) && isNumeric(rt) && !types.Equal(lt, rt) {
		c.report(e.Start, "A1", "type mismatch: %s %s %s mixes numeric types; convert one side with math.toFloat, math.toDecimal, or a math.*ToInt formula (ADR 0048 §2.2)", lt, e.Op, rt)
		return types.Unknown{}
	}
	if e.Op == syntax.OpDiv && types.Equal(lt, decimalType) {
		// ADR 0064 §3: a decimal quotient needs a precision and a rounding
		// mode, so the author writes both.
		c.report(e.Start, "A1", "`/` is not defined on decimal, since most quotients have no exact decimal value; divide with math.divide(%s, %s, places: 2, rounding: Rounding.HalfEven), choosing the places and rounding (ADR 0064 §3)", format.Expr(e.Left), format.Expr(e.Right))
		return types.Unknown{}
	}
	if isNumeric(lt) {
		return lt
	}
	return rt
}

// negate is the type of unary `-` on a non-constant operand.
func (c *checker) negate(e *syntax.NegExpr, env map[string]types.Type) types.Type {
	t := c.inferExpr(e.Operand, env)
	if _, unknown := t.(types.Unknown); unknown || isNumeric(t) {
		return t
	}
	start, _ := e.Operand.(syntax.Spanned).Bounds()
	c.report(start, "A1", "type mismatch: got %s, expected int, float, or decimal", t)
	return types.Unknown{}
}
