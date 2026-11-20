package checker

import (
	"strings"
	"time"

	"fiig-lang/compiler/schedule"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// checkScheduleRoute is ADR 0112 §1-§2 and §5, bug classes I5-I7: a
// schedule that parses and fires, a workflow that takes nothing or the
// firing time, and a mapping that says what happens to every outcome.
func (c *checker) checkScheduleRoute(route *syntax.Route, wd *syntax.WorkflowDecl) {
	if s, err := schedule.Parse(route.Verb, route.Path); err != nil {
		c.report(route.Start, "I5", "%v (ADR 0112 §1)", err)
	} else if s.Next(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)).IsZero() {
		c.report(route.Start, "I5", "cron %q never fires: no day matches it", route.Path)
	}
	c.checkScheduleArgs(route, wd)
	if route.Mapping == nil {
		c.report(route.Start, "I7", "schedule route %s %q has no mapping; nothing calls a job, so say what happens to each outcome, as in `{ Ok(_) -> done  Err(_) -> done }` (ADR 0112 §2)", route.Verb, route.Path)
		return
	}
	ret, ok := types.ResolveWithImports(wd.ReturnType, c.universe, universesOf(c.imports))
	if !ok {
		return
	}
	for _, arm := range route.Mapping {
		if !c.patternFits(arm.Pattern, ret, map[string]types.Type{}) {
			start, _ := arm.Pattern.(syntax.Spanned).Bounds()
			c.report(start, "A1", "workflow %s returns %s, which this arm's pattern can never match", wd.Name, ret)
		}
	}
	if missing := uncoveredOutcomes(route.Mapping, ret); len(missing) > 0 {
		c.report(route.Start, "I7", "schedule route %s %q's mapping doesn't say what happens to %s (ADR 0112 §2)", route.Verb, route.Path, strings.Join(missing, ", "))
	}
}

// checkScheduleArgs is I6: the workflow takes nothing, or one int, the
// firing time in Unix milliseconds, which the route names.
func (c *checker) checkScheduleArgs(route *syntax.Route, wd *syntax.WorkflowDecl) {
	takesTime := len(wd.Params) == 1
	if takesTime {
		pt, ok := types.ResolveWithImports(wd.Params[0].Type, c.universe, universesOf(c.imports))
		p, isPrim := pt.(types.Primitive)
		takesTime = !ok || (isPrim && p.Name == "int")
	}
	if len(wd.Params) > 1 || (len(wd.Params) == 1 && !takesTime) {
		c.report(route.Start, "I6", "a scheduled workflow takes nothing, or one int, the firing time in Unix milliseconds; %s takes %s (ADR 0112 §1)", wd.Name, paramList(wd.Params))
		return
	}
	if len(route.Args) != len(wd.Params) {
		c.report(route.Start, "I6", "workflow %s takes %d parameter(s), and the route gives %d", wd.Name, len(wd.Params), len(route.Args))
		return
	}
	for _, arg := range route.Args {
		if arg.Source != "" {
			c.report(arg.Start, "", "a schedule route's argument is the firing time; `from %s` is for http routes", arg.Source)
			continue
		}
		if arg.Name != wd.Params[0].Name {
			c.report(arg.Start, "I6", "workflow %q has no parameter named %q", wd.Name, arg.Name)
			continue
		}
		if t := c.resolve(arg.Type); !isUnknown(t) {
			if p, ok := t.(types.Primitive); !ok || p.Name != "int" {
				c.report(arg.Start, "I6", "route argument %q is %s; the firing time is an int", arg.Name, t)
			}
		}
	}
}

func paramList(params []syntax.Param) string {
	parts := make([]string, len(params))
	for i, p := range params {
		parts[i] = p.Name + ": " + types.SyntaxString(p.Type)
	}
	return "(" + strings.Join(parts, ", ") + ")"
}
