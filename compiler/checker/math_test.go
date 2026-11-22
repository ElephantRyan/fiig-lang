package checker

import (
	"math"
	"strings"
	"testing"

	"fiig-lang/compiler/syntax"
)

// ADR 0049 §2.4, §2.7: the prelude math group.

func TestMathFormulasCheck(t *testing.T) {
	ok := []string{
		// No `use`: math is the one group every file sees (§2.5).
		"rule r(x: float) -> int {\n    return math.floorToInt(x) + math.roundHalfEvenToInt(x * 2);\n}",
		"rule r(n: int) -> float {\n    return math.toFloat(n) / 3;\n}",
		"rule r(x: float) -> float {\n    return math.sqrt(x * x + 1) + math.atan2(y: x, x: 1.0);\n}",
		// A constant argument lands in the one form there is.
		"rule r() -> float {\n    return math.sqrt(2);\n}",
		// Two forms: typed arguments pick one, constants alone take their kind's.
		"rule r(n: int, m: int) -> int {\n    return math.max(n, m) + math.abs(-3) + math.floorMod(n, 7);\n}",
		"rule r(x: float) -> float {\n    return math.max(x, 0) + math.abs(-2.5) + math.min(1, 2.5);\n}",
		"rule r(a: float, b: float) -> bool {\n    return math.closeTo(a, b, relative: 1e-9, absolute: 0.005) || math.isZero(a - b);\n}",
		"rule r(n: int, m: int) -> int {\n    return match (math.addOrNone(n, m)) {\n        None -> 0,\n        total -> total,\n    }\n}",
		"rule r(x: float) -> float | None {\n    return math.divOrNone(1.0, x);\n}",
	}
	for _, src := range ok {
		if diags := checkSrc(t, src); len(diags) != 0 {
			t.Errorf("%s\ndiags = %v, want none", src, diags)
		}
	}
}

func TestMathFormulaMisuse(t *testing.T) {
	cases := []struct {
		src, rule, fragment string
	}{
		// §2.4: never both types at once.
		{"rule r(n: int, x: float) -> float {\n    return math.max(n, x);\n}", "A1", "math.max has no form taking (int, float)"},
		{"rule r(s: string) -> int {\n    return math.abs(s);\n}", "A1", "no form taking (string)"},
		{"rule r(n: int) -> float {\n    return math.sqrt(n);\n}", "A1", `got int, expected float (argument "x" of math.sqrt)`},
		{"rule r(n: int) -> int {\n    return math.max(n, 2.5);\n}", "A1", "constant 2.5 is not a whole number"},
		{"rule r() -> float {\n    return math.toFloat(2.5);\n}", "A1", "constant 2.5 is not a whole number"},
		{"rule r(x: float) -> float {\n    return math.abs(x) + math.floorToInt(x);\n}", "A1", "float + int mixes numeric types"},
		// ADR 0051: the tolerances are named.
		{"rule r(a: float, b: float) -> bool {\n    return math.closeTo(a, b, 0.0, absolute: 0.1);\n}", "", `"relative" must be passed by name`},
		{"rule r(a: float, b: float) -> bool {\n    return math.closeTo(a, b, relative: 0.0);\n}", "", `missing argument "absolute"`},
		{"rule r(x: float) -> float {\n    return math.cbrt(x);\n}", "", `no formula "cbrt"`},
		{"rule r() -> float {\n    return math.pi();\n}", "", "math.pi is a constant, not a formula"},
		{"rule r() -> float {\n    f = math.sqrt;\n    return 1.0;\n}", "", "math.sqrt is a formula; call it"},
		{"rule r() -> float {\n    return math.phi;\n}", "", `no member "phi"`},
	}
	for _, c := range cases {
		wantDiag(t, checkSrc(t, c.src), c.rule, c.fragment)
	}
}

