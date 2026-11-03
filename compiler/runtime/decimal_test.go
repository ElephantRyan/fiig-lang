package runtime

import (
	"encoding/json"
	"strings"
	"testing"

	"fiig-lang/compiler/decimal"
	"fiig-lang/compiler/types"
)

// ADR 0064: decimal at runtime.

func dec(t *testing.T, s string) Decimal {
	t.Helper()
	d, ok := decimal.Parse(s)
	if !ok {
		t.Fatalf("decimal %q", s)
	}
	return Decimal{d}
}

func TestDecimalArithmetic(t *testing.T) {
	src := `rule tax(price: decimal) -> decimal {
    rate: decimal = 0.0725
    return price * rate
}

rule label(d: decimal) -> string {
    return "d=${d}"
}

rule scaled() -> string {
    a: decimal = 1.50
    quarter: decimal = 10.0 / 40.0
    return "${a} ${quarter} ${a * (1.0 / 4.0)} ${-a + 0.25} ${a % 0.4} ${a - 1.50}"
}

rule cheaper(a: decimal, b: decimal) -> bool {
    return a < b
}

rule same(a: decimal, b: decimal) -> bool {
    return a == b
}`
	cases := []struct {
		name string
		args map[string]Value
		want string
	}{
		{"tax", map[string]Value{"price": dec(t, "19.99")}, "1.449275"},
		{"label", map[string]Value{"d": dec(t, "-0.07")}, "d=-0.07"},
		{"scaled", nil, "1.50 0.25 0.3750 -1.25 0.30 0.00"},
		{"cheaper", map[string]Value{"a": dec(t, "-0.1"), "b": dec(t, "0.09")}, "true"},
		{"same", map[string]Value{"a": dec(t, "1.50"), "b": dec(t, "1.5")}, "true"},
	}
	for _, c := range cases {
		got, fault := runEach(t, src, c.name, c.args)
		if fault != nil || got == nil || got.String() != c.want {
			t.Errorf("%s(%v) = %v, %v; want %s", c.name, c.args, got, fault, c.want)
		}
	}
}

// TestDecimalOverflowFaults is §2: an exact result over the cap faults
// rather than rounding.
func TestDecimalOverflowFaults(t *testing.T) {
	src := `rule grow(start: decimal, times: int) -> decimal {
    x: decimal = start
    n: int = times
    while n > 0 {
        x = x * 1.05
        n = n - 1
    }
    return x
}

rule remainder(a: decimal, b: decimal) -> decimal {
    return a % b
}`
	_, fault := runEach(t, src, "grow", map[string]Value{"start": dec(t, "1.05"), "times": Int(17)})
	if fault == nil || !strings.Contains(fault.Message, "does not fit in decimal (34 digits written in full)") {
		t.Errorf("grow: fault = %v", fault)
	}
	_, fault = runEach(t, src, "remainder", map[string]Value{"a": dec(t, "1"), "b": dec(t, "0.00")})
	if fault == nil || fault.Message != "division by zero" {
		t.Errorf("remainder by zero: fault = %v", fault)
	}
}

// TestDecimalJSON is §8's JSON row: exact, keeping the scale, and a number
// outside the range is the input's error.
func TestDecimalJSON(t *testing.T) {
	decT := types.Primitive{Name: "decimal"}
	for in, want := range map[string]string{"19.99": "19.99", "1.50": "1.50", "3": "3", "1.5e3": "1500", "1e-2": "0.01"} {
		v, err := DecodeJSON(decodeRawJSON(t, in), decT, nil)
		if err != nil || v.String() != want {
			t.Errorf("decode %s = %v, %v; want %s", in, v, err, want)
		}
	}
	for _, in := range []string{"1e6000", "1e34", "0.00000000000000000000000000000000001"} {
		if _, err := DecodeJSON(decodeRawJSON(t, in), decT, nil); err == nil || !strings.Contains(err.Error(), "does not fit in decimal") {
			t.Errorf("decode %s: err = %v", in, err)
		}
	}
	if _, err := DecodeJSON(decodeRawJSON(t, `"1.5"`), decT, nil); err == nil {
		t.Error(`decode "1.5": want an error`)
	}
	if got := EncodeJSON(dec(t, "1.50")); got != json.Number("1.50") {
		t.Errorf("encode 1.50 = %v", got)
	}
}

