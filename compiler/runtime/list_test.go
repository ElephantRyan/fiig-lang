package runtime

import (
	"context"
	"sync"
	"testing"
)

// ADR 0057: list literals, claim-the-slot append, and content equality.

func ints(ns ...int64) List {
	elems := make([]Value, len(ns))
	for i, n := range ns {
		elems[i] = Int(n)
	}
	return List{Elems: elems}
}

func build(n int) List {
	l := List{}
	for i := 0; i < n; i++ {
		l = appendList(l, Int(int64(i)))
	}
	return l
}

// TestAppendNeverChangesTheOldList is §5's base/a/b example: the second
// append to base finds slot 3 taken and copies instead of overwriting.
func TestAppendNeverChangesTheOldList(t *testing.T) {
	base := build(3) // room for 4
	a := appendList(base, Int(100))
	b := appendList(base, Int(200))
	if base.String() != "[0, 1, 2]" || a.String() != "[0, 1, 2, 100]" || b.String() != "[0, 1, 2, 200]" {
		t.Fatalf("base=%s a=%s b=%s", base, a, b)
	}
}

// TestBuildingALoopCopiesLogarithmically is §3's cost guarantee: appending
// to the newest version copies only when the buffer is full, so building n
// elements copies O(log n) times. Copying on every append would be n.
func TestBuildingALoopCopiesLogarithmically(t *testing.T) {
	before := listCopies.Load()
	l := build(100_000)
	copies := listCopies.Load() - before
	if len(l.Elems) != 100_000 {
		t.Fatalf("built %d elements", len(l.Elems))
	}
	if copies > 20 {
		t.Fatalf("building 100,000 elements copied %d times; want about log2(100,000) = 17", copies)
	}
}

func TestConcurrentAppendsToOneListEachGetTheirOwn(t *testing.T) {
	base := build(3)
	out := make([]List, 64)
	var wg sync.WaitGroup
	for i := range out {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			out[i] = appendList(base, Int(int64(i)))
		}(i)
	}
	wg.Wait()
	for i, l := range out {
		if len(l.Elems) != 4 || l.Elems[3] != Int(int64(i)) {
			t.Fatalf("list %d = %s", i, l)
		}
	}
}

// TestListEqualityComparesContents is §6.
func TestListEqualityComparesContents(t *testing.T) {
	built := build(3)
	for _, tc := range []struct {
		a, b List
		want bool
	}{
		{ints(1, 2), ints(1, 2), true},
		{ints(1, 2), ints(2, 1), false},
		{ints(1, 2), ints(1, 2, 3), false},
		{ints(), List{}, true},
		{built, ints(0, 1, 2), true},
		{built, built, true},
		{appendList(built, Int(9)), built, false},
	} {
		if got := Equal(tc.a, tc.b); got != tc.want {
			t.Errorf("Equal(%s, %s) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}

	nested := func(n int64) Value {
		return Record{TypeName: "Order", Fields: map[string]Value{"lines": ints(1, n)}}
	}
	if !Equal(nested(2), nested(2)) || Equal(nested(2), nested(3)) {
		t.Fatal("a list inside a model must compare by contents")
	}
}

func TestListLiteralAndAppendRun(t *testing.T) {
	u := universeFromSource(t, `rule codes(extra: string) -> List<string> {
    ids: List<string> = ["ACME"]
    ids = append(ids, extra)
    return ids
}
`, `rule same() -> bool {
    return [1, 2] == [1, 2]
}
`)
	in := newTestInterp(u, nil)
	v, fault := in.Call(context.Background(), "codes", map[string]Value{"extra": Str("GLBX")})
	if fault != nil {
		t.Fatalf("codes: %v", fault)
	}
	if v.String() != `[ACME, GLBX]` {
		t.Fatalf("codes = %s", v)
	}
	if v, fault := in.Call(context.Background(), "same", nil); fault != nil || v != Bool(true) {
		t.Fatalf("[1, 2] == [1, 2] = %v, %v; want true", v, fault)
	}
}
