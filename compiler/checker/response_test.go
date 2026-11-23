package checker

import "testing"

// ADR 0069 §3 and §4: the prelude's HttpResponse<B>, Cookie, and SameSite,
// and response arms checked as types.

const responseModels = `model User {
    name: string;
}`

func TestHttpResponseConstruction(t *testing.T) {
	diags := checkSrc(t, responseModels, `conversion loggedIn(user: User, token: string) -> HttpResponse<User> {
    return HttpResponse(
        status: 200,
        body: user,
        headers: ["Cache-Control": "no-store"],
        cookies: [Cookie(name: "sid", value: token, maxAge: 86400, sameSite: SameSite.Strict)]
    );
}`, `conversion noContent() -> HttpResponse<User> {
    response = HttpResponse(status: 204);
    return response;
}`, `conversion inferred(user: User) -> int {
    response = HttpResponse(status: 201, body: user);
    named: User | None = response.body;
    return response.status;
}`)
	if len(diags) != 0 {
		t.Fatalf("diags = %v, want none", diags)
	}
}

func TestHttpResponseErrors(t *testing.T) {
	cases := []struct{ src, rule, fragment string }{
		{`conversion c() -> HttpResponse<None> {
    return HttpResponse(body: None);
}`, "A3", "HttpResponse.status is required and not supplied"},
		{`conversion c(user: User) -> HttpResponse<string> {
    return HttpResponse(status: 200, body: user);
}`, "A1", "type mismatch: got User, expected string | None"},
		{`conversion c(user: User) -> HttpResponse<string> {
    return HttpResponse(status: 200, headers: ["X-Count": 3]);
}`, "A1", ""},
		{`conversion c(user: User) -> HttpResponse<User> {
    return HttpResponse(status: 200, cookies: [Cookie(name: "sid")]);
}`, "A3", "Cookie.value is required and not supplied"},
		{`conversion c(user: User) -> int {
    return HttpResponse(status: 200, body: user).contentType;
}`, "", `HttpResponse<User> has no field "contentType"`},
		{`conversion c(user: User) -> HttpResponse<string> {
    response = HttpResponse(status: 200, body: user);
    return response;
}`, "A1", "got HttpResponse<User>, expected HttpResponse<string>"},
		{`model HttpResponse {
    status: int;
}`, "K2", ""},
		{`model Cookie {
    name: string;
}`, "K2", ""},
	}
	for _, c := range cases {
		wantDiag(t, checkSrc(t, responseModels, c.src), c.rule, c.fragment)
	}
}

func responseRouteSrc(adapter string) []string {
	return []string{responseModels,
		`enum UserError {
    Missing(name: string)
}`,
		`workflow readUser(name: string) -> Result<User, UserError> {
    return Ok(User(name: name));
}`,
		`conversion userErrorToHttpResponse(error: UserError) -> HttpResponse<UserError> {
    return HttpResponse(status: 404, body: error);
}`,
		`conversion userToCsv(user: User) -> HttpResponse<string> {
    return HttpResponse(status: 200, body: "name\n${user.name}");
}`,
		`rule describe(error: UserError) -> string {
    return "missing";
}`,
		adapter,
	}
}

func TestResponseArmsAreChecked(t *testing.T) {
	diags := checkSrc(t, responseRouteSrc(`adapter http users {
    GET "/users/{name}" -> readUser(name: string)
        produces "application/json"
    {
        Ok(user) -> 200 user,
        Err(error) -> userErrorToHttpResponse(error)
    }
    GET "/users/{name}/csv" -> readUser(name: string)
        produces "text/csv"
    {
        Ok(user) -> userToCsv(user),
        Err(error) -> 404 describe(error)
    }
}`)...)
	if len(diags) != 0 {
		t.Fatalf("diags = %v, want none", diags)
	}

	cases := []struct{ adapter, rule, fragment string }{
		{`adapter http users {
    GET "/users/{name}" -> readUser(name: string)
        produces "application/json"
    {
        Ok(user) -> 200 describe(user),
        Err(error) -> 404
    }
}`, "A1", "type mismatch: got User, expected UserError"},
		{`adapter http users {
    GET "/users/{name}" -> readUser(name: string)
        produces "application/json"
    {
        Ok(user) -> 200 user,
        Err(error) -> describe(error)
    }
}`, "A1", "a response arm's expression must produce an HttpResponse, got string"},
		{`adapter http users {
    GET "/users/{name}" -> readUser(name: string)
        produces "text/csv"
    {
        Ok(user) -> 200 user,
        Err(error) -> 404
    }
}`, "", `route produces "text/csv", which sends a string or bytes body, but this response's body is User`},
		{`adapter http users {
    GET "/users/{name}" -> readUser(name: string)
        produces "text/csv"
    {
        Ok(user) -> userToCsv(user),
        Err(error) -> userErrorToHttpResponse(error)
    }
}`, "", "but this response's body is UserError"},
	}
	for _, c := range cases {
		wantDiag(t, checkSrc(t, responseRouteSrc(c.adapter)...), c.rule, c.fragment)
	}
}

