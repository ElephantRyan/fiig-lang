package runtime

import "testing"

// ADR 0069 §3 and §4: HttpResponse and Cookie are built like models, and a
// left-out optional field is None (a declared model's too: the runtime
// used to fault on one the checker had accepted).
func TestHttpResponseAndCookieConstruction(t *testing.T) {
	src := `rule respond(token: string) -> string {
    cookie = Cookie(name: "sid", value: token);
    response = HttpResponse(status: 201, cookies: [cookie]);
    return "${response.status} ${response.body} ${response.headers} ${cookie.maxAge} ${cookie.sameSite}";
}`
	got, fault := runEach(t, src, "respond", map[string]Value{"token": Str("abc")})
	if fault != nil || got.String() != "201 None None None None" {
		t.Fatalf("respond = %v, %v", got, fault)
	}
}
