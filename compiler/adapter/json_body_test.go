package adapter

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
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
// and as a signed JSON body read raw (ADR 0077), checked, and decoded in
// fiig, with JsonError as the 422 body.
func TestCorpusEventInbox(t *testing.T) {
	if diags, err := checker.CheckDir(filepath.Join(findExamplesDir(t), "event-inbox"), nil); err != nil || len(diags) > 0 {
		t.Fatalf("checker: %v %+v", err, diags)
	}
	u, files := loadProgram(t, "event-inbox/models", "event-inbox/errors", "event-inbox/config", "event-inbox/rules",
		"event-inbox/activities", "event-inbox/conversions", "event-inbox/workflows", "event-inbox/adapters")
	// loadProgram doesn't check, and json.decode needs the type the
	// checker records on each call.
	checker.Check(files, nil)
	in := runtime.New(u, runtime.NewCapabilityRegistry())
	const secret = "It's a Secret to Everybody"
	inbox, _ := u.Lookup("Inbox")
	if problems := in.LoadConfig([]runtime.ConfigSource{{Decl: inbox.(*syntax.ConfigDecl), Universe: u}}, func(name string) (string, bool) {
		return secret, name == "INBOX_WEBHOOK_SECRET"
	}); len(problems) > 0 {
		t.Fatal(problems)
	}
	h, err := HTTPHandler(findAdapter(t, files, "inboxApi"), u, in)
	if err != nil {
		t.Fatal(err)
	}
	sign := func(body string) string {
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte(body))
		return "sha256=" + hex.EncodeToString(mac.Sum(nil))
	}
	// Spacing and 5000.50 are in the bytes signed, so re-encoding the
	// decoded envelope wouldn't reproduce the signature.
	limit := `{"id": "e5",  "@type": "credit.limit_changed", "data": {"account": "a3", "limit": 5000.50}}`
	cases := []struct {
		path, contentType, in string
		code                  int
		out                   string
		signature             string
	}{
		{"/events", "application/json", `{"id": "e1", "@type": "invoice.paid", "data": {"invoice": "in_9", "amount": 12.50, "paid_at": "2026-11-09", "extra": true}}`,
			200, `{"variant":"Paid","invoice":{"invoice":"in_9","amount":12.50,"paid_at":"2026-11-09"}}`, ""},
		{"/events", "application/json", `{"id": "e2", "@type": "credit.limit_changed", "data": {"account": "a3", "limit": 5000}}`,
			200, `{"variant":"LimitChanged","change":{"account":"a3","limit":5000}}`, ""},
		{"/events", "application/json", `{"id": "e3", "@type": "customer.created", "data": null}`,
			200, `{"variant":"Ignored","eventType":"customer.created"}`, ""},
		{"/events", "application/json", `{"id": "e4", "@type": "invoice.paid", "data": {"invoice": "in_9", "amount": "12.50", "paid_at": "x"}}`,
			422, `{"variant":"Mismatch","path":"amount","reason":"expected a number, got a string"}`, ""},
		// ADR 0078: a bytes field, sent wrapped as GitHub's contents API
		// sends it, or in the URL-safe alphabet, comes back padded standard.
		{"/events", "application/json", `{"id": "e7", "@type": "document.attached", "data": {"name": "logo.png", "content": "iVBORw0K\nGgr7/w==\n"}}`,
			200, `{"variant":"Attached","document":{"name":"logo.png","content":"iVBORw0KGgr7/w=="}}`, ""},
		{"/events", "application/json", `{"id": "e8", "@type": "document.attached", "data": {"name": "logo.png", "content": "iVBORw0KGgr7_w"}}`,
			200, `{"variant":"Attached","document":{"name":"logo.png","content":"iVBORw0KGgr7/w=="}}`, ""},
		{"/events", "application/json", `{"id": "e9", "@type": "document.attached", "data": {"name": "logo.png", "content": [137, 80]}}`,
			422, `{"variant":"Mismatch","path":"content","reason":"expected a base64 string, got an array"}`, ""},
		// ADR 0080: Stripe's snake_case status, by each variant's spelling,
		// sent back the same way; an unknown one names the spellings.
		{"/events", "application/json", `{"id": "e10", "@type": "customer.subscription.updated", "data": {"id": "sub_1", "status": "past_due", "object": "subscription"}}`,
			200, `{"variant":"SubscriptionChanged","subscription":{"id":"sub_1","status":"past_due"}}`, ""},
		{"/events", "application/json", `{"id": "e11", "@type": "customer.subscription.updated", "data": {"id": "sub_1", "status": "PastDue"}}`,
			422, `{"variant":"Mismatch","path":"status","reason":"\"PastDue\" is not a variant of SubscriptionStatus (incomplete, incomplete_expired, trialing, active, past_due, canceled, unpaid, paused)"}`, ""},
		{"/events/signed", "application/json", limit,
			200, `{"variant":"LimitChanged","change":{"account":"a3","limit":5000.50}}`, sign(limit)},
		{"/events/signed", "application/json", limit, 401, ``, sign(limit + " ")},
		{"/events/signed", "application/json", limit, 401, ``, "sha256=not-hex"},
		{"/events/signed", "application/json", limit, 401, ``, ""},
		// A forged junk body is forged first: 401, not a Syntax error.
		{"/events/signed", "application/json", `{not json`, 401, ``, sign(`{other`)},
		{"/events/signed", "application/json", `{"id": "e6", "id": "e7"}`,
			422, `{"variant":"Undecodable","failure":{"variant":"Syntax","offset":13,"reason":"duplicate name \"id\""}}`, sign(`{"id": "e6", "id": "e7"}`)},
	}
	for _, c := range cases {
		req := httptest.NewRequest("POST", c.path, strings.NewReader(c.in))
		req.Header.Set("Content-Type", c.contentType)
		if c.signature != "" {
			req.Header.Set("X-Hub-Signature-256", c.signature)
		}
		if code, _, body := serve(h, req); code != c.code || strings.TrimSpace(body) != c.out {
			t.Errorf("POST %s %s\n got %d %s\nwant %d %s", c.path, c.in, code, body, c.code, c.out)
		}
	}
}

