package checker

import (
	"fmt"
	"strings"

	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// checkStreamRoute is ADR 0111 §3 and §6, bug classes I1-I4: a stream
// route binds one element, a model with a key or bytes without one, and
// says what happens to every outcome of its workflow.
func (c *checker) checkStreamRoute(route *syntax.Route, wd *syntax.WorkflowDecl) {
	// I8 (ADR 0111 §3a): the group is deployed state, so it is named, not
	// derived from where the adapter happens to live; and a new group's
	// start is a decision, not a default.
	if strings.TrimSpace(route.Group) == "" {
		c.report(route.Start, "I8", "stream route %q names no consumer group; write `group \"name\"` after the source, as in `source %q group \"order-intake\" from earliest ...` (ADR 0111 §3a)", route.Path, route.Path)
	}
	if route.From == "" {
		c.report(route.Start, "I8", "stream route %q doesn't say where a new group starts; write `from earliest` to process what is already in the stream, or `from latest` for only what arrives after (ADR 0111 §3a)", route.Path)
	}
	if len(route.Args) != 1 || len(wd.Params) != 1 {
		c.report(route.Start, "I3", "a stream route's call binds exactly one argument, the element, and %s takes %d parameter(s); the route gives %d", wd.Name, len(wd.Params), len(route.Args))
	}
	if len(route.Args) == 1 {
		c.checkStreamElement(route, wd, route.Args[0])
	}
	c.checkStreamMapping(route, wd)
}

// checkStreamElement is I4 (the element is a model or bytes) and I1 (a
// model's route names a key field text can carry; bytes has no key).
func (c *checker) checkStreamElement(route *syntax.Route, wd *syntax.WorkflowDecl, arg syntax.RouteArg) {
	if arg.Source != "" {
		c.report(arg.Start, "", "a stream route's argument is the element; `from %s` is for http routes", arg.Source)
		return
	}
	written := c.resolve(arg.Type)
	var param *syntax.Param
	for i := range wd.Params {
		if wd.Params[i].Name == arg.Name {
			param = &wd.Params[i]
		}
	}
	if param == nil {
		c.report(arg.Start, "", "workflow %q has no parameter named %q", wd.Name, arg.Name)
		return
	}
	if pt, ok := types.ResolveWithImports(param.Type, c.universe, universesOf(c.imports)); ok && !isUnknown(written) && !types.Equal(written, pt) {
		c.report(arg.Start, "A1", "route argument %q is %s, but %s's parameter is %s", arg.Name, written, wd.Name, pt)
		return
	}
	if isUnknown(written) {
		return
	}
	if p, ok := written.(types.Primitive); ok && p.Name == "bytes" {
		if route.Key != nil {
			c.report(route.Key.Start, "I1", "the element %q is bytes, which has no fields, so the route has no key; the delivery's id names it (ADR 0111 §3)", arg.Name)
		}
		return
	}
	named, ok := written.(types.Named)
	model, isModel := (*syntax.ModelDecl)(nil), false
	if ok {
		model, isModel = named.Decl.(*syntax.ModelDecl)
	}
	if !isModel {
		c.report(arg.Start, "I4", "the element %q is %s; a stream element decodes from JSON into a model, or binds the payload as bytes (ADR 0111 §3)", arg.Name, written)
		return
	}
	if why := c.jsonRefusal(written, true); why != "" {
		c.report(arg.Start, "J1", "the element %q decodes into %s: %s, which can't be decoded from JSON (ADR 0073 §8)", arg.Name, written, why)
	}
	if route.Key == nil {
		c.report(route.Start, "I1", "stream route %q binds a %s, so it names the field that identifies an element, as in `source %q key id -> ...` (ADR 0047 §4)", route.Path, model.Name, route.Path)
		return
	}
	for _, f := range model.AllFields() {
		if f.Name != route.Key.Name {
			continue
		}
		ft := c.resolveField(model, f)
		if bare := withoutNone(ft); !isUnknown(ft) && !isWireScalar(bare) && !isTextEnum(bare) {
			c.report(route.Key.Start, "I1", "key %q is %s; a key is written in logs and a halt's message, so it is a string, int, float, decimal, bool, or an enum without payloads (ADR 0111 §6)", f.Name, ft)
		}
		return
	}
	c.report(route.Key.Start, "I1", "key %q isn't a field of %s", route.Key.Name, model.Name)
}

// checkStreamMapping is I2: the mapping is there, every arm fits the
// workflow's result, retry limits are at least 1, and the arms cover Ok
// and every variant of the Err enum.
func (c *checker) checkStreamMapping(route *syntax.Route, wd *syntax.WorkflowDecl) {
	if route.Mapping == nil {
		c.report(route.Start, "I2", "stream route %q has no mapping; a stream has no caller to hand an error to, so say what happens to each outcome, as in `{ Ok(_) -> ack  Err(_) -> retry(attempts: 5) }` (ADR 0047 §5)", route.Path)
		return
	}
	ret, ok := types.ResolveWithImports(wd.ReturnType, c.universe, universesOf(c.imports))
	if !ok {
		ret = types.Unknown{}
	}
	for _, arm := range route.Mapping {
		env := map[string]types.Type{}
		if !c.patternFits(arm.Pattern, ret, env) {
			start, _ := arm.Pattern.(syntax.Spanned).Bounds()
			c.report(start, "A1", "workflow %s returns %s, which this arm's pattern can never match", wd.Name, ret)
		}
		if d, ok := arm.Value.(*syntax.DispositionResponse); ok && d.Kind == "retry" && d.Attempts < 1 {
			c.report(d.Start, "I2", "retry(attempts: %d) never runs the workflow again; the limit is at least 1", d.Attempts)
		}
	}
	if isUnknown(ret) {
		return
	}
	if missing := uncoveredOutcomes(route.Mapping, ret); len(missing) > 0 {
		c.report(route.Start, "I2", "stream route %q's mapping doesn't say what happens to %s (ADR 0111 §3)", route.Path, strings.Join(missing, ", "))
	}
}

// uncoveredOutcomes is the outcomes of a workflow's result that no arm
// covers: Ok, and each variant of an enum Err, for a Result; the value
// for anything else, which only a catch-all covers.
func uncoveredOutcomes(arms []syntax.ResponseArm, ret types.Type) []string {
	g, isResult := ret.(types.Generic)
	isResult = isResult && g.Name == "Result" && len(g.Args) == 2
	var variants []string
	if isResult {
		if d, ok := enumOf(g.Args[1]); ok {
			for _, v := range d.Variants {
				variants = append(variants, v.Name)
			}
		}
	}
	okCovered, errCovered := false, false
	covered := map[string]bool{}
	for _, arm := range arms {
		if catchesAll(arm.Pattern) {
			return nil
		}
		cp, ok := arm.Pattern.(*syntax.ConstructorPattern)
		if !ok || !isResult || len(cp.Path) != 1 || len(cp.Args) != 1 {
			continue
		}
		inner := cp.Args[0]
		switch cp.Path[0] {
		case "Ok":
			okCovered = okCovered || catchesAll(inner)
		case "Err":
			if catchesAll(inner) {
				errCovered = true
				continue
			}
			if v := variantNamed(inner); v != "" {
				covered[v] = true
			}
		}
	}
	if !isResult {
		return []string{fmt.Sprintf("its result (%s)", ret)}
	}
	var missing []string
	if !okCovered {
		missing = append(missing, "Ok")
	}
	if errCovered {
		return missing
	}
	if len(variants) == 0 {
		return append(missing, "Err")
	}
	for _, v := range variants {
		if !covered[v] {
			missing = append(missing, "Err("+v+")")
		}
	}
	return missing
}

// catchesAll reports whether p matches any value: `_` or a bare name that
// binds it.
func catchesAll(p syntax.Pattern) bool {
	switch p := p.(type) {
	case *syntax.WildcardPattern:
		return true
	case *syntax.NamePattern:
		return len(p.Path) == 1 && p.Path[0] != noneTag
	}
	return false
}

// variantNamed is the enum variant a pattern such as `E.V(_)` or `E.V`
// names, when its payload, if any, is matched whole.
func variantNamed(p syntax.Pattern) string {
	switch p := p.(type) {
	case *syntax.NamePattern:
		if len(p.Path) > 1 {
			return p.Path[len(p.Path)-1]
		}
	case *syntax.ConstructorPattern:
		for _, a := range p.Args {
			if !catchesAll(a) {
				return ""
			}
		}
		return p.Path[len(p.Path)-1]
	}
	return ""
}
