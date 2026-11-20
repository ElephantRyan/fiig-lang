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
    larger = if a > b { a } else { b }
    return larger
}
`,
		"if in a match arm": `rule newest(seen: int | None, updatedAt: int) -> int {
    match seen {
        None -> updatedAt
        seen -> if updatedAt > seen { updatedAt } else { seen }
    }
}
`,
		"match in a binding": `rule pick(a: int | None) -> int {
    larger = match a {
        None -> 0
        n -> n
    }
    return larger
}
`,
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			_, errs := Parse(src)
			if len(errs) != 1 || !strings.Contains(errs[0].Msg, "is a statement, not a value") {
				t.Fatalf("want one statement-not-a-value error, got %v", errs)
			}
		})
	}
}
