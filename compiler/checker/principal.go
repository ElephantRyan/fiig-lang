package checker

import (
	"strings"

	"fiig-lang/compiler/httpspec"
	"fiig-lang/compiler/lexer"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// principalInfo is one of an adapter's principals as its routes see it:
// the declaration and its resolved type, and what a route's reference to it
// points at: the adapter's own member for an inline one, the top-level
// declaration for a named one (ADR 0141).
type principalInfo struct {
	decl   *syntax.PrincipalDecl
	typ    types.Type
	target syntax.Decl
	member string
}

// checkPrincipals checks an adapter's principals (ADR 0137, ADR 0141): U7
// and U8 for the ones it names, U1, U2 and U5 for its inline ones, U6 for an
// adapter that isn't http. It returns them by the name routes take them by
// (U3, U4). Named ones come first, so an inline one may be settled from one.
func (c *checker) checkPrincipals(d *syntax.AdapterDecl) map[string]principalInfo {
	if !d.HasPrincipals() {
		return nil
	}
	out := map[string]principalInfo{}
	taken := func(name string, at lexer.Pos) bool {
		if _, dup := out[name]; dup {
			c.report(at, "U8", "adapter %s has two principals named %s; rename one with `as` (ADR 0141 §2)", d.Name, name)
			return true
		}
		return false
	}
	for _, r := range d.PrincipalRefs {
		if d.Kind != syntax.AdapterHTTP {
			c.report(r.Start, "U6", "a %s adapter settles no principal (who is calling); only an http request carries one (ADR 0137 §7)", d.Kind)
			continue
		}
		pr, pkg := c.resolvePrincipalRef(r)
		if pr == nil || taken(r.Local(), r.Start) {
			continue
		}
		out[r.Local()] = principalInfo{decl: pr, typ: c.resolveIn(pkg, pr.Type), target: pr}
	}
	for _, pr := range d.Principals {
		if d.Kind != syntax.AdapterHTTP {
			c.report(pr.Start, "U6", "a %s adapter settles no principal (who is calling); only an http request carries one (ADR 0137 §7)", d.Kind)
			continue
		}
		if taken(pr.Name, pr.Start) {
			continue
		}
		var input types.Type
		if pr.From != nil {
			earlier, ok := out[pr.From.Name]
			if !ok {
				c.report(pr.From.Start, "U5", "principal %s is settled from %s, which isn't a principal declared before it in this adapter (ADR 0137 §2)", pr.Name, pr.From.Name)
				input = types.Unknown{}
			} else {
				c.ref(pr.From.Start, pr.From.Name, earlier.target, earlier.member)
				input = earlier.typ
			}
		}
		t := c.checkPrincipal(pr, input)
		out[pr.Name] = principalInfo{decl: pr, typ: t, target: d, member: pr.Name}
	}
	return out
}

// checkPrincipal is U1 and U2 for one principal, inline or top-level, and
// returns its type. input is what a `from` principal is settled from, nil
// for one read from a header or cookie.
func (c *checker) checkPrincipal(pr *syntax.PrincipalDecl, input types.Type) types.Type {
	t := c.resolve(pr.Type)
	if admitsNone(t) {
		c.report(pr.Start, "U1", "principal %s is %s; a principal that may be absent (ADR 0137 §9) isn't built yet, so settle a required one, and mark a route that needs none `public`", pr.Name, t)
	}
	if input == nil {
		if !httpspec.IsToken(pr.SourceName) {
			c.report(pr.Start, "", "%s name %q isn't an HTTP token (letters, digits, and !#$%%&'*+-.^_`|~)", pr.Source, pr.SourceName)
		}
		input = types.Union{Members: []types.Type{types.Primitive{Name: "string"}, types.NoneType{}}}
	}
	refused := c.checkBinder(pr, input, t)
	c.checkRefusal(pr, refused)
	return t
}

// resolvePrincipalRef is the top-level principal `principal x.y` names, and
// its package (nil for this one), or U7.
func (c *checker) resolvePrincipalRef(r *syntax.PrincipalRef) (*syntax.PrincipalDecl, *types.Package) {
	ref := &syntax.QualifiedRef{Span: r.Span, Path: r.Path}
	// The reference's name starts after its keyword and a space.
	ref.Start.Column += len(r.Keyword) + 1
	ref.Start.Offset += len(r.Keyword) + 1
	decl, pkg := c.resolveRef(ref, "U7")
	if decl == nil {
		return nil, nil
	}
	pr, ok := decl.(*syntax.PrincipalDecl)
	if !ok {
		c.report(r.Start, "U7", "principal %s names a %s; an adapter names a top-level principal, such as `wire me: Identity from cookie \"sid\" by signedIn else signInFirst` in its own file (ADR 0141 §2, ADR 0143)", r.Name(), syntax.DeclKindName(decl))
		return nil, nil
	}
	return pr, pkg
}

// checkTopPrincipal checks a principal declared at the top level (ADR 0141
// §1): U1 and U2 in its own package, and U9 for its `from`, which names
// another top-level principal of this package, with no cycle.
func (c *checker) checkTopPrincipal(pr *syntax.PrincipalDecl) {
	var input types.Type
	if pr.From != nil {
		earlier, ok := c.topPrincipal(pr.From.Name)
		switch {
		case !ok:
			c.report(pr.From.Start, "U9", "principal %s is settled from %s, which isn't a top-level principal of this package (ADR 0141 §3)", pr.Name, pr.From.Name)
			input = types.Unknown{}
		case c.principalCycle(pr):
			c.report(pr.From.Start, "U9", "principal %s is settled from itself, through %s; principals are settled in order, so one can't depend on another that depends on it (ADR 0141 §6)", pr.Name, pr.From.Name)
			input = types.Unknown{}
		default:
			c.ref(pr.From.Start, pr.From.Name, earlier, "")
			input = c.resolve(earlier.Type)
		}
	}
	c.checkPrincipal(pr, input)
}

// topPrincipal is this package's top-level principal named name.
func (c *checker) topPrincipal(name string) (*syntax.PrincipalDecl, bool) {
	d, found := c.lookup(name)
	if !found {
		return nil, false
	}
	pr, ok := d.(*syntax.PrincipalDecl)
	return pr, ok
}

// principalCycle reports whether following `from` from pr comes back to it.
func (c *checker) principalCycle(pr *syntax.PrincipalDecl) bool {
	seen := map[*syntax.PrincipalDecl]bool{pr: true}
	for at := pr; at.From != nil; {
		next, ok := c.topPrincipal(at.From.Name)
		if !ok {
			return false
		}
		if seen[next] {
			return next == pr
		}
		seen[next] = true
		at = next
	}
	return false
}

// resolveRef is the declaration a principal's `by` or `else` names, and the
// package it is in (nil for this one): `name` in this package, or
// `alias.name` in an imported one (ADR 0137 §1). It reports an unknown
// name under code.
func (c *checker) resolveRef(r *syntax.QualifiedRef, code string) (syntax.Decl, *types.Package) {
	if r == nil || len(r.Path) == 0 {
		return nil, nil // a missing `by` or `else`, which the parser reported
	}
	name := r.Path[len(r.Path)-1]
	switch len(r.Path) {
	case 1:
		decl, found := c.lookup(name)
		if !found {
			if !c.ambiguous(name) {
				c.report(r.Start, code, "no declaration named %s in this package; name another package's as alias.%s", name, name)
			}
			return nil, nil
		}
		c.ref(r.Start, name, decl, "")
		return decl, nil
	case 2:
		alias := r.Path[0]
		pkg, ok := c.imports[alias]
		if !ok {
			if _, bound := c.aliases[alias]; !bound {
				c.report(r.Start, code, "%s isn't an imported package; add a `use` line for the package that declares %s", alias, name)
			}
			return nil, nil
		}
		decl, found := pkg.Universe.Lookup(name)
		if !found {
			c.report(r.Start, code, "%s has no declaration named %s", alias, name)
			return nil, nil
		}
		c.refAfter(r.Start, alias, name, decl, "")
		return decl, pkg
	}
	c.report(r.Start, code, "name a declaration as name or alias.name, not %s", r.Name())
	return nil, nil
}

// checkBinder is U1: `by` names a workflow taking the principal's input
// (`string | None` from a header or cookie, or the earlier principal) and
// returning Result<T, E> for the principal's type T. It returns E, the
// error the refusal answers.
func (c *checker) checkBinder(pr *syntax.PrincipalDecl, input, t types.Type) types.Type {
	decl, pkg := c.resolveRef(pr.By, "U1")
	if decl == nil {
		return types.Unknown{}
	}
	wd, ok := decl.(*syntax.WorkflowDecl)
	if !ok {
		c.report(pr.By.Start, "U1", "principal %s is settled by %s, a %s; a principal is settled by a workflow, which decides what the request's credential means (ADR 0137 §1)", pr.Name, pr.By.Name(), syntax.DeclKindName(decl))
		return types.Unknown{}
	}
	if len(wd.Params) != 1 {
		c.report(pr.By.Start, "U1", "workflow %s takes %d parameters; a principal's workflow takes one, the %s (ADR 0137 §1)", pr.By.Name(), len(wd.Params), describeInput(pr, input))
	} else if pt := c.resolveIn(pkg, wd.Params[0].Type); !isUnknown(pt) && !isUnknown(input) && !types.Equal(pt, input) {
		c.report(pr.By.Start, "U1", "workflow %s takes %s, but principal %s gives it the %s, which is %s (ADR 0137 §1)", pr.By.Name(), pt, pr.Name, describeInput(pr, input), input)
	}
	ret := c.resolveIn(pkg, wd.ReturnType)
	g, ok := ret.(types.Generic)
	if !ok || g.Name != "Result" || len(g.Args) != 2 {
		if !isUnknown(ret) {
			c.report(pr.By.Start, "U1", "workflow %s returns %s; a principal's workflow returns Result<%s, E>, and its Err is answered by the principal's `else` (ADR 0137 §1)", pr.By.Name(), ret, t)
		}
		return types.Unknown{}
	}
	if !isUnknown(g.Args[0]) && !isUnknown(t) && !types.Equal(g.Args[0], t) {
		c.report(pr.By.Start, "U1", "workflow %s settles %s, but principal %s is %s (ADR 0137 §1)", pr.By.Name(), g.Args[0], pr.Name, t)
	}
	return g.Args[1]
}

// describeInput is what a principal's workflow is given, in words.
func describeInput(pr *syntax.PrincipalDecl, input types.Type) string {
	if pr.From != nil {
		return "principal " + pr.From.Name
	}
	return pr.Source + " " + `"` + pr.SourceName + `"` + schemeSuffix(pr)
}

// schemeSuffix is ` scheme "Bearer"` for a wire that names one (ADR 0144).
func schemeSuffix(pr *syntax.PrincipalDecl) string {
	if pr.Scheme == "" {
		return ""
	}
	return ` scheme "` + pr.Scheme + `"`
}

// checkRefusal is U2: `else` names a conversion from the binder's error to
// an HttpResponse, the one place the refusal's wire form is written.
func (c *checker) checkRefusal(pr *syntax.PrincipalDecl, refused types.Type) {
	decl, pkg := c.resolveRef(pr.Else, "U2")
	if decl == nil {
		return
	}
	cd, ok := decl.(*syntax.ConversionDecl)
	if !ok {
		c.report(pr.Else.Start, "U2", "principal %s's refusal is %s, a %s; it is a conversion from the workflow's error to an HttpResponse (ADR 0137 §1)", pr.Name, pr.Else.Name(), syntax.DeclKindName(decl))
		return
	}
	if len(cd.Params) != 1 {
		c.report(pr.Else.Start, "U2", "conversion %s takes %d parameters; a refusal takes one, the error %s's workflow returned (ADR 0137 §1)", pr.Else.Name(), len(cd.Params), pr.Name)
	} else if pt := c.resolveIn(pkg, cd.Params[0].Type); !isUnknown(pt) && !isUnknown(refused) && !types.Equal(pt, refused) {
		c.report(pr.Else.Start, "U2", "conversion %s takes %s, but %s's workflow refuses with %s (ADR 0137 §1)", pr.Else.Name(), pt, pr.By.Name(), refused)
	}
	ret := c.resolveIn(pkg, cd.ReturnType)
	if g, ok := ret.(types.Generic); !isUnknown(ret) && (!ok || g.Name != "HttpResponse") {
		c.report(pr.Else.Start, "U2", "conversion %s returns %s; a refusal returns an HttpResponse (ADR 0137 §1)", pr.Else.Name(), ret)
	}
}

// checkRoutePrincipals is U3 for one route: in an adapter that declares a
// principal, a route takes one or is `public`, and a `public` route takes
// none. With no principals, `public` says nothing, since every route is.
func (c *checker) checkRoutePrincipals(d *syntax.AdapterDecl, route *syntax.Route) {
	var takes []string
	for _, a := range route.Args {
		if a.Principal {
			takes = append(takes, a.Name)
		}
	}
	switch {
	case !d.HasPrincipals() && route.Public:
		c.report(route.Start, "U3", "route %s %q is marked public, but adapter %s declares no principal, so every route is; remove `public`", route.Verb, route.Path, d.Name)
	case !d.HasPrincipals():
	case route.Public && len(takes) > 0:
		c.report(route.Start, "U3", "route %s %q is public but takes %s; a public route is called by anyone, so it takes no principal (ADR 0137 §3)", route.Verb, route.Path, strings.Join(takes, ", "))
	case !route.Public && len(takes) == 0:
		c.report(route.Start, "U3", "route %s %q takes no principal; adapter %s settles who is calling, so pass one (%s), or mark the route `public` (ADR 0137 §3)", route.Verb, route.Path, d.Name, principalNames(d))
	}
}

func principalNames(d *syntax.AdapterDecl) string {
	return strings.Join(d.PrincipalNames(), ", ")
}
