package adapter

import (
	"net/http"
	"strconv"
	"strings"

	"fiig-lang/compiler/httpspec"
	"fiig-lang/compiler/runtime"
)

// Header is one response header a program set, in the order it was put
// into the HttpResponse's headers map.
type Header struct {
	Name, Value string
}

// cookie is a prelude Cookie (ADR 0069 §4) read out of its record, with
// every left-out field given its secure default.
type cookie struct {
	name, value, path, domain string
	maxAge                    *int64
	sameSite                  string // "Strict", "Lax", or "None"
	httpOnly, secure          bool
}

// readHttpResponse reads the prelude HttpResponse record an expression arm
// produced (ADR 0069 §3) into a Response.
func readHttpResponse(rec runtime.Record) (Response, *runtime.Fault) {
	status, ok := rec.Fields["status"].(runtime.Int)
	if !ok {
		return Response{}, runtime.Faultf("response-mapping expression's record has no int \"status\" field")
	}
	resp := Response{Status: int(status), Body: rec.Fields["body"]}
	if resp.Body == nil {
		resp.Body = runtime.None{}
	}
	if m, ok := rec.Fields["headers"].(runtime.Map); ok {
		var fault *runtime.Fault
		m.Each(func(k, v runtime.Value) bool {
			name, ok1 := k.(runtime.Str)
			value, ok2 := v.(runtime.Str)
			if !ok1 || !ok2 {
				fault = runtime.Faultf("response headers must map string to string, got %s: %s", k, v)
				return false
			}
			resp.Headers = append(resp.Headers, Header{Name: string(name), Value: string(value)})
			return true
		})
		if fault != nil {
			return Response{}, fault
		}
	}
	if l, ok := rec.Fields["cookies"].(runtime.List); ok {
		for _, e := range l.Elems {
			c, ok := e.(runtime.Record)
			if !ok {
				return Response{}, runtime.Faultf("response cookies must be Cookie values, got %s", e)
			}
			resp.Cookies = append(resp.Cookies, c)
		}
	}
	return resp, nil
}

func readCookie(rec runtime.Record) cookie {
	str := func(field, def string) string {
		if s, ok := rec.Fields[field].(runtime.Str); ok {
			return string(s)
		}
		return def
	}
	flag := func(field string) bool {
		b, ok := rec.Fields[field].(runtime.Bool)
		return ok && bool(b)
	}
	c := cookie{
		name:     str("name", ""),
		value:    str("value", ""),
		path:     str("path", "/"),
		domain:   str("domain", ""),
		sameSite: "Lax",
		httpOnly: !flag("readableByScripts"),
		secure:   !flag("sentOverPlainHttp"),
	}
	if n, ok := rec.Fields["maxAge"].(runtime.Int); ok {
		age := int64(n)
		c.maxAge = &age
	}
	if e, ok := rec.Fields["sameSite"].(runtime.EnumValue); ok {
		c.sameSite = e.Variant
		if e.Variant == "AnySite" {
			// The attribute's real value, which fiig can't use as a
			// variant name since None is a value (§4).
			c.sameSite = "None"
		}
	}
	return c
}

// checkWire is ADR 0069 §6: a response the program built that HTTP can't
// carry, or that a browser would quietly drop, is a fault rather than a
// response Go quietly changes.
func checkWire(resp Response) *runtime.Fault {
	if p := httpspec.StatusProblem(int64(resp.Status)); p != "" {
		return runtime.Faultf("response %s", p)
	}
	if _, none := resp.Body.(runtime.None); !none && resp.Body != nil && httpspec.BodyForbidden(int64(resp.Status)) {
		return runtime.Faultf("a %d response can't carry a body", resp.Status)
	}
	seen := map[string]string{}
	for _, h := range resp.Headers {
		if !httpspec.IsToken(h.Name) {
			return runtime.Faultf("response header name %q isn't an HTTP token", h.Name)
		}
		if httpspec.AdapterOwnedHeader(h.Name) {
			return runtime.Faultf("response header %q is the adapter's: Content-Type comes from produces, Content-Length from the body, and Set-Cookie from cookies", h.Name)
		}
		lower := strings.ToLower(h.Name)
		if other, dup := seen[lower]; dup {
			return runtime.Faultf("response headers %q and %q differ only by case, and HTTP reads them as one header", other, h.Name)
		}
		seen[lower] = h.Name
		if p := httpspec.HeaderValueProblem(h.Value); p != "" {
			return runtime.Faultf("response header %q's value %s", h.Name, p)
		}
	}
	type cookieKey struct{ name, path, domain string }
	cookies := map[cookieKey]bool{}
	for _, rec := range resp.Cookies {
		c := readCookie(rec)
		if p := cookieProblem(c); p != "" {
			return runtime.Faultf("cookie %q %s", c.name, p)
		}
		key := cookieKey{c.name, c.path, strings.ToLower(c.domain)}
		if cookies[key] {
			return runtime.Faultf("cookie %q is set twice with one path and domain, and the browser keeps only one", c.name)
		}
		cookies[key] = true
	}
	return nil
}

// cookieProblem describes why a browser would refuse c or read it as more
// than it says (ADR 0069 §6), or "" if it is fine. The checker reports the
// same problems on constants.
func cookieProblem(c cookie) string {
	if !httpspec.IsToken(c.name) {
		return "has a name that isn't an HTTP token"
	}
	if p := httpspec.CookieValueProblem(c.value); p != "" {
		return "value " + p
	}
	if p := httpspec.CookiePathProblem(c.path); p != "" {
		return "path " + p
	}
	if c.domain != "" && !httpspec.IsHostname(c.domain) {
		return "domain " + strconv.Quote(c.domain) + " isn't a hostname"
	}
	if c.maxAge != nil && *c.maxAge < 0 {
		return "has a negative maxAge; 0 deletes it"
	}
	return httpspec.CookieRuleProblem(c.name, c.sameSite == "None", c.secure, c.domain != "", c.path)
}

// setCookie writes c as a Set-Cookie value, every attribute explicit.
func setCookie(c cookie) string {
	var b strings.Builder
	b.WriteString(c.name + "=" + c.value + "; Path=" + c.path)
	if c.domain != "" {
		b.WriteString("; Domain=" + c.domain)
	}
	if c.maxAge != nil {
		b.WriteString("; Max-Age=" + strconv.FormatInt(*c.maxAge, 10))
	}
	if c.secure {
		b.WriteString("; Secure")
	}
	if c.httpOnly {
		b.WriteString("; HttpOnly")
	}
	b.WriteString("; SameSite=" + c.sameSite)
	return b.String()
}

// writeHeaders writes a checked response's own headers and cookies.
func writeHeaders(h http.Header, resp Response) {
	for _, hd := range resp.Headers {
		h.Add(hd.Name, hd.Value)
	}
	for _, rec := range resp.Cookies {
		h.Add("Set-Cookie", setCookie(readCookie(rec)))
	}
}
