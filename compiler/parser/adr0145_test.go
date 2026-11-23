package parser

import "testing"

// ADR 0145: items take `;` and alternatives `,`. Until ADR 0142 §5's
// second step both forms parse, and MissingSeparators says where the old
// form left one out.
func TestItemAndAlternativeSeparators(t *testing.T) {
	newForm := "use acme.payments;\n\n" +
		"enum Region { US, EU, APAC }\n"
	if _, errs := Parse(newForm); len(errs) > 0 {
		t.Fatalf("new form: %v", errs)
	}
	if m := MissingSeparators(newForm); len(m) != 0 {
		t.Errorf("new form reports missing separators: %v", m)
	}

	oldForm := "use acme.payments\n\n" +
		"enum Region {\n    US\n    EU\n}\n"
	if _, errs := Parse(oldForm); len(errs) > 0 {
		t.Fatalf("old form: %v", errs)
	}
	got := MissingSeparators(oldForm)
	want := []struct {
		line, col int
		sep       string
	}{{1, 18, ";"}, {4, 7, ","}}
	if len(got) != len(want) {
		t.Fatalf("missing = %v, want %d", got, len(want))
	}
	for i, w := range want {
		g := got[i]
		if g.At.Line != w.line || g.At.Column != w.col || g.Sep != w.sep {
			t.Errorf("missing[%d] = %+v, want %+v", i, g, w)
		}
	}

	// A route with a response mapping, a block arm, and a declaration end
	// in `}`, and ask for nothing after it.
	braced := "adapter http api {\n    GET \"/a\" -> a() produces \"application/json\" {\n        Ok(x) -> 200 x\n    }\n}\n"
	if m := MissingSeparators(braced); len(m) != 0 {
		t.Errorf("braced: %v", m)
	}
}
