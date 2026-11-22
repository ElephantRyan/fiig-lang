package checker

import "testing"

// ADR 0081: J3, and matching a union of enums and something else.

var overlapEnums = []string{
	"enum OrderError {\n    NotFound(id: string),\n    OutOfStock(sku: string),\n}",
	"enum CustomerError {\n    NotFound(id: string),\n    Suspended,\n}",
	"enum RenamedError {\n    NotFound(id: string) from json \"customer_not_found\",\n    Suspended,\n}",
	"enum Shade {\n    Light,\n    Dark,\n}",
	"enum Tone {\n    Dark,\n    Muted,\n}",
	"enum Mood {\n    Calm from json \"Dark\",\n    Busy,\n}",
	"enum Outcome {\n    Ok(note: string),\n    Late,\n}",
	"model Order {\n    id: string;\n}",
}

func withOverlapEnums(srcs ...string) []string {
	return append(append([]string{}, overlapEnums...), srcs...)
}

// §1, §2: every overlap J3 knows, in each place J1 looks.
func TestJ3RefusesUnionsWhoseMembersReadTheSameJSON(t *testing.T) {
	cases := []struct{ src, fragment string }{
		// Two enums with payloads sharing a variant, as a response's Err side.
		{"workflow lookup(id: string) -> Result<Order, OrderError | CustomerError> {\n    return Err(OrderError.NotFound(id: id));\n}",
			`OrderError and CustomerError both read {"variant": "NotFound", ...}`},
		// Two payload-free enums sharing a text, through json.decode.
		{"rule readShade(text: string) -> Result<Shade | Tone, JsonError> {\n    picked: Shade | Tone = json.decode(text)?;\n    return Ok(picked);\n}",
			`Shade and Tone both read "Dark"`},
		// A spelling that is the other's variant name.
		{"rule readMood(text: string) -> Result<Shade | Mood, JsonError> {\n    picked: Shade | Mood = json.decode(text)?;\n    return Ok(picked);\n}",
			`Shade and Mood both read "Dark"`},
		// A variant spelled as Result's own tag, in json.encode.
		{"rule send(r: Result<string, string> | Outcome) -> bytes {\n    return json.encode(r);\n}",
			`both read {"variant": "Ok", ...}`},
	}
	route := `adapter http a {
    GET "/lookup/{id}" -> lookup(id: string)
        produces "application/json";
}`
	for i, c := range cases {
		srcs := withOverlapEnums(c.src)
		if i == 0 {
			srcs = append(srcs, route)
		}
		wantDiag(t, checkSrc(t, srcs...), "J3", c.fragment)
	}
	// The same union in a response arm's body.
	mapped := `adapter http a {
    GET "/lookup/{id}" -> lookup(id: string)
        produces "application/json"
    {
        Ok(order) -> 200 order,
        Err(error) -> 404 error,
    }
}`
	wantDiag(t, checkSrc(t, withOverlapEnums(cases[0].src, mapped)...), "J3", "this response's body")
}

// §2, §3: a spelling removes the overlap, Enum | string isn't one, and a
// union outside JSON isn't checked.
func TestJ3AllowsWhatAReaderCanTellApart(t *testing.T) {
	for _, src := range []string{
		"workflow lookup(id: string) -> Result<Order, OrderError | RenamedError> {\n    return Err(OrderError.NotFound(id: id));\n}",
		"rule readStatus(text: string) -> Result<Shade | string, JsonError> {\n    picked: Shade | string = json.decode(text)?;\n    return Ok(picked);\n}",
		"rule local(dark: bool) -> int {\n    picked: Shade | Tone = Shade.Light;\n    if (dark) {\n        picked = Tone.Dark;\n    }\n    return 1;\n}",
	} {
		if diags := checkSrc(t, withOverlapEnums(src)...); len(diags) != 0 {
			t.Errorf("%s\nunexpected diagnostics: %+v", src, diags)
		}
	}
}

// §3: a match over Enum | string names every variant and the text, and the
// bare name that takes the text is a string.
func TestMatchOverEnumAndString(t *testing.T) {
	full := "rule describe(s: Shade | string) -> string {\n    return match (s) {\n        text -> {\n            return text.trim();\n        },\n        Shade.Light -> {\n            return \"light\";\n        },\n        Shade.Dark -> {\n            return \"dark\";\n        },\n    }\n}"
	if diags := checkSrc(t, withOverlapEnums(full)...); len(diags) != 0 {
		t.Errorf("full match: unexpected diagnostics %+v", diags)
	}
	noText := "rule describe(s: Shade | string) -> string {\n    return match (s) {\n        Shade.Light -> {\n            return \"light\";\n        },\n        Shade.Dark -> {\n            return \"dark\";\n        },\n    }\n}"
	wantDiag(t, checkSrc(t, withOverlapEnums(noText)...), "A9", "missing case(s): string (a bare name binds it)")
	noDark := "rule describe(s: Shade | string) -> string {\n    return match (s) {\n        Shade.Light -> {\n            return \"light\";\n        },\n        text -> {\n            return text;\n        },\n    }\n}"
	wantDiag(t, checkSrc(t, withOverlapEnums(noDark)...), "A9", "missing case(s): Dark")
	// Two enums and None: a bare name is every variant, not None.
	withNone := "rule describe(s: Shade | Outcome | None) -> string {\n    return match (s) {\n        None -> {\n            return \"none\";\n        },\n        other -> {\n            return \"some\";\n        },\n    }\n}"
	if diags := checkSrc(t, withOverlapEnums(withNone)...); len(diags) != 0 {
		t.Errorf("enums and None: unexpected diagnostics %+v", diags)
	}
}
