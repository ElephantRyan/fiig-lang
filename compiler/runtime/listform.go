package runtime

import (
	"context"

	"fiig-lang/compiler/decimal"
	"fiig-lang/compiler/syntax"
)

// evalListForm is one of ADR 0093's list forms. The body is evaluated
// once per element, in list order; any and all stop at the first element
// that decides them.
func (in *Interp) evalListForm(ctx context.Context, e *syntax.ListForm, env *Env) (Value, bool, *Fault) {
	if e.Form == "mapErr" {
		return in.evalMapErr(ctx, e, env)
	}
	v, earlyReturn, fault := in.formList(ctx, e.Form, e.Iterable, env)
	if fault != nil || earlyReturn {
		return v, earlyReturn, fault
	}
	elems := v.(List).Elems
	var (
		out     []Value
		groups  Map       // key -> Int, the group's index in members
		members [][]Value // each group's elements, in list order
		acc     Value
		n       int64
	)
	if e.Form == "sum" {
		switch e.SumType {
		case "float":
			acc = Float(0)
		case "decimal":
			acc = Decimal{decimal.FromInt(0)}
		default:
			acc = Int(0)
		}
	}
	plus := &syntax.BinaryExpr{Span: e.Span, Op: syntax.OpAdd}
	for _, elem := range elems {
		bodyEnv := env.Child()
		bodyEnv.Bind(e.Var, elem)
		b, earlyReturn, fault := in.evalExpr(ctx, e.Body, bodyEnv)
		if fault != nil || earlyReturn {
			return b, earlyReturn, fault
		}
		switch e.Form {
		case "filter":
			if b == Bool(true) {
				out = append(out, elem)
			}
		case "map":
			out = append(out, b)
		case "count":
			if b == Bool(true) {
				n++
			}
		case "any":
			if b == Bool(true) {
				return Bool(true), false, nil
			}
		case "all":
			if b != Bool(true) {
				return Bool(false), false, nil
			}
		case "groupBy":
			if at, ok := groups.Get(b); ok {
				members[at.(Int)] = append(members[at.(Int)], elem)
			} else {
				groups = groups.Put(b, Int(len(members)))
				members = append(members, []Value{elem})
			}
		case "sum":
			s, _, fault := in.applyBinary(plus, acc, b)
			if fault != nil {
				return nil, false, fault
			}
			acc = s
		default: // max, min, and their OrNone forms
			if acc == nil {
				acc = b
				break
			}
			a, c := acc, b
			if e.Form == "max" || e.Form == "maxOrNone" {
				a, c = b, acc
			}
			less, ok := Less(c, a)
			if !ok {
				return nil, false, Faultf("%s keys %s and %s are not ordered", e.Form, acc, b)
			}
			if less {
				acc = b
			}
		}
	}
	switch e.Form {
	case "filter", "map":
		return List{Elems: out}, false, nil
	case "count":
		return Int(n), false, nil
	case "any":
		return Bool(false), false, nil
	case "all":
		return Bool(true), false, nil
	case "groupBy":
		out := Map{}
		groups.Each(func(k, at Value) bool {
			out = out.Put(k, List{Elems: members[at.(Int)]})
			return true
		})
		return out, false, nil
	}
	if acc == nil {
		if e.Form == "max" || e.Form == "min" {
			return nil, false, Faultf("%s of an empty list; use %sOrNone when it may be empty", e.Form, e.Form)
		}
		return None{}, false, nil
	}
	return acc, false, nil
}

// evalMapErr is ADR 0095's `mapErr(e in result): body`: an Ok passes
// through, and an Err's value is bound to e and replaced by the body's.
func (in *Interp) evalMapErr(ctx context.Context, e *syntax.ListForm, env *Env) (Value, bool, *Fault) {
	v, earlyReturn, fault := in.evalExpr(ctx, e.Iterable, env)
	if fault != nil || earlyReturn {
		return v, earlyReturn, fault
	}
	r, ok := v.(Result)
	if !ok {
		return nil, false, Faultf("mapErr takes a Result, got %s", v)
	}
	if r.Ok {
		return r, false, nil
	}
	bodyEnv := env.Child()
	bodyEnv.Bind(e.Var, r.Value)
	b, earlyReturn, fault := in.evalExpr(ctx, e.Body, bodyEnv)
	if fault != nil || earlyReturn {
		return b, earlyReturn, fault
	}
	return Result{Ok: false, Value: b}, false, nil
}
