package checker

import (
	"strconv"
	"strings"

	"fiig-lang/compiler/httpspec"
	"fiig-lang/compiler/lexer"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// checkHttpResponse checks a construction of the prelude's HttpResponse<B>
// (ADR 0069 §3) and returns its type. B is the expected type's, when there
// is one (a conversion's declared return type), and is otherwise inferred
// from body, as Ok(x) infers a Result's; with neither, it is None.
func (c *checker) checkHttpResponse(call *syntax.CallExpr, expected types.Type, env map[string]types.Type) types.Type {
	start, _ := call.Bounds()
	supplied := c.matchArgs(types.HttpResponseFieldNames, call.Args, types.HttpResponseName, start)
	body, hasExpected := types.IsHttpResponse(expected)
	if !hasExpected {
		body = types.NoneType{}
		if expr, ok := supplied["body"]; ok {
			body = withoutNone(c.inferExpr(expr, env))
		}
	}
	fields := types.HttpResponseFields(body)
	for _, n := range types.HttpResponseFieldNames {
		expr, ok := supplied[n]
		if !ok {
			if n == "status" {
				c.report(start, "A3", "HttpResponse.status is required and not supplied")
			}
			continue
		}
		if n == "body" && !hasExpected {
			continue // already inferred
		}
		c.checkExpected(expr, fields[n], env)
	}
	if status, ok := supplied["status"]; ok {
		c.checkStatusConstants(status, supplied["body"])
	}
	if headers, ok := supplied["headers"]; ok {
		c.checkHeaderConstants(headers)
	}
	if types.HoldsSecret(body, c.resolveField) {
		start, _ := call.Bounds()
		c.report(start, "S1", "a response body holds a Secret (%s); a Secret is never encoded (ADR 0071 §4)", body)
	}
	return types.Generic{Name: types.HttpResponseName, Args: []types.Type{body}}
}

// checkResponseArms type-checks a route's response-mapping arms (ADR 0017),
// binding each arm's pattern against the workflow's return type: an HTTP
// arm's expression must produce an HttpResponse (ADR 0069 §3), and every
// body must fit the route's `produces` (§5). Calls in an arm aren't held
// to a declaration kind's composition rules, as before.
func (c *checker) checkResponseArms(route *syntax.Route, wd *syntax.WorkflowDecl, isHTTP bool) {
	if route.Mapping == nil {
		return
	}
	ret, ok := types.ResolveWithImports(wd.ReturnType, c.universe, universesOf(c.imports))
	if !ok {
		ret = types.Unknown{}
	}
	saved := c.callerKind
	c.callerKind = ""
	defer func() { c.callerKind = saved }()
	for _, arm := range route.Mapping {
		env := map[string]types.Type{}
		c.bindPattern(arm.Pattern, ret, env)
		switch v := arm.Value.(type) {
		case *syntax.StatusResponse:
			if isHTTP {
				c.checkStatusConstants(v.Code, v.Body)
			}
			if v.Body != nil {
				c.checkProducedBody(route, c.inferExpr(v.Body, env), v.Body)
			}
		case *syntax.ExitResponse:
			if v.Body != nil {
				c.inferExpr(v.Body, env)
			}
		case *syntax.ExprResponse:
			got := c.inferExpr(v.Value, env)
			if !isHTTP || isUnknown(got) {
				continue
			}
			body, ok := types.IsHttpResponse(got)
			if !ok {
				start, _ := v.Value.(syntax.Spanned).Bounds()
				c.report(start, "A1", "a response arm's expression must produce an HttpResponse, got %s", got)
				continue
			}
			c.checkProducedBody(route, body, v.Value)
		}
	}
}

// checkProducedBody reports a response body a route's `produces` type
// can't send: a type other than JSON sends string or bytes (ADR 0069 §5).
func (c *checker) checkProducedBody(route *syntax.Route, body types.Type, at syntax.Expr) {
	if route.Produces != nil && httpspec.IsJSON(route.Produces.Text) && !isUnknown(body) {
		start, _ := at.(syntax.Spanned).Bounds()
		c.checkJsonOverlap(start, body, "this response's body")
	}
	if route.Produces == nil || !httpspec.ValidMediaType(route.Produces.Text) || httpspec.IsJSON(route.Produces.Text) {
		return
	}
	body = withoutNone(body)
	if _, isNone := body.(types.NoneType); isNone || isUnknown(body) || isRawBody(body) {
		return
	}
	start, _ := at.(syntax.Spanned).Bounds()
	c.report(start, "", "route produces %q, which sends a string or bytes body, but this response's body is %s", route.Produces.Text, body)
}

// httpResponseField is the type of field on an HttpResponse<B>, if it has
// one.
func httpResponseField(body types.Type, field string) (types.Type, bool) {
	t, ok := types.HttpResponseFields(body)[field]
	return t, ok
}

// constString is e's text when e is a string literal with no
// interpolation.
func constString(e syntax.Expr) (string, bool) {
	lit, ok := e.(*syntax.StringLit)
	if !ok {
		return "", false
	}
	s := ""
	for _, part := range lit.Parts {
		if part.Interp != nil {
			return "", false
		}
		s += part.Text
	}
	return s, true
}

// constInt is e's value when e is an integer literal, possibly negated.
func constInt(e syntax.Expr) (int64, bool) {
	switch e := e.(type) {
	case *syntax.IntLit:
		n, err := strconv.ParseInt(strings.ReplaceAll(e.Text, "_", ""), 10, 64)
		return n, err == nil
	case *syntax.NegExpr:
		n, ok := constInt(e.Operand)
		return -n, ok
	case *syntax.ParenExpr:
		return constInt(e.Inner)
	}
	return 0, false
}

func constBool(e syntax.Expr) (bool, bool) {
	if id, ok := e.(*syntax.Ident); ok && (id.Name == "true" || id.Name == "false") {
		return id.Name == "true", true
	}
	return false, false
}

func isNoneLit(e syntax.Expr) bool {
	id, ok := e.(*syntax.Ident)
	return ok && id.Name == "None"
}

// checkStatusConstants is W1 on a status and body the checker can see
// (ADR 0069 §6): a status outside 200 to 599, or a body on a 204 or 304.
func (c *checker) checkStatusConstants(status, body syntax.Expr) {
	n, ok := constInt(status)
	if !ok {
		return
	}
	start, _ := status.(syntax.Spanned).Bounds()
	if p := httpspec.StatusProblem(n); p != "" {
		c.report(start, "W1", "response %s", p)
		return
	}
	if body != nil && !isNoneLit(body) && httpspec.BodyForbidden(n) {
		c.report(start, "W1", "a %d response can't carry a body", n)
	}
}

// checkHeaderConstants is W1 on a headers map literal (ADR 0069 §6): a
// name that isn't a token or that the adapter owns, two names that differ
// only by case, and a value with a control character.
func (c *checker) checkHeaderConstants(e syntax.Expr) {
	lit, ok := e.(*syntax.MapLit)
	if !ok {
		return
	}
	seen := map[string]string{}
	for _, entry := range lit.Entries {
		start, _ := entry.Key.(syntax.Spanned).Bounds()
		if name, ok := constString(entry.Key); ok {
			lower := strings.ToLower(name)
			switch {
			case !httpspec.IsToken(name):
				c.report(start, "W1", "response header name %q isn't an HTTP token", name)
			case httpspec.AdapterOwnedHeader(name) != "":
				c.report(start, "W1", "response header %q is the adapter's: %s", name, httpspec.AdapterOwnedHeader(name))
			case seen[lower] != "":
				c.report(start, "W1", "response headers %q and %q differ only by case, and HTTP reads them as one header", seen[lower], name)
			}
			if seen[lower] == "" {
				seen[lower] = name
			}
		}
		if value, ok := constString(entry.Value); ok {
			if p := httpspec.HeaderValueProblem(value); p != "" {
				vs, _ := entry.Value.(syntax.Spanned).Bounds()
				c.report(vs, "W1", "response header value %s", p)
			}
		}
	}
}

// checkCookieConstants is W2 on a Cookie construction (ADR 0069 §6): each
// field the checker can see, and the browsers' rules for the ones it
// knows. A left-out field is its default, which it also knows.
func (c *checker) checkCookieConstants(args []syntax.Arg, at lexer.Pos) {
	fields := map[string]syntax.Expr{}
	for i, a := range args {
		name := a.Name
		if name == "" && i < len(cookieFieldOrder) {
			name = cookieFieldOrder[i]
		}
		fields[name] = a.Value
	}
	report := func(field, format string, args ...any) {
		start := at
		if e, ok := fields[field]; ok {
			start, _ = e.(syntax.Spanned).Bounds()
		}
		c.report(start, "W2", format, args...)
	}
	name, nameKnown := constString(fields["name"])
	if nameKnown && !httpspec.IsToken(name) {
		report("name", "cookie name %q isn't an HTTP token", name)
		return
	}
	if v, ok := constString(fields["value"]); ok {
		if p := httpspec.CookieValueProblem(v); p != "" {
			report("value", "cookie value %s", p)
		}
	}
	path, pathKnown := "/", true
	if e, ok := fields["path"]; ok && !isNoneLit(e) {
		path, pathKnown = constString(e)
		if pathKnown {
			if p := httpspec.CookiePathProblem(path); p != "" {
				report("path", "cookie path %s", p)
			}
		}
	}
	hasDomain, domainKnown := false, true
	if e, ok := fields["domain"]; ok && !isNoneLit(e) {
		var d string
		d, domainKnown = constString(e)
		hasDomain = true
		if domainKnown && !httpspec.IsHostname(d) {
			report("domain", "cookie domain %q isn't a hostname", d)
		}
	}
	if e, ok := fields["maxAge"]; ok {
		if n, ok := constInt(e); ok && n < 0 {
			report("maxAge", "cookie maxAge %d is negative; 0 deletes the cookie", n)
		}
	}
	sameSiteNone, sameSiteKnown := false, true
	if e, ok := fields["sameSite"]; ok && !isNoneLit(e) {
		sameSiteKnown = false
		if fa, ok := e.(*syntax.FieldAccess); ok {
			if id, ok := fa.Target.(*syntax.Ident); ok && id.Name == "SameSite" {
				sameSiteNone, sameSiteKnown = fa.Field == "AnySite", true
			}
		}
	}
	secure, secureKnown := true, true
	if e, ok := fields["sentOverPlainHttp"]; ok && !isNoneLit(e) {
		var plain bool
		plain, secureKnown = constBool(e)
		secure = !plain
	}
	if !secureKnown || !sameSiteKnown {
		return
	}
	if sameSiteNone && !secure {
		report("sameSite", "cookie %s", httpspec.CookieRuleProblem("", true, false, false, "/"))
		return
	}
	if nameKnown && pathKnown && domainKnown {
		if p := httpspec.CookieRuleProblem(name, sameSiteNone, secure, hasDomain, path); p != "" {
			report("name", "cookie %q %s", name, p)
		}
	}
}

// cookieFieldOrder is the prelude Cookie's fields, for a positional
// construction.
var cookieFieldOrder = []string{"name", "value", "maxAge", "path", "domain", "sameSite", "readableByScripts", "sentOverPlainHttp"}
