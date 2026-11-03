package format

import (
	"strings"
	"testing"
)

// TestFormatLoopSyntaxIsStable checks that for-loops, labels, jumps, and
// typed bindings (ADR 0053) print canonically and reach a fixed point after one
// pass.
func TestFormatLoopSyntaxIsStable(t *testing.T) {
	src := "rule sumUntilNegative(orders: List<Order>) -> int {\n" +
		"    total: int = 0\n" +
		"    outer: for order in orders {\n" +
		"        if order.amount < 0 {\n" +
		"            break outer\n" +
		"        }\n" +
		"        continue\n" +
		"    }\n" +
		"    return total\n" +
		"}\n"
	once, errs := Format(src)
	if len(errs) > 0 {
		t.Fatalf("format errors: %v", errs)
	}
	if once != src {
		t.Errorf("formatted output differs from canonical input:\n%s\nwant:\n%s", once, src)
	}
	twice, _ := Format(once)
	if twice != once {
		t.Errorf("formatter is not idempotent:\n%s\nvs:\n%s", once, twice)
	}
}

// TestFormatWhileIsStable checks `while` and a labeled `while` (ADR 0059).
func TestFormatWhileIsStable(t *testing.T) {
	src := "rule countdown(n: int) -> int {\n" +
		"    left: int = n\n" +
		"    scan: while left > 0 {\n" +
		"        left = left - 1\n" +
		"        while true {\n" +
		"            break scan\n" +
		"        }\n" +
		"    }\n" +
		"    return left\n" +
		"}\n"
	once, errs := Format(src)
	if len(errs) > 0 {
		t.Fatalf("format errors: %v", errs)
	}
	if once != src {
		t.Errorf("formatted output differs from canonical input:\n%s\nwant:\n%s", once, src)
	}
	twice, _ := Format(once)
	if twice != once {
		t.Errorf("formatter is not idempotent:\n%s\nvs:\n%s", once, twice)
	}
}

// TestFormatStringEscapesAreStable checks `\r` and `\$` round-trip (ADR
// 0061): CR prints as `\r`, and a literal "${" prints escaped while a lone
// '$' stays plain.
func TestFormatStringEscapesAreStable(t *testing.T) {
	src := "conversion line(path: string) -> string {\n" +
		`    return "GET ${path}\r\n\${raw} $5 $$${path}"` + "\n" +
		"}\n"
	once, errs := Format(src)
	if len(errs) > 0 {
		t.Fatalf("format errors: %v", errs)
	}
	if once != src {
		t.Errorf("formatted output differs from canonical input:\n%s\nwant:\n%s", once, src)
	}
	crlf, errs := Format(strings.ReplaceAll(src, "\n", "\r\n"))
	if len(errs) > 0 {
		t.Fatalf("format errors on CRLF source: %v", errs)
	}
	if crlf != src {
		t.Errorf("CRLF source formats differently:\n%q\nwant:\n%q", crlf, src)
	}
}

// TestFormatCRLFCommentsLoseTheirCR checks that a `//` comment in a CRLF
// file formats to the LF form, not with its CR carried into the output.
func TestFormatCRLFCommentsLoseTheirCR(t *testing.T) {
	src := "// Doubles n.\n" +
		"conversion double(n: int) -> int {\n" +
		"    // Twice.\n" +
		"    return n * 2 // trailing\n" +
		"}\n"
	got, errs := Format(strings.ReplaceAll(src, "\n", "\r\n"))
	if len(errs) > 0 {
		t.Fatalf("format errors: %v", errs)
	}
	if got != src {
		t.Errorf("CRLF source formats differently:\n%q\nwant:\n%q", got, src)
	}
}
