// Package adapter executes `adapter http { ... }` and `adapter cli { ... }`
// declarations (fiig-lang.md §3.1, Milestone 5) against an already-checked
// program — the counterpart to tooling/run-cli, which executes one
// declaration directly; this package instead builds a long-running HTTP
// server or a one-shot CLI dispatcher that routes requests/commands into
// the same compiler/runtime.Interp. See docs/decisions/0026 for the
// default response-mapping and execution decisions this package
// implements.
package adapter

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"fiig-lang/compiler/decimal"
	"fiig-lang/compiler/runtime"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// response is what a route resolved to, independent of HTTP or CLI — the
// HTTP handler turns this into a status code + JSON body; the CLI
// dispatcher turns it into an exit code + printed JSON body.
type Response struct {
	Status  int
	Body    runtime.Value
	Headers []Header         // HTTP only: the HttpResponse's headers (ADR 0069 §3)
	Cookies []runtime.Record // HTTP only: its prelude Cookie values
}

// placeholderPattern mirrors compiler/checker's own (unexported) version —
// small enough that duplicating it here is simpler than exporting checker
// internals for one three-line regex.
var placeholderPattern = regexp.MustCompile(`\{(\w+)\}`)

func extractPlaceholders(s string) []string {
	var names []string
	for _, m := range placeholderPattern.FindAllStringSubmatch(s, -1) {
		names = append(names, m[1])
	}
	return names
}

// routePlan is computed once per route (not per request): which call
// argument is claimed by which path/query placeholder, header, or cookie,
// and which single argument (if any) is body-bound. compiler/checker's
// checkRouteBinding already guarantees this is well-formed for a checked
// program — this package trusts that rather than re-validating it.
type routePlan struct {
	pathNames  []string
	queryNames []string
	bodyArg    string // "" if this route has no body-bound argument
	bodyType   types.Type
	paramTypes map[string]types.Type
	// principals is what the route needs settled before it reads the
	// request (ADR 0137 §4), in order; nil for a public route.
	principals []principalStep
}

// planRoute reads route's workflow's parameter types: through in when
// there is one, in the workflow's own package and file imports, so a
// parameter typed by another package's model (`kernel.Money`) binds; in u
// alone otherwise (OpenAPI generation).
func planRoute(route *syntax.Route, isHTTP bool, u *types.Universe, in *runtime.Interp) (routePlan, error) {
	var plan routePlan
	if isHTTP {
		pathPart, queryPart, hasQuery := strings.Cut(route.Path, "?")
		plan.pathNames = extractPlaceholders(pathPart)
		if hasQuery {
			plan.queryNames = extractPlaceholders(queryPart)
		}
	} else {
		plan.pathNames = extractPlaceholders(route.Path)
	}

	wd, err := routeWorkflow(route, u)
	if err != nil {
		return plan, err
	}
	plan.paramTypes = make(map[string]types.Type, len(wd.Params))
	for _, p := range wd.Params {
		var pt types.Type
		var ok bool
		if in != nil {
			pt, ok = in.ParamType(wd, p.Type)
		} else {
			pt, ok = types.Resolve(p.Type, u)
		}
		if !ok {
			return plan, fmt.Errorf("parameter %q has an unresolvable type", p.Name)
		}
		plan.paramTypes[p.Name] = pt
	}

	claimed := make(map[string]bool, len(plan.pathNames)+len(plan.queryNames))
	for _, n := range plan.pathNames {
		claimed[n] = true
	}
	for _, n := range plan.queryNames {
		claimed[n] = true
	}
	for _, arg := range route.Args {
		if !claimed[arg.Name] && arg.Source == "" && !arg.Principal {
			plan.bodyArg = arg.Name
			plan.bodyType = plan.paramTypes[arg.Name]
			break
		}
	}
	return plan, nil
}

func routeWorkflow(route *syntax.Route, u *types.Universe) (*syntax.WorkflowDecl, error) {
	target, found := u.Lookup(route.Workflow.Name)
	if !found {
		return nil, fmt.Errorf("route target %q not found", route.Workflow.Name)
	}
	wd, ok := target.(*syntax.WorkflowDecl)
	if !ok {
		return nil, fmt.Errorf("route target %q is not a workflow", route.Workflow.Name)
	}
	return wd, nil
}

func workflowName(route *syntax.Route) string { return route.Workflow.Name }

// valueSource supplies a path, query, header, or cookie value by name —
// http.go and cli.go each implement this against their own request shape
// (an *http.Request, or a parsed command line).
type valueSource func(name string) (string, bool)

func noValues(string) (string, bool) { return "", false }

// request is where a route's arguments come from. The body is bodyJSON for
// a route that consumes JSON, and bodyRaw for any other type (ADR 0069 §5).
type request struct {
	path, query, header, cookie valueSource
	bodyJSON                    any
	bodyRaw                     []byte
	bodyIsJSON                  bool
	principals                  map[string]runtime.Value // settled (ADR 0137)
}

