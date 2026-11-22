package runtime

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"

	"fiig-lang/compiler/types"
)

// ADR 0048: float at runtime.

// runEach is run over several declarations, each in its own file (G1).
func runEach(t *testing.T, src, name string, args map[string]Value) (Value, *Fault) {
	t.Helper()
	parts := strings.Split(src, "\n\nrule ")
	for i := 1; i < len(parts); i++ {
		parts[i] = "rule " + parts[i]
	}
	checkClean(t, parts...)
	in := newTestInterp(universeFromSource(t, parts...), nil)
	return in.Call(context.Background(), name, args)
}

func TestFloatArithmetic(t *testing.T) {
	src := `rule fahrenheit(celsius: float) -> float {
    return celsius * 9 / 5 + 32;
}

rule halfOfSeven() -> float {
    return 7 / 2;
}

rule remainder(a: float, b: float) -> float {
    return a % b;
}

rule label(x: float) -> string {
    return "x=${x}";
}

rule colder(a: float, b: float) -> bool {
    return a < b;
}`
	cases := []struct {
		name string
		args map[string]Value
		want Value
	}{
		{"fahrenheit", map[string]Value{"celsius": Float(-40)}, Float(-40)},
		{"fahrenheit", map[string]Value{"celsius": Float(37)}, Float(98.6)},
		// Both operands are integer constants, so `/` is integer division
		// before the result lands as a float: Go's rule, and §2.4's wart.
		{"halfOfSeven", nil, Float(3)},
		{"remainder", map[string]Value{"a": Float(-7.5), "b": Float(2)}, Float(-1.5)},
		{"remainder", map[string]Value{"a": Float(7.5), "b": Float(-2)}, Float(1.5)},
		{"label", map[string]Value{"x": Float(3)}, Str("x=3.0")},
		{"label", map[string]Value{"x": Float(0.1)}, Str("x=0.1")},
		{"label", map[string]Value{"x": Float(1e21)}, Str("x=1e+21")},
		{"colder", map[string]Value{"a": Float(-0.5), "b": Float(0.25)}, Bool(true)},
	}
	for _, c := range cases {
		got, fault := runEach(t, src, c.name, c.args)
		if fault != nil || got != c.want {
			t.Errorf("%s(%v) = %v, %v; want %v", c.name, c.args, got, fault, c.want)
		}
	}
}

// TestNegativeZeroIsNormalized is §2.3: two floats that compare equal are
// indistinguishable, so -0.0 never appears.
func TestNegativeZeroIsNormalized(t *testing.T) {
	src := `rule wrap(a: float, b: float) -> float {
    return a % b;
}

rule flip(x: float) -> float {
    return -x;
}

rule scale(x: float) -> float {
    return x * -1.0;
}`
	for _, c := range []struct {
		name string
		args map[string]Value
	}{
		{"wrap", map[string]Value{"a": Float(-3), "b": Float(3)}},
		{"flip", map[string]Value{"x": Float(0)}},
		{"scale", map[string]Value{"x": Float(0)}},
	} {
		got, fault := runEach(t, src, c.name, c.args)
		f, ok := got.(Float)
		if fault != nil || !ok || f != 0 || math.Signbit(float64(f)) {
			t.Errorf("%s = %v, %v; want +0.0", c.name, got, fault)
		}
	}
}

// TestNonFiniteResultsFault is §2.3's table: every operation IEEE 754
// answers with NaN or Inf is a fault. Underflow is not.
func TestNonFiniteResultsFault(t *testing.T) {
	src := `rule divide(a: float, b: float) -> float {
    return a / b;
}

rule remainder(a: float, b: float) -> float {
    return a % b;
}

rule multiply(a: float, b: float) -> float {
    return a * b;
}`
	cases := []struct {
		name  string
		a, b  float64
		fault string
	}{
		{"divide", 1, 0, "division by zero"},
		{"divide", 0, 0, "division by zero"},
		{"remainder", 1, 0, "division by zero"},
		{"multiply", 1e308, 10, "float overflow at 2:12"},
		{"multiply", -1e308, 10, "float overflow"},
	}
	for _, c := range cases {
		_, fault := runEach(t, src, c.name, map[string]Value{"a": Float(c.a), "b": Float(c.b)})
		if fault == nil || !strings.Contains(fault.Message, c.fault) {
			t.Errorf("%s(%v, %v): fault = %v, want %q", c.name, c.a, c.b, fault, c.fault)
		}
	}
	got, fault := runEach(t, src, "multiply", map[string]Value{"a": Float(1e-300), "b": Float(1e-300)})
	if fault != nil || got != Float(0) {
		t.Fatalf("underflow = %v, %v; want 0.0", got, fault)
	}
}

func TestFloatJSON(t *testing.T) {
	floatT := types.Primitive{Name: "float"}
	v, err := DecodeJSON(decodeRawJSON(t, "2.5"), floatT, nil)
	if err != nil || v != Float(2.5) {
		t.Fatalf("decode 2.5 = %v, %v", v, err)
	}
	// A whole number is a float when a float is wanted (play nice at
	// boundaries), and an out-of-range one is the input's error, not a
	// fault (§2.3's boundary table).
	if v, err := DecodeJSON(decodeRawJSON(t, "3"), floatT, nil); err != nil || v != Float(3) {
		t.Fatalf("decode 3 = %v, %v", v, err)
	}
	if _, err := DecodeJSON(decodeRawJSON(t, "1e400"), floatT, nil); err == nil || !strings.Contains(err.Error(), "beyond float's range") {
		t.Fatalf("decode 1e400: err = %v", err)
	}
	if got := EncodeJSON(Float(3)); got != json.Number("3.0") {
		t.Fatalf("encode 3.0 = %v", got)
	}
}
