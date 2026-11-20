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
