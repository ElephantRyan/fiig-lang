package registry

import (
	"sort"

	"fiig-lang/compiler/syntax"
)

func bodyCallNames(body *syntax.Block) []string { return syntax.BodyCallNames(body) }

// adapterCallNames collects the names an adapter depends on: each route's
// target workflow, plus any call inside a response-mapping arm (ADR 0017
// — most commonly a shared conversion). A route's own call arguments are
// deliberately not walked as expressions — per checker.checkAdapterRoutes,
// a route's argument position holds a type reference for path/query/body
// binding, not an ordinary value expression, so a name there (e.g.
// CustomerId) is a type, not a call dependency.
func adapterCallNames(d *syntax.AdapterDecl) []string {
	names := map[string]bool{}
	for _, pr := range d.Principals {
		syntax.CollectExprCallNames(pr.By.Call(), names)
		syntax.CollectExprCallNames(pr.Else.Call(), names)
	}
	// ADR 0141 §5: a named principal is a dependency like a called workflow.
	for _, r := range d.PrincipalRefs {
		names[r.Name()] = true
	}
	for _, route := range d.Routes {
		if !syntax.PreludeCallForms[route.Workflow.Name] {
			names[route.Workflow.Name] = true
		}
		for _, arm := range route.Mapping {
			switch v := arm.Value.(type) {
			case *syntax.StatusResponse:
				syntax.CollectExprCallNames(v.Code, names)
				if v.Body != nil {
					syntax.CollectExprCallNames(v.Body, names)
				}
			case *syntax.ExitResponse:
				syntax.CollectExprCallNames(v.Code, names)
				if v.Body != nil {
					syntax.CollectExprCallNames(v.Body, names)
				}
			case *syntax.ExprResponse:
				syntax.CollectExprCallNames(v.Value, names)
			}
		}
	}
	return sortedKeys(names)
}

func sortedKeys(m map[string]bool) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Callers returns the names of every other card whose own Dependencies
// list includes c's name — the inverse of Dependencies, and the other
// half of Milestone 3's "dependency and caller graph" deliverable.
// Computed on demand from the whole card set, the same treatment Siblings
// already gets, rather than stored redundantly on every card. Unlike
// Siblings, this is deliberately not scoped to c's own catalog: a gateway in
// the default catalog is routinely called by a workflow in the
// composition catalog, and that cross-catalog edge is exactly the
// relationship this is for.
func Callers(cards []*Card, c *Card) []string {
	var names []string
	for _, other := range cards {
		if other == c {
			continue
		}
		for _, dep := range other.Dependencies {
			if dep == c.Name {
				names = append(names, other.Name)
				break
			}
		}
	}
	sort.Strings(names)
	return names
}
