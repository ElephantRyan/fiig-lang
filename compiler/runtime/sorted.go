package runtime

import (
	"context"
	"sort"

	"fiig-lang/compiler/syntax"
)

// evalSorted is ADR 0065 §3's keyed sort: each element's key is evaluated
// exactly once, before any comparison, and the elements are ordered stably
// by key. Descending is its own stable sort, so ties keep list order either
// way.
func (in *Interp) evalSorted(ctx context.Context, e *syntax.SortedExpr, env *Env) (Value, bool, *Fault) {
	v, earlyReturn, fault := in.evalExpr(ctx, e.Iterable, env)
	if fault != nil || earlyReturn {
		return v, earlyReturn, fault
	}
	list, ok := v.(List)
	if !ok {
		return nil, false, Faultf("sorted takes a List, got %s", v)
	}
	keys := make([]Value, len(list.Elems))
	for i, elem := range list.Elems {
		keyEnv := env.Child()
		keyEnv.Bind(e.Var, elem)
		k, earlyReturn, fault := in.evalExpr(ctx, e.Key, keyEnv)
		if fault != nil || earlyReturn {
			return k, earlyReturn, fault
		}
		keys[i] = k
	}
	order := make([]int, len(keys))
	for i := range order {
		order[i] = i
	}
	var bad *Fault
	sort.SliceStable(order, func(i, j int) bool {
		a, b := keys[order[i]], keys[order[j]]
		if e.Descending {
			a, b = b, a
		}
		less, ok := Less(a, b)
		if !ok && bad == nil {
			bad = Faultf("sorted keys %s and %s are not ordered", a, b)
		}
		return less
	})
	if bad != nil {
		return nil, false, bad
	}
	out := make([]Value, len(order))
	for i, idx := range order {
		out[i] = list.Elems[idx]
	}
	return List{Elems: out}, false, nil
}
