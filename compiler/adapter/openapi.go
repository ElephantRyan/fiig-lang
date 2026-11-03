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
// This isn't a complete, fully faithful OpenAPI generator — enum variants
// collapse to a generic object schema rather than a discriminated union,
// and an ExprResponse arm (a shared conversion call whose actual status
// code is only known once it runs) is described under PATCH's "default"
// response slot rather than a specific status. Both are honest
// simplifications, not silent gaps: good enough to document and exercise
// real clients against, not a claim of exhaustive fidelity.
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
	target, found := u.Lookup(workflowName(route))
	if !found {
		return nil, fmt.Errorf("route target %q not found", workflowName(route))
	}
	wd, ok := target.(*syntax.WorkflowDecl)
	if !ok {
		return nil, fmt.Errorf("route target %q is not a workflow", workflowName(route))
	}
	paramType := make(map[string]types.Type, len(wd.Params))
	for _, p := range wd.Params {
		pt, ok := types.Resolve(p.Type, u)
		if !ok {
			return nil, fmt.Errorf("parameter %q has an unresolvable type", p.Name)
		}
		paramType[p.Name] = pt
	}

	op := map[string]any{"operationId": workflowName(route)}

	var params []any
	for _, n := range plan.pathNames {
		params = append(params, map[string]any{"name": n, "in": "path", "required": true, "schema": schemaFor(paramType[n], u)})
	}
	for _, n := range plan.queryNames {
		params = append(params, map[string]any{"name": n, "in": "query", "required": true, "schema": schemaFor(paramType[n], u)})
	}
	if len(params) > 0 {
		op["parameters"] = params
	}
	if plan.bodyArg != "" {
		op["requestBody"] = map[string]any{
			"required": true,
			"content":  map[string]any{"application/json": map[string]any{"schema": schemaFor(paramType[plan.bodyArg], u)}},
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

	responses := map[string]any{}
	if route.Mapping == nil {
		if isResult {
			responses["200"] = responseObject(okType, u)
			responses["500"] = responseObject(errType, u)
		} else {
			responses["200"] = responseObject(retType, u)
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
			code, _, hasBody := statusAndBody(v)
			if hasBody {
				responses[code] = responseObject(bodyType, u)
			} else {
				responses[code] = map[string]any{"description": ""}
			}
		case *syntax.ExprResponse:
			// The actual status is only known dynamically (inside whatever
			// this expression calls) — described under "default" rather
			// than guessed at.
			exprType, _ := resolveExprType(v.Value, u)
			responses["default"] = responseObject(exprType, u)
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

func responseObject(t types.Type, u *types.Universe) map[string]any {
	return map[string]any{
		"description": "",
		"content":     map[string]any{"application/json": map[string]any{"schema": schemaFor(t, u)}},
	}
}

// schemaFor renders t as a JSON Schema fragment. Enum types collapse to a generic object rather than a discriminated
// union (package doc comment); Unknown becomes a schema-less "any".
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
		if (t.Name == "Result" || t.Name == "BatchResult") && len(t.Args) >= 2 {
			variants := make([]any, 0, 2)
			variants = append(variants, schemaFor(t.Args[0], u), schemaFor(t.Args[1], u))
			return map[string]any{"oneOf": variants}
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
			return map[string]any{"type": "object", "description": decl.Name + " (a tagged enum variant)"}
		default:
			return map[string]any{}
		}
	default:
		return map[string]any{}
	}
}
