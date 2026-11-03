package registry

import (
	"testing"

	"fiig-lang/compiler/constant"
	"fiig-lang/compiler/types"
)

// TestPreludeCardsCoverMath is grammar.md's backlog item 9 for the prelude
// math group: a card for every formula and constant.
func TestPreludeCardsCoverMath(t *testing.T) {
	cards := Prelude()
	for _, name := range types.MathFormulaNames() {
		if c := ByName(cards, "math."+name); c == nil || c.Kind != "formula" || !c.Pure || c.Source != PreludeSource {
			t.Errorf("math.%s: card = %+v", name, c)
		}
	}
	for _, name := range constant.MathConstantNames() {
		if c := ByName(cards, "math."+name); c == nil || c.Kind != "constant" {
			t.Errorf("math.%s: card = %+v", name, c)
		}
	}
	if c := ByName(cards, "math.pi"); c.Returns != "float" {
		t.Errorf("math.pi returns %q, want float (its default type)", c.Returns)
	}
	closeTo := ByName(cards, "math.closeTo")
	if len(closeTo.Params) != 4 || closeTo.Params[2].Name != "relative" || closeTo.Returns != "bool" || closeTo.Forms != nil {
		t.Errorf("math.closeTo = %+v", closeTo)
	}
}

// TestFindSearchesEveryForm: math.abs's float form is found by a float
// search although its card's own Params are the int form's.
func TestFindSearchesEveryForm(t *testing.T) {
	cards := Prelude()
	names := func(q Query) map[string]bool {
		out := map[string]bool{}
		for _, c := range Find(cards, q) {
			out[c.Name] = true
		}
		return out
	}
	toInt := names(Query{Input: "float", Output: "int"})
	for _, want := range []string{"math.floorToInt", "math.roundHalfEvenToInt"} {
		if !toInt[want] {
			t.Errorf("find --input float --output int: missing %s in %v", want, toInt)
		}
	}
	if toInt["math.abs"] {
		t.Errorf("math.abs has no float -> int form, but was found")
	}
	floats := names(Query{Input: "float", Output: "float"})
	for _, want := range []string{"math.abs", "math.max", "math.sqrt"} {
		if !floats[want] {
			t.Errorf("find --input float --output float: missing %s", want)
		}
	}
	if abs := ByName(cards, "math.abs"); len(abs.Forms) != 3 || abs.Forms[1].Returns != "float" || abs.Forms[2].Returns != "decimal" {
		t.Errorf("math.abs forms = %+v", abs.Forms)
	}
}

// TestConstantsHaveNoSiblings: a constant's Returns is a default type, not
// a signature, so it neither has siblings nor is one.
func TestConstantsHaveNoSiblings(t *testing.T) {
	cards := append(Prelude(), &Card{Name: "gravity", Kind: "rule", Catalog: Default, Returns: "float"})
	if s := Siblings(cards, ByName(cards, "math.pi")); s != nil {
		t.Errorf("math.pi siblings = %v", s)
	}
	if s := Siblings(cards, ByName(cards, "gravity")); s != nil {
		t.Errorf("gravity siblings = %v", s)
	}
}
