package adapter

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"fiig-lang/compiler/checker"
	"fiig-lang/compiler/parser"
	"fiig-lang/compiler/runtime"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// ADR 0073: a request body and json.decode are one decoder, and a response
// body and json.encode one encoder.

var webhookProgram = []string{
	`model Hook {
    in: string
    contentType: string from json "content-type"
    data: JsonValue
    items: List<int>
}`,
	`workflow echoHook(hook: Hook) -> Hook {
    return hook
}`,
	`adapter http hookApi {
    POST "/hooks" -> echoHook(hook: Hook)
        consumes "application/json"
        produces "application/json"
}`,
}

func TestABodyUsesJsonNamesAndKeepsAJsonValue(t *testing.T) {
	h := handlerFor(t, webhookProgram...)
	in := `{"data": {"z": [1.50, {"k": null}], "a": "x"}, "items": [1, 2], "content-type": "text/csv", "in": "inbox"}`
	req := httptest.NewRequest("POST", "/hooks", strings.NewReader(in))
	req.Header.Set("Content-Type", "application/json")
	code, _, body := serve(h, req)
	want := `{"in":"inbox","content-type":"text/csv","data":{"z":[1.50,{"k":null}],"a":"x"},"items":[1,2]}`
	if code != 200 || strings.TrimSpace(body) != want {
		t.Errorf("got %d %s\nwant 200 %s", code, body, want)
	}

	req = httptest.NewRequest("POST", "/hooks", strings.NewReader(`{"data": 1, "items": [1, 2.5], "content-type": "a", "in": "b"}`))
	req.Header.Set("Content-Type", "application/json")
	if code, _, body := serve(h, req); code != 400 || !strings.Contains(body, `items[1]: 2.5 has a fractional part`) {
		t.Errorf("mismatch: got %d %s", code, body)
	}
	req = httptest.NewRequest("POST", "/hooks", strings.NewReader(`{"data": 1,}`))
	req.Header.Set("Content-Type", "application/json")
	if code, _, body := serve(h, req); code != 400 || !strings.Contains(body, `at byte 11: expected a member name, got '}'`) {
		t.Errorf("syntax: got %d %s", code, body)
	}
}

func TestOpenAPIListsJsonNames(t *testing.T) {
	var files []*syntax.File
	var ad *syntax.AdapterDecl
	for _, src := range webhookProgram {
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
	post := doc["paths"].(map[string]any)["/hooks"].(map[string]any)["post"].(map[string]any)
	schema := post["requestBody"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"]
	got, _ := json.Marshal(schema)
	want := `{"properties":{"content-type":{"type":"string"},"data":{"description":"any JSON value"},"in":{"type":"string"},"items":{"items":{"type":"integer"},"type":"array"}},"required":["content-type","data","in","items"],"type":"object"}`
	if string(got) != want {
		t.Errorf("schema:\n got %s\nwant %s", got, want)
	}
}

// TestCorpusEventInbox serves examples/event-inbox: an envelope whose
// JsonValue payload a rule decodes by its type, received as a JSON body
// and as raw bytes decoded in fiig, with JsonError as the 422 body.
func TestCorpusEventInbox(t *testing.T) {
	if diags, err := checker.CheckDir(filepath.Join(findExamplesDir(t), "event-inbox"), nil); err != nil || len(diags) > 0 {
		t.Fatalf("checker: %v %+v", err, diags)
	}
	u, files := loadProgram(t, "event-inbox/models", "event-inbox/rules", "event-inbox/workflows", "event-inbox/adapters")
	// loadProgram doesn't check, and json.decode needs the type the
	// checker records on each call.
	checker.Check(files, nil)
	h, err := HTTPHandler(findAdapter(t, files, "inboxApi"), u, runtime.New(u, runtime.NewCapabilityRegistry()))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		path, contentType, in string
		code                  int
		out                   string
	}{
		{"/events", "application/json", `{"id": "e1", "@type": "invoice.paid", "data": {"invoice": "in_9", "amount": 12.50, "paid_at": "2026-11-09", "extra": true}}`,
			200, `{"variant":"Paid","invoice":{"invoice":"in_9","amount":12.50,"paid_at":"2026-11-09"}}`},
		{"/events", "application/json", `{"id": "e2", "@type": "credit.limit_changed", "data": {"account": "a3", "limit": 5000}}`,
			200, `{"variant":"LimitChanged","change":{"account":"a3","limit":5000}}`},
		{"/events", "application/json", `{"id": "e3", "@type": "customer.created", "data": null}`,
			200, `{"variant":"Ignored","eventType":"customer.created"}`},
		{"/events", "application/json", `{"id": "e4", "@type": "invoice.paid", "data": {"invoice": "in_9", "amount": "12.50", "paid_at": "x"}}`,
			422, `{"variant":"Mismatch","path":"amount","reason":"expected a number, got a string"}`},
		{"/events/raw", "application/octet-stream", `{"id": "e5", "@type": "credit.limit_changed", "data": {"account": "a3", "limit": 5000}}`,
			200, `{"variant":"LimitChanged","change":{"account":"a3","limit":5000}}`},
		{"/events/raw", "application/octet-stream", `{"id": "e6", "id": "e7"}`,
			422, `{"variant":"Syntax","offset":13,"reason":"duplicate name \"id\""}`},
	}
	for _, c := range cases {
		req := httptest.NewRequest("POST", c.path, strings.NewReader(c.in))
		req.Header.Set("Content-Type", c.contentType)
		if code, _, body := serve(h, req); code != c.code || strings.TrimSpace(body) != c.out {
			t.Errorf("POST %s %s\n got %d %s\nwant %d %s", c.path, c.in, code, body, c.code, c.out)
		}
	}
}
