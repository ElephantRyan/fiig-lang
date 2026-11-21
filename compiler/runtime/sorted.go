package runtime

import (
	"context"
	"sort"

	"fiig-lang/compiler/syntax"
)

// evalSorted is ADR 0065 §3's keyed sort, with ADR 0093 §4's keys: each
// element's keys are evaluated exactly once, before any comparison, and
// the elements are ordered stably by the first key, ties by the next, and
// so on. A descending key compares the other way, so ties keep list order
// either way.
func (in *Interp) evalSorted(ctx context.Context, e *syntax.SortedExpr, env *Env) (Value, bool, *Fault) {
	list, earlyReturn, fault := in.formList(ctx, "sorted", e.Iterable, env)
	if fault != nil || earlyReturn {
		return list, earlyReturn, fault
	}
	elems := list.(List).Elems
	keys := make([][]Value, len(elems))
	// One scope for the keys, emptied for each element (ADR 0115 §3).
	keyEnv := env.Child()
	for i, elem := range elems {
		keyEnv.reset()
		keyEnv.Bind(e.Var, elem)
		keys[i] = make([]Value, len(e.Keys))
		for j, k := range e.Keys {
			v, earlyReturn, fault := in.evalExpr(ctx, k.Key, keyEnv)
			if fault != nil || earlyReturn {
				return v, earlyReturn, fault
			}
			keys[i][j] = v
		}
	}
	order := make([]int, len(keys))
	for i := range order {
		order[i] = i
	}
	var bad *Fault
	sort.SliceStable(order, func(i, j int) bool {
		for n, k := range e.Keys {
			a, b := keys[order[i]][n], keys[order[j]][n]
			if k.Descending {
				a, b = b, a
			}
			less, ok := Less(a, b)
			if !ok {
				if bad == nil {
					bad = Faultf("sorted keys %s and %s are not ordered", a, b)
				}
				return false
			}
			if less {
				return true
			}
			if greater, _ := Less(b, a); greater {
				return false
			}
		}
		return false
	})
	if bad != nil {
		return nil, false, bad
	}
	out := make([]Value, len(order))
	for i, idx := range order {
		out[i] = elems[idx]
	}
	return List{Elems: out}, false, nil
}

// formList evaluates a form's iterable, which the checker has made a List.
func (in *Interp) formList(ctx context.Context, form string, iterable syntax.Expr, env *Env) (Value, bool, *Fault) {
	v, earlyReturn, fault := in.evalExpr(ctx, iterable, env)
	if fault != nil || earlyReturn {
		return v, earlyReturn, fault
	}
	if _, ok := v.(List); !ok {
		return nil, false, Faultf("%s takes a List, got %s", form, v)
	}
	return v, false, nil
}
