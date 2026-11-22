package parser

import (
	"strings"
	"testing"

	"fiig-lang/compiler/syntax"
)

// ADR 0049 §2.1: `formulas name { formula ... }`.

func TestParseFormulasGroup(t *testing.T) {
	f, errs := Parse("formulas vector {\n    formula dot(a: int, b: int) -> int {\n        return a * b;\n    }\n\n    formula sqrt(x: int) -> int;\n\n    formula fetch(x: int) -> int requires Socket {\n        return x;\n    }\n}\n")
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	g, ok := f.Decls[0].(*syntax.FormulasDecl)
	if !ok || g.Name != "vector" || len(g.Members) != 3 {
		t.Fatalf("got %#v", f.Decls[0])
	}
	if g.Members[0].Body == nil || g.Members[1].Body != nil {
		t.Errorf("dot should have a body and sqrt none")
	}
	if g.Members[2].Requires != "Socket" || g.Members[2].Body == nil {
		t.Errorf("fetch: requires %q, body %v", g.Members[2].Requires, g.Members[2].Body)
	}
}

func TestFormulasGroupHoldsOnlyFormulas(t *testing.T) {
	_, errs := Parse("formulas vector {\n    rule dot(a: int) -> int {\n        return a\n    }\n}\n")
	if len(errs) == 0 || !strings.Contains(errs[0].Msg, "holds only formulas") {
		t.Fatalf("got %v", errs)
	}
}

// TestFormulaIsANameOutsideDeclarationStart is ADR 0018: the new keywords
// are reserved only where a declaration starts.
func TestFormulaIsANameOutsideDeclarationStart(t *testing.T) {
	if _, errs := Parse("rule f(formula: int, formulas: int) -> int {\n    return formula + formulas;\n}\n"); len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
}
