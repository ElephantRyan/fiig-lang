package runtime

import (
	"context"
	"reflect"
	"sync/atomic"

	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// listCopies counts the times appendList had to copy into a new buffer.
// Tests read it to pin ADR 0057 §3's cost guarantee: building a list by
// appending to the newest version copies O(log n) times, not n times.
var listCopies atomic.Int64

// appendList is ADR 0057 §5's claim-the-next-slot append. l is unchanged.
// If l ends exactly where its buffer's claimed slots end and the buffer has
// room, it claims the next slot with one compare-and-swap and writes v
// there. Otherwise another list has already grown from l, or the buffer is
// full, so it copies into a buffer of twice the size. A slot is only ever
// written by the append that claimed it, which is the check Go's own
// append leaves out.
func appendList(l List, v Value) List {
	n := len(l.Elems)
	if l.used != nil && n < cap(l.Elems) && l.used.CompareAndSwap(int64(n), int64(n+1)) {
		elems := l.Elems[:n+1]
		elems[n] = v
		return List{Elems: elems, used: l.used}
	}
	listCopies.Add(1)
	size := 2 * n
	if size < 4 {
		size = 4
	}
	elems := make([]Value, n+1, size)
	copy(elems, l.Elems)
	elems[n] = v
	used := new(atomic.Int64)
	used.Store(int64(n + 1))
	return List{Elems: elems, used: used}
}

func (in *Interp) evalAppend(ctx context.Context, e *syntax.CallExpr, env *Env) (Value, bool, *Fault) {
	vals, earlyReturn, fault := in.evalArgsPositional(ctx, e.Args, env)
	if fault != nil || earlyReturn {
		return nil, earlyReturn, fault
	}
	if len(vals) != 2 {
		return nil, false, Faultf("append(list, element) takes exactly two arguments")
	}
	if vals, fault = placeArgs("append", types.AppendParams, e.Args, vals); fault != nil {
		return nil, false, fault
	}
	list, ok := vals[0].(List)
	if !ok {
		return nil, false, Faultf("append's first argument is not a list (got %s)", vals[0])
	}
	return appendList(list, vals[1]), false, nil
}

// sameMap reports whether two records share one field map, so Equal can
// answer without walking it (ADR 0057 §6).
func sameMap(a, b map[string]Value) bool {
	return reflect.ValueOf(a).UnsafePointer() == reflect.ValueOf(b).UnsafePointer()
}