// buildArgs resolves every one of route's arguments into a runtime.Value:
// path, query, header, and cookie values decode from their text (always a
// primitive, ADR 0009/0021, ADR 0069 §2), and the one body-bound argument
// (if any) decodes from the JSON body via runtime.DecodeJSON, or binds the
// raw body as a string or bytes. A missing value is None for an argument
// typed `T | None`, and an error otherwise.
func buildArgs(route *syntax.Route, plan routePlan, req request, u *types.Universe, in *runtime.Interp) (map[string]runtime.Value, error) {
	pathSet := make(map[string]bool, len(plan.pathNames))
	for _, n := range plan.pathNames {
		pathSet[n] = true
	}
	querySet := make(map[string]bool, len(plan.queryNames))
	for _, n := range plan.queryNames {
		querySet[n] = true
	}
	args := make(map[string]runtime.Value, len(route.Args))
	for _, arg := range route.Args {
		name := arg.Name
		pt, ok := plan.paramTypes[name]
		if !ok {
			return nil, fmt.Errorf("no parameter named %q", name)
		}
		if arg.Principal {
			v, ok := req.principals[name]
			if !ok {
				return nil, fmt.Errorf("principal %q was not settled", name)
			}
			args[name] = v
			continue
		}
		var src valueSource
		var what, key string
		switch {
		case arg.Source == "header":
			src, what, key = req.header, "header", arg.SourceName
		case arg.Source == "cookie":
			src, what, key = req.cookie, "cookie", arg.SourceName
		case pathSet[name]:
			src, what, key = req.path, "path value", name
		case querySet[name]:
			src, what, key = req.query, "query value", name
		case name == plan.bodyArg:
			v, err := decodeBody(req, pt, u, in)
			if err != nil {
				return nil, fmt.Errorf("body %q: %w", name, err)
			}
			args[name] = v
			continue
		default:
			return nil, fmt.Errorf("argument %q is claimed by neither a placeholder, a header, a cookie, nor the body", name)
		}
		raw, ok := src(key)
		if !ok {
			if runtime.IsOptional(pt) {
				args[name] = runtime.None{}
				continue
			}
			return nil, fmt.Errorf("missing %s %q", what, key)
		}
		v, err := decodeString(raw, pt, u)
		if err != nil {
			return nil, fmt.Errorf("%s %q: %w", what, key, err)
		}
		args[name] = v
	}
	return args, nil
}

func isBytes(t types.Type) bool {
	p, ok := t.(types.Primitive)
	return ok && p.Name == "bytes"
}

// decodeBody binds the request body to the body-bound argument: decoded as
// JSON for a JSON route, and raw, as a string or bytes, for any other or
// for a JSON route binding bytes (ADR 0077).
func decodeBody(req request, t types.Type, u *types.Universe, in *runtime.Interp) (runtime.Value, error) {
	if req.bodyIsJSON {
		if in != nil {
			return in.DecodeBodyJSON(req.bodyJSON, t)
		}
		return runtime.DecodeJSON(req.bodyJSON, t, u)
	}
	if p, ok := t.(types.Primitive); ok && p.Name == "bytes" {
		return runtime.Bytes{B: req.bodyRaw}, nil
	}
	if !utf8.Valid(req.bodyRaw) {
		return nil, fmt.Errorf("the body isn't UTF-8 text")
	}
	return runtime.Str(req.bodyRaw), nil
}

// decodeString decodes a single path/query placeholder's raw string value
// — always a primitive (ADR 0009/0021, ADR 0055), so
// this is deliberately narrower than runtime.DecodeJSON, which also
// handles models, lists, and unions that never appear in this position.
func decodeString(raw string, t types.Type, u *types.Universe) (runtime.Value, error) {
	if runtime.IsOptional(t) {
		t = nonNone(t)
	}
	switch t := t.(type) {
	case types.Primitive:
		switch t.Name {
		case "int":
			n, err := strconv.ParseInt(raw, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("expected an int, got %q", raw)
			}
			return runtime.Int(n), nil
		case "float":
			// ParseFloat also reads "NaN" and "Inf", which NewFloat refuses.
			f, err := strconv.ParseFloat(raw, 64)
			v, ok := runtime.NewFloat(f)
			if err != nil || !ok {
				return nil, fmt.Errorf("expected a finite float, got %q", raw)
			}
			return v, nil
		case "decimal":
			// The text number grammar a string's toDecimalOrNone reads (ADR
			// 0064 §8, widened by ADR 0067 §7).
			d, ok := decimal.ParseText(raw)
			if !ok {
				return nil, fmt.Errorf("expected a decimal of at most 34 digits written in full, got %q", raw)
			}
			return runtime.Decimal{Decimal: d}, nil
		case "string":
			return runtime.Str(raw), nil
		case "bool":
			b, err := strconv.ParseBool(raw)
			if err != nil {
				return nil, fmt.Errorf("expected a boolean, got %q", raw)
			}
			return runtime.Bool(b), nil
		}
		return nil, fmt.Errorf("unsupported primitive %q", t.Name)
	case types.Named:
		// A payload-free enum, by its text form (ADR 0080 §3-§4): exact,
		// and an unknown value names the ones it may be.
		if d, ok := t.Decl.(*syntax.EnumDecl); ok && !d.HasPayload() {
			if v, ok := d.VariantByText(raw); ok {
				return runtime.EnumValue{TypeName: d.Name, Variant: v.Name, Decl: d}, nil
			}
			return nil, fmt.Errorf("%q isn't a %s; expected %s", raw, d.Name, orList(d.Texts()))
		}
		return nil, fmt.Errorf("%s cannot be decoded from a path/query value", t.String())
	default:
		return nil, fmt.Errorf("%s cannot be decoded from a path/query value", t.String())
	}
}

