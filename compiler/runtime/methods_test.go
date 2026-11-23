package runtime

import (
	"sort"
	"strings"
	"testing"

	"fiig-lang/compiler/types"
)

// TestEveryBuiltinMethodIsImplemented is ADR 0049 §2.8: the runtime
// implements exactly the methods compiler/types declares, so a method the
// checker accepts can't fault for want of a body, and a body can't exist
// that the checker rejects.
func TestEveryBuiltinMethodIsImplemented(t *testing.T) {
	var kinds []string
	for k := range builtinMethods {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	if want := types.MethodKinds(); strings.Join(kinds, ",") != strings.Join(want, ",") {
		t.Fatalf("runtime kinds %v, types kinds %v", kinds, want)
	}
	for _, k := range kinds {
		sigs, _ := types.MethodsOfKind(k)
		for name := range sigs {
			if builtinMethods[k][name] == nil {
				t.Errorf("%s.%s is declared but has no runtime body", k, name)
			}
		}
		for name := range builtinMethods[k] {
			if _, ok := sigs[name]; !ok {
				t.Errorf("%s.%s has a runtime body but isn't declared", k, name)
			}
		}
	}
}

// TestListMethods is ADR 0049 §2.6's four List methods.
func TestListMethods(t *testing.T) {
	src := `rule probe(xs: List<string>, i: int) -> string {
    head = xs.slice(0, 2);
    tail = append(head, "z");
    return match (xs.atOrNone(i)) {
        None -> {
            return "none ${xs.length()} ${tail.length()} ${xs.at(1)}";
        },
        x -> {
            return "${x} ${head.length()} ${xs.at(2)}";
        }
    }
}
`
	xs := List{Elems: []Value{Str("a"), Str("b"), Str("c")}}
	for _, c := range []struct {
		i    int64
		want string
	}{{1, "b 2 c"}, {7, "none 3 3 b"}, {-1, "none 3 3 b"}} {
		v, fault := run(t, src, "probe", map[string]Value{"xs": xs, "i": Int(c.i)})
		if fault != nil || v != Str(c.want) {
			t.Errorf("probe(i=%d) = %v, %v; want %q", c.i, v, fault, c.want)
		}
	}
	// Appending to a slice copies; the original's third element survives.
	if xs.Elems[2] != Str("c") {
		t.Fatalf("append to a slice overwrote the original: %v", xs)
	}
}

func TestListAtAndSliceOutOfRangeFault(t *testing.T) {
	for _, call := range []string{"xs.at(3)", "xs.at(-1)", "xs.slice(2, 1)", "xs.slice(0, 4)"} {
		src := "rule probe(xs: List<int>) -> int {\n    x = " + call + "\n    return 0\n}\n"
		xs := List{Elems: []Value{Int(1), Int(2), Int(3)}}
		if _, fault := run(t, src, "probe", map[string]Value{"xs": xs}); fault == nil || !strings.Contains(fault.Message, "out of range") {
			t.Errorf("%s: fault %v, want out of range", call, fault)
		}
	}
}
