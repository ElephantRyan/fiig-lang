package decimal

import (
	"math"
	"math/big"
	"strings"
	"testing"
)

func mustParse(t *testing.T, s string) Decimal {
	t.Helper()
	d, ok := Parse(s)
	if !ok {
		t.Fatalf("Parse(%q) failed", s)
	}
	return d
}

func TestParseKeepsScale(t *testing.T) {
	for in, want := range map[string]string{
		"19.99": "19.99", "1.50": "1.50", "-0.07": "-0.07", "1e-2": "0.01",
		"1.5e3": "1500", "12": "12", "0.00": "0.00", "-0": "0", "1E+2": "100",
		"0e-1000000000":                      "0.0000000000000000000000000000000000",
		"1" + strings.Repeat("0", 33):        "1" + strings.Repeat("0", 33),
		"0." + strings.Repeat("0", 33) + "1": "0." + strings.Repeat("0", 33) + "1",
		// 35 digits, the last a trailing zero: trimmed, not refused (§5).
		"1." + strings.Repeat("0", 34): "1." + strings.Repeat("0", 33),
	} {
		if got := mustParse(t, in).String(); got != want {
			t.Errorf("Parse(%q) = %s, want %s", in, got, want)
		}
	}
	for _, in := range []string{
		"", "-", "01", "1.", ".5", "+1", "1e", "1e+", "0x10", "NaN", "Infinity", "1 ",
		"1e34", "1e6000", "1e1000000000", "1e-35", "0." + strings.Repeat("0", 34) + "1",
		"1." + strings.Repeat("0", 33) + "1",
	} {
		if d, ok := Parse(in); ok {
			t.Errorf("Parse(%q) = %s, want failure", in, d)
		}
	}
}

func TestArithmeticIsExact(t *testing.T) {
	p := func(s string) Decimal { return mustParse(t, s) }
	cases := []struct {
		got  func() (Decimal, bool)
		want string
	}{
		{func() (Decimal, bool) { return p("19.99").Mul(p("0.0725")) }, "1.449275"},
		{func() (Decimal, bool) { return p("1.5").Add(p("0.25")) }, "1.75"},
		{func() (Decimal, bool) { return p("1.50").Sub(p("1.5")) }, "0.00"},
		{func() (Decimal, bool) { return p("-0.01").Mul(p("0")) }, "0.00"},
		{func() (Decimal, bool) { return p("-7.5").Rem(p("2")), true }, "-1.5"},
		{func() (Decimal, bool) { return p("7.5").Rem(p("-2")), true }, "1.5"},
		{func() (Decimal, bool) { return p("16.00000000").Mul(p("16.00000000")) }, "256.0000000000000000"},
	}
	for i, c := range cases {
		d, ok := c.got()
		if !ok || d.String() != c.want {
			t.Errorf("case %d = %s, %v; want %s", i, d, ok, c.want)
		}
	}
	big33 := p(strings.Repeat("9", 34))
	if d, ok := big33.Add(p("1")); ok {
		t.Errorf("10^34 fits: %s", d)
	}
	if d, ok := p("1.1").Mul(p(strings.Repeat("1", 33) + ".1")); ok {
		t.Errorf("35 significant digits fit: %s", d)
	}
	// Squaring 2.00 repeatedly keeps the trailing zeros until the cap, then
	// trims them rather than faulting (§5).
	sq := p("2.00")
	for range 6 {
		var ok bool
		if sq, ok = sq.Mul(sq); !ok {
			t.Fatalf("2.00 squared repeatedly faulted at %s", sq)
		}
	}
	if sq.Cmp(p("18446744073709551616")) != 0 || sq.Scale() > Digits {
		t.Errorf("2^64 = %s", sq)
	}
}

func TestCmpIsByValue(t *testing.T) {
	if mustParse(t, "1.50").Cmp(mustParse(t, "1.5")) != 0 {
		t.Error("1.50 != 1.5")
	}
	if mustParse(t, "-0.1").Cmp(mustParse(t, "0.09")) >= 0 {
		t.Error("-0.1 >= 0.09")
	}
}

func TestRound(t *testing.T) {
	cases := []struct {
		in     string
		places int
		mode   Rounding
		want   string
	}{
		{"2.345", 2, HalfUp, "2.35"}, {"-2.345", 2, HalfUp, "-2.35"},
		{"2.345", 2, HalfEven, "2.34"}, {"2.355", 2, HalfEven, "2.36"},
		{"2.341", 2, Up, "2.35"}, {"-2.341", 2, Up, "-2.35"},
		{"2.349", 2, Down, "2.34"}, {"-2.349", 2, Down, "-2.34"},
		{"2.341", 2, Ceiling, "2.35"}, {"-2.349", 2, Ceiling, "-2.34"},
		{"2.349", 2, Floor, "2.34"}, {"-2.341", 2, Floor, "-2.35"},
		{"1234.5", -2, HalfUp, "1200"}, {"1250", -2, HalfEven, "1200"},
		{"1.5", 3, HalfUp, "1.500"}, {"-0.001", 2, HalfUp, "0.00"},
		{"5", -1000, HalfUp, "0"}, {"1.5", 1000, HalfUp, "1.500000000000000000000000000000000"},
	}
	for _, c := range cases {
		d, ok := mustParse(t, c.in).Round(c.places, c.mode)
		if !ok || d.String() != c.want {
			t.Errorf("round(%s, %d, %s) = %s, %v; want %s", c.in, c.places, RoundingNames[c.mode], d, ok, c.want)
		}
	}
	if d, ok := mustParse(t, "5").Round(-1000, Up); ok {
		t.Errorf("round(5, -1000, Up) = %s, want over the cap", d)
	}
}