// orList is "a", "a or b", or "a, b, or c".
func orList(items []string) string {
	switch len(items) {
	case 0:
		return "nothing"
	case 1:
		return items[0]
	case 2:
		return items[0] + " or " + items[1]
	}
	return strings.Join(items[:len(items)-1], ", ") + ", or " + items[len(items)-1]
}

// callRoute invokes route's target workflow and resolves its result into
// a response — either via an explicit response-mapping arm (ADR 0017) or
// the default convention (ADR 0026).
func callRoute(ctx context.Context, in *runtime.Interp, route *syntax.Route, args map[string]runtime.Value) (Response, *runtime.Fault) {
	result, fault := in.Call(ctx, workflowName(route), args)
	if fault != nil {
		return Response{}, fault
	}
	if route.Mapping != nil {
		return evalMapping(ctx, in, route.Mapping, result)
	}
	return defaultResponse(result), nil
}

// defaultResponse is ADR 0026's no-mapping convention.
func defaultResponse(result runtime.Value) Response {
	rv, ok := result.(runtime.Result)
	if !ok {
		return Response{Status: 200, Body: result}
	}
	if rv.Ok {
		return Response{Status: 200, Body: rv.Value}
	}
	return Response{Status: 500, Body: rv.Value}
}

func evalMapping(ctx context.Context, in *runtime.Interp, arms []syntax.ResponseArm, result runtime.Value) (Response, *runtime.Fault) {
	for _, arm := range arms {
		env := runtime.NewEnv()
		if !runtime.MatchPattern(arm.Pattern, result, env) {
			continue
		}
		return evalResponseValue(ctx, in, arm.Value, env)
	}
	return Response{}, runtime.Faultf("no response-mapping arm matched %s", result)
}

func evalResponseValue(ctx context.Context, in *runtime.Interp, rv syntax.ResponseValue, env *runtime.Env) (Response, *runtime.Fault) {
	switch rv := rv.(type) {
	case *syntax.StatusResponse:
		return evalCodeAndBody(ctx, in, rv.Code, rv.Body, env)
	case *syntax.ExitResponse:
		return evalCodeAndBody(ctx, in, rv.Code, rv.Body, env)
	case *syntax.ExprResponse:
		val, fault := in.EvalExpr(ctx, rv.Value, env)
		if fault != nil {
			return Response{}, fault
		}
		rec, ok := val.(runtime.Record)
		if !ok {
			return Response{}, runtime.Faultf("response-mapping expression produced %s, want an HttpResponse", val)
		}
		return readHttpResponse(rec)
	default:
		return Response{}, runtime.Faultf("unsupported response value %T", rv)
	}
}

func evalCodeAndBody(ctx context.Context, in *runtime.Interp, code, body syntax.Expr, env *runtime.Env) (Response, *runtime.Fault) {
	codeVal, fault := in.EvalExpr(ctx, code, env)
	if fault != nil {
		return Response{}, fault
	}
	n, ok := codeVal.(runtime.Int)
	if !ok {
		return Response{}, runtime.Faultf("response code did not evaluate to an int (got %s)", codeVal)
	}
	if body == nil {
		return Response{Status: int(n), Body: runtime.None{}}, nil
	}
	bodyVal, fault := in.EvalExpr(ctx, body, env)
	if fault != nil {
		return Response{}, fault
	}
	return Response{Status: int(n), Body: bodyVal}, nil
}

// nonNone is `T | None` without its None.
func nonNone(t types.Type) types.Type {
	u, ok := t.(types.Union)
	if !ok {
		return t
	}
	var rest []types.Type
	for _, m := range u.Members {
		if _, isNone := m.(types.NoneType); !isNone {
			rest = append(rest, m)
		}
	}
	if len(rest) == 1 {
		return rest[0]
	}
	return types.Union{Members: rest}
}
