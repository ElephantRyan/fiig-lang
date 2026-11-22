package runtime

import (
	"context"
	"testing"
)

// ADR 0130: a literal in a pattern matches a value equal to it.

func TestLiteralPatternsMatchByEquality(t *testing.T) {
	in := newTestInterp(universeFromSource(t,
		`conversion describe(code: int) -> string {
    return match (code) {
        200 -> "ok",
        404 -> "missing",
        -1 -> "unset",
        0x10 -> "sixteen",
        _ -> "other",
    }
}`,
		`conversion owner(name: string) -> int {
    return match (name) {
        "ACME" -> 1,
        "Globex" -> 2,
        _ -> 0,
    }
}`,
		`conversion flag(on: bool) -> int {
    return match (on) {
        true -> 1,
        false -> 2,
    }
}`,
		`conversion optional(name: string | None) -> string {
    return match (name) {
        "a" -> "A",
        None -> "none",
        other -> other,
    }
}`,
	), nil)
	call := func(name string, arg string, v Value) Value {
		t.Helper()
		got, f := in.Call(context.Background(), name, map[string]Value{arg: v})
		if f != nil {
			t.Fatalf("%s: %v", name, f)
		}
		return got
	}
	for _, c := range []struct {
		n    int64
		want string
	}{{200, "ok"}, {404, "missing"}, {-1, "unset"}, {16, "sixteen"}, {500, "other"}, {0, "other"}} {
		if got := call("describe", "code", Int(c.n)); got != Str(c.want) {
			t.Errorf("describe(%d) = %v, want %s", c.n, got, c.want)
		}
	}
	for name, want := range map[string]int64{"ACME": 1, "Globex": 2, "acme": 0, "": 0} {
		if got := call("owner", "name", Str(name)); got != Int(want) {
			t.Errorf("owner(%q) = %v, want %d", name, got, want)
		}
	}
	for _, c := range []struct {
		v    Value
		want string
	}{{Str("a"), "A"}, {None{}, "none"}, {Str("z"), "z"}} {
		if got := call("optional", "name", c.v); got != Str(c.want) {
			t.Errorf("optional(%v) = %v, want %s", c.v, got, c.want)
		}
	}
}

// Before ADR 0130, `true` and `false` in a pattern were names: the first arm
// bound the value and always matched, so flag(false) was 1.
func TestTrueAndFalseInAPatternAreValuesNotBindings(t *testing.T) {
	in := newTestInterp(universeFromSource(t, `conversion flag(on: bool) -> int {
    return match (on) {
        true -> 1,
        false -> 2,
    }
}`), nil)
	for on, want := range map[bool]int64{true: 1, false: 2} {
		got, f := in.Call(context.Background(), "flag", map[string]Value{"on": Bool(on)})
		if f != nil || got != Int(want) {
			t.Errorf("flag(%v) = %v %v, want %d", on, got, f, want)
		}
	}
}

func TestALiteralInsideAConstructorPattern(t *testing.T) {
	in := newTestInterp(universeFromSource(t, `enum Failure {
    Rejected(status: int),
    Unknown,
}`, `conversion kind(x: Result<int, Failure>) -> string {
    return match (x) {
        Err(Failure.Rejected(404)) -> "missing",
        Err(Failure.Rejected(_)) -> "rejected",
        Err(Failure.Unknown) -> "unknown",
        Ok(_) -> "fine",
    }
}`), nil)
	rejected := func(n int64) Value {
		return Result{Ok: false, Value: EnumValue{TypeName: "Failure", Variant: "Rejected", Fields: []Value{Int(n)}}}
	}
	for _, c := range []struct {
		v    Value
		want string
	}{{rejected(404), "missing"}, {rejected(500), "rejected"}, {Result{Ok: true, Value: Int(1)}, "fine"}} {
		got, f := in.Call(context.Background(), "kind", map[string]Value{"x": c.v})
		if f != nil || got != Str(c.want) {
			t.Errorf("kind(%v) = %v %v, want %s", c.v, got, f, c.want)
		}
	}
}
