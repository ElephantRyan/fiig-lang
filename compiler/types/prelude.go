package types

import (
	"fmt"

	"fiig-lang/compiler/parser"
	"fiig-lang/compiler/syntax"
)

// ADR 0060: the prelude types the Socket primitive works in. Connection is
// opaque; SocketError is an ordinary enum, written in fiig so it is matched
// and constructed exactly like a declared one. Rounding (ADR 0064 §4) is
// the same, for math.round and math.divide on decimals.

// Opaque is a prelude type with no fields, methods, or constructor: only a
// primitive capability creates or consumes a value of it (ADR 0060 §2).
type Opaque struct{ Name string }

func (Opaque) isType()          {}
func (o Opaque) String() string { return o.Name }

// ConnectionType is `Connection`, a Socket handle.
var ConnectionType = Opaque{Name: "Connection"}

// SecretType is `Secret` (ADR 0071 §4): a config value that is never shown.
// Only a config field produces one; it is passed on, matched, or revealed
// in a gateway.
var SecretType = Opaque{Name: "Secret"}

// HoldsSecret reports whether a value of type t is, or contains at any
// depth, a Secret, by the same walk as HoldsConnection.
func HoldsSecret(t Type, resolveField func(model *syntax.ModelDecl, f syntax.Field) Type) bool {
	return Holds(t, func(t Type) bool {
		o, ok := t.(Opaque)
		return ok && o == SecretType
	}, resolveField)
}

// preludeSources are the prelude's declarations, one per source as in any
// fiig file (ADR 0054's one public declaration per file).
var preludeSources = []string{`enum SocketError {
    HostNotFound
    Refused
    Unreachable
    Reset
    Closed
    Tls(reason: string)
}
`, `enum Rounding {
    HalfUp
    HalfEven
    Up
    Down
    Ceiling
    Floor
}
`, `model Cookie {
    name: string
    value: string
    maxAge: int | None
    path: string | None
    domain: string | None
    sameSite: SameSite | None
    readableByScripts: bool | None
    sentOverPlainHttp: bool | None
}
`, `enum SameSite {
    Strict
    Lax
    AnySite
}
`}

// preludeDecls holds the prelude's declarations, found by every Universe's
// Lookup after its own declarations.
var preludeDecls = func() map[string]syntax.Decl {
	out := map[string]syntax.Decl{}
	for _, src := range preludeSources {
		f, errs := parser.Parse(src)
		if len(errs) > 0 {
			panic(fmt.Sprintf("prelude does not parse: %v", errs))
		}
		for _, d := range f.Decls {
			out[syntax.DeclName(d)] = d
		}
	}
	return out
}()

// SocketErrorDecl is the prelude's `SocketError` enum.
var SocketErrorDecl = preludeDecls["SocketError"].(*syntax.EnumDecl)

// SocketErrorType is `SocketError` as a type.
var SocketErrorType = Named{Name: "SocketError", Decl: SocketErrorDecl}

// RoundingType is the prelude's `Rounding` enum (ADR 0064 §4), whose
// variants are in compiler/decimal's Rounding order.
var RoundingType = Named{Name: "Rounding", Decl: preludeDecls["Rounding"]}

// CookieType is the prelude's `Cookie` model (ADR 0069 §4), whose left-out
// fields take the secure defaults.
var CookieType = Named{Name: "Cookie", Decl: preludeDecls["Cookie"]}

// SameSiteType is the prelude's `SameSite` enum (ADR 0069 §4).
var SameSiteType = Named{Name: "SameSite", Decl: preludeDecls["SameSite"]}

// HttpResponseName is the prelude generic HttpResponse<B> (ADR 0069 §3),
// the envelope a response-mapping arm produces.
const HttpResponseName = "HttpResponse"

// HttpResponseFieldNames are HttpResponse's fields in declaration order,
// the order a positional construction binds.
var HttpResponseFieldNames = []string{"status", "body", "headers", "cookies"}

