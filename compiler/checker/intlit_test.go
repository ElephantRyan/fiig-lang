package checker

import (
	"strings"
	"testing"

	"fiig-lang/compiler/syntax"
)

// ADR 0063 §3, completed by ADR 0048 §2.4: an int constant outside int64
// is a compile error. The whole constant expression is evaluated exactly
// first, so `-9223372036854775808` is legal without a special fold and
// `9223372036854775807 + 1` is caught here rather than faulting at runtime.

func TestIntLiteralOutOfRangeIsAnError(t *testing.T) {
	cases := []struct {
		expr string
		bad  bool
	}{
		{"9223372036854775807", false},
		{"-9223372036854775808", false},
		{"-0x8000000000000000", false},
		{"-(9223372036854775808)", false},
		{"(2 - 3) * 9223372036854775808", false},
		{"9223372036854775808 - 1", false},
		{"9223372036854775808", true},
		{"-9223372036854775809", true},
		{"99999999999999999999", true},
		{"0x8000000000000000", true},
		{"9223372036854775807 + 1", true},
		{"1e19", true},
	}
	for _, c := range cases {
		f := mustParse(t, "rule r() -> int {\n    return "+c.expr+"\n}\n")
		diags := Check([]*syntax.File{f}, nil)
		got := len(diags) == 1 && strings.Contains(diags[0].Msg, "does not fit in int")
		if c.bad != got || (!c.bad && len(diags) != 0) {
			t.Errorf("%s: diags %v, want error=%v", c.expr, diags, c.bad)
		}
	}
}

func TestIntLiteralOutOfRangeInBinaryPatterns(t *testing.T) {
	f := mustParse(t, `conversion frame(buffer: bytes) -> int {
    match buffer {
        <<99999999999999999999: u64be, body: bytes(99999999999999999999)>> -> body.length()
        _ -> 0
    }
}
`)
	diags := Check([]*syntax.File{f}, nil)
	n := 0
	for _, d := range diags {
		if strings.Contains(d.Msg, "does not fit in int") {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("want 2 out-of-range diagnostics (literal and size), got %v", diags)
	}
}
