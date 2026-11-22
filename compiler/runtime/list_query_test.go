package runtime

import (
	"strings"
	"testing"
)

// ADR 0065 §1-§2: list queries and sorting at run time.

func TestListQueries(t *testing.T) {
	src := `rule ends(xs: List<int>) -> string {
    return "${xs.first()} ${xs.last()} ${xs.firstOrNone()} ${xs.lastOrNone()}";
}

rule endsOfEmpty(xs: List<int>) -> string {
    return "${xs.firstOrNone()} ${xs.lastOrNone()}";
}

rule firstOfEmpty(xs: List<int>) -> int {
    return xs.first();
}

rule has(xs: List<decimal>, x: decimal) -> bool {
    return xs.contains(x);
}

rule hasName(xs: List<string>, x: string) -> bool {
    return xs.contains(x);
}

rule sortUp(xs: List<decimal>) -> List<decimal> {
    return xs.sorted();
}

rule sortDown(xs: List<decimal>) -> List<decimal> {
    return xs.sortedDescending();
}

rule sortNames(xs: List<string>) -> List<string> {
    return xs.sorted();
}

rule near(xs: List<float>, x: float) -> bool {
    return math.containsClose(xs, x, relative: 1e-9, absolute: 0.0);
}

rule same(a: List<float>, b: List<float>) -> bool {
    return math.allClose(a, b, relative: 1e-9, absolute: 0.0);
}`
	ints := func(ns ...int) List {
		l := List{}
		for _, n := range ns {
			l.Elems = append(l.Elems, Int(n))
		}
		return l
	}
	decs := func(ss ...string) List {
		l := List{}
		for _, s := range ss {
			l.Elems = append(l.Elems, dec(t, s))
		}
		return l
	}
	floats := func(fs ...float64) List {
		l := List{}
		for _, f := range fs {
			l.Elems = append(l.Elems, Float(f))
		}
		return l
	}
	cases := []struct {
		name string
		args map[string]Value
		want string
	}{
		{"ends", map[string]Value{"xs": ints(3, 1, 2)}, "3 2 3 2"},
		{"endsOfEmpty", map[string]Value{"xs": ints()}, "None None"},
		// §1: == semantics, so a decimal matches by value.
		{"has", map[string]Value{"xs": decs("1.50", "2"), "x": dec(t, "1.5")}, "true"},
		{"has", map[string]Value{"xs": decs("1.50"), "x": dec(t, "1.51")}, "false"},
		{"hasName", map[string]Value{"xs": List{Elems: []Value{Str("a"), Str("b")}}, "x": Str("b")}, "true"},
		// §2: stable, so the equal 1.5 and 1.50 keep their order, in both
		// directions.
		{"sortUp", map[string]Value{"xs": decs("2", "1.50", "0.5", "1.5")}, "[0.5, 1.50, 1.5, 2]"},
		{"sortDown", map[string]Value{"xs": decs("2", "1.50", "0.5", "1.5")}, "[2, 1.50, 1.5, 0.5]"},
		{"sortNames", map[string]Value{"xs": List{Elems: []Value{Str("b"), Str("a"), Str("c")}}}, "[a, b, c]"},
		{"near", map[string]Value{"xs": floats(0.1+0.2, 1), "x": Float(0.3)}, "true"},
		{"near", map[string]Value{"xs": floats(1), "x": Float(0.3)}, "false"},
		{"same", map[string]Value{"a": floats(0.1+0.2, 1), "b": floats(0.3, 1)}, "true"},
		{"same", map[string]Value{"a": floats(1), "b": floats(1, 1)}, "false"},
	}
	for _, c := range cases {
		got, fault := runEach(t, src, c.name, c.args)
		if fault != nil {
			t.Errorf("%s(%v): fault %v", c.name, c.args, fault)
			continue
		}
		if got.String() != c.want {
			t.Errorf("%s(%v) = %s, want %s", c.name, c.args, got, c.want)
		}
	}
	_, fault := runEach(t, src, "firstOfEmpty", map[string]Value{"xs": ints()})
	if fault == nil || !strings.Contains(fault.String(), "use firstOrNone()") {
		t.Errorf("first() on an empty list: fault = %v", fault)
	}
	// sorted returns a new list; the original is unchanged.
	xs := decs("2", "1")
	if _, fault := runEach(t, src, "sortUp", map[string]Value{"xs": xs}); fault != nil || xs.String() != "[2, 1]" {
		t.Errorf("sorted changed its receiver: %s (%v)", xs, fault)
	}
}
