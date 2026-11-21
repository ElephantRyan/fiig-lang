package adapter

import (
	"context"

	"fiig-lang/compiler/runtime"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// principalStep is one principal a route needs settled (ADR 0137): its
// declaration, and the calls to its binder and refusal, built once. The
// binder is called with its input bound to inputName, and the refusal with
// the binder's error bound to refusedName, in the adapter's package, the
// same way a response arm's call to a conversion is evaluated.
type principalStep struct {
	decl    *syntax.PrincipalDecl
	binder  *syntax.CallExpr
	refusal *syntax.CallExpr
}

// Names no fiig source can bind, so the synthetic calls can't capture one.
const (
	inputName   = "$principal"
	refusedName = "$refused"
)

// principalsFor is the principals route needs, in declaration order: the
// ones it takes, and each one they are settled from (§2). A public route,
// or an adapter with none, needs none.
func principalsFor(d *syntax.AdapterDecl, route *syntax.Route) []principalStep {
	if len(d.Principals) == 0 || route.Public {
		return nil
	}
	byName := make(map[string]*syntax.PrincipalDecl, len(d.Principals))
	for _, pr := range d.Principals {
		byName[pr.Name] = pr
	}
	needed := map[string]bool{}
	var need func(name string)
	need = func(name string) {
		pr := byName[name]
		if pr == nil || needed[name] {
			return
		}
		needed[name] = true
		if pr.From != nil {
			need(pr.From.Name)
		}
	}
	for _, a := range route.Args {
		if a.Principal {
			need(a.Name)
		}
	}
	var steps []principalStep
	for _, pr := range d.Principals {
		if !needed[pr.Name] {
			continue
		}
		steps = append(steps, principalStep{
			decl:    pr,
			binder:  pr.By.Call(&syntax.Ident{Name: inputName}),
			refusal: pr.Else.Call(&syntax.Ident{Name: refusedName}),
		})
	}
	return steps
}

// settlePrincipals runs each step's binder once, in order (§4). It returns
// the settled values by name, or the refusal's response for the first
// binder that refused; a fault is a defect in a binder or refusal.
func settlePrincipals(ctx context.Context, in *runtime.Interp, steps []principalStep, req request) (map[string]runtime.Value, *Response, *runtime.Fault) {
	settled := make(map[string]runtime.Value, len(steps))
	for _, s := range steps {
		var input runtime.Value = runtime.None{}
		switch {
		case s.decl.From != nil:
			input = settled[s.decl.From.Name]
		case s.decl.Source == "cookie":
			if v, ok := req.cookie(s.decl.SourceName); ok {
				input = runtime.Str(v)
			}
		case s.decl.Source == "header":
			if v, ok := req.header(s.decl.SourceName); ok {
				input = runtime.Str(v)
			}
		}
		env := runtime.NewEnv()
		env.Bind(inputName, input)
		result, fault := in.EvalExpr(ctx, s.binder, env)
		if fault != nil {
			return nil, nil, fault
		}
		rv, ok := result.(runtime.Result)
		if !ok {
			return nil, nil, runtime.Faultf("principal %s's workflow %s returned %s, not a Result", s.decl.Name, s.decl.By.Name(), result)
		}
		if rv.Ok {
			settled[s.decl.Name] = rv.Value
			continue
		}
		env = runtime.NewEnv()
		env.Bind(refusedName, rv.Value)
		answer, fault := in.EvalExpr(ctx, s.refusal, env)
		if fault != nil {
			return nil, nil, fault
		}
		rec, ok := answer.(runtime.Record)
		if !ok {
			return nil, nil, runtime.Faultf("principal %s's refusal %s produced %s, want an HttpResponse", s.decl.Name, s.decl.Else.Name(), answer)
		}
		resp, fault := readHttpResponse(rec)
		if fault != nil {
			return nil, nil, fault
		}
		return nil, &resp, nil
	}
	return settled, nil, nil
}

// describePrincipals adds what a route's principals mean to its OpenAPI
// operation (ADR 0137 §5): each one read from a cookie or a header is an
// `apiKey` security scheme the operation requires, and each refusal's
// statuses are among its responses, as a response arm's conversion's are.
func describePrincipals(op map[string]any, steps []principalStep, media string, r resolver) {
	if len(steps) == 0 {
		return
	}
	required := map[string]any{}
	for _, s := range steps {
		if s.decl.From != nil {
			continue
		}
		if r.schemes != nil {
			r.schemes[s.decl.SourceName] = map[string]any{"type": "apiKey", "in": s.decl.Source, "name": s.decl.SourceName}
		}
		required[s.decl.SourceName] = []any{}
	}
	if len(required) > 0 {
		op["security"] = []any{required}
	}
	responses, _ := op["responses"].(map[string]any)
	if responses == nil {
		return
	}
	for _, s := range steps {
		call := s.decl.Else.Call()
		t, _ := resolveExprType(call, r)
		if b, ok := types.IsHttpResponse(t); ok {
			t = b
		}
		built := builtResponses(call, r)
		if len(built) == 0 {
			built = []builtResponse{{code: "default"}}
		}
		for _, b := range built {
			if _, taken := responses[b.code]; !taken {
				responses[b.code] = responseObject(t, media, r)
			}
		}
	}
}
