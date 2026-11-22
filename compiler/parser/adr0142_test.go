package parser

import (
	"strings"
	"testing"

	"fiig-lang/compiler/syntax"
)

// ADR 0142, step 1 (§5): the new forms parse beside the old ones.
func TestTheNewStatementSyntaxParses(t *testing.T) {
	src := `rule r(a: bool, b: bool, items: List<int>, n: int | None) -> int {
    x = 1;
    if (a && !b || b) {
        return x;
    }
    for (item in items) {
        x = item;
    }
    outer: for (k, v in [1: 2]) {
        break outer;
    }
    while (a) {
        continue;
    }
    match (n) {
        None -> 0
        given -> given
    }
    return n ?? x;
}`
	f, errs := Parse(src)
	if len(errs) != 0 {
		t.Fatalf("errs = %v", errs)
	}
	body := f.Decls[0].(*syntax.RuleDecl).Body
	if len(body.Stmts) != 7 {
		t.Fatalf("got %d statements, want 7", len(body.Stmts))
	}
	cond := body.Stmts[1].(*syntax.IfStmt).Cond.(*syntax.ParenExpr).Inner.(*syntax.BinaryExpr)
	if cond.Op != syntax.OpOr || !cond.Symbol {
		t.Errorf("`a && !b || b` parsed as %+v, want `||` at the top", cond)
	}
	and := cond.Left.(*syntax.BinaryExpr)
	if and.Op != syntax.OpAnd || !and.Symbol || !and.Right.(*syntax.NotExpr).Bang {
		t.Errorf("left of `||` is %+v, want `a && !b`", and)
	}
	ret := body.Stmts[6].(*syntax.ReturnStmt).Value.(*syntax.BinaryExpr)
	if ret.Op != syntax.OpDefault {
		t.Errorf("`n ?? x` parsed as %s", ret.Op)
	}
}

// The old forms still parse during step 1, and `f()??x` is a default
// while `f()? ?? x` propagates first.
func TestTheOldFormsStillParse(t *testing.T) {
	for _, src := range []string{
		"rule r(a: bool) -> bool {\n    return a and not a or a\n}",
		"rule r(xs: List<int>) -> int {\n    for x in xs {\n        return x\n    }\n    return 0\n}",
		"rule r(n: int | None) -> int {\n    return n or 0\n}",
	} {
		if _, errs := Parse(src); len(errs) != 0 {
			t.Errorf("%q: errs = %v", src, errs)
		}
	}
	f, errs := Parse("rule r(f: int) -> int {\n    a = f()??3\n    b = f()? ?? 3\n    return a\n}")
	if len(errs) != 0 {
		t.Fatalf("errs = %v", errs)
	}
	stmts := f.Decls[0].(*syntax.RuleDecl).Body.Stmts
	if b := stmts[0].(*syntax.Binding).Value.(*syntax.BinaryExpr); b.Op != syntax.OpDefault {
		t.Errorf("f()??3 is %s, want ??", b.Op)
	}
	if b := stmts[1].(*syntax.Binding).Value.(*syntax.BinaryExpr); b.Op != syntax.OpDefault {
		t.Errorf("f()? ?? 3 is %s, want ??", b.Op)
	} else if _, ok := b.Left.(*syntax.TryExpr); !ok {
		t.Errorf("f()? ?? 3: left is %T, want the `?`", b.Left)
	}
}

// `??` beside `&&` or `||` needs parentheses (ADR 0142 §3).
func TestADefaultBesideLogicNeedsParentheses(t *testing.T) {
	for _, body := range []string{"a ?? b || c", "a || b ?? c", "a && b ?? c"} {
		_, errs := Parse("rule r(a: bool | None, b: bool, c: bool) -> bool {\n    return " + body + "\n}")
		if len(errs) != 1 || !strings.Contains(errs[0].Msg, "needs parentheses") {
			t.Errorf("%s: errs = %v", body, errs)
		}
	}
	if _, errs := Parse("rule r(a: bool | None, b: bool, c: bool) -> bool {\n    return (a ?? b) || c\n}"); len(errs) != 0 {
		t.Errorf("errs = %v", errs)
	}
}