// HttpResponseFields is HttpResponse<B>'s fields: every one but status
// may be left out, and is then None.
func HttpResponseFields(body Type) map[string]Type {
	return map[string]Type{
		"status":  Primitive{Name: "int"},
		"body":    optional(body),
		"headers": Union{Members: []Type{Generic{Name: "Map", Args: []Type{Primitive{Name: "string"}, Primitive{Name: "string"}}}, NoneType{}}},
		"cookies": Union{Members: []Type{Generic{Name: "List", Args: []Type{CookieType}}, NoneType{}}},
	}
}

// IsHttpResponse reports whether t is HttpResponse<B>, and its B.
func IsHttpResponse(t Type) (Type, bool) {
	g, ok := t.(Generic)
	if !ok || g.Name != HttpResponseName || len(g.Args) != 1 {
		return nil, false
	}
	return g.Args[0], true
}

// optional is `t | None`, or None itself when t is None.
func optional(t Type) Type {
	switch t := t.(type) {
	case NoneType, Unknown:
		return t
	case Union:
		for _, m := range t.Members {
			if _, ok := m.(NoneType); ok {
				return t
			}
		}
		return Union{Members: append(append([]Type{}, t.Members...), NoneType{})}
	}
	return Union{Members: []Type{t, NoneType{}}}
}

// HoldsConnection reports whether a value of type t is, or contains at any
// depth, a Connection: directly, inside a generic, a union or a record, or in
// a model field. resolveField resolves one field of a model in that model's
// own scope; a field it cannot resolve is skipped.
func HoldsConnection(t Type, resolveField func(model *syntax.ModelDecl, f syntax.Field) Type) bool {
	return Holds(t, func(t Type) bool {
		o, ok := t.(Opaque)
		return ok && o == ConnectionType
	}, resolveField)
}

// HoldsResult reports whether a value of type t is, or contains at any depth,
// a Result, by the same walk as HoldsConnection.
func HoldsResult(t Type, resolveField func(model *syntax.ModelDecl, f syntax.Field) Type) bool {
	return Holds(t, func(t Type) bool {
		g, ok := t.(Generic)
		return ok && g.Name == "Result"
	}, resolveField)
}

// Holds reports whether leaf matches t or any type reachable from it: a
// generic's arguments, a union's members, a record's fields, or a model's
// fields. A model already visited is not walked again, so a model that
// reaches itself through `T | None` terminates.
func Holds(t Type, leaf func(Type) bool, resolveField func(model *syntax.ModelDecl, f syntax.Field) Type) bool {
	return holds(t, leaf, resolveField, map[*syntax.ModelDecl]bool{})
}

func holds(t Type, leaf func(Type) bool, resolveField func(*syntax.ModelDecl, syntax.Field) Type, seen map[*syntax.ModelDecl]bool) bool {
	if leaf(t) {
		return true
	}
	switch t := t.(type) {
	case Generic:
		for _, a := range t.Args {
			if holds(a, leaf, resolveField, seen) {
				return true
			}
		}
	case Union:
		for _, m := range t.Members {
			if holds(m, leaf, resolveField, seen) {
				return true
			}
		}
	case Record:
		for _, f := range t.Fields {
			if holds(f, leaf, resolveField, seen) {
				return true
			}
		}
	case Named:
		model, ok := t.Decl.(*syntax.ModelDecl)
		if !ok || seen[model] {
			return false
		}
		seen[model] = true
		for _, f := range model.Fields {
			if holds(resolveField(model, f), leaf, resolveField, seen) {
				return true
			}
		}
	}
	return false
}

// PreludeName reports what the prelude binds name to in every file, if
// anything: a primitive type, a prelude type, a Result constructor, or the
// math group. A declaration of one of these names is K2 (ADR 0049), since
// resolution and calls reach the prelude's meaning first and the
// declaration would be silently unreachable or would silently replace it.
func PreludeName(name string) (string, bool) {
	switch name {
	case "string", "int", "float", "decimal", "bool", "bytes":
		return "a primitive type", true
	case "None", "Connection", "Secret":
		return "a prelude type", true
	case "Ok", "Err":
		return "Result's constructor", true
	case MathGroup:
		return "the prelude math group", true
	}
	if preludeGenerics[name] {
		return "a prelude type", true
	}
	if _, ok := preludeDecls[name]; ok {
		return "a prelude type", true
	}
	return "", false
}
