package format

import "testing"

// ADR 0145: `fiig fmt` ends every item with `;` and follows every
// alternative with `,`, a block arm after its `}`, and a second format
// changes nothing.
func TestFormatWritesItemAndAlternativeSeparators(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"model and import",
			"use acme.payments\n\nmodel Customer {\n    id: string\n    payments.Audit\n}\n",
			"use acme.payments;\n\nmodel Customer {\n    id: string;\n    payments.Audit;\n}\n"},
		{"enum",
			"enum Region {\n    US\n    EU(code: string)\n}\n",
			"enum Region {\n    US,\n    EU(code: string)\n}\n"},
		{"interface",
			"interface Payments {\n    charge(amount: int) -> Result<string, string>\n}\n",
			"interface Payments {\n    charge(amount: int) -> Result<string, string>;\n}\n"},
		{"adapter",
			"adapter http api {\n    wire sessions.me\n\n    GET \"/a\" -> a() produces \"application/json\"\n    GET \"/b\" -> b() produces \"application/json\" {\n        Ok(x) -> 200 x\n        Err(_) -> 404\n    }\n}\n",
			"adapter http api {\n    wire sessions.me;\n\n    GET \"/a\" -> a() produces \"application/json\";\n    GET \"/b\" -> b() produces \"application/json\" {\n        Ok(x) -> 200 x,\n        Err(_) -> 404\n    }\n}\n"},
		{"match arms",
			"rule r(n: int | None) -> int {\n    return match (n) {\n        None -> 0\n        given -> {\n            twice = given + given;\n            return twice;\n        }\n    }\n}\n",
			"rule r(n: int | None) -> int {\n    return match (n) {\n        None -> 0,\n        given -> {\n            twice = given + given;\n            return twice;\n        }\n    }\n}\n"},
		{"application",
			"application App {\n    serve {\n        ratings.api\n    }\n}\n",
			"application App {\n    serve {\n        ratings.api;\n    }\n}\n"},
	}
	for _, c := range cases {
		got, errs := Format(c.src)
		if len(errs) > 0 {
			t.Fatalf("%s: format errors: %v", c.name, errs)
		}
		if got != c.want {
			t.Errorf("%s: got:\n%s\nwant:\n%s", c.name, got, c.want)
		}
		if again, _ := Format(got); again != got {
			t.Errorf("%s: not idempotent:\n%s", c.name, again)
		}
	}
}
