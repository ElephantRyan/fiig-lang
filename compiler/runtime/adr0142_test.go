package runtime

import (
	"context"
	"testing"
)

// ADR 0142 at run time: `??` behaves as ADR 0099's default `or`, `||` and
// `&&` short-circuit as `or` and `and` do, and `;` and `for (...)` change
// nothing about what runs.
func TestSymbolOperatorsRun(t *testing.T) {
	rule := `rule r(note: string | None, alias: string | None, done: bool | None, items: List<int>) -> string {
    name = alias ?? note ?? "anonymous";
    finished: bool = done ?? true;
    total: int = 0;
    for (item in items) {
        if (item > 1 && !(item == 3)) {
            total = total + item;
        }
    }
    either = finished || total > 100;
    return "${note ?? ""}|${name}|${finished}|${total}|${either}";
}`
	checkClean(t, rule)
	in := newTestInterp(universeFromSource(t, rule), nil)
	items := List{Elems: []Value{Int(1), Int(2), Int(3), Int(4)}}
	for _, c := range []struct {
		note, alias, done Value
		want              string
	}{
		{None{}, None{}, None{}, "|anonymous|true|6|true"},
		{Str("hi"), None{}, Bool(false), "hi|hi|false|6|false"},
		{Str("hi"), Str("al"), Bool(true), "hi|al|true|6|true"},
	} {
		got, fault := in.Call(context.Background(), "r", map[string]Value{"note": c.note, "alias": c.alias, "done": c.done, "items": items})
		if fault != nil || got.String() != c.want {
			t.Errorf("%v %v %v: got %v, %v; want %s", c.note, c.alias, c.done, got, fault, c.want)
		}
	}
}

// ADR 0142 §2: `match` as a value, after `return` and in a binding. A
// value arm is the value; an arm block's `return` ends the function.
func TestMatchAsAValueRuns(t *testing.T) {
	rule := `rule r(n: int | None, code: int) -> string {
    label = match (code) {
        401 -> "unauthorized",
        404 -> "not found",
        _ -> "rejected"
    };
    size: string | None = match (n) {
        None -> None,
        given -> "${given}"
    };
    return match (n) {
        None -> "${label}/none",
        given -> {
            if (given > 9) {
                return "big";
            }
            return "${label}/${size ?? ""}";
        }
    }
}`
	checkClean(t, rule)
	in := newTestInterp(universeFromSource(t, rule), nil)
	for _, c := range []struct {
		n    Value
		code int
		want string
	}{
		{None{}, 401, "unauthorized/none"},
		{Int(3), 404, "not found/3"},
		{Int(12), 500, "big"},
	} {
		got, fault := in.Call(context.Background(), "r", map[string]Value{"n": c.n, "code": Int(c.code)})
		if fault != nil || got.String() != c.want {
			t.Errorf("%v %d: got %v, %v; want %s", c.n, c.code, got, fault, c.want)
		}
	}
}
