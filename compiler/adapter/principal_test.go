package adapter

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"fiig-lang/compiler/parser"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// ADR 0137: the principal is settled before the request is read.

var principalProgram = []string{
	"model Identity {\n    userId: string\n}",
	"model Admin {\n    userId: string\n}",
	"enum IdentityError {\n    NotSignedIn\n}",
	"enum AdminRefusal {\n    NotAnAdmin\n}",
	"model Note {\n    text: string\n}",
	`workflow signedIn(token: string | None) -> Result<Identity, IdentityError> {
    match token {
        None -> {
            return Err(IdentityError.NotSignedIn)
        }
        given -> {
            if given == "ann" or given == "boss" {
                return Ok(Identity(userId: given))
            }
            return Err(IdentityError.NotSignedIn)
        }
    }
}`,
	`workflow signedInAdmin(me: Identity) -> Result<Admin, AdminRefusal> {
    if me.userId == "boss" {
        return Ok(Admin(userId: me.userId))
    }
    return Err(AdminRefusal.NotAnAdmin)
}`,
	`conversion signInFirst(error: IdentityError) -> HttpResponse<string> {
    return HttpResponse(status: 401, body: "sign in first")
}`,
	`conversion notAnAdmin(error: AdminRefusal) -> HttpResponse<string> {
    return HttpResponse(status: 403, body: "admins only")
}`,
	`workflow addNote(me: Identity, note: Note) -> string {
    return "${me.userId}: ${note.text}"
}`,
	`workflow showSettings(admin: Admin) -> string {
    return "settings for ${admin.userId}"
}`,
	`workflow showCover(id: string) -> string {
    return "cover ${id}"
}`,
	`adapter http api {
    principal me: Identity from cookie "sid" by signedIn else signInFirst
    principal admin: Admin from me by signedInAdmin else notAnAdmin

    POST "/notes" -> addNote(me, note: Note)
        consumes "application/json"
        produces "application/json"
    GET "/settings" -> showSettings(admin)
        produces "application/json"
    GET "/covers/{id}" public -> showCover(id: string)
        produces "application/json"
}`,
}

// watchedBody records whether the handler read the request body.
type watchedBody struct {
	r    io.Reader
	read bool
}

func (b *watchedBody) Read(p []byte) (int, error) {
	b.read = true
	return b.r.Read(p)
}

func (b *watchedBody) Close() error { return nil }

func TestAPrincipalIsSettledBeforeTheBodyIsRead(t *testing.T) {
	h := handlerFor(t, principalProgram...)

	// No cookie and a body that isn't JSON: the refusal, and the body is
	// never read, so it can't produce a 400 that describes the route.
	body := &watchedBody{r: strings.NewReader("not json")}
	req := httptest.NewRequest(http.MethodPost, "/notes", nil)
	req.Body = body
	req.Header.Set("Content-Type", "text/plain")
	code, _, out := serve(h, req)
	if code != 401 || !strings.Contains(out, "sign in first") {
		t.Fatalf("no cookie: %d %s, want 401 sign in first", code, out)
	}
	if body.read {
		t.Error("the body of a refused request was read")
	}

	req = httptest.NewRequest(http.MethodPost, "/notes", strings.NewReader(`{"text": "hi"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "sid", Value: "ann"})
	if code, _, out := serve(h, req); code != 200 || !strings.Contains(out, "ann: hi") {
		t.Fatalf("signed in: %d %s", code, out)
	}

	req = httptest.NewRequest(http.MethodPost, "/notes", strings.NewReader(`{"text": "hi"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "sid", Value: "forged"})
	if code, _, _ := serve(h, req); code != 401 {
		t.Fatalf("an invalid cookie: %d, want 401", code)
	}
}

func TestARolePrincipalIsSettledFromAnother(t *testing.T) {
	h := handlerFor(t, principalProgram...)
	cases := []struct {
		cookie string
		code   int
		want   string
	}{
		{"", 401, "sign in first"},
		{"ann", 403, "admins only"},
		{"boss", 200, "settings for boss"},
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodGet, "/settings", nil)
		if c.cookie != "" {
			req.AddCookie(&http.Cookie{Name: "sid", Value: c.cookie})
		}
		if code, _, out := serve(h, req); code != c.code || !strings.Contains(out, c.want) {
			t.Errorf("cookie %q: %d %s, want %d %s", c.cookie, code, out, c.code, c.want)
		}
	}
}

