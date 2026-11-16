package adapter

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"fiig-lang/compiler/parser"
	"fiig-lang/compiler/runtime"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// ADR 0069 §1, §2, and §5: header and cookie arguments, the 415, and
// bodies of types other than JSON.

func handlerFor(t *testing.T, srcs ...string) http.Handler {
	t.Helper()
	return handlerWithLimits(t, DefaultLimits(), srcs...)
}

func handlerWithLimits(t *testing.T, l Limits, srcs ...string) http.Handler {
	t.Helper()
	var files []*syntax.File
	var ad *syntax.AdapterDecl
	for _, src := range srcs {
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
	u, conflicts := types.BuildUniverse(files)
	if len(conflicts) > 0 {
		t.Fatalf("conflicts: %v", conflicts)
	}
	h, err := HTTPHandlerWithLimits(ad, u, runtime.New(u, runtime.NewCapabilityRegistry()), l)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func serve(h http.Handler, req *http.Request) (int, http.Header, string) {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	body, _ := io.ReadAll(rec.Result().Body)
	return rec.Code, rec.Result().Header, string(body)
}

var sourcesProgram = []string{
	`workflow whoAmI(token: string, requestId: string | None, session: string | None, page: int | None) -> string {
    return "${token}|${requestId}|${session}|${page}"
}`,
	`workflow echoCsv(csv: string) -> Result<string, string> {
    return Ok("rows: ${csv}")
}`,
	`workflow echoBytes(data: bytes) -> bytes {
    return data
}`,
	`workflow nothing() -> Result<None, string> {
    return Ok(None)
}`,
	`adapter http api {
    GET "/me" -> whoAmI(
        token: string from header "Authorization",
        requestId: string | None from header "X-Request-Id",
        session: string | None from cookie "sid",
        page: int | None from header "X-Page",
    )
        produces "application/json"
    POST "/csv" -> echoCsv(csv: string)
        consumes "text/csv"
        produces "text/csv"
    POST "/bin" -> echoBytes(data: bytes)
        consumes "application/octet-stream"
        produces "application/octet-stream"
    GET "/nothing" -> nothing()
        produces "application/json"
}`,
}

func TestHeaderAndCookieArguments(t *testing.T) {
	h := handlerFor(t, sourcesProgram...)

	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	req.Header.Set("authorization", "Bearer abc") // header names match case-insensitively
	req.Header.Add("X-Request-Id", "r1")
	req.Header.Add("X-Request-Id", "r2") // repeated: joined by ", "
	req.Header.Set("X-Page", "3")
	req.AddCookie(&http.Cookie{Name: "sid", Value: "first"})
	req.AddCookie(&http.Cookie{Name: "sid", Value: "second"}) // repeated: the first wins
	req.AddCookie(&http.Cookie{Name: "SID", Value: "other"})  // cookie names match exactly
	code, hdr, body := serve(h, req)
	if code != 200 || strings.TrimSpace(body) != `"Bearer abc|r1, r2|first|3"` || hdr.Get("Content-Type") != "application/json" {
		t.Fatalf("got %d %q %q", code, hdr.Get("Content-Type"), body)
	}

	req = httptest.NewRequest(http.MethodGet, "/me", nil)
	req.Header.Set("Authorization", "t")
	code, _, body = serve(h, req)
	if code != 200 || strings.TrimSpace(body) != `"t|None|None|None"` {
		t.Fatalf("optional values missing: got %d %q", code, body)
	}

	code, _, body = serve(h, httptest.NewRequest(http.MethodGet, "/me", nil))
	if code != 400 || !strings.Contains(body, `missing header \"Authorization\"`) {
		t.Fatalf("missing required header: got %d %q", code, body)
	}

	req = httptest.NewRequest(http.MethodGet, "/me", nil)
	req.Header.Set("Authorization", "t")
	req.Header.Set("X-Page", "three")
	code, _, body = serve(h, req)
	if code != 400 || !strings.Contains(body, `header \"X-Page\": expected an int`) {
		t.Fatalf("undecodable header: got %d %q", code, body)
	}
}

func TestRawBodiesAndTheirContentTypes(t *testing.T) {
	h := handlerFor(t, sourcesProgram...)

	req := httptest.NewRequest(http.MethodPost, "/csv", strings.NewReader("a,b\n1,2"))
	req.Header.Set("Content-Type", "text/csv; charset=utf-8") // parameters aren't compared
	code, hdr, body := serve(h, req)
	if code != 200 || body != "rows: a,b\n1,2" || hdr.Get("Content-Type") != "text/csv; charset=utf-8" {
		t.Fatalf("got %d %q %q", code, hdr.Get("Content-Type"), body)
	}

	req = httptest.NewRequest(http.MethodPost, "/csv", strings.NewReader(`{"a": 1}`))
	req.Header.Set("Content-Type", "application/json")
	code, hdr, body = serve(h, req)
	if code != 415 || hdr.Get("Content-Type") != "application/json" || !strings.Contains(body, `this route consumes \"text/csv\"`) {
		t.Fatalf("wrong content type: got %d %q %q", code, hdr.Get("Content-Type"), body)
	}

	req = httptest.NewRequest(http.MethodPost, "/csv", strings.NewReader("\xff"))
	req.Header.Set("Content-Type", "text/csv")
	if code, _, body = serve(h, req); code != 400 || !strings.Contains(body, "isn't UTF-8 text") {
		t.Fatalf("invalid UTF-8: got %d %q", code, body)
	}

	req = httptest.NewRequest(http.MethodPost, "/bin", strings.NewReader("\x00\xff"))
	req.Header.Set("Content-Type", "application/octet-stream")
	code, hdr, body = serve(h, req)
	if code != 200 || body != "\x00\xff" || hdr.Get("Content-Type") != "application/octet-stream" {
		t.Fatalf("bytes: got %d %q %q", code, hdr.Get("Content-Type"), body)
	}
}

func TestNoBodyHasNoContentType(t *testing.T) {
	h := handlerFor(t, sourcesProgram...)
	code, hdr, body := serve(h, httptest.NewRequest(http.MethodGet, "/nothing", nil))
	if code != 200 || body != "" || hdr.Get("Content-Type") != "" {
		t.Fatalf("got %d %q %q", code, hdr.Get("Content-Type"), body)
	}
}
