package runtime

import (
	"encoding/json"
	"strings"
	"testing"

	"fiig-lang/compiler/types"
)

// ADR 0065 §4-§8: Map in fiig programs.
func TestMapPrograms(t *testing.T) {
	src := `rule count(names: List<string>) -> Map<string, int> {
    counts: Map<string, int> = [:];
    for (name in names) {
        if (counts.containsKey(name)) {
            counts = counts.put(name, counts.at(name) + 1);
        } else {
            counts = counts.put(name, 1);
        }
    }
    return counts;
}

rule describe(m: Map<string, int>) -> string {
    out: string = "";
    for (key, value in m) {
        out = "${out}${key}=${value};";
    }
    return "${out} keys ${m.keys()} values ${m.values()} length ${m.length()}";
}

rule edits() -> string {
    base = ["small": 10, "large": 500];
    kept = base.putIfAbsent("small", 99);
    moved = base.remove("small").put("small", 1);
    return "${base} ${kept} ${moved} ${base.atOrNone("tiny")} ${base == ["large": 500, "small": 10]}";
}

rule missing(m: Map<string, int>) -> int {
    return m.at("nope");
}

rule repeated(a: string, b: string) -> Map<string, int> {
    return [a: 1, b: 2];
}

rule decimalKeys() -> string {
    m: Map<decimal, string> = [1.50: "a"];
    return "${m.put(1.5, "b")} ${m.containsKey(1.500)}";
}

rule close(a: Map<string, float>, b: Map<string, float>) -> bool {
    return math.allClose(a, b, relative: 1e-9, absolute: 0.0);
}`
	names := List{Elems: []Value{Str("b"), Str("a"), Str("b")}}
	got, fault := runEach(t, src, "count", map[string]Value{"names": names})
	if fault != nil || got.String() != "[b: 2, a: 1]" {
		t.Fatalf("count = %v, %v", got, fault)
	}
	counts := got
	cases := []struct {
		name string
		args map[string]Value
		want string
	}{
		{"describe", map[string]Value{"m": counts}, "b=2;a=1; keys [b, a] values [2, 1] length 2"},
		// §6: putIfAbsent leaves a present key alone; a removed key comes
		// back last; §8: == ignores order.
		{"edits", nil, "[small: 10, large: 500] [small: 10, large: 500] [large: 500, small: 1] None true"},
		// §4: 1.5 replaces 1.50's value but keeps its spelling.
		{"decimalKeys", nil, "[1.50: b] true"},
		{"close", map[string]Value{
			"a": Map{}.Put(Str("x"), Float(0.1+0.2)).Put(Str("y"), Float(1)),
			"b": Map{}.Put(Str("y"), Float(1)).Put(Str("x"), Float(0.3)),
		}, "true"},
	}
	for _, c := range cases {
		got, fault := runEach(t, src, c.name, c.args)
		if fault != nil || got == nil || got.String() != c.want {
			t.Errorf("%s = %v, %v; want %s", c.name, got, fault, c.want)
		}
	}
	if _, fault := runEach(t, src, "missing", map[string]Value{"m": counts}); fault == nil || !strings.Contains(fault.Message, "use atOrNone or containsKey") {
		t.Errorf("at on a missing key: fault = %v", fault)
	}
	// §5: a repeated key the checker couldn't see faults.
	if _, fault := runEach(t, src, "repeated", map[string]Value{"a": Str("x"), "b": Str("x")}); fault == nil || !strings.Contains(fault.Message, "repeats the key x") {
		t.Errorf("repeated key: fault = %v", fault)
	}

	// A string-keyed map is a JSON object in the map's order.
	out, err := EncodeBodyJSON(counts)
	if err != nil || string(out) != `{"b":2,"a":1}` {
		t.Errorf("JSON = %s, %v", out, err)
	}
	decoded, err := DecodeJSON(map[string]any{"z": json.Number("1"), "a": json.Number("2")}, mapType("string", "int"), nil)
	if err != nil || decoded.String() != "[a: 2, z: 1]" {
		t.Errorf("decoded = %v, %v", decoded, err)
	}
}

func mapType(key, val string) types.Type {
	return types.Generic{Name: "Map", Args: []types.Type{types.Primitive{Name: key}, types.Primitive{Name: val}}}
}