func TestAPublicRouteSettlesNoPrincipal(t *testing.T) {
	h := handlerFor(t, principalProgram...)
	if code, _, out := serve(h, httptest.NewRequest(http.MethodGet, "/covers/c1", nil)); code != 200 || !strings.Contains(out, "cover c1") {
		t.Fatalf("public: %d %s", code, out)
	}
}

// §5: a principal's cookie is a security scheme its routes require, the
// refusal's status is among their responses, and a public route has none.
func TestOpenAPIDescribesPrincipals(t *testing.T) {
	var files []*syntax.File
	var ad *syntax.AdapterDecl
	for _, src := range principalProgram {
		f, errs := parser.Parse(src)
		if len(errs) > 0 {
			t.Fatal(errs)
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
	schemes := doc["components"].(map[string]any)["securitySchemes"].(map[string]any)
	if s, _ := schemes["sid"].(map[string]any); s["in"] != "cookie" || s["type"] != "apiKey" {
		t.Errorf("schemes = %v", schemes)
	}
	paths := doc["paths"].(map[string]any)
	notes := paths["/notes"].(map[string]any)["post"].(map[string]any)
	if sec := fmt.Sprint(notes["security"]); sec != "[map[sid:[]]]" {
		t.Errorf("POST /notes security = %s", sec)
	}
	if _, ok := notes["responses"].(map[string]any)["401"]; !ok {
		t.Errorf("POST /notes responses = %v, want the refusal's 401", notes["responses"])
	}
	settings := paths["/settings"].(map[string]any)["get"].(map[string]any)
	for _, code := range []string{"401", "403"} {
		if _, ok := settings["responses"].(map[string]any)[code]; !ok {
			t.Errorf("GET /settings responses = %v, want %s", settings["responses"], code)
		}
	}
	cover := paths["/covers/{id}"].(map[string]any)["get"].(map[string]any)
	if _, ok := cover["security"]; ok {
		t.Errorf("a public route has security %v", cover["security"])
	}
}

// ADR 0141: the same program with its principals declared at the top level
// and named by the adapter. It settles alike, and the document names the
// scheme after the principal.
func namedPrincipalProgram() []string {
	out := append([]string(nil), principalProgram[:len(principalProgram)-1]...)
	return append(out,
		`principal me: Identity from cookie "sid" by signedIn else signInFirst`,
		`principal admin: Admin from me by signedInAdmin else notAnAdmin`,
		`adapter http api {
    principal admin

    GET "/settings" -> showSettings(admin)
        produces "application/json"
}`)
}

func TestANamedRoleSettlesWhatItIsSettledFrom(t *testing.T) {
	h := handlerFor(t, namedPrincipalProgram()...)
	for _, c := range []struct {
		cookie string
		code   int
	}{{"", 401}, {"ann", 403}, {"boss", 200}} {
		req := httptest.NewRequest(http.MethodGet, "/settings", nil)
		if c.cookie != "" {
			req.AddCookie(&http.Cookie{Name: "sid", Value: c.cookie})
		}
		if code, _, out := serve(h, req); code != c.code {
			t.Errorf("cookie %q: %d %s, want %d", c.cookie, code, out, c.code)
		}
	}
	var files []*syntax.File
	var ad *syntax.AdapterDecl
	for _, src := range namedPrincipalProgram() {
		f, errs := parser.Parse(src)
		if len(errs) > 0 {
			t.Fatal(errs)
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
	schemes := doc["components"].(map[string]any)["securitySchemes"].(map[string]any)
	if s, _ := schemes["me"].(map[string]any); s["in"] != "cookie" || s["name"] != "sid" {
		t.Errorf("schemes = %v, want one named after the principal", schemes)
	}
}
