package runtime

import (
	"context"
	"testing"
)

// ADR 0099 at run time: the default runs only for None, and a present
// false stays false (boolean `or` would have taken the right side).
func TestOrDefault(t *testing.T) {
	rule := `rule r(note: string | None, alias: string | None, done: bool | None) -> string {
    name = alias ?? note ?? "anonymous";
    finished: bool = done ?? true;
    return "${note ?? ""}|${name}|${finished}";
}`
	checkClean(t, rule)
	in := newTestInterp(universeFromSource(t, rule), nil)
	for _, c := range []struct {
		note, alias, done Value
		want              string
	}{
		{None{}, None{}, None{}, "|anonymous|true"},
		{Str("hi"), None{}, Bool(false), "hi|hi|false"},
		{Str("hi"), Str("al"), Bool(true), "hi|al|true"},
	} {
		got, fault := in.Call(context.Background(), "r", map[string]Value{"note": c.note, "alias": c.alias, "done": c.done})
		if fault != nil || got.String() != c.want {
			t.Errorf("%v %v %v: got %v, %v; want %s", c.note, c.alias, c.done, got, fault, c.want)
		}
	}
}
