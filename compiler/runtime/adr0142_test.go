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
