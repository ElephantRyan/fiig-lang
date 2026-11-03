package format

import "testing"

// TestFormatKeepsFloatLiteralsAsWritten checks ADR 0048 literals print in
// the author's spelling, exponent case and all: a constant's text is its
// exact value, and rewriting it could only lose that.
func TestFormatKeepsFloatLiteralsAsWritten(t *testing.T) {
	src := "rule scaled(x: float) -> float {\n" +
		"    return x * 6.02E23 + 1e-9 - 0.50\n" +
		"}\n"
	once, errs := Format(src)
	if len(errs) > 0 {
		t.Fatalf("format errors: %v", errs)
	}
	if once != src {
		t.Errorf("formatted output differs from canonical input:\n%s\nwant:\n%s", once, src)
	}
}
