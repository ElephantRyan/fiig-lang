package format

import "testing"

// TestFormatFormulasGroupIsStable checks ADR 0049's group prints the way a
// capability group does: members one level in, a blank line between them,
// and a native member as its header line alone.
func TestFormatFormulasGroupIsStable(t *testing.T) {
	src := "use geo.vector\n" +
		"\n" +
		"formulas shapes {\n" +
		"    formula diagonal(v: Vector) -> int {\n" +
		"        return vector.lengthSquared(v)\n" +
		"    }\n" +
		"\n" +
		"    // native: the compiler supplies the body\n" +
		"    formula sqrt(x: int) -> int\n" +
		"\n" +
		"    formula twice(n: int) -> int {\n" +
		"        return diagonal(Vector(x: n, y: n))\n" +
		"    }\n" +
		"}\n"
	once, errs := Format(src)
	if len(errs) > 0 {
		t.Fatalf("format errors: %v", errs)
	}
	if once != src {
		t.Errorf("formatted output differs from canonical input:\n%s\nwant:\n%s", once, src)
	}
	if twice, _ := Format(once); twice != once {
		t.Errorf("formatter is not idempotent:\n%s\nvs:\n%s", once, twice)
	}
}
