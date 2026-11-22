package parser

import (
	"strings"
	"testing"
)

// An `if` or `match` where a value goes, a habit from Kotlin and Rust, is
// one error saying so, not a cascade from its braces.
func TestIfOrMatchAsAValueIsOneError(t *testing.T) {
	cases := map[string]string{
		"if in a binding": `rule pick(a: int, b: int) -> int {
    larger = if (a > b) { a } else { b };
    return larger;
}
`,
		"if in a match arm": `rule newest(seen: int | None, updatedAt: int) -> int {
    return match (seen) {
        None -> updatedAt
        seen -> if (updatedAt > seen) { updatedAt } else { seen }
    }
}
`,
		// A whole binding's or return's value may be a match (ADR 0142 §2);
		// inside another expression it may not.
		"match in an argument": `rule pick(a: int | None) -> int {
    return math.max(1, match (a) {
        None -> 0
        n -> n
    })
}
`,
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			_, errs := Parse(src)
			if len(errs) != 1 || !(strings.Contains(errs[0].Msg, "is a statement, not a value") || strings.Contains(errs[0].Msg, "is a value only as a whole")) {
				t.Fatalf("want one statement-not-a-value error, got %v", errs)
			}
		})
	}
}
