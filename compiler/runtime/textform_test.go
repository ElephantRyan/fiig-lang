package runtime

import (
	"strings"
	"testing"

	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// ADR 0080 §2: a variant's spelling is its text form in JSON, both ways,
// and in config.

var spelledSources = []string{
	`enum Status {
    Active from json "active"
    PastDue from json "past_due"
    Legacy
}`,
	`enum Outcome {
    Paid(receipt: string) from json "paid"
    Refused(reason: string)
}`,
}

func spelledType(t *testing.T, u *types.Universe, name string) types.Type {
	t.Helper()
	d, ok := u.Lookup(name)
	if !ok {
		t.Fatalf("no %s", name)
	}
	return types.Named{Name: name, Decl: d}
}

func TestSpellingsInJSON(t *testing.T) {
	u := universeFromSource(t, spelledSources...)
	status, outcome := spelledType(t, u, "Status"), spelledType(t, u, "Outcome")

	decode := func(text string, into types.Type) (Value, error) {
		raw, err := ReadJSON([]byte(text))
		if err != nil {
			t.Fatal(err)
		}
		return DecodeJSON(raw, into, u)
	}
	for text, want := range map[string]string{`"past_due"`: "PastDue", `"active"`: "Active", `"Legacy"`: "Legacy"} {
		v, err := decode(text, status)
		if err != nil || v.(EnumValue).Variant != want {
			t.Errorf("decode %s: got %v %v, want %s", text, v, err, want)
		}
		if out, err := EncodeBodyJSON(v); err != nil || string(out) != text {
			t.Errorf("encode %s: got %s %v", want, out, err)
		}
	}
	// The variant's own name is no longer its text once it has a spelling.
	if _, err := decode(`"PastDue"`, status); err == nil || !strings.Contains(err.Error(), `"PastDue" is not a variant of Status (active, past_due, Legacy)`) {
		t.Errorf("PastDue: got %v", err)
	}

	v, err := decode(`{"variant": "paid", "receipt": "r_1"}`, outcome)
	if err != nil || v.(EnumValue).Variant != "Paid" {
		t.Fatalf("decode paid: got %v %v", v, err)
	}
	if out, _ := EncodeBodyJSON(v); string(out) != `{"variant":"paid","receipt":"r_1"}` {
		t.Errorf("encode Paid: got %s", out)
	}
	if out, _ := EncodeBodyJSON(EnumValue{TypeName: "Outcome", Variant: "Refused", Fields: []Value{Str("card")}, Decl: v.(EnumValue).Decl}); string(out) != `{"variant":"Refused","reason":"card"}` {
		t.Errorf("encode Refused: got %s", out)
	}
	if _, err := decode(`{"variant": "Paid", "receipt": "r_1"}`, outcome); err == nil || !strings.Contains(err.Error(), "(paid, Refused)") {
		t.Errorf("Paid by name: got %v", err)
	}
}

func TestSpellingsInConfig(t *testing.T) {
	u := universeFromSource(t, append([]string{`config Billing {
    status: Status from env "STATUS"
}`}, spelledSources...)...)
	d, _ := u.Lookup("Billing")
	load := func(value string) (*Interp, []string) {
		in := New(u, NewCapabilityRegistry())
		return in, in.LoadConfig([]ConfigSource{{Decl: d.(*syntax.ConfigDecl), Universe: u}}, func(string) (string, bool) { return value, true })
	}
	in, problems := load("past_due")
	if len(problems) > 0 {
		t.Fatalf("past_due: %v", problems)
	}
	if got := in.Config[d.(*syntax.ConfigDecl)].Fields["status"].(EnumValue).Variant; got != "PastDue" {
		t.Errorf("past_due read as %s", got)
	}
	if _, problems := load("PastDue"); len(problems) != 1 || !strings.Contains(problems[0], "(active, past_due, Legacy)") {
		t.Errorf("PastDue: got %v", problems)
	}
}
