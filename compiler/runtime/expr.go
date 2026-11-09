package runtime

import (
	"context"
	"strconv"
	"strings"

	"fiig-lang/compiler/syntax"
)

// evalExpr evaluates e. The bool result is "early return": a `?` (TryExpr)
// that hit Err — the whole enclosing declaration returns that Err value
// immediately, same as an explicit `return`, and every caller of evalExpr
// in this package must check and forward it rather than keep evaluating
// sibling expressions.
func (in *Interp) evalExpr(ctx context.Context, e syntax.Expr, env *Env) (Value, bool, *Fault) {
	if v, ok := constValue(e); ok {
		return v, false, nil
	}
	switch e := e.(type) {
	case *syntax.Ident:
		if e.Name == "None" {
			return None{}, false, nil
		}
		if e.Name == "true" || e.Name == "false" {
			return Bool(e.Name == "true"), false, nil
		}
		v, ok := env.Get(e.Name)
		if !ok {
			if p, isGroup := in.groupValue(in.scope(ctx), e.Name); isGroup {
				return p, false, nil
			}
			return nil, false, Faultf("undefined name %q", e.Name)
		}
		return v, false, nil
	case *syntax.IntLit:
		n, err := syntax.IntValue(e.Text)
		if err != nil {
			return nil, false, Faultf("invalid integer literal %q", e.Text)
		}
		return Int(n), false, nil
	case *syntax.FloatLit:
		// Only reached for a literal the checker never typed.
		f, err := strconv.ParseFloat(e.Text, 64)
		f, ok := finite(f)
		if err != nil || !ok {
			return nil, false, Faultf("invalid float literal %q", e.Text)
		}
		return Float(f), false, nil
	case *syntax.StringLit:
		var sb strings.Builder
		for _, part := range e.Parts {
			if part.Interp == nil {
				sb.WriteString(part.Text)
				continue
			}
			v, earlyReturn, fault := in.evalExpr(ctx, part.Interp, env)
			if fault != nil || earlyReturn {
				return v, earlyReturn, fault
			}
			sb.WriteString(v.String())
		}
		return Str(sb.String()), false, nil
	case *syntax.FieldAccess:
		return in.evalFieldAccess(ctx, e, env)
	case *syntax.CallExpr:
		return in.evalCall(ctx, e, env)
	case *syntax.NotExpr:
		v, earlyReturn, fault := in.evalExpr(ctx, e.Operand, env)
		if fault != nil || earlyReturn {
			return v, earlyReturn, fault
		}
		b, ok := v.(Bool)
		if !ok {
			return nil, false, Faultf("`not` operand is not a boolean (got %s)", v)
		}
		return Bool(!b), false, nil
	case *syntax.NegExpr:
		v, earlyReturn, fault := in.evalExpr(ctx, e.Operand, env)
		if fault != nil || earlyReturn {
			return v, earlyReturn, fault
		}
		if f, ok := v.(Float); ok {
			neg, _ := finite(-float64(f))
			return Float(neg), false, nil
		}
		if d, ok := v.(Decimal); ok {
			return Decimal{d.Neg()}, false, nil
		}
		n, ok := v.(Int)
		if !ok {
			return nil, false, Faultf("unary `-` operand is not a number (got %s)", v)
		}
		neg, ok := intNeg(int64(n))
		if !ok {
			return nil, false, Faultf("integer overflow at %s: -(%d) does not fit in int", e.Start, n)
		}
		return Int(neg), false, nil
	case *syntax.BinaryExpr:
		return in.evalBinary(ctx, e, env)
	case *syntax.TryExpr:
		v, earlyReturn, fault := in.evalExpr(ctx, e.Operand, env)
		if fault != nil || earlyReturn {
			return v, earlyReturn, fault
		}
		rv, ok := v.(Result)
		if !ok {
			return nil, false, Faultf("`?` operand is not a Result (got %s)", v)
		}
		if rv.Ok {
			return rv.Value, false, nil
		}
		return rv, true, nil
	case *syntax.ParenExpr:
		return in.evalExpr(ctx, e.Inner, env)
	case *syntax.BytesLit:
		return in.evalBytesLit(ctx, e, env)
	case *syntax.MapLit:
		// ADR 0065 §5: a repeated key the checker couldn't see is a fault,
		// never a silent overwrite.
		m := Map{}
		for _, en := range e.Entries {
			k, earlyReturn, fault := in.evalExpr(ctx, en.Key, env)
			if fault != nil || earlyReturn {
				return k, earlyReturn, fault
			}
			v, earlyReturn, fault := in.evalExpr(ctx, en.Value, env)
			if fault != nil || earlyReturn {
				return v, earlyReturn, fault
			}
			if _, dup := m.Get(k); dup {
				return nil, false, Faultf("the map literal at %s repeats the key %s; build a map with repeated keys with put", e.Start, k)
			}
			m = m.Put(k, v)
		}
		return m, false, nil
	case *syntax.ListLit:
		elems := make([]Value, len(e.Elems))
		for i, el := range e.Elems {
			v, earlyReturn, fault := in.evalExpr(ctx, el, env)
			if fault != nil || earlyReturn {
				return v, earlyReturn, fault
			}
			elems[i] = v
		}
		return List{Elems: elems}, false, nil
	case *syntax.ConcurrentExpr:
		return in.evalConcurrent(ctx, e, env)
	case *syntax.SortedExpr:
		return in.evalSorted(ctx, e, env)
	case *syntax.RetryExpr:
		return in.evalRetry(ctx, e, env)
	default:
		return nil, false, Faultf("unsupported expression %T", e)
	}
}

