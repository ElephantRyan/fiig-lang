package adapter

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ADR 0069 §3, §4, and §6: headers and cookies a program sets, their
// secure defaults, and what the adapter refuses to write.

func wireProgram(conversion string) []string {
	return []string{
		`model User {
    name: string
}`,
		`workflow logIn(name: string) -> User {
    return User(name: name)
}`,
		conversion,
		`adapter http api {
    POST "/login/{name}" -> logIn(name: string)
        produces "application/json"
    {
        user -> respond(user)
    }
}`,
	}
}

func TestCookiesAndHeadersAreWritten(t *testing.T) {
	h := handlerFor(t, wireProgram(`conversion respond(user: User) -> HttpResponse<User> {
    return HttpResponse(
        status: 200,
        body: user,
        headers: ["Cache-Control": "no-store", "X-Trace": "a\tb ünï"],
        cookies: [
            Cookie(name: "sid", value: "tok_en-1"),
            Cookie(
                name: "theme",
                value: "dark",
                maxAge: 0,
                path: "/app",
                domain: "example.com",
                sameSite: SameSite.Strict,
                readableByScripts: true,
            ),
            Cookie(name: "embed", value: "x", sameSite: SameSite.AnySite),
        ],
    )
}`)...)
	code, hdr, body := serve(h, httptest.NewRequest(http.MethodPost, "/login/ada", nil))
	if code != 200 || strings.TrimSpace(body) != `{"name":"ada"}` {
		t.Fatalf("got %d %q", code, body)
	}
	if hdr.Get("Cache-Control") != "no-store" || hdr.Get("X-Trace") != "a\tb ünï" || hdr.Get("Content-Type") != "application/json" {
		t.Fatalf("headers = %v", hdr)
	}
	want := []string{
		"sid=tok_en-1; Path=/; Secure; HttpOnly; SameSite=Lax",
		"theme=dark; Path=/app; Domain=example.com; Max-Age=0; Secure; SameSite=Strict",
		"embed=x; Path=/; Secure; HttpOnly; SameSite=None",
	}
	got := hdr.Values("Set-Cookie")
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("Set-Cookie = %q, want %q", got, want)
	}
}

func TestTheAdapterRefusesWhatHTTPCantCarry(t *testing.T) {
	cases := []struct{ build, fault string }{
		{`HttpResponse(status: 200, headers: ["X-Name": "a${user.name}\r\nX-Evil: 1"])`, `response header "X-Name"'s value contains control character a carriage return`},
		{`HttpResponse(status: 200, headers: ["X-${user.name} y": "v"])`, `isn't an HTTP token`},
		{`HttpResponse(status: 200, headers: ["Content-Type": "text/html${user.name}"])`, `response header "Content-Type" is the adapter's`},
		{`HttpResponse(status: 200, headers: ["X-Id": "1", "x-id": "${user.name}"])`, `response headers "X-Id" and "x-id" differ only by case`},
		{`HttpResponse(status: 100 + user.name.length())`, `response status 103 is outside 200 to 599`},
		{`HttpResponse(status: 204, body: user)`, `a 204 response can't carry a body`},
		{`HttpResponse(status: 200, cookies: [Cookie(name: "s id${user.name}", value: "v")])`, `has a name that isn't an HTTP token`},
		{`HttpResponse(status: 200, cookies: [Cookie(name: "sid", value: "a;b${user.name}")])`, `value contains ";", which a cookie value can't carry`},
		{`HttpResponse(status: 200, cookies: [Cookie(name: "sid", value: "v", path: "/;Domain=evil${user.name}")])`, `path contains ";"`},
		{`HttpResponse(status: 200, cookies: [Cookie(name: "sid", value: "v", domain: "evil.com;x${user.name}")])`, `isn't a hostname`},
		{`HttpResponse(status: 200, cookies: [Cookie(name: "sid", value: "v", maxAge: 0 - user.name.length())])`, `negative maxAge`},
		{`HttpResponse(status: 200, cookies: [Cookie(name: "sid${user.name}", value: "v", sameSite: SameSite.AnySite, sentOverPlainHttp: true)])`, `browsers refuse SameSite=None without Secure`},
		{`HttpResponse(status: 200, cookies: [Cookie(name: "__Host-sid${user.name}", value: "v", path: "/app")])`, `__Host- prefix, which browsers refuse with a path other than "/"`},
		{`HttpResponse(status: 200, cookies: [Cookie(name: "sid", value: "a"), Cookie(name: "sid", value: "${user.name}")])`, `is set twice with one path and domain`},
	}
	for _, c := range cases {
		h := handlerFor(t, wireProgram("conversion respond(user: User) -> HttpResponse<User> {\n    return "+c.build+"\n}")...)
		code, hdr, body := serve(h, httptest.NewRequest(http.MethodPost, "/login/ada", nil))
		var decoded struct{ Fault string }
		_ = json.Unmarshal([]byte(body), &decoded)
		if code != 500 || !strings.Contains(decoded.Fault, c.fault) || len(hdr.Values("Set-Cookie")) != 0 {
			t.Errorf("%s: got %d %q, want a 500 fault containing %q", c.build, code, body, c.fault)
		}
	}
}
