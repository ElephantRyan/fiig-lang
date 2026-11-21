package runtime

import (
	"context"
	"testing"

	"fiig-lang/compiler/checker"
	"fiig-lang/compiler/parser"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// A `?` that hits Err returns that Err from the declaration wherever it
// is: in a condition, or in a call's or a method's arguments, as much as
// in a binding. Both the tree walk and compiled bodies (ADR 0115).
func TestTryReturnsTheErrFromAnyPosition(t *testing.T) {
	cases := []struct{ name, src string }{
		{"if condition", `conversion f(r: Result<int, string>) -> Result<int, string> {
    if (r?) > 0 {
        return Ok(1)
    }
    return Ok(0)
}`},
		{"else-if condition", `conversion f(r: Result<int, string>) -> Result<int, string> {
    if false {
        return Ok(2)
    } else if (r?) > 0 {
        return Ok(1)
    }
    return Ok(0)
}`},
		{"while condition", `conversion f(r: Result<int, string>) -> Result<int, string> {
    while (r?) > 0 {
        return Ok(1)
    }
    return Ok(0)
}`},
		{"call argument", `conversion f(r: Result<int, string>) -> Result<int, string> {
    return Ok(g(r?))
}`},
		{"method argument", `conversion f(r: Result<int, string>) -> Result<int, string> {
    return Ok("abc".slice(0, r?).length())
}`},
	}
	g := `rule g(n: int) -> int {
    return n + 1
}`
	for _, c := range cases {
		for _, walk := range []bool{true, false} {
			name := c.name + ", compiled"
			if walk {
				name = c.name + ", tree walk"
			}
			t.Run(name, func(t *testing.T) {
				var files []*syntax.File
				for _, s := range []string{c.src, g} {
					f, errs := parser.Parse(s)
					if len(errs) > 0 {
						t.Fatalf("parse: %v", errs)
					}
					files = append(files, f)
				}
				universe, _ := types.BuildUniverse(files)
				if diags := checker.Check(files, nil); len(diags) > 0 {
					t.Fatalf("check: %v", diags)
				}
				in := newTestInterp(universe, nil)
				in.walkOnly = walk
				v, f := in.Call(context.Background(), "f", map[string]Value{"r": Result{Ok: false, Value: Str("no")}})
				if f != nil {
					t.Fatalf("fault: %s", f.Message)
				}
				want := Result{Ok: false, Value: Str("no")}
				if !Equal(v, want) {
					t.Fatalf("got %s, want %s", v, want)
				}
			})
		}
	}
}
