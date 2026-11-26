package registry

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"testing/fstest"
)

// TestFormulaCardsAreOnePerMember checks ADR 0049's cards: a formulas group
// gets no card of its own, each member gets one named as callers write it,
// and `find` reaches a member by its own name.
func TestFormulaCardsAreOnePerMember(t *testing.T) {
	root := t.TempDir()
	src := "formulas vector {\n\tformula dot(a: int, b: int) -> int {\n\t\treturn a * b;\n\t}\n\n\tformula square(a: int) -> int {\n\t\treturn dot(a, a);\n\t}\n}\n"
	if err := os.WriteFile(filepath.Join(root, "vector.fiig"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	cards, fileErrs, err := Generate(root)
	if err != nil || len(fileErrs) > 0 {
		t.Fatalf("Generate: %v %+v", err, fileErrs)
	}
	var names []string
	for _, c := range cards {
		names = append(names, c.Name)
		if c.Kind != "formula" || c.Group != "vector" || !c.Pure {
			t.Errorf("card %s: kind %q group %q pure %v", c.Name, c.Kind, c.Group, c.Pure)
		}
	}
	if want := []string{"vector.dot", "vector.square"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("cards %v, want %v", names, want)
	}
	if deps := cards[1].Dependencies; !reflect.DeepEqual(deps, []string{"vector.dot"}) {
		t.Errorf("square's dependencies %v, want [vector.dot]", deps)
	}
	found := Find(cards, Query{Text: "square"})
	if len(found) != 1 || found[0].Name != "vector.square" {
		t.Errorf("find square: %+v", found)
	}
}

// ADR 0151 §4: a formula that may fail shows its error, and `find --error`
// reaches it as it reaches a rule.
func TestAFormulaCardShowsItsError(t *testing.T) {
	cards, fileErrs, err := GenerateFS(fstest.MapFS{
		"src/Date.fiig": {Data: []byte("formulas date {\n\tformula month(n: int) -> Result<int, DateError> {\n\t\treturn Ok(n);\n\t}\n\n\tformula clamp(n: int) -> int {\n\t\treturn n;\n\t}\n}\n")},
	})
	if err != nil || len(fileErrs) > 0 {
		t.Fatalf("GenerateFS: %v %+v", err, fileErrs)
	}
	found := Find(cards, Query{Error: "DateError"})
	if len(found) != 1 || found[0].Name != "date.month" || found[0].Error != "DateError" {
		t.Errorf("find --error DateError = %+v, want date.month alone", found)
	}
}
