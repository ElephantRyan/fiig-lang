package format

import "testing"

// A comment between a body's last item and its "}" stays inside the body;
// fmt used to move it after the "}", so an empty gateway kept its comment
// but lost what it was a comment on. One after the "}" stays outside.
func TestFormatKeepsACommentBeforeTheClosingBrace(t *testing.T) {
	cases := []struct{ name, src string }{
		{"the only thing in a body", "gateway charge(order: string) -> Result<int, string> requires HttpClient {\n" +
			"    // POST to the route\n" +
			"}\n"},
		{"after a body's last statement", "activity a() -> int {\n" +
			"    x = 1;\n" +
			"    return x;\n" +
			"    // nothing after the return\n" +
			"}\n"},
		{"after a then block's last statement", "activity a(b: bool) -> int {\n" +
			"    if (b) {\n" +
			"        return 1;\n" +
			"        // the then side\n" +
			"    } else {\n" +
			"        return 2;\n" +
			"        // the else side\n" +
			"    }\n" +
			"}\n"},
		{"after a match's last arm", "conversion describe(code: int) -> string {\n" +
			"    return match (code) {\n" +
			"        200 -> \"ok\",\n" +
			"        _ -> \"other\"\n" +
			"        // the last arm\n" +
			"    }\n" +
			"}\n"},
		{"after a model's last field", "model Row {\n" +
			"    score: int;\n" +
			"    // more fields later\n" +
			"}\n"},
		{"after an adapter's last route", "adapter http api {\n" +
			"    GET \"/x\" -> x() produces \"application/json\";\n" +
			"    // more routes later\n" +
			"}\n"},
		{"after a route's last arm", "adapter http api {\n" +
			"    GET \"/x\" -> x() produces \"application/json\" {\n" +
			"        Ok(n) -> 200 n\n" +
			"        // more arms later\n" +
			"    }\n" +
			"}\n"},
		{"after a capability's last gateway", "capability Shelf {\n" +
			"    gateway book(id: string) -> Result<int, string> requires HttpClient {\n" +
			"        return Ok(1);\n" +
			"    }\n" +
			"\n" +
			"    // more gateways later\n" +
			"}\n"},
		{"after a serve block's last adapter", "application App {\n" +
			"    serve web {\n" +
			"        api.web;\n" +
			"        // more adapters later\n" +
			"    }\n" +
			"\n" +
			"    // more roles later\n" +
			"}\n"},
		{"after the brace, outside", "model Row {\n" +
			"    score: int;\n" +
			"}\n" +
			"\n" +
			"// after the model\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if out := mustFormat(t, c.src); out != c.src {
				t.Fatalf("got:\n%s\nwant:\n%s", out, c.src)
			}
		})
	}
}

// A comment trailing a "} else if (...) {" or "} else {" line stays on it;
// fmt used to move it down into the branch's body.
func TestFormatKeepsACommentOnAnElseLine(t *testing.T) {
	src := "activity a(x: int) -> int {\n" +
		"    if (x == 1) {\n" +
		"        return 1;\n" +
		"    } else if (x == 2) { // the second case\n" +
		"        return 2;\n" +
		"    } else { // the rest\n" +
		"        return 3;\n" +
		"    }\n" +
		"}\n"
	if out := mustFormat(t, src); out != src {
		t.Fatalf("got:\n%s\nwant:\n%s", out, src)
	}
}

// A blank line between two comments stays, at the top of a file and in a
// body; several collapse to one.
func TestFormatKeepsABlankLineBetweenComments(t *testing.T) {
	src := "// first paragraph\n" +
		"\n" +
		"// second paragraph\n" +
		"activity a() -> int {\n" +
		"    // step one\n" +
		"\n" +
		"    // step two\n" +
		"    return 1;\n" +
		"}\n"
	if out := mustFormat(t, src); out != src {
		t.Fatalf("got:\n%s\nwant:\n%s", out, src)
	}
	if out := mustFormat(t, "// a\n\n\n\n// b\n"); out != "// a\n\n// b\n" {
		t.Fatalf("got %q, want the blank lines collapsed to one", out)
	}
}
