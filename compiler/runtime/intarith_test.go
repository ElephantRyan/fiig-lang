package runtime

import (
	"math"
	"strings"
	"testing"
)

// ADR 0063: int overflow is a fault, and an overflowing binary pattern
// size is a non-match.

func TestIntOverflowFaults(t *testing.T) {
	src := `rule calc(a: int, b: int) -> int {
    return a + b;
}
`
	ops := map[string]string{"+": "a + b", "-": "a - b", "*": "a * b", "/": "a / b", "%": "a % b", "neg": "-a"}
	cases := []struct {
		op         string
		a, b       int64
		want       int64
		overflowed bool
	}{
		{"+", math.MaxInt64, 1, 0, true},
		{"+", math.MinInt64, -1, 0, true},
		{"+", math.MaxInt64, math.MinInt64, -1, false},
		{"-", math.MinInt64, 1, 0, true},
		{"-", 0, math.MinInt64, 0, true},
		{"-", -1, math.MinInt64, math.MaxInt64, false},
		{"*", math.MaxInt64, 2, 0, true},
		{"*", 1 << 32, 1 << 31, 0, true},
		{"*", math.MinInt64, -1, 0, true},
		{"*", -1, math.MinInt64, 0, true},
		{"*", math.MinInt64, 1, math.MinInt64, false},
		{"*", -3037000499, 3037000499, -9223372030926249001, false},
		{"/", math.MinInt64, -1, 0, true},
		{"/", math.MinInt64, 1, math.MinInt64, false},
		{"%", math.MinInt64, -1, 0, false},
		{"neg", math.MinInt64, 0, 0, true},
		{"neg", math.MaxInt64, 0, -math.MaxInt64, false},
	}
	for _, c := range cases {
		prog := strings.Replace(src, "a + b", ops[c.op], 1)
		v, fault := run(t, prog, "calc", map[string]Value{"a": Int(c.a), "b": Int(c.b)})
		if c.overflowed {
			if fault == nil || !strings.Contains(fault.Message, "integer overflow") {
				t.Errorf("%d %s %d = %v, %v; want an overflow fault", c.a, c.op, c.b, v, fault)
			}
			continue
		}
		if fault != nil || v != Int(c.want) {
			t.Errorf("%d %s %d = %v, %v; want %d", c.a, c.op, c.b, v, fault, c.want)
		}
	}
}

// TestOverflowFaultNamesOperatorAndPosition is §1: the fault says which
// operator and where.
func TestOverflowFaultNamesOperatorAndPosition(t *testing.T) {
	src := "rule grow(n: int) -> int {\n    return n * 4;\n}\n"
	_, fault := run(t, src, "grow", map[string]Value{"n": Int(math.MaxInt64 / 2)})
	if fault == nil || !strings.Contains(fault.Message, "at 2:12") || !strings.Contains(fault.Message, "* 4") {
		t.Fatalf("fault = %v, want the operator and 2:12", fault)
	}
}

// TestMinIntLiteralIsLegal is §3: the checker evaluates `-` and the literal
// as one constant (ADR 0048 §2.4).
func TestMinIntLiteralIsLegal(t *testing.T) {
	for _, lit := range []string{"-9223372036854775808", "-0x8000000000000000"} {
		src := "rule lowest() -> int {\n    return " + lit + "\n}\n"
		if v, fault := run(t, src, "lowest", nil); fault != nil || v != Int(math.MinInt64) {
			t.Errorf("%s = %v, %v; want MinInt64", lit, v, fault)
		}
	}
}

// TestOverflowingPatternSizeIsANonMatch is §4: `len * 8` with a hostile
// len of 2^61 + 1 wraps to exactly 8 under Go's arithmetic, which would
// pass the bounds check and match.
func TestOverflowingPatternSizeIsANonMatch(t *testing.T) {
	src := `conversion frame(buffer: bytes) -> int {
    return match (buffer) {
        <<len: i64be, body: bytes(len * 8)>> -> body.length()
        _ -> -1
    }
}
`
	hostile := b(0x20, 0, 0, 0, 0, 0, 0, 1, 1, 2, 3, 4, 5, 6, 7, 8)
	if v, fault := run(t, src, "frame", map[string]Value{"buffer": hostile}); fault != nil || v != Int(-1) {
		t.Fatalf("frame(hostile) = %v, %v; want the fallback arm", v, fault)
	}
	honest := b(0, 0, 0, 0, 0, 0, 0, 1, 1, 2, 3, 4, 5, 6, 7, 8)
	if v, fault := run(t, src, "frame", map[string]Value{"buffer": honest}); fault != nil || v != Int(8) {
		t.Fatalf("frame(honest) = %v, %v; want 8", v, fault)
	}
}
