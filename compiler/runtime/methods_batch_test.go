package runtime

import (
	"context"
	"math"
	"strings"
	"testing"
)

// ADR 0129: padLeft, padRight, and the list methods reverse, take, drop,
// distinct and indexOfOrNone.

func TestPadCountsGraphemeClustersAndCutsTheFill(t *testing.T) {
	cases := []struct {
		method string
		in     string
		width  int64
		fill   string
		want   string
	}{
		{"padLeft", "7", 3, "0", "007"},
		{"padRight", "7", 3, "0", "700"},
		{"padLeft", "abc", 2, "0", "abc"},
		{"padLeft", "7", 6, "ab", "ababa7"},
		{"padRight", "7", 6, "ab", "7ababa"},
		{"padLeft", "", 2, "-", "--"},
		// A cluster is one position: e plus a combining accent is one, and so is
		// a flag, in the text and in the fill.
		{"padLeft", "é", 3, "*", "**é"},
		{"padRight", "x", 3, "\U0001F1EB\U0001F1F7", "x\U0001F1EB\U0001F1F7\U0001F1EB\U0001F1F7"},
	}
	for _, c := range cases {
		got, f := evalBuiltinMethod(Str(c.in), c.method, []Value{Int(c.width), Str(c.fill)})
		if f != nil || got != Str(c.want) {
			t.Errorf("%s(%q, %d, %q) = %v %v, want %q", c.method, c.in, c.width, c.fill, got, f, c.want)
		}
	}
	if _, f := evalBuiltinMethod(Str("x"), "padLeft", []Value{Int(3), Str("")}); f == nil {
		t.Error("an empty fill should fault")
	}
}

func TestListReverseTakeDrop(t *testing.T) {
	xs := ints(1, 2, 3, 4)
	cases := []struct {
		method string
		args   []Value
		want   List
	}{
		{"reverse", nil, ints(4, 3, 2, 1)},
		{"take", []Value{Int(2)}, ints(1, 2)},
		{"take", []Value{Int(0)}, ints()},
		{"take", []Value{Int(9)}, ints(1, 2, 3, 4)},
		{"drop", []Value{Int(1)}, ints(2, 3, 4)},
		{"drop", []Value{Int(9)}, ints()},
	}
	for _, c := range cases {
		got, f := evalBuiltinMethod(xs, c.method, c.args)
		if f != nil || !Equal(got, c.want) {
			t.Errorf("%s%v = %v %v, want %v", c.method, c.args, got, f, c.want)
		}
	}
	// The receiver is unchanged, and an append to a take copies rather than
	// writing into it (ADR 0057).
	if !Equal(xs, ints(1, 2, 3, 4)) {
		t.Errorf("the receiver changed: %v", xs)
	}
	for _, m := range []string{"take", "drop"} {
		if _, f := evalBuiltinMethod(xs, m, []Value{Int(-1)}); f == nil {
			t.Errorf("%s(-1) should fault", m)
		}
	}
}

// ADR 0129: Level.values() is every variant in order.
func TestEnumValuesListsTheVariantsInOrder(t *testing.T) {
	in := newTestInterp(universeFromSource(t, `enum Level {
    Low,
    Mid,
    High,
}`, `conversion levels() -> List<Level> {
    return Level.values();
}`, `conversion names() -> string {
    names: List<string> = [];
    for (level in Level.values()) {
        names = append(names, "${level}");
    }
    return names.join(",");
}`), nil)
	if v, f := in.Call(context.Background(), "names", nil); f != nil || v != Str("Level.Low,Level.Mid,Level.High") {
		t.Fatalf("names = %v, %v", v, f)
	}
	v, f := in.Call(context.Background(), "levels", nil)
	if l, ok := v.(List); f != nil || !ok || len(l.Elems) != 3 {
		t.Fatalf("levels = %v, %v", v, f)
	}
}

func TestListDistinctKeepsTheFirstAndIndexOfFindsIt(t *testing.T) {
	got, f := evalBuiltinMethod(ints(3, 1, 3, 2, 1), "distinct", nil)
	if f != nil || !Equal(got, ints(3, 1, 2)) {
		t.Errorf("distinct = %v %v", got, f)
	}
	if got, _ := evalBuiltinMethod(ints(), "distinct", nil); !Equal(got, ints()) {
		t.Errorf("distinct of empty = %v", got)
	}
	if got, _ := evalBuiltinMethod(ints(5, 6, 7), "indexOfOrNone", []Value{Int(7)}); got != Int(2) {
		t.Errorf("indexOfOrNone = %v", got)
	}
	if got, _ := evalBuiltinMethod(ints(5, 6, 7), "indexOfOrNone", []Value{Int(9)}); got != (None{}) {
		t.Errorf("indexOfOrNone of a missing element = %v", got)
	}
}

// ADR 0129 §2a (Hearsay): concat is a new list of the receiver's elements
// then the argument's; appending to it changes neither input.
func TestListConcatIsANewList(t *testing.T) {
	first := List{Elems: make([]Value, 2, 8)}
	first.Elems[0], first.Elems[1] = Int(1), Int(2)
	got, f := evalBuiltinMethod(first, "concat", []Value{ints(3, 4)})
	if f != nil || !Equal(got, ints(1, 2, 3, 4)) {
		t.Fatalf("concat = %v %v", got, f)
	}
	joined := got.(List)
	joined.Elems = append(joined.Elems, Int(5))
	if spare := first.Elems[:3]; spare[2] == Int(5) {
		t.Error("concat wrote into the receiver's spare capacity")
	}
	if got, _ := evalBuiltinMethod(ints(), "concat", []Value{ints()}); !Equal(got, ints()) {
		t.Errorf("concat of empties = %v", got)
	}
}

// ADR 0129 §1a (Hearsay G2): repeat is the text count times; a negative
// count, or a result too long for a string, faults.
func TestStringRepeat(t *testing.T) {
	cases := []struct {
		s     string
		count int64
		want  string
	}{
		{"ab", 3, "ababab"},
		{"ab", 0, ""},
		{"", 5, ""},
		{"é", 2, "éé"},
	}
	for _, c := range cases {
		got, f := evalBuiltinMethod(Str(c.s), "repeat", []Value{Int(c.count)})
		if f != nil || got != Value(Str(c.want)) {
			t.Errorf("%q.repeat(%d) = %v %v, want %q", c.s, c.count, got, f, c.want)
		}
	}
	if _, f := evalBuiltinMethod(Str("x"), "repeat", []Value{Int(-1)}); f == nil || !strings.Contains(f.Message, "can't be negative") {
		t.Errorf("repeat(-1) fault = %v", f)
	}
	if _, f := evalBuiltinMethod(Str("xy"), "repeat", []Value{Int(math.MaxInt64)}); f == nil || !strings.Contains(f.Message, "too long for a string") {
		t.Errorf("repeat(MaxInt64) fault = %v", f)
	}
}
