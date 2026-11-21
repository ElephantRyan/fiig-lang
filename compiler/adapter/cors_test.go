package adapter

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"fiig-lang/compiler/parser"
	"fiig-lang/compiler/runtime"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// ADR 0070: the CORS policy a deployment puts in front of an http adapter.

var corsProgram = []string{
	`model Item {
    id: string
}`,
	`workflow getItem(id: string) -> Item {
    return Item(id: id)
}`,
	`workflow putItem(id: string, item: Item, token: string) -> Item {
    return item
}`,
	`workflow deleteItem(key: string) -> Result<None, string> {
    return Err(key)
}`,
	`workflow me(session: string | None) -> string {
    return "${session}"
}`,
	`workflow boom(n: int) -> int {
    return 10 / (n - n)
}`,
	`conversion created(item: Item) -> HttpResponse<Item> {
    return HttpResponse(status: 201, body: item, headers: ["Location": "/items/${item.id}", "Vary": "Accept-Language"])
}`,
	`conversion refused(reason: string) -> HttpResponse<None> {
    return HttpResponse(status: 401, headers: ["WWW-Authenticate": "Bearer"])
}`,
	`adapter http api {
    GET "/items/{id}" -> getItem(id: string)
        produces "application/json"
    PUT "/items/{id}" -> putItem(id: string, item: Item, token: string from header "Authorization")
        consumes "application/json"
        produces "application/json"
    {
        item -> created(item)
    }
    DELETE "/items/{key}" -> deleteItem(key: string)
        produces "application/json"
    {
        Ok(_) -> 204
        Err(reason) -> refused(reason)
    }
    GET "/boom/{n}" -> boom(n: int)
        produces "application/json"
    GET "/me" -> me(session: string | None from cookie "sid")
        produces "application/json"
}`,
}