// ADR 0069 §6: what the adapter would refuse, reported when it is constant.
func TestWireConstantsAreChecked(t *testing.T) {
	cases := []struct{ build, rule, fragment string }{
		{`HttpResponse(status: 99)`, "W1", "response status 99 is outside 200 to 599"},
		{`HttpResponse(status: 204, body: user)`, "W1", "a 204 response can't carry a body"},
		{`HttpResponse(status: 200, headers: ["X Name": "v"])`, "W1", `response header name "X Name" isn't an HTTP token`},
		{`HttpResponse(status: 200, headers: ["set-cookie": "v"])`, "W1", `response header "set-cookie" is the adapter's: the adapter writes it from cookies`},
		{`HttpResponse(status: 200, headers: ["access-control-allow-origin": "*"])`, "W1", `response header "access-control-allow-origin" is the adapter's: the adapter writes CORS headers from fiig serve -allow-origin`},
		{`HttpResponse(status: 200, headers: ["X-Id": "1", "x-id": "2"])`, "W1", `response headers "X-Id" and "x-id" differ only by case`},
		{`HttpResponse(status: 200, headers: ["X-Id": "1\r\nX-Evil: 2"])`, "W1", "response header value contains control character a carriage return"},
		{`HttpResponse(status: 200, cookies: [Cookie(name: "s;id", value: "v")])`, "W2", `cookie name "s;id" isn't an HTTP token`},
		{`HttpResponse(status: 200, cookies: [Cookie(name: "sid", value: "a b")])`, "W2", "cookie value contains a space"},
		{`HttpResponse(status: 200, cookies: [Cookie(name: "sid", value: "v", path: "/; Domain=evil.com")])`, "W2", `cookie path contains ";"`},
		{`HttpResponse(status: 200, cookies: [Cookie(name: "sid", value: "v", domain: "evil.com; x")])`, "W2", "isn't a hostname"},
		{`HttpResponse(status: 200, cookies: [Cookie(name: "sid", value: "v", maxAge: -1)])`, "W2", "cookie maxAge -1 is negative"},
		{`HttpResponse(status: 200, cookies: [Cookie(name: "sid", value: "v", sameSite: SameSite.AnySite, sentOverPlainHttp: true)])`, "W2", "browsers refuse SameSite=None without Secure"},
		{`HttpResponse(status: 200, cookies: [Cookie(name: "__Secure-sid", value: "v", sentOverPlainHttp: true)])`, "W2", "__Secure- prefix"},
		{`HttpResponse(status: 200, cookies: [Cookie(name: "__Host-sid", value: "v", domain: "example.com")])`, "W2", "__Host- prefix, which browsers refuse with a domain"},
	}
	for _, c := range cases {
		src := "conversion respond(user: User) -> HttpResponse<User> {\n    return " + c.build + "\n}"
		wantDiag(t, checkSrc(t, responseModels, src), c.rule, c.fragment)
	}
	// What the checker can't see is the adapter's to refuse (§6), so a
	// computed value draws nothing here.
	diags := checkSrc(t, responseModels, `conversion respond(user: User, status: int, token: string) -> HttpResponse<User> {
    return HttpResponse(
        status: status,
        headers: ["X-User": user.name],
        cookies: [Cookie(name: "__Host-sid", value: token, sameSite: SameSite.AnySite)]
    );
}`)
	if len(diags) != 0 {
		t.Fatalf("diags = %v, want none", diags)
	}
	wantDiag(t, checkSrc(t, responseRouteSrc(`adapter http users {
    GET "/users/{name}" -> readUser(name: string)
        produces "application/json"
    {
        Ok(user) -> 304 user,
        Err(error) -> 404
    }
}`)...), "W1", "a 304 response can't carry a body")
}
