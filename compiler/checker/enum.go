package checker

import (
	"fiig-lang/compiler/format"
	"fiig-lang/compiler/lexer"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// checkEnumHeader checks a headed enum (ADR 0046 §4-§6): every variant
// supplies exactly the header's fields, each a constant of the field's
// type (A15), and no two variants share a value in a `key` field (A16).
func (c *checker) checkEnumHeader(d *syntax.EnumDecl) {
	fieldTypes := make(map[string]types.Type, len(d.Header))
	for _, f := range d.Header {
		if _, dup := fieldTypes[f.Name]; dup {
			c.report(f.Start, "A15", "enum %s declares header field %q twice", d.Name, f.Name)
			continue
		}
		t := c.resolve(f.Type)
		fieldTypes[f.Name] = t
		c.checkNoCapabilityField(d.Name, syntax.Field{Span: f.Span, Name: f.Name, Type: f.Type}, t)
		if f.Key && !validKeyType(t) {
			c.report(f.Start, "A15", "key field %s.%s must be a string, an int, or a list of one, got %s (ADR 0046 §5)", d.Name, f.Name, t)
		}
	}

	// owners[field][value] is the variant that first used value, for A16.
	owners := map[string]map[string]string{}
	for _, v := range d.Variants {
		subject := d.Name + "." + v.Name
		supplied := map[string]bool{}
		for _, a := range v.Values {
			start, _ := a.Value.(syntax.Spanned).Bounds()
			if a.Name == "" {
				c.report(start, "A15", "%s: header values are named (`code: \"ACME\"`), not positional (ADR 0046 §4)", subject)
				continue
			}
			t, ok := fieldTypes[a.Name]
			if !ok {
				c.report(start, "A15", "%s supplies %q, which is not a header field of %s", subject, a.Name, d.Name)
				continue
			}
			if supplied[a.Name] {
				c.report(start, "A15", "%s supplies %q twice", subject, a.Name)
				continue
			}
			supplied[a.Name] = true
			if !c.isConstant(a.Value) {
				c.report(start, "A15", "%s.%s must be a constant: a literal, an enum variant, or a list or model built only from constants; calls and arithmetic are not (ADR 0046 §4a). A variant of a headed enum has no payload (§6)", subject, a.Name)
				continue
			}
			c.checkExpected(a.Value, t, map[string]types.Type{})
		}
		for _, f := range d.Header {
			if !supplied[f.Name] {
				c.report(v.Start, "A15", "%s does not supply header field %q; every variant supplies every header field (ADR 0046 §4)", subject, f.Name)
			}
		}
		for _, f := range d.Header {
			if !f.Key {
				continue
			}
			value, ok := v.Value(f.Name)
			if !ok || !c.isConstant(value) {
				continue
			}
			if owners[f.Name] == nil {
				owners[f.Name] = map[string]string{}
			}
			for _, k := range keyValues(value) {
				if first, taken := owners[f.Name][k]; taken {
					start, _ := value.(syntax.Spanned).Bounds()
					c.report(start, "A16", "%s and %s.%s both have %s %s; a key field's values must be unique, or %s(%s: ...) would not know which to return (ADR 0046 §5)", subject, d.Name, first, f.Name, k, d.Name, f.Name)
					continue
				}
				owners[f.Name][k] = v.Name
			}
		}
	}
}

// keyValues is the values a key field's constant contributes to its lookup:
// itself, or every element of a list (ADR 0046 §7).
func keyValues(e syntax.Expr) []string {
	if lit, ok := e.(*syntax.ListLit); ok {
		out := make([]string, len(lit.Elems))
		for i, el := range lit.Elems {
			out[i] = format.Expr(el)
		}
		return out
	}
	return []string{format.Expr(e)}
}

func validKeyType(t types.Type) bool {
	if g, ok := t.(types.Generic); ok && g.Name == "List" && len(g.Args) == 1 {
		t = g.Args[0]
	}
	p, ok := t.(types.Primitive)
	return ok && (p.Name == "string" || p.Name == "int")
}

// isConstant is ADR 0046 §4a's definition: a literal, None, an enum
// variant with no payload, or a list or model built only from constants.
func (c *checker) isConstant(e syntax.Expr) bool {
	switch e := e.(type) {
	case *syntax.IntLit:
		return true
	case *syntax.NegExpr:
		_, ok := e.Operand.(*syntax.IntLit)
		return ok
	case *syntax.StringLit:
		for _, p := range e.Parts {
			if p.Interp != nil {
				return false
			}
		}
		return true
	case *syntax.Ident:
		return e.Name == "true" || e.Name == "false" || e.Name == "None"
	case *syntax.FieldAccess:
		target, ok := e.Target.(*syntax.Ident)
		if !ok {
			return false
		}
		decl, found := c.lookup(target.Name)
		_, isEnum := decl.(*syntax.EnumDecl)
		return found && isEnum
	case *syntax.ListLit:
		for _, el := range e.Elems {
			if !c.isConstant(el) {
				return false
			}
		}
		return true
	case *syntax.CallExpr:
		callee, ok := e.Callee.(*syntax.Ident)
		if !ok {
			return false
		}
		decl, found := c.lookup(callee.Name)
		if _, isModel := decl.(*syntax.ModelDecl); !found || !isModel {
			return false
		}
		for _, a := range e.Args {
			if a.Name == "" || !c.isConstant(a.Value) {
				return false
			}
		}
		return true
	}
	return false
}

// checkKeyLookup checks `DeductionSource(code: raw)` (ADR 0046 §5): exactly
// one named argument, naming a key field, of that field's type (or its
// element type, for a list key). The result is the enum or None.
func (c *checker) checkKeyLookup(d *syntax.EnumDecl, name string, args []syntax.Arg, start lexer.Pos, env map[string]types.Type) types.Type {
	result := types.Union{Members: []types.Type{types.Named{Name: d.Name, Decl: d}, types.NoneType{}}}
	if d.Header == nil {
		c.report(start, "A17", "%s has no header, so it has no key lookup; a variant is written %s.Variant (ADR 0046 §5)", name, name)
		for _, a := range args {
			c.inferExpr(a.Value, env)
		}
		return types.Unknown{}
	}
	if len(args) != 1 || args[0].Name == "" {
		c.report(start, "A17", "a lookup on %s takes exactly one named key argument, e.g. %s(%s: value) (ADR 0046 §5)", name, name, firstKey(d))
		for _, a := range args {
			c.inferExpr(a.Value, env)
		}
		return result
	}
	arg := args[0]
	f, ok := d.HeaderField(arg.Name)
	if !ok || !f.Key {
		c.report(start, "A17", "%s.%s is not a key field, so %s cannot be looked up by it; only `key` fields have a lookup (ADR 0046 §5)", name, arg.Name, name)
		c.inferExpr(arg.Value, env)
		return result
	}
	t := c.resolve(f.Type)
	if g, ok := t.(types.Generic); ok && g.Name == "List" && len(g.Args) == 1 {
		t = g.Args[0]
	}
	c.checkExpected(arg.Value, t, env)
	return result
}

func firstKey(d *syntax.EnumDecl) string {
	for _, f := range d.Header {
		if f.Key {
			return f.Name
		}
	}
	return "key"
}