func TestQuoRoundsOnce(t *testing.T) {
	p := func(s string) Decimal { return mustParse(t, s) }
	cases := []struct {
		a, b   string
		places int
		mode   Rounding
		want   string
	}{
		{"10.00", "3", 2, HalfEven, "3.33"}, {"10", "4", 1, HalfUp, "2.5"},
		{"-10", "3", 0, Floor, "-4"}, {"10", "-3", 0, Up, "-4"},
		{"1", "3", 34, HalfUp, "0." + strings.Repeat("3", 34)},
		{"1", "4", 1000, HalfUp, "0.25" + strings.Repeat("0", 32)},
		// The exact quotient is 0.124…975, 36 places, and rounds to 0.12.
		// Rounding to 34 places first would give 0.125 and then 0.13 (§3).
		{"0." + "4" + strings.Repeat("9", 33), "4", 2, HalfUp, "0.12"},
	}
	for _, c := range cases {
		d, ok := Quo(p(c.a), p(c.b), c.places, c.mode)
		if !ok || d.String() != c.want {
			t.Errorf("%s / %s at %d = %s, %v; want %s", c.a, c.b, c.places, d, ok, c.want)
		}
	}
	if d, ok := Quo(p("1"), p("3"), 1000, HalfUp); ok {
		t.Errorf("1/3 at 1000 places = %s, want over the cap", d)
	}
}

func TestFromRat(t *testing.T) {
	if d, problem := FromRat(big.NewRat(5, 2), -1); problem != "" || d.String() != "2.5" {
		t.Errorf("5/2 = %s %q", d, problem)
	}
	if d, problem := FromRat(big.NewRat(3, 2), 2); problem != "" || d.String() != "1.50" {
		t.Errorf("3/2 at scale 2 = %s %q", d, problem)
	}
	if _, problem := FromRat(big.NewRat(1, 3), -1); !strings.Contains(problem, "no exact decimal") {
		t.Errorf("1/3: %q", problem)
	}
	for text, want := range map[string]int{"1.50": 2, "1e-2": 2, "1.5e3": 0, "1.5e+1": 0, "3": 0, "2.5E-1": 2} {
		if got := LiteralScale(text); got != want {
			t.Errorf("LiteralScale(%s) = %d, want %d", text, got, want)
		}
	}
}

func TestFloatCrossings(t *testing.T) {
	tenth, fifth := 0.1, 0.2
	for f, want := range map[float64]string{0.1: "0.1", 2.675: "2.675", tenth + fifth: "0.30000000000000004", 100: "100", 1.5e-20: "0.000000000000000000015"} {
		d, ok := FromFloat(f)
		if !ok || d.String() != want {
			t.Errorf("FromFloat(%v) = %s, %v; want %s", f, d, ok, want)
		}
		if d.Float64() != f {
			t.Errorf("FromFloat(%v) does not round-trip", f)
		}
	}
	for _, f := range []float64{1e300, 1e-40, math.MaxFloat64} {
		if d, ok := FromFloat(f); ok {
			t.Errorf("FromFloat(%v) = %s, want out of range", f, d)
		}
	}
}

func TestAllocateAddsBack(t *testing.T) {
	cases := []struct {
		amount string
		parts  int64
		places int
		want   string
	}{
		{"10.00", 3, 2, "[3.34 3.33 3.33]"},
		{"-10.00", 3, 2, "[-3.34 -3.33 -3.33]"},
		{"0.01", 3, 2, "[0.01 0.00 0.00]"},
		{"7", 2, 0, "[4 3]"},
		{"1500", 4, -2, "[400 400 400 300]"},
		// Shares keep places as their scale, trimmed to the cap (§5).
		{"1", 2, 40, "[0.5" + strings.Repeat("0", 33) + " 0.5" + strings.Repeat("0", 33) + "]"},
	}
	for _, c := range cases {
		shares, problem := Allocate(mustParse(t, c.amount), c.parts, c.places)
		strs := make([]string, len(shares))
		sum := Decimal{}
		for i, s := range shares {
			strs[i] = s.String()
			sum, _ = sum.Add(s)
		}
		if got := "[" + strings.Join(strs, " ") + "]"; problem != "" || got != c.want {
			t.Errorf("allocate(%s, %d, %d) = %s %q; want %s", c.amount, c.parts, c.places, got, problem, c.want)
		}
		if sum.Cmp(mustParse(t, c.amount)) != 0 {
			t.Errorf("allocate(%s, %d, %d) adds to %s", c.amount, c.parts, c.places, sum)
		}
	}
	for _, c := range []struct {
		amount string
		parts  int64
		places int
		want   string
	}{
		{"10.005", 2, 2, "more decimal places"},
		{"10", 0, 2, "at least 1 part"},
		{"10", MaxParts + 1, 2, "at most 10000000 parts"},
		{"1", 3, 40, "does not fit"},
	} {
		if _, problem := Allocate(mustParse(t, c.amount), c.parts, c.places); !strings.Contains(problem, c.want) {
			t.Errorf("allocate(%s, %d, %d): problem %q, want %q", c.amount, c.parts, c.places, problem, c.want)
		}
	}
}