func (in *Interp) evalBinary(ctx context.Context, e *syntax.BinaryExpr, env *Env) (Value, bool, *Fault) {
	// `and`/`or` short-circuit, so the right operand is only evaluated when
	// it can still change the result — the usual meaning, and the only one
	// attested anywhere these appear (validate_new_customer.fiig's `not
	// ... .contains(...)`  doesn't even exercise and/or, but nothing in the
	// corpus needs the right side evaluated unconditionally).
	if e.Op == syntax.OpAnd || e.Op == syntax.OpOr {
		left, earlyReturn, fault := in.evalExpr(ctx, e.Left, env)
		if fault != nil || earlyReturn {
			return left, earlyReturn, fault
		}
		lb, ok := left.(Bool)
		if !ok {
			return nil, false, Faultf("%s operand is not a boolean (got %s)", e.Op, left)
		}
		if e.Op == syntax.OpAnd && !bool(lb) {
			return Bool(false), false, nil
		}
		if e.Op == syntax.OpOr && bool(lb) {
			return Bool(true), false, nil
		}
		right, earlyReturn, fault := in.evalExpr(ctx, e.Right, env)
		if fault != nil || earlyReturn {
			return right, earlyReturn, fault
		}
		rb, ok := right.(Bool)
		if !ok {
			return nil, false, Faultf("%s operand is not a boolean (got %s)", e.Op, right)
		}
		return rb, false, nil
	}

	left, earlyReturn, fault := in.evalExpr(ctx, e.Left, env)
	if fault != nil || earlyReturn {
		return left, earlyReturn, fault
	}
	right, earlyReturn, fault := in.evalExpr(ctx, e.Right, env)
	if fault != nil || earlyReturn {
		return right, earlyReturn, fault
	}

	switch e.Op {
	case syntax.OpAdd, syntax.OpSub, syntax.OpMul, syntax.OpDiv, syntax.OpMod:
		if lf, ok := left.(Float); ok {
			rf, ok := right.(Float)
			if !ok {
				return nil, false, Faultf("arithmetic operands are a float and %s", right)
			}
			// ADR 0048 §2.3: x / 0.0 is Inf or NaN in IEEE 754, and a fault
			// here, as 7 / 0 is.
			if (e.Op == syntax.OpDiv || e.Op == syntax.OpMod) && rf == 0 {
				return nil, false, Faultf("division by zero")
			}
			f, ok := floatArith(e.Op, float64(lf), float64(rf))
			if !ok {
				return nil, false, Faultf("float overflow at %s: %s %s %s is beyond float's range", e.Start, lf, e.Op, rf)
			}
			return Float(f), false, nil
		}
		if ld, ok := left.(Decimal); ok {
			rd, ok := right.(Decimal)
			if !ok {
				return nil, false, Faultf("arithmetic operands are a decimal and %s", right)
			}
			if (e.Op == syntax.OpDiv || e.Op == syntax.OpMod) && rd.Sign() == 0 {
				return nil, false, Faultf("division by zero")
			}
			v, fault := decimalArith(e, ld, rd)
			return v, false, fault
		}
		li, ok := left.(Int)
		if !ok {
			return nil, false, Faultf("arithmetic operand is not an int (got %s)", left)
		}
		ri, ok := right.(Int)
		if !ok {
			return nil, false, Faultf("arithmetic operand is not an int (got %s)", right)
		}
		if (e.Op == syntax.OpDiv || e.Op == syntax.OpMod) && ri == 0 {
			return nil, false, Faultf("division by zero")
		}
		n, ok := intArith(e.Op, int64(li), int64(ri))
		if !ok {
			return nil, false, Faultf("integer overflow at %s: %d %s %d does not fit in int", e.Start, li, e.Op, ri)
		}
		return Int(n), false, nil
	case syntax.OpEq:
		return Bool(Equal(left, right)), false, nil
	case syntax.OpNe:
		return Bool(!Equal(left, right)), false, nil
	case syntax.OpLt, syntax.OpLe, syntax.OpGt, syntax.OpGe:
		// Trichotomy over Int/Str (the only two Less-ordered types): every
		// other comparison reduces to Less in one direction or the other,
		// so there's exactly one well-definedness check to make.
		switch e.Op {
		case syntax.OpLt:
			less, ok := Less(left, right)
			if !ok {
				return nil, false, Faultf("%s and %s are not ordered", left, right)
			}
			return Bool(less), false, nil
		case syntax.OpGt:
			greater, ok := Less(right, left)
			if !ok {
				return nil, false, Faultf("%s and %s are not ordered", left, right)
			}
			return Bool(greater), false, nil
		case syntax.OpLe:
			greater, ok := Less(right, left)
			if !ok {
				return nil, false, Faultf("%s and %s are not ordered", left, right)
			}
			return Bool(!greater), false, nil
		case syntax.OpGe:
			less, ok := Less(left, right)
			if !ok {
				return nil, false, Faultf("%s and %s are not ordered", left, right)
			}
			return Bool(!less), false, nil
		}
	}
	return nil, false, Faultf("unsupported binary operator %s", e.Op)
}

