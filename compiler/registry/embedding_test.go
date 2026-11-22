package registry

import (
	"testing"

	"fiig-lang/compiler/parser"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// TestModelCardListsFlattenedFieldsWithProvenance is ADR 0045: a card shows
// the flat field set and records where each promoted field came from, so two
// models embedding the same block are visibly related.
func TestModelCardListsFlattenedFieldsWithProvenance(t *testing.T) {
	var files []*syntax.File
	for _, src := range []string{
		"model Address {\n    city: string;\n}",
		"model ContactInfo {\n    Address;\n    email: string;\n}",
		"model Customer {\n    ContactInfo;\n    id: string;\n}",
	} {
		f, errs := parser.Parse(src)
		if len(errs) > 0 {
			t.Fatalf("parse: %v", errs)
		}
		files = append(files, f)
	}
	types.BuildUniverse(files)

	card := BuildCard(files[2].Decls[0], "")
	got := map[string]string{}
	for _, f := range card.Fields {
		got[f.Name] = f.PromotedFrom
	}
	want := map[string]string{"city": "ContactInfo.Address", "email": "ContactInfo", "id": ""}
	if len(got) != len(want) {
		t.Fatalf("fields = %+v, want %v", card.Fields, want)
	}
	for name, via := range want {
		if g, ok := got[name]; !ok || g != via {
			t.Errorf("field %q promotedFrom = %q (present=%v), want %q", name, g, ok, via)
		}
	}
}
