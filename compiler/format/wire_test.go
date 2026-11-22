package format

import (
	"strings"
	"testing"

	"fiig-lang/compiler/parser"
)

// ADR 0143 §7, with ADR 0142 §5's first step: `config`, `principal` and
// `or` between sources still parse, and the formatter writes `wire` and
// `??`.
func TestFormatWritesWire(t *testing.T) {
	cases := []struct{ old, want string }{
		{"config Abs {\n    url: string | None from env \"ABS_URL\" or setting \"abs.url\"\n}\n",
			"wire Abs {\n    url: string | None from env \"ABS_URL\" ?? setting \"abs.url\"\n}\n"},
		{"principal me: Identity from cookie \"sid\" by signedIn else refused\n",
			"wire me: Identity from cookie \"sid\" by signedIn else refused\n"},
		{"adapter http api {\n    principal sessions.me\n\n    GET \"/x\" -> x(me) {\n        _ -> 200\n    }\n}\n",
			"adapter http api {\n    wire sessions.me\n\n    GET \"/x\" -> x(me) {\n        _ -> 200\n    }\n}\n"},
	}
	for _, c := range cases {
		if got := mustFormat(t, c.old); got != c.want {
			t.Errorf("formatting\n%s\ngot\n%s\nwant\n%s", c.old, got, c.want)
		}
		if again := mustFormat(t, c.want); again != c.want {
			t.Errorf("not stable:\n%s", again)
		}
	}
}

// ADR 0143 §3: a request source is a single wire's, and a process or run
// source a grouped wire's field (U10).
func TestASourceInTheWrongShapeIsU10(t *testing.T) {
	for _, src := range []string{
		"wire Abs {\n    me: Identity from cookie \"sid\"\n}",
		"wire url: string from env \"ABS_URL\" by x else y",
	} {
		_, errs := parser.Parse(src)
		if len(errs) != 1 || !strings.Contains(errs[0].Msg, "U10") {
			t.Errorf("%s: errs = %v, want one U10", src, errs)
		}
	}
}

// `wire` is contextual: a name everywhere a declaration doesn't start.
func TestWireIsStillAName(t *testing.T) {
	src := "conversion wire(text: string) -> bytes {\n    return text.toBytes();\n}\n"
	if _, errs := parser.Parse(src); len(errs) > 0 {
		t.Fatalf("errs = %v", errs)
	}
}
