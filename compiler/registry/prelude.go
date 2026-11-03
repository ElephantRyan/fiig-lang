package registry

import (
	"fiig-lang/compiler/constant"
	"fiig-lang/compiler/types"
)

// PreludeSource is the Source of a card for a prelude identifier, which has
// no file.
const PreludeSource = "prelude"

// Prelude is a card for each member of the prelude math group (ADR 0049
// §2.4, §2.7), so `catalog find --input float` finds math.sqrt beside the
// package's own formulas. grammar.md's backlog item 9 asks for prelude
// identifiers to have cards like any exported declaration. A package's own
// catalog (Generate) leaves them out: they are no package's contract, and
// a diff between two versions of one must never see them.
//
// A formula with an int form and a float form gets one card, named as its
// callers write it, with the int form in Params and Returns and both in
// Forms. A constant's card has Kind "constant" and, in Returns, the type it
// takes where nothing else decides (ADR 0048 §2.4).
func Prelude() []*Card {
	var cards []*Card
	for _, name := range types.MathFormulaNames() {
		forms, _ := types.MathFormula(name)
		c := &Card{Name: "math." + name, Kind: "formula", Group: types.MathGroup, Catalog: Default, Source: PreludeSource, Pure: true}
		c.Params, c.Returns = mathFormParams(forms[0]), forms[0].Result.String()
		if len(forms) > 1 {
			for _, f := range forms {
				c.Forms = append(c.Forms, Operation{Name: name, Params: mathFormParams(f), Returns: f.Result.String()})
			}
		}
		cards = append(cards, c)
	}
	for _, name := range constant.MathConstantNames() {
		v, _ := constant.MathConstant(name)
		returns := "int"
		if v.Kind == constant.Float {
			returns = "float"
		}
		cards = append(cards, &Card{Name: "math." + name, Kind: "constant", Group: types.MathGroup, Catalog: Default, Source: PreludeSource, Returns: returns, Pure: true})
	}
	return cards
}

func mathFormParams(f types.MathForm) []Param {
	ps := make([]Param, len(f.Params))
	for i, p := range f.Params {
		ps[i] = Param{Name: p.Name, Type: p.Type.String()}
	}
	return ps
}
