package checker

import (
	"sort"
	"strings"

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
//   - V2: a `when` condition (ADR 0122) that isn't a bool built from
//     comparisons, and/or/not, literals, enum variants, and fields of
//     config declarations that have no condition of their own.
//   - B10, widened: reading config outside an activity or a gateway (§2).
//   - S1: a Secret shown, interpolated into a string or able to cross the
//     adapter boundary (§4).
//   - S2: `.reveal()` outside a gateway (§4).

// checkConfigDecl is V1, plus N1 for the declaration's name: a config is
// read as `Name.field`, so it is capitalized like a capability group.
func (c *checker) checkConfigDecl(d *syntax.ConfigDecl) {
	if !isUpper(d.Name) {
		c.report(d.Start, "N1", "wire %q must be capitalized; a grouped wire is read as %s.field, like a capability group (ADR 0071 §1, ADR 0143)", d.Name, d.Name)
	}
	c.checkWhen(d)
	seen := map[string]bool{}
	for _, f := range d.Fields {
		if seen[f.Name] {
			c.report(f.Start, "V1", "config %s declares field %q twice", d.Name, f.Name)
		}
		seen[f.Name] = true
		if f.Startup != "" {
			c.checkStartupField(d, f)
			continue
		}
		if f.Setting != "" {
			c.checkSettingField(d, f)
		}
		if f.Env != "" && !validEnvName(f.Env) {
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

// checkStartupField is V1 for `degraded: bool from startup "Adapter.workflow"`
// (ADR 0127): a bool, with no default, naming a startup route of this package.
func (c *checker) checkStartupField(d *syntax.ConfigDecl, f syntax.ConfigField) {
	if t := c.resolve(f.Type); t != (types.Primitive{Name: "bool"}) {
		c.report(f.Start, "V1", "config field %s.%s is %s; a field read from startup says whether the route ended degraded, so it is a bool (ADR 0127)", d.Name, f.Name, t)
	}
	if f.Default != nil {
		start, _ := f.Default.(syntax.Spanned).Bounds()
		c.report(start, "V1", "config field %s.%s has a default; a field read from startup is false until its route ends degraded (ADR 0127)", d.Name, f.Name)
	}
	adapterName, workflow, ok := strings.Cut(f.Startup, ".")
	if !ok || adapterName == "" || workflow == "" {
		c.report(f.Start, "V1", "config field %s.%s reads startup %q; name a route as \"Adapter.workflow\" (ADR 0127)", d.Name, f.Name, f.Startup)
		return
	}
	if a, found := c.lookup(adapterName); found {
		if ad, isAdapter := a.(*syntax.AdapterDecl); isAdapter && ad.Kind == syntax.AdapterStartup {
			for _, r := range ad.Routes {
				if r.Workflow != nil && r.Workflow.Name == workflow {
					return
				}
			}
		}
	}
	c.report(f.Start, "V1", "config field %s.%s reads startup %q, but this package has no startup adapter %s with a route to %s (ADR 0127)", d.Name, f.Name, f.Startup, adapterName, workflow)
}

// checkSettingField is V1 for `from setting "x"` (ADR 0140 §1): a name made
// of letters, digits, `.`, `_` and `-`; optional or defaulted, since on a
// first run nothing is saved and the process must still start; and named by
// one field only in its package.
func (c *checker) checkSettingField(d *syntax.ConfigDecl, f syntax.ConfigField) {
	if !validSettingName(f.Setting) {
		c.report(f.Start, "V1", "config field %s.%s reads setting %q; a setting's name is letters, digits, `.`, `_` and `-`, starting with a letter (ADR 0140 §1)", d.Name, f.Name, f.Setting)
	}
	if _, optional := f.Type.(*syntax.UnionType); !optional && f.Default == nil {
		c.report(f.Start, "V1", "config field %s.%s reads setting %q, so it is `| None` or has a default: on a first run nothing is saved yet, and the program must still start (ADR 0140 §1)", d.Name, f.Name, f.Setting)
	}
	for _, other := range c.settingFields(f.Setting) {
		if other.decl != d || other.field.Name != f.Name {
			c.report(f.Start, "V1", "setting %q is read by both %s.%s and %s.%s; one field reads a setting, so a save has one type to parse as (ADR 0140 §1)", f.Setting, d.Name, f.Name, other.decl.Name, other.field.Name)
			break
		}
	}
}

func validSettingName(s string) bool {
	if s == "" || !(s[0] >= 'a' && s[0] <= 'z' || s[0] >= 'A' && s[0] <= 'Z') {
		return false
	}
	for _, r := range s {
		if !(r == '.' || r == '_' || r == '-' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

type settingField struct {
	decl  *syntax.ConfigDecl
	field syntax.ConfigField
}

// settingFields is every field of this package's config declarations that
// reads setting name.
func (c *checker) settingFields(name string) []settingField {
	u := c.universe
	for u != nil && u.Parent != nil {
		u = u.Parent
	}
	if u == nil {
		return nil
	}
	var out []settingField
	for _, decl := range u.Decls {
		cd, ok := decl.(*syntax.ConfigDecl)
		if !ok {
			continue
		}
		for _, f := range cd.Fields {
			if f.Setting == name {
				out = append(out, settingField{cd, f})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].field.Start.Offset < out[j].field.Start.Offset })
	return out
}

// checkSettingName is V1 for a Settings call's first argument (ADR 0140
// §4): a string literal naming a setting this package's config reads, so a
// typo is an error here and a save always has a type to parse as.
func (c *checker) checkSettingName(op string, arg syntax.Expr) {
	start, _ := arg.(syntax.Spanned).Bounds()
	lit, ok := arg.(*syntax.StringLit)
	if !ok || hasInterpolation(lit) {
		c.report(start, "V1", "Settings.%s names its setting with a string literal, so the checker can find the config field it saves to (ADR 0140 §4)", op)
		return
	}
	name := plainLiteral(lit)
	if len(c.settingFields(name)) == 0 {
		c.report(start, "V1", "Settings.%s names setting %q, which no config field of this package reads; declare one, such as `x: string | None from setting %q` (ADR 0140 §4)", op, name, name)
	}
}

func plainLiteral(lit *syntax.StringLit) string {
	var b strings.Builder
	for _, part := range lit.Parts {
		b.WriteString(part.Text)
	}
	return b.String()
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
	case kindActivity, kindGateway, kindTest:
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

// checkWhen is V2 (ADR 0122 §1). The loader evaluates a condition at startup,
// before any program code runs, so it may use only what is known then: the
// environment, read through other config declarations. Those must have no
// condition of their own, so there is no chain to order and no cycle to
// find, and a condition never reads a config that might be absent.
func (c *checker) checkWhen(d *syntax.ConfigDecl) {
	if d.When == nil {
		return
	}
	if !c.whenShape(d, d.When) {
		return
	}
	// Reading config is for a gateway or an activity (B10); a condition is
	// the loader reading it, so it is checked as a gateway would read it.
	saved := c.callerKind
	c.callerKind = kindGateway
	t := c.inferExpr(d.When, map[string]types.Type{})
	c.callerKind = saved
	if p, ok := t.(types.Primitive); !ok || p.Name != "bool" {
		start, _ := d.When.(syntax.Spanned).Bounds()
		c.report(start, "V2", "config %s's `when` is %s, not a bool (ADR 0122 §1)", d.Name, t)
	}
}

// whenShape reports whether e uses only what a startup condition may, and
// says what it may not otherwise.
func (c *checker) whenShape(d *syntax.ConfigDecl, e syntax.Expr) bool {
	start, _ := e.(syntax.Spanned).Bounds()
	bad := func(what string) bool {
		c.report(start, "V2", "config %s's `when` can't use %s; it is read once at startup, from comparisons (==, !=), and, or, not, literals, enum variants, and fields of config that has no `when` of its own (ADR 0122 §1)", d.Name, what)
		return false
	}
	switch e := e.(type) {
	case *syntax.ParenExpr:
		return c.whenShape(d, e.Inner)
	case *syntax.NotExpr:
		return c.whenShape(d, e.Operand)
	case *syntax.BinaryExpr:
		if e.Op == syntax.OpOr {
			e.OrKnown = true // a `when` is boolean throughout, so `fiig fmt` writes `||`
		}
		switch e.Op {
		case syntax.OpEq, syntax.OpNe, syntax.OpAnd, syntax.OpOr:
			left := c.whenShape(d, e.Left)
			right := c.whenShape(d, e.Right)
			return left && right
		}
		return bad("the operator " + string(e.Op))
	case *syntax.IntLit:
		return true
	case *syntax.StringLit:
		for _, part := range e.Parts {
			if part.Interp != nil {
				return bad("an interpolated string")
			}
		}
		return true
	case *syntax.Ident:
		if e.Name == "true" || e.Name == "false" {
			return true
		}
		return bad("the name " + e.Name + " (name a config field as Config.field)")
	case *syntax.FieldAccess:
		target, ok := e.Target.(*syntax.Ident)
		if !ok {
			return bad("a field of anything but a config declaration or an enum")
		}
		if cd, isConfig := c.configRef(target.Name); isConfig {
			switch {
			case cd == d:
				return bad("its own fields")
			case cd.When != nil:
				return bad(cd.Name + ", which has a `when` of its own")
			}
			f, has := cd.Field(e.Field)
			if !has {
				c.report(start, "", "config %s has no field %q", cd.Name, e.Field)
				return false
			}
			// ADR 0140 §5: a soft setting changes while the process runs, and
			// which config is present is decided at start.
			if f.SoftSetting() {
				return bad(cd.Name + "." + e.Field + ", a setting that changes while the process runs (ADR 0140 §5; a setting read `on restart` may be used)")
			}
			return true
		}
		if decl, found := c.lookup(target.Name); found {
			if _, isEnum := decl.(*syntax.EnumDecl); isEnum {
				return true
			}
		}
		return bad(target.Name + "." + e.Field)
	}
	return bad("this expression")
}
