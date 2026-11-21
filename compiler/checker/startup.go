package checker

import (
	"strings"
	"time"

	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// checkStartupRoute is ADR 0124 §1 and §5, bug classes I7 and I9: a startup
// route calls a workflow that takes nothing, and its mapping says whether
// the process starts after each outcome. The parser has already refused
// every disposition but `proceed` and `halt`, and any route keyword but
// `run`.
func (c *checker) checkStartupRoute(route *syntax.Route, wd *syntax.WorkflowDecl) {
	if len(wd.Params) != 0 || len(route.Args) != 0 {
		c.report(route.Start, "I9", "a startup route's workflow takes nothing, and the route gives no arguments; %s takes %s and the route gives %d (ADR 0124 §1)", wd.Name, paramList(wd.Params), len(route.Args))
	}
	if route.Within != "" {
		if d, err := time.ParseDuration(route.Within); err != nil || d <= 0 {
			c.report(route.Start, "I10", "startup route `run within %q -> %s`: a deadline is a positive duration such as \"10s\" or \"2m\" (ADR 0124 §6)", route.Within, wd.Name)
		}
	}
	if route.Mapping == nil {
		c.report(route.Start, "I7", "startup route `run -> %s` has no mapping; nothing calls a check, so say what happens to each outcome, as in `{ Ok(_) -> proceed  Err(_) -> halt }` (ADR 0124 §1)", wd.Name)
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
		c.report(route.Start, "I7", "startup route `run -> %s`'s mapping doesn't say what happens to %s (ADR 0124 §1)", wd.Name, strings.Join(missing, ", "))
	}
}
