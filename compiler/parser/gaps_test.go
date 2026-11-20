package parser

import (
	"strings"
	"testing"
)

// abs-recommendations GAPS G9 and G10: a literal in a pattern, and a
// statement after an arm's `->`, are one error each, saying what to write.
func TestPatternLiteralAndArmStatementAreOneError(t *testing.T) {
	cases := map[string]struct{ src, want string }{
		"int in a pattern": {`rule r(x: Result<int, Failure>) -> int {
    match x {
        Err(Failure.Rejected(500)) -> 1
        _ -> 0
    }
}
`, "a pattern can't hold a literal (500)"},
		"negative and string": {`rule r(x: Result<int, string>) -> int {
    match x {
        Ok(-1) -> 1
        _ -> 0
    }
}
`, "a pattern can't hold a literal (-1)"},
		"expect after an arrow": {`test t {
    match found {
        None -> expect false
        seen -> expect seen == 1
    }
}
`, "`expect` is a statement, and an arm takes an expression or a block"},
		"return after an arrow": {`rule r(x: int | None) -> int {
    match x {
        None -> return 0
        n -> n
    }
}
`, "`return` is a statement"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, errs := Parse(c.src)
			n := 1
			if name == "expect after an arrow" {
				n = 2 // one per arm
			}
			if len(errs) != n || !strings.Contains(errs[0].Msg, c.want) {
				t.Fatalf("want %d error(s) containing %q, got %v", n, c.want, errs)
			}
		})
	}
}
