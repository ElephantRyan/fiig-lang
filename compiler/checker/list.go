package checker

import (
	"fiig-lang/compiler/constant"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// ADR 0057: list literals and append.

// listElem is T when t is List<T>, or a union with exactly one List<T>
// member (`List<string> | None`), so a literal can take its element type
// from either.
func listElem(t types.Type) (types.Type, bool) {
	switch t := t.(type) {
	case types.Generic:
		if t.Name == "List" && len(t.Args) == 1 {
			return t.Args[0], true
		}
	case types.Union:
		var found types.Type
		for _, m := range t.Members {
			if elem, ok := listElem(m); ok {
				if found != nil {
					return nil, false
				}
				found = elem
			}
		}
		if found != nil {
			return found, true
		}
	}
	return nil, false
}

func listOf(elem types.Type) types.Type {
	return types.Generic{Name: "List", Args: []types.Type{elem}}
}

// checkListLit checks a literal against an expected type. With an
// expected List<T>, every element is checked against T, which is how a
// literal of a union is written (`xs: List<Order | None> = [order, None]`)
// and how `[]` gets its element type. Otherwise the literal's own type is
// inferred and compared like any other expression.
func (c *checker) checkListLit(lit *syntax.ListLit, expected types.Type, env map[string]types.Type) {
	if elem, ok := listElem(expected); ok {
		for _, e := range lit.Elems {
			c.checkExpected(e, elem, env)
		}
		return
	}
	if _, unknown := expected.(types.Unknown); unknown && len(lit.Elems) == 0 {
		// An unmodeled position (a capability argument) says nothing about
		// the element type, but isn't the author's missing annotation either.
		return
	}
	got := c.inferListLit(lit, env)
	if expected == nil || types.AssignableTo(got, expected) {
		return
	}
	c.report(lit.Start, "A1", "type mismatch: got %s, expected %s", got, expected)
}

// inferListLit types a literal with no expected type: List<T>, where T is
// the first element's type and every other element must have the same one.
// There is no implicit union and no numeric widening (ADR 0048). `[]` has no
// element type to infer, and is A12.
func (c *checker) inferListLit(lit *syntax.ListLit, env map[string]types.Type) types.Type {
	if len(lit.Elems) == 0 {
		c.report(lit.Start, "A12", "an empty list literal has no element type; give it one with a typed declaration, e.g. `names: List<string> = []`")
		return listOf(types.Unknown{})
	}
	// Constants take the type of the list's other elements, so `[x, 2]` is
	// a List<float> when x is a float; a list of constants alone is a
	// List<float> when any of them is fractional (ADR 0048 §2.4).
	elems := make([]types.Type, len(lit.Elems))
	var want types.Type
	for i, e := range lit.Elems {
		if !isConstant(e) {
			elems[i] = c.inferExpr(e, env)
			if want == nil {
				want = elems[i]
			}
		}
	}
	if want == nil {
		want = intType
		for _, e := range lit.Elems {
			if v, _, _ := constant.Eval(e); v.Kind == constant.Float {
				want = floatType
			}
		}
	}
	for i, e := range lit.Elems {
		if elems[i] == nil {
			elems[i], _ = c.landConstant(e, want)
		}
	}
	first := elems[0]
	if iface, ok := listInterface(elems); ok {
		// ADR 0043 §6b: `[Stripe, Square]` is a list of the one interface
		// every group in it provides.
		return listOf(types.Named{Name: iface.Name, Decl: iface})
	}
	for i, e := range lit.Elems[1:] {
		got := elems[i+1]
		if types.Equal(got, first) {
			continue
		}
		start, _ := e.(syntax.Spanned).Bounds()
		c.report(start, "A1", "list elements must share one type: got %s, expected %s (the first element's type); to mix types, declare the list's type, e.g. `xs: List<%s | %s> = [...]`", got, first, first, got)
	}
	return listOf(first)
}

// listInterface reports the interface a literal of distinct capability
// groups is a list of: the one every group provides.
func listInterface(elems []types.Type) (*syntax.InterfaceDecl, bool) {
	var groups []*syntax.CapabilityDecl
	distinct := false
	for _, t := range elems {
		g, ok := types.CapabilityGroup(t)
		if !ok {
			return nil, false
		}
		distinct = distinct || (len(groups) > 0 && g != groups[0])
		groups = append(groups, g)
	}
	if !distinct {
		return nil, false
	}
	return commonInterface(groups...)
}

// checkAppend types `append(list, element)` as (List<T>, T) -> List<T>.
// An empty literal as the list takes its element type from the element.
func (c *checker) checkAppend(e *syntax.CallExpr, env map[string]types.Type) types.Type {
	start, _ := e.Bounds()
	param, ok := c.bindBuiltinArgs("append", types.AppendParams, e.Args, start, env)
	if !ok {
		return types.Unknown{}
	}
	var list, elem syntax.Expr
	for i, a := range e.Args {
		if param[i] == 0 {
			list = a.Value
		} else {
			elem = a.Value
		}
	}
	if lit, ok := list.(*syntax.ListLit); ok && len(lit.Elems) == 0 {
		return listOf(c.inferExpr(elem, env))
	}
	lt := c.inferExpr(list, env)
	if _, unknown := lt.(types.Unknown); unknown {
		c.inferExpr(elem, env)
		return types.Unknown{}
	}
	g, ok := lt.(types.Generic)
	if !ok || g.Name != "List" || len(g.Args) != 1 {
		start, _ := list.(syntax.Spanned).Bounds()
		c.report(start, "A1", "append's first argument must be a List, got %s", lt)
		c.inferExpr(elem, env)
		return types.Unknown{}
	}
	c.checkExpected(elem, g.Args[0], env)
	return lt
}

// isAppendCall reports whether e is a call to the append builtin, whose
// result must not be discarded (A13).
func isAppendCall(e syntax.Expr) bool {
	call, ok := e.(*syntax.CallExpr)
	if !ok {
		return false
	}
	ident, ok := call.Callee.(*syntax.Ident)
	return ok && ident.Name == "append"
}
