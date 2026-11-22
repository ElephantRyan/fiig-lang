package adapter

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"fiig-lang/compiler/parser"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// ADR 0078 §6: a Gmail-style `raw` field is sent as unpadded base64url
// and read in either alphabet; OpenAPI says so with format base64url.
var base64URLProgram = []string{
	`model Draft {
    id: string
    raw: bytes from json base64url
    parts: List<bytes> from json "chunks" base64url
    plain: bytes | None
}`,
	`workflow echoDraft(draft: Draft) -> Draft {
    return draft;
}`,
	`adapter http draftApi {
    POST "/drafts" -> echoDraft(draft: Draft)
        consumes "application/json"
        produces "application/json"
}`,
}

func TestABase64URLFieldRoundTripsThroughABody(t *testing.T) {
	h := handlerFor(t, base64URLProgram...)
	want := `{"id":"d1","raw":"iVBORw0KGgr7_w","chunks":["-w"],"plain":"iVBORw0KGgr7/w=="}`
	for _, in := range []string{
		`{"id": "d1", "raw": "iVBORw0KGgr7_w", "chunks": ["-w"], "plain": "iVBORw0KGgr7_w"}`,
		`{"id": "d1", "raw": "iVBORw0KGgr7/w==", "chunks": ["+w=="], "plain": "iVBORw0KGgr7/w=="}`,
	} {
		req := httptest.NewRequest("POST", "/drafts", strings.NewReader(in))
		req.Header.Set("Content-Type", "application/json")
		if code, _, body := serve(h, req); code != 200 || strings.TrimSpace(body) != want {
			t.Errorf("%s: got %d %s\nwant 200 %s", in, code, body, want)
		}
	}
	req := httptest.NewRequest("POST", "/drafts", strings.NewReader(`{"id": "d1", "raw": "iVBO+w0K_Ggo", "chunks": [], "plain": null}`))
	req.Header.Set("Content-Type", "application/json")
	if code, _, body := serve(h, req); code != 400 || !strings.Contains(body, "raw: base64 mixes the standard alphabet") {
		t.Errorf("mixed alphabets: got %d %s", code, body)
	}
}

func TestOpenAPIWritesABase64URLFieldsFormat(t *testing.T) {
	var files []*syntax.File
	var ad *syntax.AdapterDecl
	for _, src := range base64URLProgram {
		f, errs := parser.Parse(src)
		if len(errs) > 0 {
			t.Fatalf("parse: %v", errs)
		}
		files = append(files, f)
		for _, d := range f.Decls {
			if a, ok := d.(*syntax.AdapterDecl); ok {
				ad = a
			}
		}
	}
	u, _ := types.BuildUniverse(files)
	doc, err := GenerateOpenAPI(ad, u)
	if err != nil {
		t.Fatal(err)
	}
	post := doc["paths"].(map[string]any)["/drafts"].(map[string]any)["post"].(map[string]any)
	got, _ := json.Marshal(post["requestBody"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"])
	for _, want := range []string{
		`"raw":{"format":"base64url","type":"string"}`,
		`"chunks":{"items":{"format":"base64url","type":"string"},"type":"array"}`,
		`"plain":{"format":"byte","nullable":true,"type":"string"}`,
	} {
		if !strings.Contains(string(got), want) {
			t.Errorf("schema %s\nlacks %s", got, want)
		}
	}
}
