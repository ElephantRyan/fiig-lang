package registry

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestFormulaCardsAreOnePerMember checks ADR 0049's cards: a formulas group
// gets no card of its own, each member gets one named as callers write it,
// and `find` reaches a member by its own name.
func TestFormulaCardsAreOnePerMember(t *testing.T) {
	root := t.TempDir()
	src := "formulas vector {\n\tformula dot(a: int, b: int) -> int {\n\t\treturn a * b\n\t}\n\n\tformula square(a: int) -> int {\n\t\treturn dot(a, a)\n\t}\n}\n"
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
