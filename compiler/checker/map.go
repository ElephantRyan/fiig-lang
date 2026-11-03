package checker

import (
	"fiig-lang/compiler/constant"
	"fiig-lang/compiler/lexer"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// ADR 0065 §4-§8: Map<K, V>.

// mapTypes is K and V when t is Map<K, V>, or a union with exactly one Map
// member, so a literal can take its types from either.
func mapTypes(t types.Type) (key, val types.Type, ok bool) {
	switch t := t.(type) {
	case types.Generic:
		if t.Name == "Map" && len(t.Args) == 2 {
			return t.Args[0], t.Args[1], true
		}
	case types.Union:
		found := 0
		for _, m := range t.Members {
			if k, v, ok := mapTypes(m); ok {
				key, val, found = k, v, found+1
			}
		}
		return key, val, found == 1
	}
	return nil, nil, false
}

func mapOf(key, val types.Type) types.Type {
	return types.Generic{Name: "Map", Args: []types.Type{key, val}}
}

// checkMapLit checks a literal against an expected type, as checkListLit
// does: an expected Map<K, V> types every entry, which is how `[:]` gets
// its types.
func (c *checker) checkMapLit(lit *syntax.MapLit, expected types.Type, env map[string]types.Type) {
	if key, val, ok := mapTypes(expected); ok {
		for _, en := range lit.Entries {
			c.checkExpected(en.Key, key, env)
			c.checkExpected(en.Value, val, env)
		}
		c.checkDuplicateKeys(lit)
		return
	}
	if _, unknown := expected.(types.Unknown); unknown && len(lit.Entries) == 0 {
		return
	}
	got := c.inferMapLit(lit, env)
	if expected == nil || types.AssignableTo(got, expected) {
		return
	}
	c.report(lit.Start, "A1", "type mismatch: got %s, expected %s", got, expected)
}

// inferMapLit types a literal with no expected type: keys share one type
// and values share one type, with no implicit union (§5, as ADR 0057 §2).
// `[:]` has nothing to infer from, and is A12.
func (c *checker) inferMapLit(lit *syntax.MapLit, env map[string]types.Type) types.Type {
	if len(lit.Entries) == 0 {
		c.report(lit.Start, "A12", "an empty map literal has no key or value type; give it one with a typed declaration, e.g. `counts: Map<string, int> = [:]`")
		return mapOf(types.Unknown{}, types.Unknown{})
	}
	keys := make([]syntax.Expr, len(lit.Entries))
	vals := make([]syntax.Expr, len(lit.Entries))
	for i, en := range lit.Entries {
		keys[i], vals[i] = en.Key, en.Value
	}
	key := c.sharedType(keys, "map keys", env)
	val := c.sharedType(vals, "map values", env)
	c.checkMapKey(key, lit.Start)
	c.checkDuplicateKeys(lit)
	return mapOf(key, val)
}

// sharedType is the one type every expression in es has, constants landing
// in the others' type (or int, or float when any is fractional), as a list
// literal's elements do (ADR 0048 §2.4).
func (c *checker) sharedType(es []syntax.Expr, what string, env map[string]types.Type) types.Type {
	got := make([]types.Type, len(es))
	var want types.Type
	for i, e := range es {
		if !isConstant(e) {
			got[i] = c.inferExpr(e, env)
			if want == nil {
				want = got[i]
			}
		}
	}
	if want == nil {
		want = intType
		for _, e := range es {
			if v, _, _ := constant.Eval(e); v.Kind == constant.Float {
				want = floatType
			}
		}
	}
	for i, e := range es {
		if got[i] == nil {
			got[i], _ = c.landConstant(e, want)
		}
	}
	for i, e := range es[1:] {
		if types.Equal(got[i+1], got[0]) {
			continue
		}
		start, _ := e.(syntax.Spanned).Bounds()
		c.report(start, "A1", "%s must share one type: got %s, expected %s (the first one's type); to mix types, declare the map's type", what, got[i+1], got[0])
	}
	return got[0]
}

// checkMapKey is §4: a key must be comparable, since a map finds keys with
// ==. A float key has no counterpart, as closeness isn't transitive; the
// message gives the decimal route.
func (c *checker) checkMapKey(key types.Type, at lexer.Pos) {
	if c.mapKeyReported[at] {
		return
	}
	report := func(format string, args ...any) {
		if c.mapKeyReported == nil {
			c.mapKeyReported = map[lexer.Pos]bool{}
		}
		c.mapKeyReported[at] = true
		c.report(at, "M1", format, args...)
	}
	if what := c.handle(key); what != "" {
		report("%s can't be a map key: %s is not comparable", key, what)
	} else if types.HoldsResult(key, c.resolveField) {
		report("%s can't be a map key: it holds a Result, which is not comparable", key)
	} else if path, ok := c.floatPath(key, key.String(), map[syntax.Decl]bool{}); ok {
		if path == key.String() {
			report("float can't be a map key: two floats that should agree can differ in the last bit, and closeness can't group keys; round to a decimal key with math.toDecimal and math.round (ADR 0065 §4)")
		} else {
			report("%s can't be a map key: %s is float", key, path)
		}
	}
}

// checkMapKeysIn applies checkMapKey to every Map written in a type.
func (c *checker) checkMapKeysIn(t types.Type, at lexer.Pos) {
	switch t := t.(type) {
	case types.Generic:
		if t.Name == "Map" && len(t.Args) == 2 {
			c.checkMapKey(t.Args[0], at)
		}
		for _, a := range t.Args {
			c.checkMapKeysIn(a, at)
		}
	case types.Union:
		for _, m := range t.Members {
			c.checkMapKeysIn(m, at)
		}
	}
}

// checkDuplicateKeys is §5's compile-time half: two constant keys that are
// equal are A18. A repeated key the compiler can't see is a fault at run
// time.
func (c *checker) checkDuplicateKeys(lit *syntax.MapLit) {
	seen := map[string]bool{}
	for _, en := range lit.Entries {
		k, ok := constantKey(en.Key)
		if !ok {
			continue
		}
		if seen[k] {
			start, _ := en.Key.(syntax.Spanned).Bounds()
			c.report(start, "A18", "this map literal repeats the key %s; a literal lists distinct keys, so build a map with repeated keys with put", keyDisplay(en.Key, k))
		}
		seen[k] = true
	}
}

// constantKey identifies a key the compiler knows: a string with no
// interpolation, a number by value (1.5 and 1.50 are one key), or a bool.
func constantKey(e syntax.Expr) (string, bool) {
	switch e := e.(type) {
	case *syntax.StringLit:
		text := ""
		for _, p := range e.Parts {
			if p.Interp != nil {
				return "", false
			}
			text += p.Text
		}
		return "s" + text, true
	case *syntax.Ident:
		if e.Name == "true" || e.Name == "false" {
			return "b" + e.Name, true
		}
	}
	if v, ok, err := constant.Eval(e); ok && err == nil && !v.Irrational {
		return "n" + v.Rat.RatString(), true
	}
	return "", false
}

// keyDisplay is a repeated key as a diagnostic shows it: as written when
// it's a literal.
func keyDisplay(e syntax.Expr, key string) string {
	switch e := e.(type) {
	case *syntax.IntLit:
		return e.Text
	case *syntax.FloatLit:
		return e.Text
	case *syntax.StringLit:
		return `"` + key[1:] + `"`
	}
	return key[1:]
}
