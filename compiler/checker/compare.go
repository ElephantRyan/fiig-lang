package checker

import (
	"sort"

	"fiig-lang/compiler/lexer"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// checkComparable checks the operands of ==, !=, <, <=, >, and >= (ADR 0052).
// Unknown is accepted everywhere, as in types.Equal, so an unmodeled value
// never produces a false positive.
func (c *checker) checkComparable(e *syntax.BinaryExpr, left, right types.Type) {
	start, _ := e.Bounds()
	// M1: a handle has no value to compare, only an identity.
	for _, t := range []types.Type{left, right} {
		if what := c.handle(t); what != "" {
			c.report(start, "M1", "%s is not comparable; it is a handle, not a value", what)
			return
		}
		// A Result is an outcome to handle, not data to inspect: match it
		// and compare what's inside (ADR 0052, 2026-10-27).
		if types.HoldsResult(t, c.resolveField) {
			c.report(start, "M1", "%s is not comparable; it holds a Result, so match it and compare the value inside", t)
			return
		}
	}
	// M2: both sides must be the same type. Either side being assignable to
	// the other keeps `x == None` legal when x is `Customer | None`.
	if !types.AssignableTo(left, right) && !types.AssignableTo(right, left) {
		c.report(start, "M2", "cannot compare %s with %s: the operands have different types", left, right)
		return
	}
	switch e.Op {
	case syntax.OpEq, syntax.OpNe:
		// M1's float half (ADR 0051 §2.3, ADR 0052 §2.2): a float anywhere
		// in the operand makes == lie, and the diagnostic names the path to
		// it, since the float may be several fields below the operand.
		for _, t := range []types.Type{left, right} {
			if path, ok := c.floatPath(t, t.String(), map[syntax.Decl]bool{}); ok {
				if isFloatCollection(t) {
					c.report(start, "M1", "cannot compare %s with %s: two floats that should agree can differ in the last bit (use math.allClose)", t, e.Op)
				} else if path == t.String() {
					c.report(start, "M1", "cannot compare %s with %s: two floats that should agree can differ in the last bit (use math.closeTo or math.isZero)", t, e.Op)
				} else {
					c.report(start, "M1", "cannot compare %s with %s: %s is float (use math.closeTo or compare a specific field)", t, e.Op, path)
				}
				return
			}
		}
	case syntax.OpLt, syntax.OpLe, syntax.OpGt, syntax.OpGe:
		// M3: ordering is defined only on int, float, and string, never
		// structurally.
		for _, t := range []types.Type{left, right} {
			if !ordered(t) {
				c.report(start, "M3", "%s is not ordered; < <= > >= apply only to int, float, decimal, and string", t)
				return
			}
		}
	}
}

// checkListElement enforces what List's table can't say (ADR 0065 §1-§2):
// contains compares with ==, so its element must be comparable (M1), and
// the sorts order elements, so theirs must be ordered (M3).
func (c *checker) checkListElement(elem types.Type, method string, start lexer.Pos) {
	switch method {
	case "contains":
		if what := c.handle(elem); what != "" {
			c.report(start, "M1", "List<%s>.contains compares elements with ==, and %s is not comparable; it is a handle, not a value", elem, what)
		} else if types.HoldsResult(elem, c.resolveField) {
			c.report(start, "M1", "List<%s>.contains compares elements with ==, and %s holds a Result; loop over the list and match each one", elem, elem)
		} else if path, ok := c.floatPath(elem, elem.String(), map[syntax.Decl]bool{}); ok {
			if path == elem.String() {
				c.report(start, "M1", "List<float>.contains would compare floats with ==, and two floats that should agree can differ in the last bit (use math.containsClose)")
			} else {
				c.report(start, "M1", "List<%s>.contains compares elements with ==, and %s is float; loop over the list and compare with math.closeTo", elem, path)
			}
		}
	case "sorted", "sortedDescending":
		if !ordered(elem) {
			c.report(start, "M3", "List<%s>.%s needs an ordered element type: int, float, decimal, or string", elem, method)
		}
	}
}

// isFloatCollection is List<float> or Map<K, float>, whose == has
// math.allClose as its counterpart (ADR 0065 §1).
func isFloatCollection(t types.Type) bool {
	g, ok := t.(types.Generic)
	if !ok || len(g.Args) == 0 || g.Args[len(g.Args)-1].String() != "float" {
		return false
	}
	return (g.Name == "List" && len(g.Args) == 1) || (g.Name == "Map" && len(g.Args) == 2)
}

// ordered reports whether < and its siblings apply to t. float is ordered
// but not comparable (ADR 0052 §2.4): ADR 0048 removed NaN so that its
// ordering stays total.
func ordered(t types.Type) bool {
	switch t := t.(type) {
	case types.Unknown:
		return true
	case types.Primitive:
		return t.Name == "int" || t.Name == "float" || t.Name == "decimal" || t.Name == "string"
	}
	return false
}

// floatPath finds a float in t at any depth and spells the path to it from
// at: `Order.total.amount`, `List<float> element`, `Shape.Circle.radius`.
// It walks what types.Holds walks, plus enum variant payloads, which ==
// also compares field by field.
func (c *checker) floatPath(t types.Type, at string, seen map[syntax.Decl]bool) (string, bool) {
	switch t := t.(type) {
	case types.Primitive:
		return at, t.Name == "float"
	case types.Generic:
		for _, a := range t.Args {
			if path, ok := c.floatPath(a, at+" element", seen); ok {
				return path, true
			}
		}
	case types.Union:
		for _, m := range t.Members {
			if path, ok := c.floatPath(m, at, seen); ok {
				return path, true
			}
		}
	case types.Record:
		names := make([]string, 0, len(t.Fields))
		for name := range t.Fields {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if path, ok := c.floatPath(t.Fields[name], at+"."+name, seen); ok {
				return path, true
			}
		}
	case types.Named:
		if seen[t.Decl] {
			return "", false
		}
		seen[t.Decl] = true
		switch decl := t.Decl.(type) {
		case *syntax.ModelDecl:
			for _, f := range decl.Fields {
				if path, ok := c.floatPath(c.resolveField(decl, f), at+"."+f.Name, seen); ok {
					return path, true
				}
			}
		case *syntax.EnumDecl:
			for _, v := range decl.Variants {
				for _, f := range v.Fields {
					if path, ok := c.floatPath(c.resolve(f.Type), at+"."+v.Name+"."+f.Name, seen); ok {
						return path, true
					}
				}
			}
		}
	}
	return "", false
}

// handle names a non-comparable handle in t, or returns "". A Connection
// (ADR 0060) is found at any depth, through models, lists, and unions, the
// walk H5 uses. A capability value, an interface or a capability group, is
// found at the top or inside a list or union; models can't hold one (P6).
// Which provider was chosen is a selection point's business (ADR 0043), so
// comparing providers is rejected (ADR 0052, 2026-10-27).
func (c *checker) handle(t types.Type) string {
	if types.HoldsConnection(t, c.resolveField) {
		return "a Connection"
	}
	return capabilityValue(t)
}

func capabilityValue(t types.Type) string {
	switch t := t.(type) {
	case types.Union:
		for _, m := range t.Members {
			if what := capabilityValue(m); what != "" {
				return what
			}
		}
		return ""
	case types.Generic:
		for _, a := range t.Args {
			if what := capabilityValue(a); what != "" {
				return what
			}
		}
		return ""
	}
	if iface, ok := types.Interface(t); ok {
		return "a provider of " + iface.Name
	}
	if group, ok := types.CapabilityGroup(t); ok {
		return "the capability group " + group.Name
	}
	return ""
}
