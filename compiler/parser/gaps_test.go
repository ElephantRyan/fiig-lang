package parser

import (
	"strings"
	"testing"
)

// abs-recommendations GAPS G9 and G10: a literal in a pattern, and a
// statement after an arm's `->`, are one error each, saying what to write.
func TestPatternLiteralAndArmStatementAreOneError(t *testing.T) {
	cases := map[string]struct{ src, want string }{
		// ADR 0130 allows an int, a string and a bool in a pattern; a float and
		// a decimal stay refused, in one error.
		"float in a pattern": {`rule r(x: Result<float, Failure>) -> int {
    match x {
        Ok(1.5) -> 1
        _ -> 0
    }
}
`, "a pattern can't hold a float or decimal literal (1.5)"},
		"string with an interpolation": {`rule r(x: string) -> int {
    match x {
        "a${x}" -> 1
        _ -> 0
    }
}
`, "a string pattern can't interpolate"},
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

// GAPS G8: a private block in or after a capability group is one error,
// pointing at what a group's gateways share instead.
func TestPrivateBlockInACapabilityGroupIsOneError(t *testing.T) {
	gateway := `    gateway me(token: string) -> Result<User, ApiError>
        requires HttpClient
    {
        return Err(ApiError.Down)
    }
`
	helper := `private {
    okBody(request: http.Request) -> Result<bytes, ApiError> {
        match HttpClient.send(request) {
            Err(failure) -> Err(ApiError.Down)
            Ok(reply) -> Ok(reply.body)
        }
    }
}
`
	cases := map[string]string{
		"inside the group": "capability Api {\n" + gateway + indent(helper) + gateway + "}\n",
		"after the group":  "capability Api {\n" + gateway + "}\n\n" + helper,
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			_, errs := Parse(src)
			if len(errs) != 1 || !strings.Contains(errs[0].Msg, "put them in a conversion that turns a result into the group's error") {
				t.Fatalf("want one error pointing at a conversion, got %v", errs)
			}
		})
	}
}

func indent(s string) string {
	lines := strings.Split(strings.TrimSuffix(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = "    " + l
	}
	return strings.Join(lines, "\n") + "\n"
}

// GAPS G20: `is` inside an expression is one error.
func TestIsInAnExpressionIsOneError(t *testing.T) {
	_, errs := Parse(`rule r(users: List<User>) -> int {
    return count(u in users): u.email is not None
}
`)
	if len(errs) != 1 || !strings.Contains(errs[0].Msg, "`is` takes a pattern only in `expect x is Pattern`") {
		t.Fatalf("want one error, got %v", errs)
	}
}

// GAPS G33: a keyword where a name goes is one error saying so.
func TestKeywordAsANameIsOneError(t *testing.T) {
	_, errs := Parse("workflow page(limit: int) -> int {\n    return limit\n}\n")
	if len(errs) == 0 || !strings.Contains(errs[0].Msg, "`limit` is a keyword") {
		t.Fatalf("want a keyword error first, got %v", errs)
	}
	if len(errs) > 2 {
		t.Errorf("want at most the declaration's and the use's errors, got %v", errs)
	}
}