// ADR 0077: a JSON route binding bytes gets the body as it arrived, with
// no parse and so no 400, but still the 415.
var rawHookProgram = []string{
	`workflow hookBytes(body: bytes) -> string {
    return body.toHex()
}`,
	`adapter http rawHookApi {
    POST "/hooks" -> hookBytes(body: bytes)
        consumes "application/json"
        produces "text/plain"
}`,
}

func TestAJsonRouteBindingBytesGetsTheBodyAsItArrived(t *testing.T) {
	h := handlerFor(t, rawHookProgram...)
	for _, in := range []string{`{"b": 1.50,  "a" : "\u00e9"}`, `{not json`} {
		req := httptest.NewRequest("POST", "/hooks", strings.NewReader(in))
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
		code, _, body := serve(h, req)
		if want := hex.EncodeToString([]byte(in)); code != 200 || body != want {
			t.Errorf("%s: got %d %s, want 200 %s", in, code, body, want)
		}
	}
	req := httptest.NewRequest("POST", "/hooks", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/octet-stream")
	if code, _, _ := serve(h, req); code != 415 {
		t.Errorf("wrong content type: got %d, want 415", code)
	}
}

func TestOpenAPIListsARawJsonBodyAsAnyJson(t *testing.T) {
	var files []*syntax.File
	var ad *syntax.AdapterDecl
	for _, src := range rawHookProgram {
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
	got, _ := json.Marshal(post["requestBody"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"])
	if want := `{"description":"any JSON value"}`; string(got) != want {
		t.Errorf("schema: got %s, want %s", got, want)
	}
}

// ADR 0078 §4: a whole bytes response on a JSON route is the JSON the
// program built, sent raw once checked; bytes inside a body are base64.
var bytesBodyProgram = []string{
	`model Report {
    title: string
    digest: bytes
}`,
	`workflow report(title: string) -> bytes {
    return json.encode(Report(title: title, digest: crypto.sha256(title.utf8())))
}`,
	`workflow broken() -> bytes {
    return "not json".utf8()
}`,
	`workflow nested(title: string) -> Report {
    return Report(title: title, digest: "hi".utf8())
}`,
	`adapter http reportApi {
    GET "/report/{title}" -> report(title: string)
        produces "application/json"
    GET "/broken" -> broken()
        produces "application/json"
    GET "/nested/{title}" -> nested(title: string)
        produces "application/json"
}`,
}

func TestAWholeBytesBodyOnAJsonRouteIsSentRaw(t *testing.T) {
	h := handlerFor(t, bytesBodyProgram...)
	code, _, body := serve(h, httptest.NewRequest("GET", "/report/q3", nil))
	if !strings.HasPrefix(body, `{"title":"q3","digest":"`) || code != 200 {
		t.Errorf("raw: got %d %s, want the report itself", code, body)
	}
	if code, _, body := serve(h, httptest.NewRequest("GET", "/broken", nil)); code != 500 || !strings.Contains(body, "the bytes body isn't JSON") {
		t.Errorf("not JSON: got %d %s, want 500", code, body)
	}
	if code, _, body := serve(h, httptest.NewRequest("GET", "/nested/q3", nil)); code != 200 || strings.TrimSpace(body) != `{"title":"q3","digest":"aGk="}` {
		t.Errorf("nested: got %d %s", code, body)
	}
}

func TestOpenAPIWritesBytesAsBase64InsideJson(t *testing.T) {
	var files []*syntax.File
	var ad *syntax.AdapterDecl
	for _, src := range bytesBodyProgram {
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
	schemaOf := func(path string) string {
		get := doc["paths"].(map[string]any)[path].(map[string]any)["get"].(map[string]any)
		got, _ := json.Marshal(get["responses"].(map[string]any)["200"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"])
		return string(got)
	}
	if got := schemaOf("/report/{title}"); got != `{"description":"any JSON value"}` {
		t.Errorf("whole bytes body: %s", got)
	}
	if got := schemaOf("/nested/{title}"); !strings.Contains(got, `"digest":{"format":"byte","type":"string"}`) {
		t.Errorf("nested bytes: %s", got)
	}
}
