package checker

import (
	"fiig-lang/compiler/lexer"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// ADR 0071: configuration from the environment. The bug classes:
//
//   - V1: a config declaration the runtime couldn't read: a field type
//     outside §1's set, an environment variable name that isn't one, a
//     default that isn't a constant of the field's type, a default on a
//     Secret, or a field declared twice.
//   - B10, widened: reading config outside an activity or a gateway (§2).
//   - S1: a Secret shown, interpolated into a string or able to cross the
//     adapter boundary (§4).
//   - S2: `.reveal()` outside a gateway (§4).

// checkConfigDecl is V1, plus N1 for the declaration's name: a config is
// read as `Name.field`, so it is capitalized like a capability group.
func (c *checker) checkConfigDecl(d *syntax.ConfigDecl) {
	if !isUpper(d.Name) {
		c.report(d.Start, "N1", "config %q must be capitalized; it is read as %s.field, like a capability group (ADR 0071 §1)", d.Name, d.Name)
	}
	seen := map[string]bool{}
	for _, f := range d.Fields {
		if seen[f.Name] {
			c.report(f.Start, "V1", "config %s declares field %q twice", d.Name, f.Name)
		}
		seen[f.Name] = true
		if !validEnvName(f.Env) {
			c.report(f.Start, "V1", "config field %s.%s reads %q, which isn't an environment variable name: letters, digits, and _, not starting with a digit", d.Name, f.Name, f.Env)
		}
		t := c.resolve(f.Type)
		if !configFieldType(t) {
			c.report(f.Start, "V1", "config field %s.%s is %s; a config value is string, int, float, decimal, bool, an enum, or Secret, optionally | None (ADR 0071 §1)", d.Name, f.Name, t)
			continue
		}
		if f.Default == nil {
			continue
		}
		start, _ := f.Default.(syntax.Spanned).Bounds()
		if types.HoldsSecret(t, c.resolveField) {
			c.report(start, "V1", "config field %s.%s is a Secret, so it has no default: a default would put the secret in the source", d.Name, f.Name)
			continue
		}
		if !isConstant(f.Default) && !c.isConstant(f.Default) {
			c.report(start, "V1", "config field %s.%s's default must be a constant, such as a literal, constant arithmetic, or an enum variant", d.Name, f.Name)
			continue
		}
		c.checkExpected(f.Default, t, map[string]types.Type{})
	}
}

func validEnvName(s string) bool {
	if s == "" || s[0] >= '0' && s[0] <= '9' {
		return false
	}
	for _, r := range s {
		if !(r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

// configFieldType is §1's set: what one environment variable's text can be
// read as.
func configFieldType(t types.Type) bool {
	if u, ok := t.(types.Union); ok {
		for _, m := range u.Members {
			if _, none := m.(types.NoneType); !none && !configFieldType(m) {
				return false
			}
		}
		return len(u.Members) == 2
	}
	switch t := t.(type) {
	case types.Primitive:
		return t.Name != "bytes"
	case types.Opaque:
		return t == types.SecretType
	case types.Named:
		_, isEnum := t.Decl.(*syntax.EnumDecl)
		return isEnum
	}
	return false
}

// configRef resolves a bare name to a config declaration: this package's,
// or one imported by path tail (`use acme.postgres.Postgres`, §5).
func (c *checker) configRef(name string) (*syntax.ConfigDecl, bool) {
	if g, ok := c.groups[name]; ok && g.Config != nil {
		return g.Config, true
	}
	if d, found := c.lookup(name); found {
		if cd, ok := d.(*syntax.ConfigDecl); ok {
			return cd, true
		}
	}
	return nil, false
}

// readConfig checks `Name.field` on a config declaration: the field exists,
// and the kind reading it may (B10, §2).
func (c *checker) readConfig(d *syntax.ConfigDecl, e *syntax.FieldAccess) types.Type {
	start, _ := e.Bounds()
	switch c.callerKind {
	case kindActivity, kindGateway:
	case "":
		c.report(start, "B10", "a response mapping may not read %s.%s; config is read by an activity or a gateway, and passed on as a value (ADR 0071 §2)", d.Name, e.Field)
	default:
		c.report(start, "B10", "a %s may not read %s.%s; take the value as a parameter, read by the activity or gateway that calls this %s, so its answer depends only on its arguments (ADR 0071 §2)", c.callerKind, d.Name, e.Field, c.callerKind)
	}
	f, ok := d.Field(e.Field)
	if !ok {
		c.report(start, "", "config %s has no field %q", d.Name, e.Field)
		return types.Unknown{}
	}
	return c.resolve(f.Type)
}

// checkInterpolatedSecret is S1 for a value interpolated into a string.
func (c *checker) checkInterpolatedSecret(t types.Type, at lexer.Pos) {
	if types.HoldsSecret(t, c.resolveField) {
		c.report(at, "S1", "a Secret can't be put in a string, where it would be shown; call .reveal() in the gateway that sends it (ADR 0071 §4)")
	}
}

// checkNoSecretInSignature is S1 at the adapter boundary: a workflow's
// parameters and result are what an adapter decodes and encodes.
func (c *checker) checkNoSecretInSignature(params []syntax.Param, retType syntax.Type, at lexer.Pos) {
	check := func(t syntax.Type, where string) {
		if t == nil {
			return
		}
		rt, _ := types.ResolveWithImports(t, c.universe, universesOf(c.imports))
		if types.HoldsSecret(rt, c.resolveField) {
			pos := at
			if sp, ok := t.(syntax.Spanned); ok {
				pos, _ = sp.Bounds()
			}
			c.report(pos, "S1", "%s holds a Secret (%s); a workflow is the adapter boundary, and a Secret is never encoded (ADR 0071 §4)", where, types.SyntaxString(t))
		}
	}
	for _, p := range params {
		check(p.Type, "parameter "+p.Name)
	}
	check(retType, "the return type")
}
