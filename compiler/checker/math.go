package checker

import (
	"strings"

	"fiig-lang/compiler/constant"
	"fiig-lang/compiler/lexer"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// ADR 0049 §2.4: the prelude math group, visible in every file without a
// `use`. Its formulas are a table (types.MathFormula) rather than source,
// and a member may have an int form and a float form; its constants are
// untyped (§2.7) and are typed by landConstant like any literal. Every kind
// may call a formula, so a math call has no composition check.

// callMath checks `math.member(args)`: it binds the arguments to each of
// the member's forms (types.BindMathArgs), picks a form from the
// arguments' types, and checks each argument against it.
func (c *checker) callMath(member string, args []syntax.Arg, start lexer.Pos, env map[string]types.Type) types.Type {
	forms, ok := types.MathFormula(member)
	if !ok {
		if _, isConst := constant.MathConstant(member); isConst {
			c.report(start, "", "math.%s is a constant, not a formula; drop the parentheses", member)
		} else {
			c.report(start, "", "the prelude math group has no formula %q", member)
		}
		for _, a := range args {
			c.inferExpr(a.Value, env)
		}
		return types.Unknown{}
	}
	name := "math." + member
	argNames := make([]string, len(args))
	for i, a := range args {
		argNames[i] = a.Name
	}
	var bound []boundForm
	for _, f := range forms {
		if at, ok := types.BindMathArgs(f.Params, argNames); ok {
			bound = append(bound, boundForm{f, at})
		}
	}
	if len(bound) == 0 {
		c.reportMathArity(name, closestForm(forms, len(args)), args, start, env)
		return types.Unknown{}
	}
	c.checkNamedMathArgs(name, bound[0].form.Params, args)

	// Non-constant arguments are typed once, here; constants then land in
	// the chosen form's parameter types.
	got := make([]types.Type, len(args))
	unknown := false
	for i, a := range args {
		if !isConstant(a.Value) {
			got[i] = c.inferExpr(a.Value, env)
			_, u := got[i].(types.Unknown)
			unknown = unknown || u
		}
	}
	b, ok := pickMathForm(bound, len(forms), got, args)
	if !ok {
		c.report(start, "A1", "%s has no form taking %s; its forms are %s, and numeric types never mix (convert with math.toFloat, math.toDecimal, or a math.*ToInt formula)", name, describeMathArgs(got, args), joinForms(forms))
		for i, a := range args {
			if got[i] == nil {
				c.landConstant(a.Value, nil)
			}
		}
		return types.Unknown{}
	}
	for i, p := range b.form.Params {
		ai := b.at[i]
		e := args[ai].Value
		if got[ai] == nil {
			// A constant lands in a numeric parameter, and is wrong for any
			// other (`rounding: 2`).
			if t, _ := c.landConstant(e, p.Type); !types.AssignableTo(t, p.Type) {
				argStart, _ := e.(syntax.Spanned).Bounds()
				c.report(argStart, "A1", "type mismatch: got %s, expected %s (argument %q of %s)", t, p.Type, p.Name, name)
			}
			continue
		}
		if _, u := got[ai].(types.Unknown); !u && !types.AssignableTo(got[ai], p.Type) {
			argStart, _ := e.(syntax.Spanned).Bounds()
			c.report(argStart, "A1", "type mismatch: got %s, expected %s (argument %q of %s)", got[ai], p.Type, p.Name, name)
		}
	}
	if unknown && len(bound) > 1 {
		return types.Unknown{}
	}
	return b.form.Result
}

// boundForm is a form a call's arguments bind to: at[i] is the index of
// the argument for the form's parameter i.
type boundForm struct {
	form types.MathForm
	at   []int
}

// closestForm is the form a call with n arguments most likely meant, for
// reporting why its arguments bind to none: the one with the fewest
// parameters that is still at least n, or else the one with the most.
func closestForm(forms []types.MathForm, n int) types.MathForm {
	best := forms[0]
	for _, f := range forms[1:] {
		fits, bestFits := len(f.Params) >= n, len(best.Params) >= n
		switch {
		case fits && (!bestFits || len(f.Params) < len(best.Params)):
			best = f
		case !fits && !bestFits && len(f.Params) > len(best.Params):
			best = f
		}
	}
	return best
}

// reportMathArity reports a call whose arguments bind to no form: by
// position or name against form, which closestForm chose.
func (c *checker) reportMathArity(name string, form types.MathForm, args []syntax.Arg, start lexer.Pos, env map[string]types.Type) {
	names := make([]string, len(form.Params))
	for i, p := range form.Params {
		names[i] = p.Name
	}
	c.checkNamedMathArgs(name, form.Params, args)
	supplied := c.matchArgs(names, args, name, start)
	for _, p := range form.Params {
		if _, ok := supplied[p.Name]; !ok {
			c.report(start, "", "%s is missing argument %q", name, p.Name)
		}
	}
	for _, a := range args {
		if isConstant(a.Value) {
			c.landConstant(a.Value, nil)
		} else {
			c.inferExpr(a.Value, env)
		}
	}
}

// pickMathForm is the bound form whose parameters take every typed
// argument. With only constant arguments, a member with several forms
// takes the float form if any constant is fractional, and the int form
// otherwise, as two constants in a binary expression do (ADR 0048 §2.4); a
// constant never picks the decimal form, as a fractional constant with no
// context is a float (ADR 0064 §7). A member with one form always takes
// it, and a mismatch is reported against it argument by argument; for a
// member with several, the diagnostic lists them all, since the right
// form may take other arguments (decimal's round takes places).
func pickMathForm(bound []boundForm, forms int, got []types.Type, args []syntax.Arg) (boundForm, bool) {
	if forms == 1 {
		return bound[0], true
	}
	typed := false
	for _, t := range got {
		typed = typed || t != nil
	}
	if !typed {
		want := "int"
		for _, a := range args {
			if v, _, _ := constant.Eval(a.Value); v.Kind == constant.Float {
				want = "float"
			}
		}
		for _, b := range bound {
			if b.form.Params[0].Type.String() == want {
				return b, true
			}
		}
	}
	for _, b := range bound {
		fits := true
		for i, ai := range b.at {
			t := got[ai]
			if _, u := t.(types.Unknown); t != nil && !u && !types.AssignableTo(t, b.form.Params[i].Type) {
				fits = false
			}
		}
		if fits {
			return b, true
		}
	}
	return boundForm{}, false
}

// checkNamedMathArgs reports an argument passed by position to a parameter
// that must be named: closeTo's tolerances, which are both floats and easy
// to swap (ADR 0051, ADR 0055's named-argument defense), and decimal's
// places and rounding mode, which have no default (ADR 0064 §4).
func (c *checker) checkNamedMathArgs(name string, params []types.MathParam, args []syntax.Arg) {
	pos := 0
	for _, a := range args {
		if a.Name != "" {
			continue
		}
		if pos < len(params) && params[pos].Named {
			start, _ := a.Value.(syntax.Spanned).Bounds()
			c.report(start, "", "%s's %q must be passed by name, e.g. `%s: %s`, %s", name, params[pos].Name, params[pos].Name, namedExample(params[pos]), namedReason(name))
		}
		pos++
	}
}

// describeMathArgs is the argument types a call supplied, for a diagnostic:
// `(int, float)`, with a constant shown as its default type.
func describeMathArgs(got []types.Type, args []syntax.Arg) string {
	parts := make([]string, len(args))
	for i, a := range args {
		switch {
		case got[i] != nil:
			parts[i] = got[i].String()
		default:
			v, _, _ := constant.Eval(a.Value)
			if v.Kind == constant.Float {
				parts[i] = "float"
			} else {
				parts[i] = "int"
			}
		}
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

// namedExample is an example argument for a must-be-named parameter.
func namedExample(p types.MathParam) string {
	switch p.Type.String() {
	case "int":
		return "2"
	case "Rounding":
		return "Rounding.HalfEven"
	}
	return "0.001"
}

// namedReason is why a member's arguments are named.
func namedReason(name string) string {
	switch name {
	case "math.closeTo":
		return "so the two tolerances can't be swapped (ADR 0051)"
	case "math.allocate":
		return "so parts and places can't be swapped (ADR 0064 §3a)"
	}
	return "so the call says what it rounds to (ADR 0064 §4)"
}

func joinForms(forms []types.MathForm) string {
	parts := make([]string, len(forms))
	for i, f := range forms {
		parts[i] = f.String()
	}
	return strings.Join(parts, " and ")
}

// mathMember reports a non-call use of `math.member`: a constant is handled
// by landConstant before this is reached, so what's left is a formula used
// as a value, which fiig doesn't have, or no member at all.
func (c *checker) mathMember(e *syntax.FieldAccess) {
	start, _ := e.Bounds()
	if _, isFormula := types.MathFormula(e.Field); isFormula {
		c.report(start, "", "math.%s is a formula; call it, e.g. math.%s(...): a formula isn't a value", e.Field, e.Field)
		return
	}
	c.report(start, "", "the prelude math group has no member %q", e.Field)
}

// mathNameTaken reports a local name or import alias that would hide a
// prelude group (ADR 0049 §2.5: `math`, `json` (ADR 0073 §1), `crypto`
// (ADR 0075 §1), and `compress` (ADR 0116 §1) are visible without a
// `use`, so they are reserved everywhere).
func (c *checker) mathNameTaken(name string, at lexer.Pos, what, fix string) {
	if types.IsPreludeGroup(name) {
		c.report(at, "G4", "%s %q would hide the prelude %s group, which every file can call without a `use`; a bare name must mean one thing, so %s (ADR 0049 §2.5)", what, name, name, fix)
	}
}

// preludeNameTaken reports a declaration named after something the prelude
// binds in every file (K2): a type reference or call by that name reaches
// the prelude's meaning, so the declaration is either unreachable or, for
// SocketError, silently replaces the type the Socket primitive returns.
func (c *checker) preludeNameTaken(d syntax.Decl) {
	name := types.DeclName(d)
	what, ok := types.PreludeName(name)
	if !ok {
		return
	}
	start, _ := d.(syntax.Spanned).Bounds()
	c.report(start, "K2", "%s %q has the name of %s, which every file sees without `use`; rename it", types.DeclKindName(d), name, what)
}
