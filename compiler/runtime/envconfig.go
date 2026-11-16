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
	var problems []string
	values := map[*syntax.ConfigDecl]Record{}
	for _, src := range sources {
		rec := Record{Fields: map[string]Value{}}
		for _, f := range src.Decl.Fields {
			t, _ := types.ResolveWithImports(f.Type, src.Universe, src.Imports)
			name := src.EnvName(f)
			text, set := lookup(name)
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
			case admitsNoneType(t):
				rec.Fields[f.Name] = None{}
			default:
				problems = append(problems, fmt.Sprintf("%s is unset; %s needs %s", name, src.Where(f), article(withoutNoneType(t))))
			}
		}
		values[src.Decl] = rec
	}
	if len(problems) > 0 {
		return problems
	}
	in.Config = values
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

// configValue is `Name.field` on a config declaration, read once by
// LoadConfig.
func (in *Interp) configValue(d *syntax.ConfigDecl, field string) (Value, *Fault) {
	rec, ok := in.Config[d]
	if !ok {
		return nil, Faultf("config %s was not read before the program started", d.Name)
	}
	v, ok := rec.Fields[field]
	if !ok {
		return nil, Faultf("config %s has no field %q", d.Name, field)
	}
	return v, nil
}