// evalFieldAccess handles a model field (customer.id) and an anonymous structural
// record's field (a simulated capability's `row.id`) — all represented as
// Record (value.go). None is deliberately not handled: every corpus match
// that could expose a None to a FieldAccess already filters it out via the
// `None ->` arm first (bindPattern never binds the catch-all name to a bare
// None), so reaching one here is a real defect, not a case to special-case
// around.
func (in *Interp) evalFieldAccess(ctx context.Context, e *syntax.FieldAccess, env *Env) (Value, bool, *Fault) {
	// A zero-field enum variant construction (CheckoutError.OutOfStock)
	// parses bare, with no call parens — same shape as this function's own
	// Record-field case, but Target names a type, not a value, so it must
	// be resolved against the universe before falling through to the
	// generic path below (which would otherwise try, and fail, to look
	// "CheckoutError" up as a local variable). Mirrors the checker's own
	// inferFieldAccess special-case.
	if targetIdent, ok := e.Target.(*syntax.Ident); ok {
		if _, isLocal := env.Get(targetIdent.Name); !isLocal {
			sc := in.scope(ctx)
			if g, ok := sc.groups[targetIdent.Name]; ok && g.Config != nil {
				v, fault := in.configValue(g.Config, e.Field)
				return v, false, fault
			}
			if decl, found := sc.universe.Lookup(targetIdent.Name); found {
				if cd, ok := decl.(*syntax.ConfigDecl); ok {
					v, fault := in.configValue(cd, e.Field)
					return v, false, fault
				}
				if enumDecl, ok := decl.(*syntax.EnumDecl); ok {
					for _, v := range enumDecl.Variants {
						if v.Name == e.Field {
							return in.constructEnum(ctx, enumDecl, v, nil, env)
						}
					}
					return nil, false, Faultf("%s has no variant %q", enumDecl.Name, e.Field)
				}
			}
		}
	}

	target, earlyReturn, fault := in.evalExpr(ctx, e.Target, env)
	if fault != nil || earlyReturn {
		return target, earlyReturn, fault
	}
	if ev, ok := target.(EnumValue); ok {
		consts, fault := in.headerConsts(ctx, ev)
		if fault != nil {
			return nil, false, fault
		}
		if v, ok := consts[e.Field]; ok {
			return v, false, nil
		}
	}
	rec, ok := target.(Record)
	if !ok {
		if s, ok := target.(Str); ok {
			return s, false, Faultf("%s has no field %q", s, e.Field)
		}
		return nil, false, Faultf("%s has no field %q", target, e.Field)
	}
	v, ok := rec.Fields[e.Field]
	if !ok {
		return nil, false, Faultf("%s has no field %q", rec, e.Field)
	}
	return v, false, nil
}