// TestDecimalMath is ADR 0064's math members at runtime.
func TestDecimalMath(t *testing.T) {
	src := `rule round2(x: decimal) -> string {
    return "${math.round(x, places: 2, rounding: Rounding.HalfUp)} ${math.round(x, places: 2, rounding: Rounding.HalfEven)} ${math.round(x, places: 2, rounding: Rounding.Up)} ${math.round(x, places: 2, rounding: Rounding.Down)} ${math.round(x, places: 2, rounding: Rounding.Ceiling)} ${math.round(x, places: 2, rounding: Rounding.Floor)} ${math.round(x, places: -1, rounding: Rounding.HalfUp)}"
}

rule split(amount: decimal, n: int) -> List<decimal> {
    return math.allocate(amount, parts: n, places: 2)
}

rule third(x: decimal, by: decimal) -> decimal {
    return math.divide(x, by, places: 2, rounding: Rounding.HalfEven)
}

rule crossings(x: decimal) -> string {
    return "${math.floorToInt(x)} ${math.ceilToInt(x)} ${math.truncToInt(x)} ${math.roundToInt(x)} ${math.roundHalfEvenToInt(x)} ${math.toFloat(x)}"
}

rule fromFloat(f: float) -> decimal {
    return math.toDecimal(f)
}

rule others(a: decimal, b: decimal) -> string {
    return "${math.abs(a)} ${math.min(a, b)} ${math.max(a, b)} ${math.floorMod(a, b)} ${math.addOrNone(a, b)} ${math.mulOrNone(a, b)}"
}

rule parse(s: string) -> decimal | None {
    return s.toDecimalOrNone()
}`
	cases := []struct {
		name string
		args map[string]Value
		want string
	}{
		{"round2", map[string]Value{"x": dec(t, "2.345")}, "2.35 2.34 2.35 2.34 2.35 2.34 0"},
		{"round2", map[string]Value{"x": dec(t, "-2.345")}, "-2.35 -2.34 -2.35 -2.34 -2.34 -2.35 0"},
		{"split", map[string]Value{"amount": dec(t, "10.00"), "n": Int(3)}, "[3.34, 3.33, 3.33]"},
		{"third", map[string]Value{"x": dec(t, "10.00"), "by": dec(t, "3")}, "3.33"},
		{"crossings", map[string]Value{"x": dec(t, "-3.5")}, "-4 -3 -3 -4 -4 -3.5"},
		{"crossings", map[string]Value{"x": dec(t, "2.5")}, "2 3 2 3 2 2.5"},
		// §6: shortest round-trip, so 2.675 stays 2.675.
		{"fromFloat", map[string]Value{"f": Float(2.675)}, "2.675"},
		{"others", map[string]Value{"a": dec(t, "-7.5"), "b": dec(t, "2")}, "7.5 -7.5 2 0.5 -5.5 -15.0"},
		{"parse", map[string]Value{"s": Str("1.50")}, "1.50"},
		{"parse", map[string]Value{"s": Str("1,50")}, "None"},
	}
	for _, c := range cases {
		got, fault := runEach(t, src, c.name, c.args)
		if fault != nil || got == nil || got.String() != c.want {
			t.Errorf("%s(%v) = %v, %v; want %s", c.name, c.args, got, fault, c.want)
		}
	}
	faults := []struct {
		name  string
		args  map[string]Value
		fault string
	}{
		{"split", map[string]Value{"amount": dec(t, "10.005"), "n": Int(2)}, "more decimal places than places"},
		{"split", map[string]Value{"amount": dec(t, "10"), "n": Int(0)}, "needs at least 1 part"},
		{"third", map[string]Value{"x": dec(t, "1"), "by": dec(t, "0.00")}, "divides by zero"},
		{"fromFloat", map[string]Value{"f": Float(1e300)}, "does not fit in decimal"},
		{"crossings", map[string]Value{"x": dec(t, "1e30")}, "does not fit in int"},
	}
	for _, c := range faults {
		_, fault := runEach(t, src, c.name, c.args)
		if fault == nil || !strings.Contains(fault.Message, c.fault) {
			t.Errorf("%s(%v): fault = %v, want %q", c.name, c.args, fault, c.fault)
		}
	}
}
