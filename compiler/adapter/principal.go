package adapter

import (
	"context"
	"strings"

	"fiig-lang/compiler/runtime"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// principalStep is one principal a route needs settled (ADR 0137): its
// declaration, and the calls to its binder and refusal, built once. The
// binder is called with its input bound to inputName, and the refusal with
// the binder's error bound to refusedName, in the package the principal is
// declared in: the adapter's own for an inline one, pkg for a named one
// from another package (ADR 0141 §1). names are what the route takes it by,
// none for one settled only because another is settled from it (§3).
type principalStep struct {
	decl    *syntax.PrincipalDecl
	pkg     *types.Package
	from    *syntax.PrincipalDecl
	names   []string
	scheme  string // its OpenAPI security scheme's name, for a header or cookie
	binder  *syntax.CallExpr
	refusal *syntax.CallExpr
}

// Names no fiig source can bind, so the synthetic calls can't capture one.
const (
	inputName   = "$principal"
	refusedName = "$refused"
)

// principalScope is where an adapter's named principals resolve (ADR 0141
// §2): a bare name in the adapter's package, `alias.name` in one it
// imports.
type principalScope struct {
	in      *runtime.Interp
	pkg     *types.Package // the adapter's package; nil for the root
	u       *types.Universe
	adapter string
}

// resolve is the top-level principal r names, the package it is declared
// in (nil for the adapter's own), and the alias it was named through, ""
// in the adapter's own package.
func (s principalScope) resolve(r *syntax.PrincipalRef) (*syntax.PrincipalDecl, *types.Package, string) {
	name := r.Path[len(r.Path)-1]
	if len(r.Path) == 1 {
		pr, _ := lookupPrincipal(s.u, name)
		return pr, nil, ""
	}
	pkg := s.imported(r.Path[0])
	if pkg == nil {
		return nil, nil, ""
	}
	pr, _ := lookupPrincipal(pkg.Universe, name)
	return pr, pkg, r.Path[0]
}

// schemeName is the security scheme a named principal is described by,
// after the package declaring it, however an adapter spells it: `me` in
// package hearsay.sessions is sessionsMe, named as `me` there or as
// `sessions.me` (or another alias) elsewhere (Hearsay G69). One in a
// package with no path is its name alone.
func schemeName(pkg *types.Package, name string) string {
	if pkg == nil || pkg.Path == "" {
		return name
	}
	tail := pkg.Path[strings.LastIndex(pkg.Path, ".")+1:]
	return tail + capitalized(name)
}

func lookupPrincipal(u *types.Universe, name string) (*syntax.PrincipalDecl, bool) {
	if u == nil {
		return nil, false
	}
	d, found := u.Lookup(name)
	if !found {
		return nil, false
	}
	pr, ok := d.(*syntax.PrincipalDecl)
	return pr, ok
}

// imported is the package alias names from the adapter's package.
func (s principalScope) imported(alias string) *types.Package {
	if s.pkg == nil {
		if s.in == nil {
			return nil
		}
		return s.in.Imports[s.adapter][alias]
	}
	u, ok := s.pkg.Imports[alias]
	if !ok {
		return nil
	}
	for _, dep := range s.pkg.Dependencies {
		if dep.Universe == u {
			return dep
		}
	}
	if s.in != nil {
		for _, pkg := range s.in.Packages {
			if pkg.Universe == u {
				return pkg
			}
		}
	}
	return nil
}

// principalsFor is the principals route needs, each after the one it is
// settled from: the ones it takes, and each one they are settled from (§2,
// ADR 0141 §3), each once. A public route, or an adapter with none, needs
// none.
func principalsFor(d *syntax.AdapterDecl, route *syntax.Route, sc principalScope) []principalStep {
	if !d.HasPrincipals() || route.Public {
		return nil
	}
	type bound struct {
		decl   *syntax.PrincipalDecl
		pkg    *types.Package
		alias  string
		inline bool
	}
	byName := map[string]bound{}
	for _, r := range d.PrincipalRefs {
		if pr, pkg, alias := sc.resolve(r); pr != nil {
			byName[r.Local()] = bound{pr, pkg, alias, false}
		}
	}
	for _, pr := range d.Principals {
		byName[pr.Name] = bound{decl: pr, inline: true}
	}
	var steps []principalStep
	index := map[*syntax.PrincipalDecl]int{}
	var need func(b bound, name string)
	need = func(b bound, name string) {
		if i, done := index[b.decl]; done {
			if name != "" {
				steps[i].names = append(steps[i].names, name)
			}
			return
		}
		var from *syntax.PrincipalDecl
		if f := b.decl.From; f != nil {
			// An inline principal is settled from another of the adapter's;
			// a top-level one from another top-level one of its package.
			earlier, ok := byName[f.Name]
			if !b.inline {
				earlier = bound{pkg: b.pkg, alias: b.alias}
				scope := sc.u
				if b.pkg != nil {
					scope = b.pkg.Universe
				}
				earlier.decl, ok = lookupPrincipal(scope, f.Name)
			}
			if !ok || earlier.decl == nil {
				return
			}
			need(earlier, "")
			from = earlier.decl
		}
		index[b.decl] = len(steps)
		scheme := b.decl.SourceName
		if !b.inline {
			declaring := b.pkg
			if declaring == nil {
				declaring = sc.pkg
			}
			scheme = schemeName(declaring, b.decl.Name)
		}
		step := principalStep{
			decl:    b.decl,
			pkg:     b.pkg,
			from:    from,
			scheme:  scheme,
			binder:  b.decl.By.Call(&syntax.Ident{Name: inputName}),
			refusal: b.decl.Else.Call(&syntax.Ident{Name: refusedName}),
		}
		if name != "" {
			step.names = []string{name}
		}
		steps = append(steps, step)
	}
	for _, a := range route.Args {
		if b, ok := byName[a.Name]; ok && a.Principal {
			need(b, a.Name)
		}
	}
	return steps
}

func capitalized(name string) string {
	if name == "" {
		return ""
	}
	return strings.ToUpper(name[:1]) + name[1:]
}

// settlePrincipals runs each step's binder once, in order (§4). It returns
// the settled values by the names the route takes them by, or the
// refusal's response for the first binder that refused; a fault is a
// defect in a binder or refusal.
func settlePrincipals(ctx context.Context, in *runtime.Interp, steps []principalStep, req request) (map[string]runtime.Value, *Response, *runtime.Fault) {
	settled := make(map[*syntax.PrincipalDecl]runtime.Value, len(steps))
	byName := make(map[string]runtime.Value, len(steps))
	for _, s := range steps {
		var input runtime.Value = runtime.None{}
		switch {
		case s.from != nil:
			input = settled[s.from]
		case s.decl.Source == "cookie":
			if v, ok := req.cookie(s.decl.SourceName); ok {
				input = runtime.Str(v)
			}
		case s.decl.Source == "header":
			if v, ok := req.header(s.decl.SourceName); ok {
				input = runtime.Str(v)
			}
		}
		// A named principal's binder and refusal are its package's names.
		ectx := ctx
		if s.pkg != nil {
			ectx = in.EnterPackage(ctx, s.pkg)
		}
		env := runtime.NewEnv()
		env.Bind(inputName, input)
		result, fault := in.EvalExpr(ectx, s.binder, env)
		if fault != nil {
			return nil, nil, fault
		}
		rv, ok := result.(runtime.Result)
		if !ok {
			return nil, nil, runtime.Faultf("principal %s's workflow %s returned %s, not a Result", s.decl.Name, s.decl.By.Name(), result)
		}
		if rv.Ok {
			settled[s.decl] = rv.Value
			for _, name := range s.names {
				byName[name] = rv.Value
			}
			continue
		}
		env = runtime.NewEnv()
		env.Bind(refusedName, rv.Value)
		answer, fault := in.EvalExpr(ectx, s.refusal, env)
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
	return byName, nil, nil
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
		// A named principal's scheme is named after it, so every adapter
		// naming it shares one (ADR 0141 §5).
		if r.schemes != nil {
			r.schemes[s.scheme] = map[string]any{"type": "apiKey", "in": s.decl.Source, "name": s.decl.SourceName}
		}
		required[s.scheme] = []any{}
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
		// A named principal's refusal resolves in its own package.
		pr := r
		if s.pkg != nil {
			pr.u, pr.imports = s.pkg.Universe, s.pkg.Imports
		}
		t, _ := resolveExprType(call, pr)
		if b, ok := types.IsHttpResponse(t); ok {
			t = b
		}
		built := builtResponses(call, pr)
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
