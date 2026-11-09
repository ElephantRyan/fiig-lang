package adapter

import (
	"fmt"
	"sort"
	"strings"

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
	if d.Kind != syntax.AdapterHTTP {
		return nil, fmt.Errorf("adapter %q is not an http adapter", d.Name)
	}
	paths := map[string]any{}
	for _, route := range d.Routes {
		op, err := buildOperation(route, u)
		if err != nil {
			return nil, fmt.Errorf("route %s %q: %w", route.Verb, route.Path, err)
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
		entry[strings.ToLower(route.Verb)] = op
	}
	return map[string]any{
		"openapi": "3.0.3",
		"info":    map[string]any{"title": d.Name, "version": "0.0.0"},
		"paths":   paths,
	}, nil
}

func buildOperation(route *syntax.Route, u *types.Universe) (map[string]any, error) {
	plan, err := planRoute(route, true, u)
	if err != nil {
		return nil, err
	}
	wd, err := routeWorkflow(route, u)
	if err != nil {
		return nil, err
	}
	paramType := plan.paramTypes

	op := map[string]any{"operationId": workflowName(route)}

	var params []any
	for _, n := range plan.pathNames {
		params = append(params, map[string]any{"name": n, "in": "path", "required": true, "schema": schemaFor(paramType[n], u)})
	}
	for _, n := range plan.queryNames {
		params = append(params, map[string]any{"name": n, "in": "query", "required": true, "schema": schemaFor(paramType[n], u)})
	}
	// ADR 0069 §1: header and cookie arguments, which OpenAPI has its own
	// `in` for. A `T | None` argument is optional.
	for _, arg := range route.Args {
		if arg.Source == "" {
			continue
		}
		pt := paramType[arg.Name]
		params = append(params, map[string]any{"name": arg.SourceName, "in": arg.Source, "required": !runtime.IsOptional(pt), "schema": schemaFor(nonNone(pt), u)})
	}
	if len(params) > 0 {
		op["parameters"] = params
	}
	if plan.bodyArg != "" {
		op["requestBody"] = map[string]any{
			"required": true,
			"content":  map[string]any{consumesOf(route): map[string]any{"schema": schemaFor(paramType[plan.bodyArg], u)}},
		}
	}

	responses, err := buildResponses(route, wd, u)
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
func buildResponses(route *syntax.Route, wd *syntax.WorkflowDecl, u *types.Universe) (map[string]any, error) {
	retType, ok := types.Resolve(wd.ReturnType, u)
	if !ok {
		return nil, fmt.Errorf("workflow %q has an unresolvable return type", wd.Name)
	}
	okType, errType, isResult := resultMembers(retType)

	media := producesOf(route)
	responses := map[string]any{}
	if route.Mapping == nil {
		if isResult {
			responses["200"] = responseObject(okType, media, u)
			responses["500"] = responseObject(errType, media, u)
		} else {
			responses["200"] = responseObject(retType, media, u)
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
		}
		switch v := arm.Value.(type) {
		case *syntax.StatusResponse, *syntax.ExitResponse:
			code, body, hasBody := statusAndBody(v)
			if hasBody {
				// A body that is a call has that call's type; a bare name
				// is the value the arm's pattern bound.
				if t, ok := resolveExprType(body, u); ok {
					bodyType = t
				}
				responses[code] = responseObject(bodyType, media, u)
			} else {
				responses[code] = map[string]any{"description": ""}
			}
		case *syntax.ExprResponse:
			// The actual status is only known dynamically (inside whatever
			// this expression calls) — described under "default" rather
			// than guessed at. Its body is HttpResponse<B>'s B (ADR 0069 §3).
			exprType, _ := resolveExprType(v.Value, u)
			if b, ok := types.IsHttpResponse(exprType); ok {
				exprType = b
			}
			responses["default"] = responseObject(exprType, media, u)
		}
	}
	return responses, nil
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
func resolveExprType(e syntax.Expr, u *types.Universe) (types.Type, bool) {
	call, ok := e.(*syntax.CallExpr)
	if !ok {
		return types.Unknown{}, false
	}
	ident, ok := call.Callee.(*syntax.Ident)
	if !ok {
		return types.Unknown{}, false
	}
	decl, found := u.Lookup(ident.Name)
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
	return types.Resolve(rt, u)
}

// responseObject describes a response whose body is t, sent as media (the
// route's `produces`, ADR 0069 §5). A body that is only ever None is no
// content at all.
func responseObject(t types.Type, media string, u *types.Universe) map[string]any {
	if _, none := t.(types.NoneType); none {
		return map[string]any{"description": ""}
	}
	return map[string]any{
		"description": "",
		"content":     map[string]any{media: map[string]any{"schema": schemaFor(t, u)}},
	}
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
func schemaFor(t types.Type, u *types.Universe) map[string]any {
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
			return map[string]any{"type": "string", "format": "binary"}
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
			s := schemaFor(nonNone[0], u)
			if hasNone {
				s["nullable"] = true
			}
			return s
		}
		variants := make([]any, len(nonNone))
		for i, m := range nonNone {
			variants[i] = schemaFor(m, u)
		}
		s := map[string]any{"oneOf": variants}
		if hasNone {
			s["nullable"] = true
		}
		return s
	case types.Generic:
		if t.Name == "List" && len(t.Args) == 1 {
			return map[string]any{"type": "array", "items": schemaFor(t.Args[0], u)}
		}
		if t.Name == "Map" && len(t.Args) == 2 && t.Args[0].String() == "string" {
			// A JSON object; other key types encode as [key, value] pairs.
			return map[string]any{"type": "object", "additionalProperties": schemaFor(t.Args[1], u)}
		}
		if t.Name == "Map" && len(t.Args) == 2 {
			pair := map[string]any{"type": "array", "prefixItems": []any{schemaFor(t.Args[0], u), schemaFor(t.Args[1], u)}}
			return map[string]any{"type": "array", "items": pair}
		}
		if t.Name == "Result" && len(t.Args) == 2 {
			return taggedUnion([]any{
				variantSchema("Ok", map[string]any{"value": schemaFor(t.Args[0], u)}, []string{"value"}),
				variantSchema("Err", map[string]any{"error": schemaFor(t.Args[1], u)}, []string{"error"}),
			})
		}
		if t.Name == "BatchResult" && len(t.Args) == 3 {
			outcome := schemaFor(types.Generic{Name: "Result", Args: t.Args[1:]}, u)
			item := map[string]any{
				"type":       "object",
				"properties": map[string]any{"key": schemaFor(t.Args[0], u), "outcome": outcome},
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
				ft, ok := types.Resolve(f.Type, u)
				if !ok {
					continue
				}
				props[f.Name] = schemaFor(ft, u)
				if !runtime.IsOptional(ft) {
					required = append(required, f.Name)
				}
			}
			sort.Strings(required)
			s := map[string]any{"type": "object", "properties": props}
			if len(required) > 0 {
				s["required"] = required
			}
			return s
		case *syntax.EnumDecl:
			return enumSchema(decl, u)
		default:
			return map[string]any{}
		}
	default:
		return map[string]any{}
	}
}

// enumSchema describes an enum as ADR 0072 sends it (§6): a string naming
// a variant when no variant carries a payload, and otherwise one object
// schema per variant, told apart by its `variant` member.
func enumSchema(d *syntax.EnumDecl, u *types.Universe) map[string]any {
	if !d.HasPayload() {
		names := make([]any, len(d.Variants))
		for i, v := range d.Variants {
			names[i] = v.Name
		}
		return map[string]any{"type": "string", "enum": names}
	}
	variants := make([]any, len(d.Variants))
	for i, v := range d.Variants {
		props := map[string]any{}
		var required []string
		for _, f := range v.Fields {
			ft, ok := types.Resolve(f.Type, u)
			if !ok {
				continue
			}
			props[f.Name] = schemaFor(ft, u)
			if !runtime.IsOptional(ft) {
				required = append(required, f.Name)
			}
		}
		sort.Strings(required)
		variants[i] = variantSchema(v.Name, props, required)
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
