package checker

import (
	"sort"
	"strings"

	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// ADR 0073: json.decode, json.encode, and the bug classes J1 and J2.

var (
	jsonResultOf = func(t types.Type) types.Type {
		return types.Generic{Name: "Result", Args: []types.Type{t, types.JsonErrorType}}
	}
	jsonDecodeInputs = []types.Type{types.Primitive{Name: "string"}, types.Primitive{Name: "bytes"}, types.JsonValueType}
)

// jsonCall is the member of e when e calls the prelude json group.
func jsonCall(e syntax.Expr, env map[string]types.Type) (*syntax.CallExpr, string, bool) {
	call, ok := e.(*syntax.CallExpr)
	if !ok {
		return nil, "", false
	}
	fa, ok := call.Callee.(*syntax.FieldAccess)
	if !ok {
		return nil, "", false
	}
	target, ok := fa.Target.(*syntax.Ident)
	if !ok || target.Name != types.JsonGroup {
		return nil, "", false
	}
	if _, isLocal := env[target.Name]; isLocal {
		return nil, "", false
	}
	return call, fa.Field, true
}

// checkExpectedJson handles json.decode where the context gives it a type
// (§2): `event: Event = json.decode(body)?` decodes into Event, and
// `return json.decode(body)` from a `Result<Event, JsonError>` does too.
// It reports whether e was such a call.
func (c *checker) checkExpectedJson(e syntax.Expr, expected types.Type, env map[string]types.Type) bool {
	if try, ok := e.(*syntax.TryExpr); ok {
		call, member, ok := jsonCall(try.Operand, env)
		if !ok || member != "decode" {
			return false
		}
		c.checkJsonDecode(call, expected, env)
		c.checkTryError(try, types.Generic{Name: "Result", Args: []types.Type{expected, types.JsonErrorType}})
		return true
	}
	call, member, ok := jsonCall(e, env)
	if !ok || member != "decode" {
		return false
	}
	result, isResult := expected.(types.Generic)
	if !isResult || result.Name != "Result" || len(result.Args) != 2 {
		if expected == nil {
			return false // no context: inferCall reports it
		}
		start, _ := call.Bounds()
		c.report(start, "A1", "json.decode returns a Result<%s, JsonError>, not a %s; add `?` to take the value out", expected, expected)
		c.checkJsonDecode(call, expected, env)
		return true
	}
	if !types.AssignableTo(types.JsonErrorType, result.Args[1]) {
		start, _ := call.Bounds()
		c.report(start, "A1", "type mismatch: got Result<%s, JsonError>, expected %s", result.Args[0], expected)
	}
	c.checkJsonDecode(call, result.Args[0], env)
	return true
}

// inferJsonCall types a json call with no expected type: json.encode and
// json.encodePretty, which need none, and json.decode, which then has nothing to decode
// into, as `[]` has no element type (A12).
func (c *checker) inferJsonCall(call *syntax.CallExpr, member string, env map[string]types.Type) types.Type {
	start, _ := call.Bounds()
	switch member {
	case "decode":
		c.report(start, "A12", "json.decode has no type to decode into; give it one with a typed declaration, e.g. `event: Event = json.decode(body)?`")
		c.checkJsonDecode(call, nil, env)
		return jsonResultOf(types.Unknown{})
	case "encode", "encodePretty":
		// ADR 0073 §9 (amended 2026-11-21): encodePretty accepts exactly
		// what encode does.
		arg, ok := c.jsonArg(call, "value", env)
		if ok {
			if why := c.jsonRefusal(arg, false); why != "" {
				c.report(start, "J1", "json.%s of %s: %s, which can't be encoded as JSON (ADR 0073 §8)", member, arg, why)
			} else {
				c.checkJsonOverlap(start, arg, "json."+member+" of "+arg.String())
			}
		}
		return types.Primitive{Name: "bytes"}
	}
	c.report(start, "", "the prelude json group has no member %q; it has decode, encode, and encodePretty", member)
	for _, a := range call.Args {
		c.inferExpr(a.Value, env)
	}
	return types.Unknown{}
}

// checkJsonDecode checks json.decode's argument and the type it decodes
// into, and records that type on the call for the runtime (into is nil
// when the context gives none).
func (c *checker) checkJsonDecode(call *syntax.CallExpr, into types.Type, env map[string]types.Type) {
	start, _ := call.Bounds()
	if arg, ok := c.jsonArg(call, "text", env); ok && !isUnknown(arg) {
		fits := false
		for _, in := range jsonDecodeInputs {
			fits = fits || types.AssignableTo(arg, in)
		}
		if !fits {
			c.report(start, "A1", "json.decode takes a string, bytes, or a JsonValue, got %s", arg)
		}
	}
	if into == nil || isUnknown(into) {
		if into != nil {
			c.report(start, "A12", "json.decode has no type to decode into here; bind it with a typed declaration, e.g. `event: Event = json.decode(body)?`")
		}
		return
	}
	if why := c.jsonRefusal(into, true); why != "" {
		c.report(start, "J1", "json.decode into %s: %s, which can't be decoded from JSON (ADR 0073 §8)", into, why)
		return
	}
	c.checkJsonOverlap(start, into, "json.decode into "+into.String())
	call.DecodeInto = into
}

// jsonArg checks a json call's one argument, positional or named param,
// and returns its type.
func (c *checker) jsonArg(call *syntax.CallExpr, param string, env map[string]types.Type) (types.Type, bool) {
	names := make([]string, len(call.Args))
	for i, a := range call.Args {
		names[i] = a.Name
	}
	if _, ok := types.BindArgs([]string{param}, names); !ok {
		start, _ := call.Bounds()
		c.report(start, "", "json calls take one argument, %s", param)
		for _, a := range call.Args {
			c.inferExpr(a.Value, env)
		}
		return nil, false
	}
	return c.inferExpr(call.Args[0].Value, env), true
}

// jsonRefusal says why a value of type t can't be decoded (decode) or
// encoded (ADR 0073 §8), naming the part of t at fault, or "" when it can:
// a Secret, a Connection, a capability or interface value, a BatchResult,
// and, to decode, a Map whose keys aren't strings. bytes is base64 (ADR
// 0078).
func (c *checker) jsonRefusal(t types.Type, decode bool) string {
	return c.refusal(t, decode, "", map[syntax.Decl]bool{})
}

func (c *checker) refusal(t types.Type, decode bool, where string, seen map[syntax.Decl]bool) string {
	is := func(what string) string {
		if where == "" {
			return "it is " + what
		}
		return "field " + where + " is " + what
	}
	field := func(name string) string {
		if where == "" {
			return name
		}
		return where + "." + name
	}
	switch t := t.(type) {
	case types.Opaque:
		return is("a " + t.Name)
	case types.Union:
		for _, m := range t.Members {
			if why := c.refusal(m, decode, where, seen); why != "" {
				return why
			}
		}
	case types.Record:
		names := make([]string, 0, len(t.Fields))
		for n := range t.Fields {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			if why := c.refusal(t.Fields[n], decode, field(n), seen); why != "" {
				return why
			}
		}
	case types.Generic:
		if t.Name == "BatchResult" {
			return is("a BatchResult, which only a concurrent block produces")
		}
		if decode && t.Name == "Map" && len(t.Args) == 2 && !types.AssignableTo(t.Args[0], types.Primitive{Name: "string"}) && !isUnknown(t.Args[0]) {
			return is("a Map with " + t.Args[0].String() + " keys, and a JSON object's names are strings")
		}
		for _, a := range t.Args {
			if why := c.refusal(a, decode, where, seen); why != "" {
				return why
			}
		}
	case types.Named:
		if seen[t.Decl] {
			return ""
		}
		seen[t.Decl] = true
		switch d := t.Decl.(type) {
		case *syntax.CapabilityDecl:
			return is("a capability")
		case *syntax.InterfaceDecl:
			return is("an interface value")
		case *syntax.ModelDecl:
			for _, f := range d.AllFields() {
				if why := c.refusal(c.declFieldType(d, f.Type), decode, field(f.Name), seen); why != "" {
					return why
				}
			}
		case *syntax.EnumDecl:
			for _, v := range d.Variants {
				for _, f := range v.Fields {
					if why := c.refusal(c.declFieldType(d, f.Type), decode, field(v.Name+"."+f.Name), seen); why != "" {
						return why
					}
				}
			}
		}
	}
	return ""
}

// declFieldType resolves a field's type t in the scope of the package that
// declares decl, silently, as resolveField does a model's field.
func (c *checker) declFieldType(decl syntax.Decl, t syntax.Type) types.Type {
	u, imports := c.universe, universesOf(c.imports)
	if pkg := c.pkgOf(decl); pkg != nil {
		u, imports = pkg.Universe, pkg.Imports
	}
	resolved, _ := types.ResolveWithImports(t, u, imports)
	if resolved == nil {
		return types.Unknown{}
	}
	return resolved
}

// checkJsonNames is J2, J4, and W3 for one model or variant: no two fields
// read the same JSON member, `from json base64url` only on a field holding
// `bytes`, and no payload field's member is `variant`, which names the
// variant (ADR 0072 §3, ADR 0073 §7b, ADR 0078 §6).
func (c *checker) checkJsonNames(owner string, fields []syntax.Field, payload bool) {
	seen := map[string]syntax.Field{}
	for _, f := range fields {
		if f.Embedded {
			continue
		}
		if f.Base64URL {
			c.checkBase64URLField(owner, f)
		}
		name := f.JSONName()
		if payload && name == "variant" {
			how := "a field named `variant`"
			if f.JSON != "" {
				how = "a field read `from json \"variant\"`"
			}
			c.report(f.Start, "W3", "%s has %s, which is the member that names the variant in JSON (ADR 0072 §3); rename the field, or give it another member with `from json`", owner, how)
		}
		if first, dup := seen[name]; dup {
			c.report(f.Start, "J2", "%s.%s reads the JSON member %q, which %s.%s already reads (ADR 0073 §7b)", owner, f.Name, name, owner, first.Name)
			continue
		}
		seen[name] = f
	}
}

// checkBase64URLField is J4 (ADR 0078 §6): `from json base64url` says how
// the field's own `bytes` are written, so its type must be bytes all the
// way down: `bytes`, or `| None`, a `List`, or a `Map`'s values around
// bytes. Anywhere else the clause would say nothing, or would reach into a
// model or enum whose fields say for themselves. A type that doesn't
// resolve is reported where it's resolved.
func (c *checker) checkBase64URLField(owner string, f syntax.Field) {
	t, ok := types.ResolveWithImports(f.Type, c.universe, universesOf(c.imports))
	if !ok || t == nil || holdsOnlyBytes(t) {
		return
	}
	c.report(f.Start, "J4", "%s.%s is written `from json base64url`, but its type %s isn't bytes: only a field of `bytes`, `bytes | None`, or a List or Map of them can choose base64url (ADR 0078 §6)", owner, f.Name, t)
}

// holdsOnlyBytes is J4's shape: t is `bytes`, or wraps nothing but bytes
// in `| None`, `List`, or a `Map`'s values.
func holdsOnlyBytes(t types.Type) bool {
	switch t := t.(type) {
	case types.Primitive:
		return t.Name == "bytes"
	case types.Union:
		some := false
		for _, m := range t.Members {
			if _, ok := m.(types.NoneType); ok {
				continue
			}
			if !holdsOnlyBytes(m) {
				return false
			}
			some = true
		}
		return some
	case types.Generic:
		switch {
		case t.Name == "List" && len(t.Args) == 1:
			return holdsOnlyBytes(t.Args[0])
		case t.Name == "Map" && len(t.Args) == 2:
			return holdsOnlyBytes(t.Args[1])
		}
	}
	return false
}

// checkVariantSpellings is J2 for an enum: no two variants with one text
// form (ADR 0080 §1), as `A from json "B"` beside a variant `B`, since
// reading that text couldn't say which one it is.
func (c *checker) checkVariantSpellings(d *syntax.EnumDecl) {
	seen := map[string]string{}
	for _, v := range d.Variants {
		text := v.Text()
		if first, dup := seen[text]; dup {
			c.report(v.Start, "J2", "%s.%s is spelled %q, which %s.%s already is (ADR 0080 §1)", d.Name, v.Name, text, d.Name, first)
			continue
		}
		seen[text] = v.Name
	}
}

// checkPromotedJsonNames is J2 across a model's flattened fields: an own
// field and a promoted one, or two promoted ones, that read one member.
// Two own fields are checkJsonNames's.
func (c *checker) checkPromotedJsonNames(m *syntax.ModelDecl) {
	if m.Flat == nil {
		return
	}
	own := map[string]bool{}
	for _, f := range m.Fields {
		if !f.Embedded {
			own[f.Name] = true
		}
	}
	readers := map[string][]string{}
	var order []string
	for _, f := range m.AllFields() {
		name := f.JSONName()
		if _, ok := readers[name]; !ok {
			order = append(order, name)
		}
		readers[name] = append(readers[name], f.Name)
	}
	for _, name := range order {
		fs := readers[name]
		if len(fs) < 2 {
			continue
		}
		allOwn := true
		for _, f := range fs {
			allOwn = allOwn && own[f]
		}
		if !allOwn {
			c.report(m.Start, "J2", "fields %s of %s all read the JSON member %q (ADR 0073 §7b)", strings.Join(fs, " and "), m.Name, name)
		}
	}
}
