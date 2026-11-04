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

	"fiig-lang/compiler/decimal"
	"fiig-lang/compiler/runtime"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// response is what a route resolved to, independent of HTTP or CLI — the
// HTTP handler turns this into a status code + JSON body; the CLI
// dispatcher turns it into an exit code + printed JSON body.
type Response struct {
	Status int
	Body   runtime.Value
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
// argument is claimed by which path/query placeholder, and which single
// argument (if any) is body-bound. compiler/checker.checkRouteBinding
// already guarantees this is well-formed for a checked program — this
// package trusts that rather than re-validating it.
type routePlan struct {
	pathNames  []string
	queryNames []string
	bodyArg    string // "" if this route has no body-bound argument
	bodyType   types.Type
}

func planRoute(route *syntax.Route, isHTTP bool, u *types.Universe) (routePlan, error) {
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

	claimed := make(map[string]bool, len(plan.pathNames)+len(plan.queryNames))
	for _, n := range plan.pathNames {
		claimed[n] = true
	}
	for _, n := range plan.queryNames {
		claimed[n] = true
	}
	for _, arg := range route.Target.Args {
		if arg.Name != "" && !claimed[arg.Name] {
			plan.bodyArg = arg.Name
			break
		}
	}
	if plan.bodyArg != "" {
		target, found := u.Lookup(workflowName(route))
		if !found {
			return plan, fmt.Errorf("route target %q not found", workflowName(route))
		}
		wd, ok := target.(*syntax.WorkflowDecl)
		if !ok {
			return plan, fmt.Errorf("route target %q is not a workflow", workflowName(route))
		}
		for _, p := range wd.Params {
			if p.Name == plan.bodyArg {
				bt, ok := types.Resolve(p.Type, u)
				if !ok {
					return plan, fmt.Errorf("parameter %q has an unresolvable type", p.Name)
				}
				plan.bodyType = bt
				break
			}
		}
	}
	return plan, nil
}

func workflowName(route *syntax.Route) string {
	ident, ok := route.Target.Callee.(*syntax.Ident)
	if !ok {
		return ""
	}
	return ident.Name
}

// valueSource supplies a path or query placeholder's raw string value by
// name — http.go and cli.go each implement this against their own request
// shape (an *http.Request, or a parsed command line).
type valueSource func(name string) (string, bool)

// buildArgs resolves every one of route's call arguments into a
// runtime.Value: path/query placeholders decode from pathVal/queryVal
// (plain strings — ADR 0009/0021: always a primitive,
// never a model), and the one body-bound argument (if any) decodes from
// bodyJSON via runtime.DecodeJSON.
func buildArgs(route *syntax.Route, plan routePlan, pathVal, queryVal valueSource, bodyJSON any, u *types.Universe) (map[string]runtime.Value, error) {
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

	args := make(map[string]runtime.Value, len(route.Target.Args))
	pathSet := make(map[string]bool, len(plan.pathNames))
	for _, n := range plan.pathNames {
		pathSet[n] = true
	}
	querySet := make(map[string]bool, len(plan.queryNames))
	for _, n := range plan.queryNames {
		querySet[n] = true
	}
	for _, arg := range route.Target.Args {
		name := arg.Name
		pt, ok := paramType[name]
		if !ok {
			return nil, fmt.Errorf("no parameter named %q", name)
		}
		switch {
		case pathSet[name]:
			raw, ok := pathVal(name)
			if !ok {
				return nil, fmt.Errorf("missing path value %q", name)
			}
			v, err := decodeString(raw, pt, u)
			if err != nil {
				return nil, fmt.Errorf("path %q: %w", name, err)
			}
			args[name] = v
		case querySet[name]:
			raw, ok := queryVal(name)
			if !ok {
				return nil, fmt.Errorf("missing query value %q", name)
			}
			v, err := decodeString(raw, pt, u)
			if err != nil {
				return nil, fmt.Errorf("query %q: %w", name, err)
			}
			args[name] = v
		case name == plan.bodyArg:
			v, err := runtime.DecodeJSON(bodyJSON, pt, u)
			if err != nil {
				return nil, fmt.Errorf("body %q: %w", name, err)
			}
			args[name] = v
		default:
			return nil, fmt.Errorf("argument %q is claimed by neither a placeholder nor the body", name)
		}
	}
	return args, nil
}

// decodeString decodes a single path/query placeholder's raw string value
// — always a primitive (ADR 0009/0021, ADR 0055), so
// this is deliberately narrower than runtime.DecodeJSON, which also
// handles models, lists, and unions that never appear in this position.
func decodeString(raw string, t types.Type, u *types.Universe) (runtime.Value, error) {
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
	default:
		return nil, fmt.Errorf("%s cannot be decoded from a path/query value", t.String())
	}
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
			return Response{}, runtime.Faultf("response-mapping expression produced %s, want a record with a status field", val)
		}
		statusVal, ok := rec.Fields["status"].(runtime.Int)
		if !ok {
			return Response{}, runtime.Faultf("response-mapping expression's record has no int \"status\" field")
		}
		body := rec.Fields["body"]
		if body == nil {
			body = runtime.None{}
		}
		return Response{Status: int(statusVal), Body: body}, nil
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
