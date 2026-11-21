package runtime

import (
	"context"
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"fiig-lang/compiler/constant"
	"fiig-lang/compiler/decimal"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// ADR 0071: config values, read from the environment once, before the
// program starts, and never again.

// Lookup reads one environment variable; ok is false when it is unset.
type Lookup func(name string) (value string, ok bool)

// ConfigSource is one config declaration to read, with what reading it
// needs: the package scope its enum types and defaults resolve in, the
// package's path for messages ("" for the application), and the
// application manifest's renames for that package (§5).
type ConfigSource struct {
	Decl     *syntax.ConfigDecl
	Universe *types.Universe
	Imports  map[string]*types.Universe
	Package  string
	Rename   map[string]string // the package's variable -> the deployment's
	// Groups are the capability-group and config imports its `when`
	// condition may name (`use acme.signin.SignInConfig`); WhenText is the
	// condition as written, for messages and `fiig config` (ADR 0122).
	Groups   map[string]*types.GroupRef
	WhenText string
}

// EnvName is the variable a field actually reads: its own, or the one the
// application renamed it to.
func (s ConfigSource) EnvName(f syntax.ConfigField) string {
	if to, ok := s.Rename[f.Env]; ok {
		return to
	}
	return f.Env
}

// Where is how a message names a field: `config Stripe.apiKey (acme.stripe;
// STRIPE_KEY renamed in fiig.pkg.json)`.
func (s ConfigSource) Where(f syntax.ConfigField) string {
	w := "config " + s.Decl.Name + "." + f.Name
	var notes []string
	if s.Package != "" {
		notes = append(notes, s.Package)
	}
	if to, ok := s.Rename[f.Env]; ok && to != f.Env {
		notes = append(notes, f.Env+" renamed in fiig.pkg.json")
	}
	if len(notes) > 0 {
		w += " (" + strings.Join(notes, "; ") + ")"
	}
	return w
}

// LoadConfig reads every source's fields through lookup (§3) and keeps the
// values for the life of in. It returns every problem found, not just the
// first, so one restart finds them all; in reads nothing when there are
// any. A variable set to the empty string reads as unset (§1).
func (in *Interp) LoadConfig(sources []ConfigSource, lookup Lookup) []string {
	return in.loadConfig(sources, lookup, false)
}

// LoadTestConfig is LoadConfig for `fiig test` (ADR 0091 §5): a field
// that is unset and has no default is left out instead of reported, so a
// test that reads it fails naming it, and one that fakes it or never
// reads it runs.
func (in *Interp) LoadTestConfig(sources []ConfigSource, lookup Lookup) []string {
	return in.loadConfig(sources, lookup, true)
}

// startupField is one config field read from a startup route, `Adapter.workflow`
// of package pkg ("" for the application).
type startupField struct {
	rec        Record
	field      string
	pkg, route string
}

// MarkDegraded says the startup route `Adapter.workflow` of package pkg ended
// degraded (ADR 0127), so the config fields that read it are true from here
// on. It runs once, after the startup routes and before anything serves.
func (in *Interp) MarkDegraded(pkg, route string) {
	for _, s := range in.startupFields {
		if s.pkg == pkg && s.route == route {
			s.rec.Fields[s.field] = Bool(true)
		}
	}
}

func (in *Interp) loadConfig(sources []ConfigSource, lookup Lookup, partial bool) []string {
	in.startupFields = nil
	// ADR 0140: what is saved at start, for hard settings and Pending.
	in.settingRefs, in.softFields, in.startSettings = map[*types.Universe]map[string]*settingRef{}, map[*syntax.ConfigDecl]map[string]*settingRef{}, nil
	if in.Settings != nil {
		snap, f := in.currentSettings(context.Background())
		if f != nil {
			return []string{f.Message}
		}
		in.startSettings = snap
	}
	var problems []string
	values := map[*syntax.ConfigDecl]Record{}
	absent := map[*syntax.ConfigDecl]string{}
	// A config with a `when` (ADR 0122) is read after the others, since its
	// condition is evaluated from their values: first the unconditional
	// sources, then each conditional one that its condition lets in.
	var conditional []ConfigSource
	for _, src := range sources {
		if src.Decl.When != nil {
			conditional = append(conditional, src)
			continue
		}
		problems = append(problems, in.readSource(src, lookup, partial, values)...)
	}
	savedConfig := in.Config
	in.Config = values
	for _, src := range conditional {
		// `fiig test` loads config partially, and a test fakes what it reads
		// (ADR 0091 §5), so a conditional config is present there.
		if !partial {
			present, why := in.whenHolds(src)
			if why != "" {
				// Its condition read config that didn't load, which is
				// already a problem above; nothing more to say yet.
				continue
			}
			if !present {
				absent[src.Decl] = fmt.Sprintf("config %s isn't set in this deployment: its condition (%s) is false (ADR 0122)", src.Decl.Name, src.WhenText)
				continue
			}
		}
		problems = append(problems, in.readSource(src, lookup, partial, values)...)
	}
	if len(problems) > 0 {
		in.Config = savedConfig
		return problems
	}
	in.Config = values
	in.ConfigAbsent = absent
	return nil
}

// whenHolds evaluates src's `when` from the config values read so far. why
// is set instead when it can't be evaluated.
func (in *Interp) whenHolds(src ConfigSource) (holds bool, why string) {
	ctx := withScope(context.Background(), scope{universe: src.Universe, groups: src.Groups})
	v, fault := in.EvalExpr(ctx, src.Decl.When, NewEnv())
	if fault != nil {
		return false, fault.Message
	}
	b, ok := v.(Bool)
	if !ok {
		return false, fmt.Sprintf("its condition is %s, not a bool", v)
	}
	return bool(b), ""
}

// readSource reads one config declaration's fields into values and returns
// what was wrong with them.
func (in *Interp) readSource(src ConfigSource, lookup Lookup, partial bool, values map[*syntax.ConfigDecl]Record) []string {
	var problems []string
	rec := Record{Fields: map[string]Value{}}
	for _, f := range src.Decl.Fields {
		// ADR 0127: no variable to read. It is false until the route says so.
		if f.Startup != "" {
			rec.Fields[f.Name] = Bool(false)
			in.startupFields = append(in.startupFields, startupField{rec: rec, field: f.Name, pkg: src.Package, route: f.Startup})
			continue
		}
		t, _ := types.ResolveWithImports(f.Type, src.Universe, src.Imports)
		if f.Setting != "" {
			problems = append(problems, in.readSetting(src, f, t, lookup, rec)...)
			continue
		}
		name := src.EnvName(f)
		text, set := lookup(name)
		given := set // the variable is there, even when empty
		if set && text == "" {
			set = false
		}
		switch {
		case set:
			v, problem := in.readConfigValue(src, text, name, t)
			if problem != "" {
				shown := strconv.Quote(text)
				if types.HoldsSecret(t, nil) {
					shown = "(hidden)"
				}
				problems = append(problems, fmt.Sprintf("%s=%s isn't %s; %s", name, shown, problem, src.Where(f)))
				continue
			}
			rec.Fields[f.Name] = v
		case f.Default != nil:
			v, fault := in.configDefault(src, f, t)
			if fault != nil {
				problems = append(problems, fmt.Sprintf("%s's default: %s", src.Where(f), fault.Message))
				continue
			}
			rec.Fields[f.Name] = v
		case partial && !given:
			// `fiig test`: a field the env file doesn't name stays unset, even
			// one that may be None, so reading it fails the test naming it
			// (ADR 0091 §5, Hearsay G68). `NAME=` in the env file, or
			// `fake C { field: None }`, says unset on purpose.
		case admitsNoneType(t):
			rec.Fields[f.Name] = None{}
		case partial:
		default:
			problems = append(problems, fmt.Sprintf("%s is unset; %s needs %s", name, src.Where(f), article(withoutNoneType(t))))
		}
	}
	values[src.Decl] = rec
	return problems
}

// readSetting reads a field that names a setting (ADR 0140): its env source
// and default now, and, for a hard setting, the value it keeps for the life
// of the process. A soft setting is resolved per run (softValue).
func (in *Interp) readSetting(src ConfigSource, f syntax.ConfigField, t types.Type, lookup Lookup, rec Record) []string {
	ref := &settingRef{src: src, field: f, t: t}
	if f.Env != "" {
		name := src.EnvName(f)
		if text, set := lookup(name); set && text != "" {
			v, problem := in.readConfigValue(src, text, name, t)
			if problem != "" {
				shown := strconv.Quote(text)
				if types.HoldsSecret(t, nil) {
					shown = "(hidden)"
				}
				return []string{fmt.Sprintf("%s=%s isn't %s; %s", name, shown, problem, src.Where(f))}
			}
			ref.env = v
		}
	}
	if f.Default != nil {
		v, fault := in.configDefault(src, f, t)
		if fault != nil {
			return []string{fmt.Sprintf("%s's default: %s", src.Where(f), fault.Message)}
		}
		ref.def = v
	}
	u := src.Universe
	for u != nil && u.Parent != nil {
		u = u.Parent
	}
	if in.settingRefs[u] == nil {
		in.settingRefs[u] = map[string]*settingRef{}
	}
	in.settingRefs[u][f.Setting] = ref
	if f.SoftSetting() {
		if in.softFields[src.Decl] == nil {
			in.softFields[src.Decl] = map[string]*settingRef{}
		}
		in.softFields[src.Decl][f.Name] = ref
		return nil
	}
	v, fault := in.settingValue(ref, in.startSettings)
	if fault != nil {
		return []string{fault.Message}
	}
	rec.Fields[f.Name] = v
	return nil
}

// readConfigValue reads one variable's text as t, or says what it isn't.
func (in *Interp) readConfigValue(src ConfigSource, text, env string, t types.Type) (Value, string) {
	t = withoutNoneType(t)
	switch t := t.(type) {
	case types.Opaque:
		if t == types.SecretType {
			return NewSecret(env, text), ""
		}
	case types.Primitive:
		switch t.Name {
		case "string":
			return Str(text), ""
		case "bool":
			switch text {
			case "true":
				return Bool(true), ""
			case "false":
				return Bool(false), ""
			}
			return nil, "a bool (true or false)"
		case "int":
			n, err := syntax.IntValue(strings.TrimPrefix(text, "-"))
			if err != nil || strings.HasPrefix(text, "+") {
				return nil, "an int"
			}
			if strings.HasPrefix(text, "-") {
				n = -n
			}
			return Int(n), ""
		case "float":
			f, err := strconv.ParseFloat(text, 64)
			if f, ok := finite(f); err == nil && ok && !strings.ContainsAny(text, "xXpP_") && !strings.EqualFold(strings.TrimLeft(text, "+-"), "inf") {
				return Float(f), ""
			}
			return nil, "a float"
		case "decimal":
			if d, ok := decimal.ParseText(text); ok {
				return Decimal{d}, ""
			}
			return nil, "a decimal"
		}
	case types.Named:
		if e, ok := t.Decl.(*syntax.EnumDecl); ok {
			var names []string
			for _, v := range e.Variants {
				if v.Text() == text && len(v.Fields) == 0 {
					ctx := withScope(context.Background(), scope{universe: src.Universe})
					val, _, fault := in.constructEnum(ctx, e, v, nil, NewEnv())
					if fault != nil {
						return nil, fault.Message
					}
					return val, ""
				}
				if len(v.Fields) == 0 {
					names = append(names, v.Text())
				}
			}
			return nil, fmt.Sprintf("a %s (%s)", e.Name, strings.Join(names, ", "))
		}
	}
	return nil, t.String()
}

// configDefault evaluates a field's constant default in its package, as the
// field's own type: `= 1` for a float field is 1.0.
func (in *Interp) configDefault(src ConfigSource, f syntax.ConfigField, t types.Type) (Value, *Fault) {
	t = withoutNoneType(t)
	if p, ok := t.(types.Primitive); ok && (p.Name == "float" || p.Name == "decimal") {
		if c, isConst, _ := constant.Eval(f.Default); isConst {
			if p.Name == "float" {
				fl, _ := c.Rat.Float64()
				return Float(fl), nil
			}
			// A literal keeps its written scale: `= 1.50` is 1.50.
			if lit, ok := f.Default.(*syntax.FloatLit); ok {
				if d, ok := decimal.ParseText(lit.Text); ok {
					return Decimal{d}, nil
				}
			}
			if d, ok := decimal.ParseText(ratText(c.Rat)); ok {
				return Decimal{d}, nil
			}
		}
	}
	ctx := withScope(context.Background(), scope{universe: src.Universe})
	return in.EvalExpr(ctx, f.Default, NewEnv())
}

// ratText writes an exact constant as decimal text; a constant default is
// a finite decimal, since it was written as one.
func ratText(r *big.Rat) string {
	if s := r.FloatString(34); strings.Contains(s, ".") {
		return strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	return r.FloatString(0)
}

func admitsNoneType(t types.Type) bool {
	if u, ok := t.(types.Union); ok {
		for _, m := range u.Members {
			if _, none := m.(types.NoneType); none {
				return true
			}
		}
	}
	return false
}

func withoutNoneType(t types.Type) types.Type {
	u, ok := t.(types.Union)
	if !ok {
		return t
	}
	for _, m := range u.Members {
		if _, none := m.(types.NoneType); !none {
			return m
		}
	}
	return t
}

func article(t types.Type) string {
	s := t.String()
	if strings.ContainsRune("aeiouAEIOU", rune(s[0])) {
		return "an " + s
	}
	return "a " + s
}

// readConfig is `Name.field` on a config declaration: a test's fake of
// it first (ADR 0091 §4), then configValue.
func (in *Interp) readConfig(ctx context.Context, d *syntax.ConfigDecl, field string) (Value, *Fault) {
	if v, fault, inTest := in.testConfigValue(ctx, d, field); inTest {
		return v, fault
	}
	if ref, soft := in.softField(d, field); soft {
		return in.softValue(ctx, ref)
	}
	return in.configValue(d, field)
}

// configValue is `Name.field` on a config declaration, read once by
// LoadConfig.
func (in *Interp) configValue(d *syntax.ConfigDecl, field string) (Value, *Fault) {
	rec, ok := in.Config[d]
	if !ok {
		if why, absent := in.ConfigAbsent[d]; absent {
			return nil, Faultf("%s", why)
		}
		return nil, Faultf("config %s was not read before the program started", d.Name)
	}
	v, ok := rec.Fields[field]
	if !ok {
		return nil, Faultf("config %s has no field %q", d.Name, field)
	}
	return v, nil
}
