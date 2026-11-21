package checker

import (
	"fmt"
	"strings"

	"fiig-lang/compiler/httpspec"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// checkAdapterRoutes is bug class B6: an adapter route must call an
// exported workflow, never a gateway (or anything else) directly. It then
// checks each route's binding (ADR 0021, ADR 0069 §1-§2) and its content
// type clauses (ADR 0069 §5).
func (c *checker) checkAdapterRoutes(d *syntax.AdapterDecl) {
	isHTTP := d.Kind == syntax.AdapterHTTP
	isStream := d.Kind == syntax.AdapterStream
	isSchedule := d.Kind == syntax.AdapterSchedule
	isStartup := d.Kind == syntax.AdapterStartup
	for _, route := range d.Routes {
		if !isStream && !isSchedule && !isStartup {
			c.checkMediaClauses(route, isHTTP)
		}
		decl, found := c.lookup(route.Workflow.Name)
		if !found {
			if !c.ambiguous(route.Workflow.Name) {
				c.reportUndefined(route.Workflow.Start, "name", route.Workflow.Name)
			}
			continue
		}
		// The workflow's name is a reference like any other, so Go to
		// Declaration and Find Usages reach it from the route.
		c.ref(route.Workflow.Start, route.Workflow.Name, decl, "")
		wd, ok := decl.(*syntax.WorkflowDecl)
		if !ok {
			c.report(route.Workflow.Start, "B6", "adapter route must call an exported workflow; %q is not one", route.Workflow.Name)
			continue
		}
		if isStream {
			c.checkStreamRoute(route, wd)
			continue
		}
		if isSchedule {
			c.checkScheduleRoute(route, wd)
			continue
		}
		if isStartup {
			c.checkStartupRoute(route, wd)
			continue
		}
		c.checkRouteBinding(route, wd, isHTTP)
		c.checkResponseArms(route, wd, isHTTP)
	}
}

// checkMediaClauses checks that `consumes` and `produces` appear only on
// HTTP routes, that every HTTP route has `produces`, and that each names
// a media type (ADR 0069 §5). Whether `consumes` belongs is
// checkRouteBinding's, since it depends on the body.
func (c *checker) checkMediaClauses(route *syntax.Route, isHTTP bool) {
	for _, mt := range []*syntax.MediaType{route.Consumes, route.Produces} {
		if mt == nil {
			continue
		}
		if !isHTTP {
			c.report(mt.Start, "", "a cli route has no content type; consumes and produces are for http routes")
			continue
		}
		if !httpspec.ValidMediaType(mt.Text) {
			c.report(mt.Start, "", "%q isn't a media type; write type/subtype, such as \"application/json\", without parameters", mt.Text)
		}
	}
	if isHTTP && route.Produces == nil {
		c.report(route.Start, "", "route %s %q has no produces clause; declare the content type it sends, such as produces \"application/json\"", route.Verb, route.Path)
	}
}

// checkRouteBinding is ADR 0021, extended by ADR 0069 §1: every path (and,
// for HTTP routes, query) placeholder must name one of the route's
// arguments, an argument may instead name the header or cookie it is read
// from, and every remaining argument is the request body, which only a
// body-carrying verb has and only one argument may be. CLI routes have no
// query region, headers, cookies, or body.
func (c *checker) checkRouteBinding(route *syntax.Route, wd *syntax.WorkflowDecl, isHTTP bool) {
	if isHTTP && !httpMethods[route.Verb] {
		c.report(route.Start, "", "unknown HTTP method %q", route.Verb)
		return
	}
	paramTypes := map[string]types.Type{}
	for _, p := range wd.Params {
		pt, ok := types.ResolveWithImports(p.Type, c.universe, universesOf(c.imports))
		if !ok {
			pt = types.Unknown{}
		}
		paramTypes[p.Name] = pt
	}

	argTypes := map[string]types.Type{}
	for _, arg := range route.Args {
		if _, dup := argTypes[arg.Name]; dup {
			c.report(arg.Start, "", "route argument %q is given twice", arg.Name)
			continue
		}
		written := c.resolve(arg.Type)
		argTypes[arg.Name] = written
		pt, ok := paramTypes[arg.Name]
		if !ok {
			c.report(arg.Start, "", "workflow %q has no parameter named %q", wd.Name, arg.Name)
			continue
		}
		if !isUnknown(written) && !isUnknown(pt) && !types.Equal(written, pt) {
			c.report(arg.Start, "A1", "route argument %q is %s, but %s's parameter is %s", arg.Name, written, wd.Name, pt)
		}
		if arg.Source != "" {
			c.checkSourcedArg(arg, written, isHTTP)
		}
	}

	var pathNames, queryNames []string
	if isHTTP {
		pathPart, queryPart, hasQuery := strings.Cut(route.Path, "?")
		pathNames = extractPlaceholders(pathPart)
		if hasQuery {
			queryNames = extractPlaceholders(queryPart)
			// ADR 0021: a query key and its placeholder are one name, the
			// parameter's, which is the key the request is read by. A key
			// that differs was accepted and silently never read
			// (abs-recommendations GAPS G32).
			for _, pair := range strings.Split(queryPart, "&") {
				key, value, ok := strings.Cut(pair, "=")
				if !ok {
					continue
				}
				if m := placeholderPattern.FindStringSubmatch(value); m != nil && m[0] == value && m[1] != key {
					c.report(route.Start, "", "query key %q takes its placeholder's name, so `%s={%s}` reads ?%s=, not ?%s=; write `%s={%s}` and name the parameter %s (ADR 0021)", key, key, m[1], m[1], key, key, key, key)
				}
			}
		}
	} else {
		pathNames = extractPlaceholders(route.Path)
	}
	claimed := map[string]bool{}
	claim := func(n, where string) {
		claimed[n] = true
		arg := routeArg(route, n)
		if arg == nil {
			c.report(route.Start, "", "%s placeholder %q does not match any parameter of this route's call", where, n)
			return
		}
		if arg.Source != "" {
			c.report(arg.Start, "", "%q is bound by a %s placeholder and from a %s; bind it once", n, where, arg.Source)
			return
		}
		from := "a " + where + " placeholder"
		if !isHTTP {
			from = "the command line"
		}
		c.checkReadFromText(*arg, argTypes[n], from)
	}
	for _, n := range pathNames {
		claim(n, "path")
	}
	for _, n := range queryNames {
		claim(n, "query")
	}

	var unclaimed []syntax.RouteArg
	for _, arg := range route.Args {
		if !claimed[arg.Name] && arg.Source == "" {
			unclaimed = append(unclaimed, arg)
		}
	}

	if !isHTTP || !bodyCarryingHTTPMethods[route.Verb] {
		for _, arg := range unclaimed {
			c.report(arg.Start, "", "%q is bound by neither a path nor a query placeholder, and this route has no request body", arg.Name)
		}
		if route.Consumes != nil && isHTTP {
			c.report(route.Consumes.Start, "", "%s %q has no request body, so it consumes nothing; remove the consumes clause", route.Verb, route.Path)
		}
		c.checkDefaultBody(route, wd)
		return
	}
	switch len(unclaimed) {
	case 0:
		if route.Consumes != nil {
			c.report(route.Consumes.Start, "", "route %s %q binds no request body, so it consumes nothing; remove the consumes clause", route.Verb, route.Path)
		}
	case 1:
		if route.Consumes == nil {
			c.report(route.Start, "", "route %s %q binds its request body to %q but has no consumes clause; declare the type it reads, such as consumes \"application/json\"", route.Verb, route.Path, unclaimed[0].Name)
		} else {
			c.checkBodyBinding(unclaimed[0], argTypes[unclaimed[0].Name], route.Consumes)
		}
	default:
		names := make([]string, len(unclaimed))
		for i, arg := range unclaimed {
			names[i] = arg.Name
		}
		c.report(route.Start, "", "ambiguous request body: %d parameters unclaimed by path/query placeholders (%s)", len(unclaimed), strings.Join(names, ", "))
	}
	c.checkDefaultBody(route, wd)
}

func routeArg(route *syntax.Route, name string) *syntax.RouteArg {
	for i := range route.Args {
		if route.Args[i].Name == name {
			return &route.Args[i]
		}
	}
	return nil
}

// checkSourcedArg checks an argument read from a header or cookie (ADR
// 0069 §1-§2): only on an HTTP route, with a path or query value's types,
// optionally `| None`, and a name that is an HTTP token.
func (c *checker) checkSourcedArg(arg syntax.RouteArg, t types.Type, isHTTP bool) {
	if !isHTTP {
		c.report(arg.Start, "", "a cli route has no %ss; `from %s` is for http routes", arg.Source, arg.Source)
		return
	}
	c.checkReadFromText(arg, t, "a "+arg.Source)
	if !httpspec.IsToken(arg.SourceName) {
		c.report(arg.Start, "", "%s name %q isn't an HTTP token (letters, digits, and !#$%%&'*+-.^_`|~)", arg.Source, arg.SourceName)
	}
}

// checkReadFromText is W4 (ADR 0080 §6): an argument read from a path
// segment, query value, header, cookie, or command-line word has a type
// text can be read as. from says where it is read from.
func (c *checker) checkReadFromText(arg syntax.RouteArg, t types.Type, from string) {
	if isUnknown(t) {
		return
	}
	bare := withoutNone(t)
	if isWireScalar(bare) || isTextEnum(bare) {
		return
	}
	if d, ok := enumOf(bare); ok {
		c.report(arg.Start, "W4", "%q is read from %s, but %s has variants with payloads, which no single text can carry; bind the fields as separate arguments, or send it in a body (ADR 0080 §6)", arg.Name, from, d.Name)
		return
	}
	c.report(arg.Start, "W4", "%q is read from %s, so it must be string, int, float, decimal, bool, or an enum without payloads, optionally | None; got %s (ADR 0080 §6)", arg.Name, from, t)
}

// isTextEnum reports whether t is an enum whose variants carry no payload,
// which binds from text by its text form (ADR 0080 §3).
func isTextEnum(t types.Type) bool {
	d, ok := enumOf(t)
	return ok && !d.HasPayload()
}

func enumOf(t types.Type) (*syntax.EnumDecl, bool) {
	n, ok := t.(types.Named)
	if !ok {
		return nil, false
	}
	d, ok := n.Decl.(*syntax.EnumDecl)
	return d, ok
}

// isWireScalar reports whether t is one of the primitives a path, query,
// header, or cookie value decodes into (ADR 0069 §2).
func isWireScalar(t types.Type) bool {
	p, ok := t.(types.Primitive)
	if !ok {
		return false
	}
	switch p.Name {
	case "string", "int", "float", "decimal", "bool":
		return true
	}
	return false
}

func isUnknown(t types.Type) bool {
	_, ok := t.(types.Unknown)
	return ok
}

// checkBodyBinding is bug class A7: a body read as JSON must bind a model,
// since a primitive has no fields to decode the body against, and binding
// one skips decoding instead of performing it. A body the route consumes
// as any other type (ADR 0069 §5) is the reverse: there is nothing for the
// adapter to decode it into, so it binds raw, as string or bytes.
func (c *checker) checkBodyBinding(arg syntax.RouteArg, t types.Type, consumes *syntax.MediaType) {
	if isUnknown(t) {
		return
	}
	if !httpspec.ValidMediaType(consumes.Text) {
		return
	}
	if !httpspec.IsJSON(consumes.Text) {
		if !isRawBody(t) {
			c.report(arg.Start, "A7", "request body parameter %q is %s, but the route consumes %q, which binds raw as string or bytes", arg.Name, t, consumes.Text)
		}
		return
	}
	// ADR 0077: bytes is the JSON body as it arrived, for a signature
	// checked before json.decode; string stays A7 (§2).
	if p, ok := t.(types.Primitive); ok && p.Name == "bytes" {
		return
	}
	if named, ok := t.(types.Named); ok {
		if _, isModel := named.Decl.(*syntax.ModelDecl); isModel {
			// A Secret is S1's, at the workflow's parameter (ADR 0071 §4).
			if why := c.jsonRefusal(t, true); why != "" && !types.HoldsSecret(t, c.resolveField) {
				c.report(arg.Start, "J1", "request body parameter %q decodes into %s: %s, which can't be decoded from JSON (ADR 0073 §8)", arg.Name, t, why)
				return
			}
			c.checkJsonOverlap(arg.Start, t, fmt.Sprintf("request body parameter %q", arg.Name))
			return
		}
	}
	c.report(arg.Start, "A7", "request body parameter %q binds to %q, which isn't a model; a primitive type has no fields to structurally decode the body against (bind bytes to read the JSON body raw, ADR 0077)", arg.Name, t)
}

func isRawBody(t types.Type) bool {
	p, ok := t.(types.Primitive)
	return ok && (p.Name == "string" || p.Name == "bytes")
}

// checkDefaultBody checks, for a route with no response mapping, that the
// bodies ADR 0026's default mapping sends fit the route's `produces` (ADR
// 0069 §5): a type other than JSON sends string or bytes, so both the Ok
// and the Err side of the workflow's Result must be one.
func (c *checker) checkDefaultBody(route *syntax.Route, wd *syntax.WorkflowDecl) {
	// J3 (ADR 0081): what a JSON route sends by ADR 0026's default
	// mapping is the workflow's result, Ok side or Err side.
	if route.Mapping == nil && route.Produces != nil && httpspec.IsJSON(route.Produces.Text) {
		if ret, ok := types.ResolveWithImports(wd.ReturnType, c.universe, universesOf(c.imports)); ok {
			c.checkJsonOverlap(route.Start, ret, fmt.Sprintf("route %s %q sends %s", route.Verb, route.Path, ret))
		}
	}
	if route.Mapping != nil || route.Produces == nil || !httpspec.ValidMediaType(route.Produces.Text) || httpspec.IsJSON(route.Produces.Text) {
		return
	}
	ret, ok := types.ResolveWithImports(wd.ReturnType, c.universe, universesOf(c.imports))
	if !ok {
		return
	}
	bodies := map[string]types.Type{"result": ret}
	if g, ok := ret.(types.Generic); ok && g.Name == "Result" && len(g.Args) == 2 {
		bodies = map[string]types.Type{"Ok value": g.Args[0], "Err value": g.Args[1]}
	}
	for _, side := range []string{"result", "Ok value", "Err value"} {
		t, ok := bodies[side]
		if !ok || isUnknown(t) || isRawBody(t) {
			continue
		}
		c.report(route.Produces.Start, "", "route produces %q, which sends a string or bytes body, but %s's %s is %s; map the response, or produce a JSON type", route.Produces.Text, wd.Name, side, t)
	}
}
