package checker

import (
	"fiig-lang/compiler/httpspec"
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
