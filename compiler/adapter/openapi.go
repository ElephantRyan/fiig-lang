package adapter

import (
	"fmt"
	"sort"
	"strings"

	"fiig-lang/compiler/httpspec"
	"fiig-lang/compiler/runtime"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// GenerateOpenAPI builds an OpenAPI 3.0.3 document (as a plain
// JSON-able map, same convention as runtime.EncodeJSON) describing every
// route in d — Milestone 5's "generated OpenAPI from endpoint types and
// mappings" deliverable. Parameters and request bodies come straight from
// the same checked parameter types HTTPHandler decodes against; response
// schemas come from the target workflow's own declared return type (its
// Result<S, E> Ok/Err members, when it has one) and, for an explicit
// response-mapping arm, from that arm's own pattern/value shape.
//
// This isn't a complete, fully faithful OpenAPI generator — an
// ExprResponse arm (a shared conversion call whose actual status code is
// only known once it runs) is described under PATCH's "default" response
// slot rather than a specific status. That is an honest simplification,
// not a silent gap: good enough to document and exercise real clients
// against, not a claim of exhaustive fidelity. Enums and Results are
// described in the shapes ADR 0072 sends them in.
func GenerateOpenAPI(d *syntax.AdapterDecl, u *types.Universe) (map[string]any, error) {
	return GenerateOpenAPIFor([]Served{{Decl: d}}, u, nil)
}

// GenerateOpenAPIFor describes every adapter `fiig serve` would serve
// on one listener (ADR 0079 §6) in one document: their routes merged by
// path, a dependency's in its own package. With in, each declaration's
// types resolve where it is declared, its imports included, as the
// handler does (so a body typed kernel.Money is described); without
// one, in u alone.
func GenerateOpenAPIFor(adapters []Served, u *types.Universe, in *runtime.Interp) (map[string]any, error) {
	paths := map[string]any{}
	schemes := map[string]any{}
	var names []string
	for _, a := range adapters {
		d := a.Decl
		if d.Kind != syntax.AdapterHTTP {
			return nil, fmt.Errorf("adapter %q is not an http adapter", a.Name())
		}
		names = append(names, a.Name())
		r := resolver{u: u, in: in, schemes: schemes}
		if a.Package != nil {
			r.u = a.Package.Universe
			r.imports = a.Package.Imports
		} else if in != nil {
			r.imports = map[string]*types.Universe{}
			for alias, pkg := range in.Imports[d.Name] {
				r.imports[alias] = pkg.Universe
			}
		}
		for _, route := range d.Routes {
			op, err := buildOperation(route, r)
			if err == nil {
				describePrincipals(op, principalsFor(d, route), producesOf(route), r)
			}
			if err != nil {
				return nil, fmt.Errorf("adapter %s, route %s %q: %w", a.Name(), route.Verb, route.Path, err)
			}
			// OpenAPI path templating already uses `{name}` — identical to
			// this grammar's own placeholder syntax (grammar.md's own note),
			// so the path string needs no translation at all.
			pathPart, _, _ := strings.Cut(route.Path, "?")
			entry, ok := paths[pathPart].(map[string]any)
			if !ok {
				entry = map[string]any{}
				paths[pathPart] = entry
			}
			method := strings.ToLower(route.Verb)
			if _, taken := entry[method]; taken {
				return nil, fmt.Errorf("two routes serve %s %q; give each domain its own path prefix (ADR 0079 §6)", route.Verb, pathPart)
			}
			entry[method] = op
		}
	}
	doc := map[string]any{
		"openapi": "3.0.3",
		"info":    map[string]any{"title": strings.Join(names, ", "), "version": "0.0.0"},
		"paths":   paths,
	}
	if len(schemes) > 0 {
		doc["components"] = map[string]any{"securitySchemes": schemes}
	}
	return doc, nil
}

// resolver resolves the types the document describes: in u, the package
// the adapter is declared in, and through in, when there is one, in the
// package and file that declare each type's owner.
type resolver struct {
	u  *types.Universe
	in *runtime.Interp
	// imports is the adapter's package's import aliases, for a response
	// built by another package's conversion (`common.apiErrorResponse(e)`).
	imports map[string]*types.Universe
	// schemes collects the document's security schemes: one per cookie a
	// route reads (ADR 0098 §2).
	schemes map[string]any
}

// callee is the declaration a call names: a bare name in the adapter's
// package, or `alias.name` in an imported one (abs-recommendations G42).
func (r resolver) callee(call *syntax.CallExpr) (syntax.Decl, bool) {
	switch c := call.Callee.(type) {
	case *syntax.Ident:
		return r.u.Lookup(c.Name)
	case *syntax.FieldAccess:
		if target, ok := c.Target.(*syntax.Ident); ok {
			if u, ok := r.imports[target.Name]; ok && u != nil {
				return u.Lookup(c.Field)
			}
		}
	}
	return nil, false
}

// decl resolves t, written in owner's declaration.
func (r resolver) decl(owner syntax.Decl, t syntax.Type) (types.Type, bool) {
	if r.in != nil {
		return r.in.ParamType(owner, t)
	}
	return types.Resolve(t, r.u)
}

func buildOperation(route *syntax.Route, r resolver) (map[string]any, error) {
	plan, err := planRoute(route, true, r.u, r.in)
	if err != nil {
		return nil, err
	}
	wd, err := routeWorkflow(route, r.u)
	if err != nil {
		return nil, err
	}
	paramType := plan.paramTypes

	op := map[string]any{"operationId": workflowName(route)}

	var params []any
	for _, n := range plan.pathNames {
		params = append(params, map[string]any{"name": n, "in": "path", "required": true, "schema": schemaFor(paramType[n], r)})
	}
	for _, n := range plan.queryNames {
		pt := paramType[n]
		params = append(params, map[string]any{"name": n, "in": "query", "required": !runtime.IsOptional(pt), "schema": schemaFor(nonNone(pt), r)})
	}
	// ADR 0069 §1: header and cookie arguments, which OpenAPI has its own
	// `in` for. A `T | None` argument is optional.
	var security []any
	for _, arg := range route.Args {
		if arg.Source == "" {
			continue
		}
		pt := paramType[arg.Name]
		if arg.Source == "cookie" && r.schemes != nil {
			// ADR 0098 §2: a browser sends cookies by itself, and its code
			// can't set one (Cookie is a forbidden header in fetch), so a
			// cookie is a security scheme, not a parameter a client fills.
			r.schemes[arg.SourceName] = map[string]any{"type": "apiKey", "in": "cookie", "name": arg.SourceName}
			security = append(security, map[string]any{arg.SourceName: []any{}})
			if runtime.IsOptional(pt) {
				security = append(security, map[string]any{})
			}
			continue
		}
		params = append(params, map[string]any{"name": arg.SourceName, "in": arg.Source, "required": !runtime.IsOptional(pt), "schema": schemaFor(nonNone(pt), r)})
	}
	if len(security) > 0 {
		op["security"] = security
	}
	if len(params) > 0 {
		op["parameters"] = params
	}
	if plan.bodyArg != "" {
		op["requestBody"] = map[string]any{
			"required": true,
			"content":  map[string]any{consumesOf(route): map[string]any{"schema": bodySchema(paramType[plan.bodyArg], consumesOf(route), r)}},
		}
	}

	responses, err := buildResponses(route, wd, r)
	if err != nil {
		return nil, err
	}
	op["responses"] = responses
	return op, nil
}

// buildResponses derives an OpenAPI "responses" object either from an
// explicit response-mapping (ADR 0017) or from ADR 0026's default
// (Ok -> 200, Err -> 500), using the workflow's own Result<S, E> return
// type to know what each status's body actually is.
func buildResponses(route *syntax.Route, wd *syntax.WorkflowDecl, r resolver) (map[string]any, error) {
	retType, ok := r.decl(wd, wd.ReturnType)
	if !ok {
		return nil, fmt.Errorf("workflow %q has an unresolvable return type", wd.Name)
	}
	okType, errType, isResult := resultMembers(retType)

	media := producesOf(route)
	responses := map[string]any{}
	if route.Mapping == nil {
		if isResult {
			responses["200"] = responseObject(okType, media, r)
			responses["500"] = responseObject(errType, media, r)
		} else {
			responses["200"] = responseObject(retType, media, r)
		}
		return responses, nil
	}

	for _, arm := range route.Mapping {
		armOk, armErr := classifyPattern(arm.Pattern)
		var bodyType types.Type = types.Unknown{}
		if armOk && isResult {
			bodyType = okType
		} else if armErr && isResult {
			bodyType = errType
		} else if !isResult {
			// A workflow that returns a plain value: the arm's pattern binds
			// that value (`status -> 200 status`), abs-recommendations G38.
			bodyType = retType
		}
		switch v := arm.Value.(type) {
		case *syntax.StatusResponse, *syntax.ExitResponse:
			code, body, hasBody := statusAndBody(v)
			if hasBody {
				// A body that is a call has that call's type; a bare name
				// is the value the arm's pattern bound.
				if t, ok := resolveExprType(body, r); ok {
					bodyType = t
				}
				responses[code] = responseObject(bodyType, media, r)
			} else {
				responses[code] = map[string]any{"description": ""}
			}
		case *syntax.ExprResponse:
			// The body is HttpResponse<B>'s B (ADR 0069 §3). The status is
			// the conversion's: each constant `status:` it builds its
			// response with (ADR 0098 §1), else 2XX for an Ok arm and
			// "default" for any other, never one arm over another's.
			exprType, _ := resolveExprType(v.Value, r)
			if b, ok := types.IsHttpResponse(exprType); ok {
				exprType = b
			}
			built := builtResponses(v.Value, r)
			if len(built) == 0 {
				code := "default"
				if armOk {
					code = "2XX"
				}
				built = []builtResponse{{code: code}}
			}
			for _, b := range built {
				if _, taken := responses[b.code]; taken {
					continue
				}
				obj := responseObject(exprType, media, r)
				if b.redirect {
					obj = map[string]any{"description": "a redirect"}
					if b.location {
						obj["headers"] = map[string]any{"Location": map[string]any{"schema": map[string]any{"type": "string"}}}
					}
				}
				responses[b.code] = obj
			}
		}
	}
	return responses, nil
}

// builtResponse is one response a conversion builds with a constant
// status.
type builtResponse struct {
	code     string
	redirect bool // a 3xx, which has no body to describe
	location bool // its headers name a Location
}

// builtResponses reads the statuses of the HttpResponse(...) constructions
// in the body of the declaration e calls, when each is a constant (ADR
// 0098 §1): `return HttpResponse(status: 302, headers: ["Location": ...])`
// is a 302 with a Location. A computed status gives nothing to read.
func builtResponses(e syntax.Expr, r resolver) []builtResponse {
	out, computed := builtResponsesIn(e, r, map[syntax.Decl]bool{})
	if computed {
		// One status known and another not: describing only the known one
		// would hide the rest.
		return nil
	}
	return out
}

// builtResponsesIn is builtResponses for the declaration e calls, also
// reading each conversion that one calls in turn: `RatingError.Session(c)
// -> sessions.identityErrorResponse(c)` answers with that conversion's 401
// (Hearsay G57). visiting stops a cycle. computed reports a status that
// isn't a constant anywhere it reached.
func builtResponsesIn(e syntax.Expr, r resolver, visiting map[syntax.Decl]bool) (out []builtResponse, computed bool) {
	call, ok := e.(*syntax.CallExpr)
	if !ok {
		return nil, false
	}
	decl, found := r.callee(call)
	if !found || visiting[decl] {
		return nil, false
	}
	visiting[decl] = true
	var body *syntax.Block
	switch d := decl.(type) {
	case *syntax.ConversionDecl:
		body = d.Body
	case *syntax.RuleDecl:
		body = d.Body
	}
	if body == nil {
		return nil, false
	}
	seen := map[string]bool{}
	syntax.WalkBlockExprs(body, func(x syntax.Expr) {
		c, ok := x.(*syntax.CallExpr)
		if !ok {
			return
		}
		if id, ok := c.Callee.(*syntax.Ident); !ok || id.Name != types.HttpResponseName {
			nested, nestedComputed := builtResponsesIn(c, r, visiting)
			computed = computed || nestedComputed
			for _, b := range nested {
				if !seen[b.code] {
					seen[b.code] = true
					out = append(out, b)
				}
			}
			return
		}
		var b builtResponse
		for _, a := range c.Args {
			switch a.Name {
			case "status":
				lit, ok := a.Value.(*syntax.IntLit)
				if !ok {
					computed = true
					return
				}
				b.code = lit.Text
			case "headers":
				if m, ok := a.Value.(*syntax.MapLit); ok {
					for _, en := range m.Entries {
						if s, ok := en.Key.(*syntax.StringLit); ok && strings.EqualFold(stringLitText(s), "Location") {
							b.location = true
						}
					}
				}
			}
		}
		if b.code == "" || seen[b.code] {
			return
		}
		seen[b.code] = true
		b.redirect = strings.HasPrefix(b.code, "3")
		out = append(out, b)
	})
	return out, computed
}

func statusAndBody(v syntax.ResponseValue) (code string, body syntax.Expr, hasBody bool) {
	switch v := v.(type) {
	case *syntax.StatusResponse:
		return intLitText(v.Code), v.Body, v.Body != nil
	case *syntax.ExitResponse:
		return intLitText(v.Code), v.Body, v.Body != nil
	default:
		return "default", nil, false
	}
}

func intLitText(e syntax.Expr) string {
	if lit, ok := e.(*syntax.IntLit); ok {
		return lit.Text
	}
	return "default"
}

// classifyPattern reports whether p is Ok-shaped or Err-shaped, the only
// two cases response-mapping arms are ever attested to use at the top
// level (`Ok(customer)`, `Err(error)`, `Err(CustomerError.NotFound(_))`).
// A 2-segment constructor pattern is always an enum-variant constructor
// in this grammar, hence treated as Err; anything else (wildcard, a bare
// catch-all name) is neither — not attested in any response-mapping arm.
func classifyPattern(p syntax.Pattern) (isOk, isErr bool) {
	cp, ok := p.(*syntax.ConstructorPattern)
	if !ok {
		return false, false
	}
	if len(cp.Path) == 1 {
		return cp.Path[0] == "Ok", cp.Path[0] == "Err"
	}
	if len(cp.Path) == 2 {
		return false, true
	}
	return false, false
}

func resultMembers(t types.Type) (ok, err types.Type, isResult bool) {
	g, isGeneric := t.(types.Generic)
	if !isGeneric || g.Name != "Result" || len(g.Args) != 2 {
		return nil, nil, false
	}
	return g.Args[0], g.Args[1], true
}

// resolveExprType resolves an ExprResponse value's own return type —
// every attested case is a bare call to a declared rule/conversion/
// activity/gateway/workflow (`customerErrorToHttpResponse(error)`); anything else (a call
// through a FieldAccess, a non-call expression) isn't attested and
// resolves to Unknown rather than guessed at.
func resolveExprType(e syntax.Expr, r resolver) (types.Type, bool) {
	call, ok := e.(*syntax.CallExpr)
	if !ok {
		return types.Unknown{}, false
	}
	decl, found := r.callee(call)
	if !found {
		return types.Unknown{}, false
	}
	var rt syntax.Type
	switch decl := decl.(type) {
	case *syntax.RuleDecl:
		rt = decl.ReturnType
	case *syntax.ConversionDecl:
		rt = decl.ReturnType
	case *syntax.ActivityDecl:
		rt = decl.ReturnType
	case *syntax.GatewayDecl:
		rt = decl.ReturnType
	case *syntax.WorkflowDecl:
		rt = decl.ReturnType
	default:
		return types.Unknown{}, false
	}
	return r.decl(decl, rt)
}

// responseObject describes a response whose body is t, sent as media (the
// route's `produces`, ADR 0069 §5). A body that is only ever None is no
// content at all.
func responseObject(t types.Type, media string, r resolver) map[string]any {
	if _, none := t.(types.NoneType); none {
		return map[string]any{"description": ""}
	}
	return map[string]any{
		"description": "",
		"content":     map[string]any{media: map[string]any{"schema": bodySchema(t, media, r)}},
	}
}

// bodySchema is schemaFor for a whole body sent as media. A whole body of
// bytes is raw: any JSON value on a JSON route, which the program built or
// decodes itself (ADR 0077 §5, ADR 0078 §4), and binary on any other.
func bodySchema(t types.Type, media string, r resolver) map[string]any {
	if !isBytes(t) {
		return schemaFor(t, r)
	}
	if httpspec.IsJSON(media) {
		return map[string]any{"description": "any JSON value"} // as JsonValue
	}
	return map[string]any{"type": "string", "format": "binary"}
}

// producesOf and consumesOf are a route's declared content types, or JSON
// for a route that hasn't declared one (the checker requires both where
// they apply).
func producesOf(route *syntax.Route) string {
	if route.Produces != nil {
		return route.Produces.Text
	}
	return "application/json"
}

func consumesOf(route *syntax.Route) string {
	if route.Consumes != nil {
		return route.Consumes.Text
	}
	return "application/json"
}

// schemaFor renders t as a JSON Schema fragment; Unknown becomes a
// schema-less "any".
func schemaFor(t types.Type, r resolver) map[string]any {
	switch t := t.(type) {
	case types.Primitive:
		switch t.Name {
		case "int":
			return map[string]any{"type": "integer"}
		case "float":
			return map[string]any{"type": "number", "format": "double"}
		case "decimal":
			// The OpenAPI format registry's "decimal": a fixed-point number,
			// so a client doesn't read it as a double (ADR 0064).
			return map[string]any{"type": "number", "format": "decimal"}
		case "string":
			return map[string]any{"type": "string"}
		case "bytes":
			// Base64 inside JSON (ADR 0078 §3); a whole raw body is
			// bodySchema's.
			return map[string]any{"type": "string", "format": "byte"}
		case "bool":
			return map[string]any{"type": "boolean"}
		}
		return map[string]any{}
	case types.NoneType:
		return map[string]any{"nullable": true}
	case types.Union:
		var nonNone []types.Type
		hasNone := false
		for _, m := range t.Members {
			if _, isNone := m.(types.NoneType); isNone {
				hasNone = true
				continue
			}
			nonNone = append(nonNone, m)
		}
		if len(nonNone) == 1 {
			s := schemaFor(nonNone[0], r)
			if hasNone {
				s["nullable"] = true
			}
			return s
		}
		variants := make([]any, len(nonNone))
		for i, m := range nonNone {
			variants[i] = schemaFor(m, r)
		}
		// oneOf says a value matches exactly one member; an enum beside
		// string can match both, so that union is anyOf (ADR 0081 §3).
		kind := "oneOf"
		if hasString(nonNone) {
			kind = "anyOf"
		}
		s := map[string]any{kind: variants}
		if hasNone {
			s["nullable"] = true
		}
		return s
	case types.Generic:
		if t.Name == "List" && len(t.Args) == 1 {
			return map[string]any{"type": "array", "items": schemaFor(t.Args[0], r)}
		}
		if t.Name == "Map" && len(t.Args) == 2 && t.Args[0].String() == "string" {
			// A JSON object; other key types encode as [key, value] pairs.
			return map[string]any{"type": "object", "additionalProperties": schemaFor(t.Args[1], r)}
		}
		if t.Name == "Map" && len(t.Args) == 2 {
			pair := map[string]any{"type": "array", "prefixItems": []any{schemaFor(t.Args[0], r), schemaFor(t.Args[1], r)}}
			return map[string]any{"type": "array", "items": pair}
		}
		if t.Name == "Result" && len(t.Args) == 2 {
			return taggedUnion([]any{
				variantSchema("Ok", map[string]any{"value": schemaFor(t.Args[0], r)}, []string{"value"}),
				variantSchema("Err", map[string]any{"error": schemaFor(t.Args[1], r)}, []string{"error"}),
			})
		}
		if t.Name == "BatchResult" && len(t.Args) == 3 {
			outcome := schemaFor(types.Generic{Name: "Result", Args: t.Args[1:]}, r)
			item := map[string]any{
				"type":       "object",
				"properties": map[string]any{"key": schemaFor(t.Args[0], r), "outcome": outcome},
				"required":   []string{"key", "outcome"},
			}
			return map[string]any{"type": "array", "items": item}
		}
		return map[string]any{}
	case types.Named:
		switch decl := t.Decl.(type) {
		case *syntax.ModelDecl:
			props := map[string]any{}
			var required []string
			for _, f := range decl.AllFields() {
				ft, ok := r.decl(decl, f.Type)
				if !ok {
					continue
				}
				props[f.JSONName()] = fieldSchema(f, ft, r)
				if !runtime.IsOptional(ft) {
					required = append(required, f.JSONName())
				}
			}
			sort.Strings(required)
			s := map[string]any{"type": "object", "properties": props}
			if len(required) > 0 {
				s["required"] = required
			}
			return s
		case *syntax.EnumDecl:
			if decl == types.JsonValueDecl {
				// Any JSON at all (ADR 0073 §4); the empty schema says so.
				return map[string]any{"description": "any JSON value"}
			}
			return enumSchema(decl, r)
		default:
			return map[string]any{}
		}
	default:
		return map[string]any{}
	}
}

// fieldSchema is schemaFor for a model or payload field: a field written
// `from json base64url` (ADR 0078 §6) describes its bytes with the OpenAPI
// format registry's "base64url" instead of "byte". OpenAPI 3.0's schema
// has no contentEncoding (3.1's JSON Schema form of the same thing), so
// the format is the 3.0 way to say it.
func fieldSchema(f syntax.Field, ft types.Type, r resolver) map[string]any {
	s := schemaFor(ft, r)
	if f.Base64URL {
		useBase64URL(s)
	}
	return s
}

// useBase64URL rewrites the bytes in s, a schema of a type J4 lets choose
// base64url (bytes through `| None`, List, and a Map's values), to
// base64url. A pair-encoded Map's key keeps the standard form, as
// writeJSONMap writes it.
func useBase64URL(s map[string]any) {
	if s["format"] == "byte" {
		s["format"] = "base64url"
	}
	for _, k := range []string{"items", "additionalProperties"} {
		if sub, ok := s[k].(map[string]any); ok {
			useBase64URL(sub)
		}
	}
	for _, k := range []string{"oneOf", "anyOf"} {
		if subs, ok := s[k].([]any); ok {
			for _, sub := range subs {
				if sub, ok := sub.(map[string]any); ok {
					useBase64URL(sub)
				}
			}
		}
	}
	if pair, ok := s["prefixItems"].([]any); ok && len(pair) == 2 {
		if value, ok := pair[1].(map[string]any); ok {
			useBase64URL(value)
		}
	}
}

func hasString(members []types.Type) bool {
	for _, m := range members {
		if p, ok := m.(types.Primitive); ok && p.Name == "string" {
			return true
		}
	}
	return false
}

// enumSchema describes an enum as ADR 0072 sends it (§6): a string naming
// a variant when no variant carries a payload, and otherwise one object
// schema per variant, told apart by its `variant` member.
func enumSchema(d *syntax.EnumDecl, r resolver) map[string]any {
	if !d.HasPayload() {
		names := make([]any, len(d.Variants))
		for i, v := range d.Variants {
			names[i] = v.Text() // ADR 0080 §5
		}
		return map[string]any{"type": "string", "enum": names}
	}
	variants := make([]any, len(d.Variants))
	for i, v := range d.Variants {
		props := map[string]any{}
		var required []string
		for _, f := range v.Fields {
			ft, ok := r.decl(d, f.Type)
			if !ok {
				continue
			}
			props[f.JSONName()] = fieldSchema(f, ft, r)
			if !runtime.IsOptional(ft) {
				required = append(required, f.JSONName())
			}
		}
		sort.Strings(required)
		variants[i] = variantSchema(v.Text(), props, required)
	}
	return taggedUnion(variants)
}

// variantSchema is one variant's object: its `variant` member, which can
// only be name, beside its payload's properties.
func variantSchema(name string, props map[string]any, required []string) map[string]any {
	props["variant"] = map[string]any{"type": "string", "enum": []any{name}}
	return map[string]any{
		"type":       "object",
		"properties": props,
		"required":   append([]string{"variant"}, required...),
	}
}

// taggedUnion is a oneOf of variant objects with `variant` as their
// discriminator.
func taggedUnion(variants []any) map[string]any {
	return map[string]any{"oneOf": variants, "discriminator": map[string]any{"propertyName": "variant"}}
}

// stringLitText is a string literal's text with any interpolation left
// out; enough to recognise a constant header name.
func stringLitText(s *syntax.StringLit) string {
	var b strings.Builder
	for _, p := range s.Parts {
		if p.Interp == nil {
			b.WriteString(p.Text)
		}
	}
	return b.String()
}
