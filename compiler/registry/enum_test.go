package registry

import (
	"strings"
	"testing"

	"fiig-lang/compiler/parser"
)

func enumCard(t *testing.T, src string) *Card {
	t.Helper()
	f, errs := parser.Parse(src)
	if len(errs) > 0 {
		t.Fatalf("parse: %v", errs)
	}
	return BuildCard(f.Decls[0], "")
}

// TestEnumCardListsHeaderAndConstants is ADR 0046's registry consequence:
// the catalog can answer "which source has code ACME" without source.
func TestEnumCardListsHeaderAndConstants(t *testing.T) {
	card := enumCard(t, "enum DeductionSource(key code: string, layout: FieldLayout) {\n    Acme(layout: FieldLayout.FixedWidth, code: \"ACME\")\n}")
	if len(card.Header) != 2 || !card.Header[0].Key || card.Header[1].Key {
		t.Fatalf("header = %+v", card.Header)
	}
	got := card.Variants[0].Constants
	if len(got) != 2 || got[0].Name != "code" || got[0].Value != `"ACME"` || got[1].Name != "layout" || got[1].Value != "FieldLayout.FixedWidth" {
		t.Fatalf("constants = %+v, want header order", got)
	}
}

// TestChangedKeyValueIsMajor: a lookup that found a variant by its old key
// now returns None, so it breaks callers; a non-key constant does not.
func TestChangedKeyValueIsMajor(t *testing.T) {
	old := enumCard(t, "enum E(key code: string, label: string) {\n    A(code: \"A\", label: \"x\")\n}")
	label := enumCard(t, "enum E(key code: string, label: string) {\n    A(code: \"A\", label: \"y\")\n}")
	code := enumCard(t, "enum E(key code: string, label: string) {\n    A(code: \"B\", label: \"x\")\n}")
	unkeyed := enumCard(t, "enum E(code: string, label: string) {\n    A(code: \"A\", label: \"x\")\n}")
	if bump, _ := Diff([]*Card{old}, []*Card{label}); bump != Minor {
		t.Errorf("changed non-key constant: bump %v, want Minor", bump)
	}
	if bump, _ := Diff([]*Card{old}, []*Card{code}); bump != Major {
		t.Errorf("changed key value: bump %v, want Major", bump)
	}
	if bump, _ := Diff([]*Card{old}, []*Card{unkeyed}); bump != Major {
		t.Errorf("dropped key marker: bump %v, want Major", bump)
	}
}

// TestFindConstantNamesTheVariant is ADR 0046's catalog question, "which
// source has code ACME": the answer is the variant, matched exactly, with
// list fields matched by element and non-key fields allowed to match many.
func TestFindConstantNamesTheVariant(t *testing.T) {
	card := enumCard(t, "enum DeductionSource(key code: string, key aliases: List<string>, layout: FieldLayout) {\n"+
		"    Acme(code: \"ACME\", aliases: [\"ACMEINC\"], layout: FieldLayout.FixedWidth)\n"+
		"    Globex(code: \"GLBX\", aliases: [], layout: FieldLayout.Csv)\n"+
		"    Initech(code: \"INIT\", aliases: [\"INITECH\"], layout: FieldLayout.Csv)\n"+
		"}")
	cards := []*Card{card}
	variants := func(field, value string) []string {
		var out []string
		for _, m := range FindConstant(cards, field, value) {
			out = append(out, m.Variant)
		}
		return out
	}
	cases := []struct {
		field, value string
		want         []string
	}{
		{"code", "ACME", []string{"Acme"}},
		{"code", `"ACME"`, []string{"Acme"}},
		{"code", "ACM", nil}, // exact, not a substring
		{"aliases", "INITECH", []string{"Initech"}},
		{"aliases", "ACME", nil}, // the code is not an alias
		{"layout", "FieldLayout.Csv", []string{"Globex", "Initech"}},
		{"colour", "red", nil},
	}
	for _, c := range cases {
		got := variants(c.field, c.value)
		if strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("%s=%s: got %v, want %v", c.field, c.value, got, c.want)
		}
	}
	if _, _, err := ParseConstantQuery("code"); err == nil {
		t.Errorf("a query with no = should be rejected")
	}
}
