package checker

import "testing"

// ADR 0069 §1, §2, and §5: header and cookie arguments, and the content
// types a route declares.

const routeWorkflows = `model NewOrder {
    item: string
}`

func routeSrc(adapter string) []string {
	return []string{routeWorkflows,
		`workflow placeOrder(input: NewOrder, token: string, requestId: string | None) -> Result<NewOrder, string> {
    return Ok(input)
}`,
		`workflow readOrder(id: string, token: string) -> Result<NewOrder, string> {
    return Err(id)
}`,
		`workflow importOrders(csv: string) -> Result<int, string> {
    return Ok(1)
}`,
		`workflow exportOrders() -> Result<string, string> {
    return Ok("a,b")
}`,
		`workflow orderCount() -> int {
    return 1
}`,
		`workflow receiveHook(body: bytes) -> Result<int, string> {
    return Ok(body.length())
}`,
		`workflow receiveMaybeHook(body: bytes | None) -> Result<int, string> {
    return Ok(1)
}`,
		adapter,
	}
}

func TestRouteHeadersCookiesAndMediaTypes(t *testing.T) {
	diags := checkSrc(t, routeSrc(`adapter http orders {
    POST "/orders" -> placeOrder(
        input: NewOrder,
        token: string from header "Authorization",
        requestId: string | None from header "X-Request-Id",
    )
        consumes "application/json"
        produces "application/json"
    GET "/orders/{id}" -> readOrder(id: string, token: string from cookie "sid")
        produces "application/problem+json"
    POST "/imports" -> importOrders(csv: string)
        consumes "text/csv"
        produces "application/json"
    GET "/export" -> exportOrders()
        produces "text/csv"
    GET "/count" -> orderCount()
        produces "text/plain"
}`)...)
	if len(diags) != 1 || diags[0].Msg != `route produces "text/plain", which sends a string or bytes body, but orderCount's result is int; map the response, or produce a JSON type` {
		t.Fatalf("diags = %v", diags)
	}
}

func TestRouteBindingErrors(t *testing.T) {
	cases := []struct{ adapter, rule, fragment string }{
		{`adapter http a {
    GET "/count" -> orderCount()
}`, "", `route GET "/count" has no produces clause; declare the content type it sends, such as produces "application/json"`},
		{`adapter http a {
    GET "/count" -> orderCount()
        consumes "application/json"
        produces "application/json"
}`, "", `GET "/count" has no request body, so it consumes nothing; remove the consumes clause`},
		{`adapter http a {
    POST "/imports" -> importOrders(csv: string)
        consumes "application/json"
        produces "application/json"
}`, "A7", `request body parameter "csv" binds to "string", which isn't a model`},
		{`adapter http a {
    GET "/count" -> orderCount()
        produces "json"
}`, "", `"json" isn't a media type; write type/subtype`},
		{`adapter http a {
    GET "/count" -> orderCount()
        produces "text/plain; charset=utf-8"
}`, "", `isn't a media type`},
		{`adapter cli a {
    command "count" -> orderCount()
        produces "application/json"
}`, "", "a cli route has no content type"},
		{`adapter cli a {
    command "read {id}" -> readOrder(id: string, token: string from header "Authorization")
}`, "", "a cli route has no headers; `from header` is for http routes"},
		{`adapter http a {
    POST "/orders" -> placeOrder(input: NewOrder from header "X-Order", token: string from header "Authorization", requestId: string | None from header "X-Request-Id")
        produces "application/json"
}`, "W4", `"input" is read from a header, so it must be string, int, float, decimal, bool, or an enum without payloads, optionally | None; got NewOrder`},
		{`adapter http a {
    GET "/orders/{id}" -> readOrder(id: string, token: string from header "Bad Name")
        produces "application/json"
}`, "", `header name "Bad Name" isn't an HTTP token`},
		{`adapter http a {
    GET "/orders/{id}" -> readOrder(id: string, token: string from cookie "sid", extra: string from cookie "x")
        produces "application/json"
}`, "", `workflow "readOrder" has no parameter named "extra"`},
		{`adapter http a {
    GET "/orders/{id}" -> readOrder(id: string, token: int from cookie "sid")
        produces "application/json"
}`, "A1", `route argument "token" is int, but readOrder's parameter is string`},
		{`adapter http a {
    GET "/orders/{id}" -> readOrder(id: string, id: string)
        produces "application/json"
}`, "", `route argument "id" is given twice`},
		{`adapter http a {
    GET "/orders/{id}" -> readOrder(id: string from header "X-Id", token: string from cookie "sid")
        produces "application/json"
}`, "", `"id" is bound by a path placeholder and from a header; bind it once`},
		{`adapter http a {
    POST "/orders" -> placeOrder(input: NewOrder, token: string from header "Authorization", requestId: string | None from header "X-Request-Id")
        produces "application/json"
}`, "", `binds its request body to "input" but has no consumes clause`},
		{`adapter http a {
    POST "/orders" -> placeOrder(input: NewOrder, token: string from header "Authorization", requestId: string | None from header "X-Request-Id")
        consumes "text/xml"
        produces "application/json"
}`, "A7", `request body parameter "input" is NewOrder, but the route consumes "text/xml", which binds raw as string or bytes`},
		{`adapter http a {
    POST "/hooks" -> receiveMaybeHook(body: bytes | None)
        consumes "application/json"
        produces "application/json"
}`, "A7", `(bind bytes to read the JSON body raw, ADR 0077)`},
	}
	for _, c := range cases {
		diags := checkSrc(t, routeSrc(c.adapter)...)
		wantDiag(t, diags, c.rule, c.fragment)
	}
}

// ADR 0077: a JSON route may bind its body raw, as bytes.
func TestJSONRouteMayBindItsBodyAsBytes(t *testing.T) {
	diags := checkSrc(t, routeSrc(`adapter http a {
    POST "/hooks" -> receiveHook(body: bytes)
        consumes "application/json"
        produces "application/json"
    POST "/hooks/vendor" -> receiveHook(body: bytes)
        consumes "application/vnd.github+json"
        produces "application/json"
}`)...)
	if len(diags) != 0 {
		t.Fatalf("diags = %v", diags)
	}
}

func TestNonJSONRouteNeedsRawBodiesOnBothSides(t *testing.T) {
	diags := checkSrc(t, routeSrc(`adapter http a {
    GET "/orders/{id}" -> readOrder(id: string, token: string from cookie "sid")
        produces "text/csv"
}`)...)
	if len(diags) != 1 || diags[0].Msg != `route produces "text/csv", which sends a string or bytes body, but readOrder's Ok value is NewOrder; map the response, or produce a JSON type` {
		t.Fatalf("diags = %v", diags)
	}
}
