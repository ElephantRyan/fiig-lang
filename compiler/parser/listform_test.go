package parser

import (
	"testing"

	"fiig-lang/compiler/syntax"
)

// ADR 0093: `descending` and `then` belong to a sorted key only on the
// line the key ends on, and a list form's name is a form only before
// `( name in`.
func TestSortedKeysStayOnTheirLine(t *testing.T) {
	f, errs := Parse(`rule r(xs: List<Order>, descending: int) -> int {
    a = sorted(x in xs): x.region then x.total descending
    b = sorted(x in xs): x.total
    descending = 2
    return count(descending) + max(x in xs): x.total
}
`)
	if len(errs) != 0 {
		t.Fatalf("errors: %v", errs)
	}
	body := f.Decls[0].(*syntax.RuleDecl).Body.Stmts
	a := body[0].(*syntax.Binding).Value.(*syntax.SortedExpr)
	if len(a.Keys) != 2 || a.Keys[0].Descending || !a.Keys[1].Descending {
		t.Fatalf("a's keys = %+v", a.Keys)
	}
	if b := body[1].(*syntax.Binding).Value.(*syntax.SortedExpr); len(b.Keys) != 1 || b.Keys[0].Descending {
		t.Fatalf("b's keys = %+v", b.Keys)
	}
	if len(body) != 4 {
		t.Fatalf("want 4 statements, got %d", len(body))
	}
}