func corsHandlerFor(t *testing.T, origins ...string) (http.Handler, error) {
	t.Helper()
	var files []*syntax.File
	var ad *syntax.AdapterDecl
	for _, src := range corsProgram {
		f, errs := parser.Parse(src)
		if len(errs) != 0 {
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
	h, err := HTTPHandler(ad, u, runtime.New(u, runtime.NewCapabilityRegistry()))
	if err != nil {
		t.Fatal(err)
	}
	policy, err := ParseOrigins(origins)
	if err != nil {
		t.Fatal(err)
	}
	return WithCORS(h, ad, policy)
}

func mustCORS(t *testing.T, origins ...string) http.Handler {
	t.Helper()
	h, err := corsHandlerFor(t, origins...)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func preflight(path, origin, method, headers string) *http.Request {
	r := httptest.NewRequest(http.MethodOptions, path, nil)
	r.Header.Set("Origin", origin)
	r.Header.Set("Access-Control-Request-Method", method)
	if headers != "" {
		r.Header.Set("Access-Control-Request-Headers", headers)
	}
	return r
}

func TestParseOrigins(t *testing.T) {
	if c, err := ParseOrigins(nil); c != nil || err != nil {
		t.Fatalf("no origins: got %v, %v; want CORS off", c, err)
	}
	if c, err := ParseOrigins([]string{"", " "}); c != nil || err != nil {
		t.Fatalf("empty origins: got %v, %v; want CORS off", c, err)
	}
	good := []string{"https://app.example.com", "http://localhost:5173", "HTTPS://App.Example.com", "http://[::1]:3000"}
	if _, err := ParseOrigins(good); err != nil {
		t.Fatalf("%v: %v", good, err)
	}
	bad := map[string]string{
		"null":                        "sandboxed iframes",
		"https://app.example.com/":    "isn't an origin",
		"https://app.example.com/x":   "isn't an origin",
		"https://app.example.com?":    "isn't an origin",
		"app.example.com":             "isn't an origin",
		"ftp://app.example.com":       "isn't an origin",
		"https://u@app.example.com":   "isn't an origin",
		"https://app.example.com:443": "default port",
		"http://localhost:80":         "default port",
	}
	for origin, want := range bad {
		if _, err := ParseOrigins([]string{origin}); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: got %v, want an error containing %q", origin, err, want)
		}
	}
	if _, err := ParseOrigins([]string{"*", "https://a.example"}); err == nil || !strings.Contains(err.Error(), "only one") {
		t.Errorf("* with another origin: got %v", err)
	}
}

// §9 as amended 2026-11-21: `*` may be the whole first label of a host,
// and nowhere else.
func TestParseSubdomainPatterns(t *testing.T) {
	good := []string{"https://*.example.com", "http://*.localhost.test:5173", "HTTPS://*.Tenants.Example.COM", "https://*.my-app.example.co"}
	c, err := ParseOrigins(append(good, "https://example.com"))
	if err != nil {
		t.Fatalf("%v: %v", good, err)
	}
	if got := strings.Join(c.Origins(), " "); got != "http://*.localhost.test:5173 https://*.example.com https://*.my-app.example.co https://*.tenants.example.com https://example.com" {
		t.Errorf("Origins() = %q", got)
	}
	bad := map[string]string{
		"https://*":                 "wildcard where none is allowed",
		"https://*.":                "isn't an origin pattern",
		"*.example.com":             "wildcard where none is allowed",
		"https://a.*.example.com":   "wildcard where none is allowed",
		"https://*example.com":      "wildcard where none is allowed",
		"https://**.example.com":    "wildcard where none is allowed",
		"https://*.*.example.com":   "wildcard where none is allowed",
		"https://app.example.*":     "wildcard where none is allowed",
		"https://example.com:*":     "wildcard where none is allowed",
		"*://*.example.com":         "wildcard where none is allowed",
		"ftp://*.example.com":       "wildcard where none is allowed",
		"https://*.com":             "at least two labels",
		"http://*.localhost:3000":   "at least two labels",
		"https://*.example.com/":    "isn't an origin pattern",
		"https://*.example.com/x":   "isn't an origin pattern",
		"https://*.exa_mple.com":    "isn't an origin pattern",
		"https://*.-example.com":    "isn't an origin pattern",
		"https://*.example..com":    "isn't an origin pattern",
		"https://*.example.com:443": "default port",
		"http://*.example.com:80":   "default port",
		"https://*.example.com:":    "port that isn't a number",
		"https://*.example.com:x1":  "port that isn't a number",
		"https://*.example.com:080": "port that isn't a number",
		"https://*.10.0.0.1":        "IP address",
	}
	for origin, want := range bad {
		if _, err := ParseOrigins([]string{origin}); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: got %v, want an error containing %q", origin, err, want)
		}
	}
}

// A pattern matches any subdomain depth with the same scheme and port, and
// never the apex, a look-alike suffix, or another scheme or port; a match
// echoes the origin as sent, with credentials and Vary: Origin, as an exact
// entry does.
func TestSubdomainPatternsMatch(t *testing.T) {
	h := mustCORS(t, "https://*.example.com", "http://*.dev.example.com:5173")
	allowed := []string{
		"https://acme.example.com",
		"https://ACME.Example.com",
		"https://eu.acme.example.com",
		"https://a-1.b.c.example.com",
		"http://acme.dev.example.com:5173",
	}
	for _, origin := range allowed {
		code, hdr, _ := serve(h, preflight("/items/7", origin, "PUT", ""))
		if code != 204 || hdr.Get("Access-Control-Allow-Origin") != origin || hdr.Get("Access-Control-Allow-Credentials") != "true" || !strings.Contains(strings.Join(hdr.Values("Vary"), ", "), "Origin") {
			t.Errorf("preflight from %q: %d %v", origin, code, hdr)
		}
		r := httptest.NewRequest(http.MethodGet, "/items/7", nil)
		r.Header.Set("Origin", origin)
		code, hdr, _ = serve(h, r)
		if code != 200 || hdr.Get("Access-Control-Allow-Origin") != origin || hdr.Get("Vary") != "Origin" {
			t.Errorf("GET from %q: %d %v", origin, code, hdr)
		}
	}
	refused := []string{
		"https://example.com",                    // the apex
		"https://evilexample.com",                // a suffix, not a subdomain
		"https://acme.example.com.evil.net",      // the domain inside another
		"http://acme.example.com",                // another scheme
		"https://acme.example.com:8443",          // another port
		"http://acme.dev.example.com",            // the pattern's port missing
		"http://acme.dev.example.com:5174",       // another port
		"https://.example.com",                   // an empty label
		"https://a..example.com",                 // an empty label
		"https://a_b.example.com",                // not a host label
		"https://user@acme.example.com",          // not an origin
		"https://acme.example.com/x.example.com", // a path
		"null",
	}
	for _, origin := range refused {
		code, hdr, _ := serve(h, preflight("/items/7", origin, "PUT", ""))
		if code != 403 || hdr.Get("Access-Control-Allow-Origin") != "" {
			t.Errorf("preflight from %q: got %d %v, want a bare 403", origin, code, hdr)
		}
	}
}

func TestStarRefusesAnAdapterThatReadsACookie(t *testing.T) {
	_, err := corsHandlerFor(t, "*")
	if err == nil || !strings.Contains(err.Error(), `route GET "/me" reads cookie "sid"`) {
		t.Fatalf("got %v, want the cookie route named", err)
	}
}

func TestPreflightDerivesMethodsAndEchoesHeaders(t *testing.T) {
	h := mustCORS(t, "https://app.example.com")
	code, hdr, _ := serve(h, preflight("/items/7", "https://app.example.com", "PUT", "content-type, authorization, traceparent"))
	if code != http.StatusNoContent {
		t.Fatalf("code = %d", code)
	}
	want := map[string]string{
		"Access-Control-Allow-Origin":      "https://app.example.com",
		"Access-Control-Allow-Credentials": "true",
		// Both placeholder spellings of one path are one path.
		"Access-Control-Allow-Methods": "GET, PUT, DELETE",
		"Access-Control-Allow-Headers": "content-type, authorization, traceparent",
		"Access-Control-Max-Age":       "1800",
	}
	for k, v := range want {
		if hdr.Get(k) != v {
			t.Errorf("%s = %q, want %q", k, hdr.Get(k), v)
		}
	}
	if vary := strings.Join(hdr.Values("Vary"), ", "); !strings.Contains(vary, "Origin") || !strings.Contains(vary, "Access-Control-Request-Method") || !strings.Contains(vary, "Access-Control-Request-Headers") {
		t.Errorf("Vary = %q", vary)
	}
	if code, hdr, _ := serve(h, preflight("/me", "https://app.example.com", "GET", "")); code != 204 || hdr.Get("Access-Control-Allow-Methods") != "GET" {
		t.Errorf("/me: %d %v", code, hdr)
	}
}

func TestPreflightRefusals(t *testing.T) {
	h := mustCORS(t, "https://app.example.com")
	cases := []struct {
		name string
		req  *http.Request
		code int
	}{
		{"unlisted origin", preflight("/items/7", "https://evil.example.com", "PUT", ""), 403},
		{"suffix of a listed origin", preflight("/items/7", "https://evil-app.example.com", "PUT", ""), 403},
		{"no route has the method", preflight("/me", "https://app.example.com", "DELETE", ""), 403},
		{"header name isn't a token", preflight("/items/7", "https://app.example.com", "PUT", "x y"), 403},
		{"no route has the path", preflight("/nowhere", "https://app.example.com", "GET", ""), 404},
	}
	for _, c := range cases {
		code, hdr, _ := serve(h, c.req)
		if code != c.code || hdr.Get("Access-Control-Allow-Origin") != "" || hdr.Get("Access-Control-Allow-Methods") != "" {
			t.Errorf("%s: got %d %v, want a bare %d", c.name, code, hdr, c.code)
		}
	}
}

func TestResponsesCarryCORSHeaders(t *testing.T) {
	h := mustCORS(t, "https://app.example.com", "http://localhost:5173")
	req := func(method, path, body string) *http.Request {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Origin", "http://localhost:5173")
		return r
	}

	// A program's response exposes exactly the headers it set (§4), and
	// its own Vary keeps its value with Origin added (§5).
	put := req(http.MethodPut, "/items/7", `{"id":"7"}`)
	put.Header.Set("Content-Type", "application/json")
	put.Header.Set("Authorization", "Bearer t")
	code, hdr, _ := serve(h, put)
	if code != 201 || hdr.Get("Access-Control-Allow-Origin") != "http://localhost:5173" || hdr.Get("Access-Control-Allow-Credentials") != "true" {
		t.Fatalf("PUT: %d %v", code, hdr)
	}
	if got := hdr.Get("Access-Control-Expose-Headers"); got != "Location" {
		t.Errorf("Expose-Headers = %q, want Location only", got)
	}
	if got := hdr.Values("Vary"); strings.Join(got, "|") != "Accept-Language, Origin" {
		t.Errorf("Vary = %q", got)
	}

	// §8: errors carry them too, whoever produced the error.
	errors := []struct {
		name   string
		req    *http.Request
		code   int
		expose string
	}{
		{"a mapped 401", req(http.MethodDelete, "/items/7", ""), 401, "WWW-Authenticate"},
		{"a 415", req(http.MethodPut, "/items/7", `{"id":"7"}`), 415, ""},
		{"a 400 for a missing header", func() *http.Request {
			r := req(http.MethodPut, "/items/7", `{"id":"7"}`)
			r.Header.Set("Content-Type", "application/json")
			return r
		}(), 400, ""},
		{"a 404 for an unknown path", req(http.MethodGet, "/nowhere", ""), 404, ""},
		{"a fault's 500", req(http.MethodGet, "/boom/3", ""), 500, ""},
	}
	for _, e := range errors {
		code, hdr, body := serve(h, e.req)
		if code != e.code || hdr.Get("Access-Control-Allow-Origin") != "http://localhost:5173" || hdr.Get("Access-Control-Expose-Headers") != e.expose || hdr.Get("Vary") != "Origin" {
			t.Errorf("%s: got %d %v %q", e.name, code, hdr, body)
		}
	}

	// An unlisted origin, or none, gets no CORS headers, but still Vary.
	for _, origin := range []string{"https://evil.example.com", ""} {
		r := httptest.NewRequest(http.MethodGet, "/items/7", nil)
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		code, hdr, _ := serve(h, r)
		if code != 200 || hdr.Get("Access-Control-Allow-Origin") != "" || hdr.Get("Vary") != "Origin" {
			t.Errorf("origin %q: %d %v", origin, code, hdr)
		}
	}
}

func TestStarPolicy(t *testing.T) {
	// The cookie route is the only thing * refuses, so serve a program
	// without it.
	saved := corsProgram
	defer func() { corsProgram = saved }()
	corsProgram = append([]string{}, saved...)
	last := len(corsProgram) - 1
	corsProgram[last] = strings.Replace(corsProgram[last], `    GET "/me" -> me(session: string | None from cookie "sid")
        produces "application/json"
`, "", 1)
	corsProgram = append(corsProgram[:4], corsProgram[5:]...)

	h := mustCORS(t, "*")
	r := httptest.NewRequest(http.MethodGet, "/items/7", nil)
	r.Header.Set("Origin", "https://anyone.example")
	_, hdr, _ := serve(h, r)
	if hdr.Get("Access-Control-Allow-Origin") != "*" || hdr.Get("Access-Control-Allow-Credentials") != "" || hdr.Get("Vary") != "" {
		t.Errorf("* response: %v", hdr)
	}
	code, hdr, _ := serve(h, preflight("/items/7", "https://anyone.example", "DELETE", ""))
	if code != 204 || hdr.Get("Access-Control-Allow-Origin") != "*" || hdr.Get("Access-Control-Allow-Credentials") != "" {
		t.Errorf("* preflight: %d %v", code, hdr)
	}
}

func TestCORSOffChangesNothing(t *testing.T) {
	h := mustCORS(t)
	code, hdr, _ := serve(h, preflight("/items/7", "https://app.example.com", "PUT", ""))
	if code != http.StatusMethodNotAllowed && code != http.StatusNotFound || hdr.Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("preflight with CORS off: %d %v", code, hdr)
	}
	r := httptest.NewRequest(http.MethodGet, "/items/7", nil)
	r.Header.Set("Origin", "https://app.example.com")
	if _, hdr, _ := serve(h, r); hdr.Get("Vary") != "" || hdr.Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("GET with CORS off: %v", hdr)
	}
}
