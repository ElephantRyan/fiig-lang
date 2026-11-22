package checker

import "testing"

// ADR 0065 §4-§8: Map in the checker.

func TestMapCheck(t *testing.T) {
	diags := checkSrc(t, `rule count(names: List<string>, prices: Map<string, decimal>, weights: Map<string, float>) -> Map<string, int> {
    counts: Map<string, int> = [:];
    for (name in names) {
        if (counts.containsKey(name)) {
            counts = counts.put(name, counts.at(name) + 1);
        } else {
            counts = counts.put(name, 1);
        }
    }
    limits = ["small": 10, "large": 500];
    ks: List<string> = limits.keys();
    vs: List<int> = limits.values();
    n: int = limits.length() + ks.length() + vs.length();
    for (key, value in limits) {
        n = n + value;
    }
    for (_, value in prices) {
        n = n + 1;
    }
    byScale: Map<decimal, string> = [1.5: "a", 2: "b"];
    maybe: decimal | None = prices.atOrNone("x");
    same: bool = limits == ["large": 500, "small": 10];
    close: bool = math.allClose(weights, weights, relative: 1e-9, absolute: 0.0);
    trimmed = limits.remove("small").putIfAbsent("medium", 50);
    return counts;
}`)
	if len(diags) != 0 {
		t.Fatalf("diags = %v, want none", diags)
	}
}

func TestMapMisuse(t *testing.T) {
	cases := []struct{ src, rule, fragment string }{
		// §4: keys are comparable; a float key gets the decimal route.
		{"rule r(m: Map<float, int>) -> int {\n    return m.length();\n}", "M1", "round to a decimal key with math.toDecimal and math.round"},
		{"rule r(m: Map<Result<int, string>, int>) -> int {\n    return m.length();\n}", "M1", "can't be a map key: it holds a Result"},
		{"rule r() -> int {\n    m = [1.5: 1];\n    return m.length();\n}", "", ""},
		// §5: [:] needs an expected type; entries share types.
		{"rule r() -> int {\n    m = [:];\n    return 0;\n}", "A12", "an empty map literal has no key or value type"},
		{"rule r() -> int {\n    m = [\"a\": 1, \"b\": \"x\"];\n    return 0;\n}", "A1", "map values must share one type"},
		// §5: a repeated constant key, by value.
		{"rule r() -> Map<string, int> {\n    return [\"a\": 1, \"a\": 2];\n}", "A18", `repeats the key "a"`},
		{"rule r() -> Map<decimal, int> {\n    return [1.5: 1, 1.50: 2];\n}", "A18", "repeats the key 1.50"},
		// §7: two names over a map, one over a list.
		{"rule r(m: Map<string, int>) -> int {\n    for (k in m) {\n    }\n    return 0;\n}", "I1", "for over a Map binds two names"},
		{"rule r(xs: List<int>) -> int {\n    for (a, b in xs) {\n    }\n    return 0;\n}", "I1", "binds two names, which is for a Map"},
		// §6: A13 covers Map's methods.
		{"rule r(m: Map<string, int>) -> int {\n    m.put(\"a\", 1);\n    return 0;\n}", "A13", "the result of put() is discarded"},
		// §8: == on a float-valued map names allClose.
		{"rule r(a: Map<string, float>, b: Map<string, float>) -> bool {\n    return a == b;\n}", "M1", "use math.allClose"},
		{"rule r(m: Map<string, int>) -> int {\n    return m.at(1);\n}", "A1", ""},
		{"model Map {\n    n: int;\n}", "K2", "a prelude type"},
	}
	for _, c := range cases {
		if c.rule == "" {
			// A float-keyed literal is the inferred-type path of the key rule.
			wantDiag(t, checkSrc(t, c.src), "M1", "float can't be a map key")
			continue
		}
		wantDiag(t, checkSrc(t, c.src), c.rule, c.fragment)
	}
}
