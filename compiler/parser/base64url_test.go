package parser

import (
	"testing"

	"fiig-lang/compiler/syntax"
)

// ADR 0078 §6: `from json` takes a member name, `base64url`, or both in
// that order.
func TestFromJsonBase64URL(t *testing.T) {
	f := mustParse(t, `model Message {
    raw: bytes from json base64url;
    data: bytes from json "raw_data" base64url;
    named: bytes from json "n";
    plain: bytes;
}`)
	m := f.Decls[0].(*syntax.ModelDecl)
	want := []struct {
		json string
		url  bool
	}{{"", true}, {"raw_data", true}, {"n", false}, {"", false}}
	for i, w := range want {
		if got := m.Fields[i]; got.JSON != w.json || got.Base64URL != w.url {
			t.Errorf("field %d = %+v, want JSON %q base64url %v", i, got, w.json, w.url)
		}
	}
	e := mustParse(t, `enum Upload {
    Sent(body: bytes from json base64url),
}`)
	if v := e.Decls[0].(*syntax.EnumDecl).Variants[0]; !v.Fields[0].Base64URL {
		t.Fatalf("payload = %+v", v.Fields)
	}
	for _, src := range []string{
		"model M {\n    a: bytes from json\n}",
		"model M {\n    a: bytes from json base64\n}",
		"model M {\n    a: bytes from json base64url \"x\"\n}",
		"enum E {\n    A from json base64url\n}",
	} {
		if _, errs := Parse(src); len(errs) == 0 {
			t.Errorf("parsed %q", src)
		}
	}
}