// TestMathConstantsAreUntyped is §2.7: a math constant is exact until it
// lands, so `math.pi * 2` needs no conversion and the boundaries ADR 0063
// names are range-checked like any literal.
func TestMathConstantsAreUntyped(t *testing.T) {
	cases := []struct {
		expr, typ string
		want      syntax.Const
	}{
		{"math.pi", "float", syntax.Const{Float: true, F: math.Pi}},
		{"math.pi * 2", "float", syntax.Const{Float: true, F: 2 * math.Pi}},
		{"math.tau / 4", "float", syntax.Const{Float: true, F: math.Pi / 2}},
		{"math.e", "float", syntax.Const{Float: true, F: math.E}},
		{"math.maxFloat", "float", syntax.Const{Float: true, F: math.MaxFloat64}},
		{"math.maxInt", "int", syntax.Const{Int: math.MaxInt64}},
		{"math.minInt", "int", syntax.Const{Int: math.MinInt64}},
		{"-(math.minInt + 1)", "int", syntax.Const{Int: math.MaxInt64}},
		// An int constant lands in float when float is wanted.
		{"math.maxInt", "float", syntax.Const{Float: true, F: 9223372036854775807}},
	}
	for _, c := range cases {
		f := mustParse(t, "rule r() -> "+c.typ+" {\n    return "+c.expr+"\n}\n")
		if diags := Check([]*syntax.File{f}, nil); len(diags) != 0 {
			t.Fatalf("%s: diags = %v", c.expr, diags)
		}
		ret := f.Decls[0].(*syntax.RuleDecl).Body.Stmts[0].(*syntax.ReturnStmt)
		got := ret.Value.(syntax.ConstRoot).Constant()
		if got == nil || *got != c.want {
			t.Errorf("%s as %s = %+v, want %+v", c.expr, c.typ, got, c.want)
		}
	}
	wantDiag(t, checkSrc(t, "rule r() -> int {\n    return math.maxInt + 1;\n}"), "", "constant math.maxInt + 1 does not fit in int")
	wantDiag(t, checkSrc(t, "rule r() -> int {\n    return -math.minInt;\n}"), "", "does not fit in int")
	wantDiag(t, checkSrc(t, "rule r() -> float {\n    return math.maxFloat * 2;\n}"), "", "does not fit in float")
	wantDiag(t, checkSrc(t, "rule r() -> int {\n    return math.pi;\n}"), "A1", "constant math.pi is not a whole number")
	if diags := checkSrc(t, "rule r(r: float) -> float {\n    return math.pi * r * r;\n}"); len(diags) != 0 {
		t.Fatalf("diags = %v, want none", diags)
	}
}

// TestMathNameIsReserved is §2.5's resolved open question: a parameter,
// binding, or import alias named math would hide the group, so it is G4.
func TestMathNameIsReserved(t *testing.T) {
	cases := []string{
		"rule r(math: int) -> int {\n    return math;\n}",
		"rule r() -> int {\n    math = 3;\n    return math;\n}",
		"use acme.math;\n\nrule r() -> int {\n    return 3;\n}",
	}
	for _, src := range cases {
		diags := checkSrc(t, src)
		found := false
		for _, d := range diags {
			found = found || (d.Rule == "G4" && strings.Contains(d.Msg, "would hide the prelude math group"))
		}
		if !found {
			t.Errorf("%s\ndiags = %v, want G4 for math", src, diags)
		}
	}
}

// TestFormulaMayCallMath is §2.3: a formula calls only formulas, and
// math's members are formulas.
func TestFormulaMayCallMath(t *testing.T) {
	src := "formulas geometry {\n    formula hypot(a: float, b: float) -> float {\n        return math.sqrt(a * a + b * b);\n    }\n}"
	if diags := checkSrc(t, src); len(diags) != 0 {
		t.Fatalf("diags = %v, want none", diags)
	}
}

// TestDeclarationNamedAfterPrelude is K2: a declaration, public or private,
// named after anything the prelude binds in every file.
func TestDeclarationNamedAfterPrelude(t *testing.T) {
	cases := map[string]string{
		"formulas math {\n    formula twice(n: int) -> int {\n        return n * 2;\n    }\n}":                "the prelude math group",
		"model Result {\n    code: int;\n}":                                                                   "a prelude type",
		"enum SocketError {\n    Lost,\n}":                                                                    "a prelude type",
		"enum Rounding {\n    Nearest,\n}":                                                                    "a prelude type",
		"model Connection {\n    host: string;\n}":                                                            "a prelude type",
		"rule Ok(n: int) -> int {\n    return n;\n}":                                                          "Result's constructor",
		"rule r(n: int) -> int {\n    return n;\n}\n\nprivate {\n    model List {\n        n: int;\n    }\n}": "a prelude type",
	}
	for src, want := range cases {
		diags := checkSrc(t, src)
		found := false
		for _, d := range diags {
			found = found || (d.Rule == "K2" && strings.Contains(d.Msg, want))
		}
		if !found {
			t.Errorf("%s\ndiags = %v, want K2 naming %s", src, diags, want)
		}
	}
	if diags := checkSrc(t, "model Results {\n    code: int;\n}"); len(diags) != 0 {
		t.Errorf("Results: diags = %v, want none", diags)
	}
}
