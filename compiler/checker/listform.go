package checker

import (
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// A form's key or body (sorted, ADR 0065 §3; the list forms, ADR 0093)
// may call only what a rule body may, whatever kind encloses it, so a
// form can't reach a gateway and take its value from outside state.

// formElem types a form's iterable, which must be a List, and returns the
// list's type and its element type, each Unknown when it isn't one.
func (c *checker) formElem(form string, iterable syntax.Expr, env map[string]types.Type) (types.Type, types.Type) {
	iterType := c.inferExpr(iterable, env)
	if g, ok := iterType.(types.Generic); ok && g.Name == "List" && len(g.Args) == 1 {
		return iterType, g.Args[0]
	}
	if _, unknown := iterType.(types.Unknown); !unknown {
		start, _ := iterable.(syntax.Spanned).Bounds()
		c.report(start, "A1", "%s takes a List, got %s", form, iterType)
	}
	return types.Unknown{}, types.Unknown{}
}

// inFormBody types e as form's key or body, with the element bound to
// name and the calls a rule may make.
func (c *checker) inFormBody(form string, e syntax.Expr, env map[string]types.Type) types.Type {
	savedKind, savedForm := c.callerKind, c.inForm
	c.callerKind, c.inForm = kindRule, form
	defer func() { c.callerKind, c.inForm = savedKind, savedForm }()
	if form == "sum" && isConstant(e) {
		t, _ := c.landConstant(e, intType)
		return t
	}
	return c.inferExpr(e, env)
}

// formWhat is what report calls the form being checked.
func (c *checker) formWhat() string {
	if c.inForm == "sorted" {
		return "a sorted key"
	}
	return "a " + c.inForm + " body"
}

func (c *checker) formADR() string {
	switch c.inForm {
	case "sorted":
		return "ADR 0065 §3"
	case "mapErr":
		return "ADR 0095 §3"
	}
	return "ADR 0093 §2"
}

// formMayCall is what report and checkEffect say a form's body may call.
func (c *checker) formMayCall() string {
	if c.inForm == "mapErr" {
		return "what the enclosing declaration may, but nothing with an effect"
	}
	return "only what a rule may, formulas and methods"
}

// checkExpectedMapErr handles a mapErr form where the context gives it a
// type (ADR 0095 §2): `user: AbsUser = mapErr(...)?` and `return
// mapErr(...)` from a Result<AbsUser, E> both pass AbsUser to the result
// inside, so a json.decode there knows what to decode into. It reports
// whether e was such a form.
func (c *checker) checkExpectedMapErr(e syntax.Expr, expected types.Type, env map[string]types.Type) bool {
	form, _ := e.(*syntax.ListForm)
	viaTry := false
	if try, ok := e.(*syntax.TryExpr); ok {
		form, viaTry = try.Operand.(*syntax.ListForm)
	}
	if form == nil || form.Form != "mapErr" || expected == nil {
		return false
	}
	if viaTry {
		got := c.inferMapErr(form, expected, env)
		if g, ok := got.(types.Generic); ok && len(g.Args) == 2 && !isUnknown(g.Args[0]) && !types.AssignableTo(g.Args[0], expected) {
			start, _ := form.Bounds()
			c.report(start, "A1", "type mismatch: got %s, expected %s", g.Args[0], expected)
		}
		return true
	}
	result, ok := expected.(types.Generic)
	if !ok || result.Name != "Result" || len(result.Args) != 2 {
		return false
	}
	got := c.inferMapErr(form, result.Args[0], env)
	if !isUnknown(got) && !types.AssignableTo(got, expected) {
		start, _ := form.Bounds()
		c.report(start, "A1", "type mismatch: got %s, expected %s", got, expected)
	}
	return true
}

// inferMapErr types `mapErr(e in result): body` (ADR 0095): the result's
// Ok type with the body's type as its error. valueWant, when the context
// gives one, is the Ok type a json.decode inside decodes into.
func (c *checker) inferMapErr(e *syntax.ListForm, valueWant types.Type, env map[string]types.Type) types.Type {
	var inner types.Type
	if call, member, ok := jsonCall(e.Iterable, env); ok && member == "decode" && valueWant != nil {
		c.checkJsonDecode(call, valueWant, env)
		inner = jsonResultOf(valueWant)
	} else {
		inner = c.inferExpr(e.Iterable, env)
	}
	g, ok := inner.(types.Generic)
	if !ok || g.Name != "Result" || len(g.Args) != 2 {
		if !isUnknown(inner) {
			start, _ := e.Iterable.(syntax.Spanned).Bounds()
			c.report(start, "A1", "mapErr takes a Result, got %s", inner)
		}
		return types.Unknown{}
	}
	bodyEnv := childEnv(env)
	bodyEnv[e.Var] = g.Args[1]
	c.checkLocalName(e.Var, g.Args[1], e.Start)
	// §3: what the enclosing declaration may call, minus effects. A
	// conversion's table is exactly the pure part of every wider kind's.
	savedKind, savedForm := c.callerKind, c.inForm
	if c.callerKind != kindRule && c.callerKind != kindFormula {
		c.callerKind = kindConversion
	}
	c.inForm = "mapErr"
	body := c.inferExpr(e.Body, bodyEnv)
	c.callerKind, c.inForm = savedKind, savedForm
	return types.Generic{Name: "Result", Args: []types.Type{g.Args[0], body}}
}

// inferSorted checks a keyed sort (ADR 0065 §3, ADR 0093 §4). Each key
// must be ordered (M3). The old header `descending` is A1, saying where
// the word goes now; fiig fmt rewrites it.
func (c *checker) inferSorted(e *syntax.SortedExpr, env map[string]types.Type) types.Type {
	iterType, elemType := c.formElem("sorted", e.Iterable, env)
	if e.HeaderDescending {
		c.report(e.Start, "A1", "`descending` now follows the key it applies to: write `sorted(%s in ...): key descending`; fiig fmt rewrites this (ADR 0093 §4)", e.Var)
	}
	keyEnv := childEnv(env)
	keyEnv[e.Var] = elemType
	c.checkLocalName(e.Var, elemType, e.Start)
	for _, k := range e.Keys {
		c.checkOrderedKey("a sorted key", c.inFormBody("sorted", k.Key, keyEnv), k.Key)
	}
	return iterType
}

// checkOrderedKey is M3 for a sorted, max, or min key.
func (c *checker) checkOrderedKey(what string, t types.Type, key syntax.Expr) {
	if ordered(t) {
		return
	}
	start, _ := key.(syntax.Spanned).Bounds()
	if admitsNone(t) {
		c.report(start, "M3", "%s of type %s is not ordered; decide where None goes, for example with a match in a private helper", what, t)
	} else {
		c.report(start, "M3", "%s must be int, float, decimal, or string, got %s", what, t)
	}
}

// inferListForm checks one of ADR 0093's list forms (§3).
func (c *checker) inferListForm(e *syntax.ListForm, env map[string]types.Type) types.Type {
	if e.Form == "mapErr" {
		return c.inferMapErr(e, nil, env)
	}
	listType, elemType := c.formElem(e.Form, e.Iterable, env)
	bodyEnv := childEnv(env)
	bodyEnv[e.Var] = elemType
	c.checkLocalName(e.Var, elemType, e.Start)
	bodyType := c.inFormBody(e.Form, e.Body, bodyEnv)
	_, unknown := bodyType.(types.Unknown)
	start, _ := e.Body.(syntax.Spanned).Bounds()

	switch e.Form {
	case "filter", "count", "any", "all":
		if !unknown && !types.Equal(bodyType, boolType) {
			c.report(start, "A1", "a %s body must be bool, got %s", e.Form, bodyType)
		}
		switch e.Form {
		case "filter":
			return listType
		case "count":
			return intType
		}
		return boolType
	case "map":
		return types.Generic{Name: "List", Args: []types.Type{bodyType}}
	case "groupBy":
		if unknown {
			return types.Unknown{}
		}
		c.checkMapKey(bodyType, start)
		return types.Generic{Name: "Map", Args: []types.Type{bodyType, listType}}
	case "sum":
		if !unknown && !isNumeric(bodyType) {
			c.report(start, "A1", "a sum body must be int, float, or decimal, got %s", bodyType)
			return types.Unknown{}
		}
		if p, ok := bodyType.(types.Primitive); ok {
			e.SumType = p.Name
		}
		return bodyType
	}
	// max, min, and their OrNone forms.
	c.checkOrderedKey("a "+e.Form+" key", bodyType, e.Body)
	if unknown || !ordered(bodyType) {
		return types.Unknown{}
	}
	if e.Form == "maxOrNone" || e.Form == "minOrNone" {
		return types.Union{Members: []types.Type{bodyType, types.NoneType{}}}
	}
	return bodyType
}
