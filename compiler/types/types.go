// Package types is the checker's semantic type representation — distinct
// from syntax.Type, which is just parsed syntax (a dotted name plus
// optional generic args). Resolving a syntax.Type into one of these is
// what makes two types comparable: fiig-lang.md §3.3 has no implicit
// conversions anywhere, so equality here is always exact nominal identity,
// never structural compatibility.
package types

import (
	"sort"
	"strings"

	"fiig-lang/compiler/syntax"
)

// Type is a resolved, semantic type.
type Type interface {
	String() string
	isType()
}

// Primitive is a built-in scalar — "string", "int", "float" (ADR 0048),
// "decimal" (ADR 0064), "bool" (ADR 0041), or "bytes" (ADR 0058).
type Primitive struct{ Name string }

func (Primitive) isType()          {}
func (p Primitive) String() string { return p.Name }

// Named is a reference to a declared model, value, or error. Decl is the
// actual declaration, so Equal can compare by identity — that's the
// nominal-typing guarantee: two Named types are the same type iff they
// point at the same declaration, never by matching shape.
type Named struct {
	Name string
	Decl syntax.Decl
}

func (Named) isType()          {}
func (n Named) String() string { return n.Name }

// Generic is an instantiated prelude generic: List<T>, Result<T, E>,
// BatchResult<K, S, F> (grammar.md §1 — these are prelude identifiers, not
// keywords, so the checker special-cases their names rather than finding
// them as ordinary declarations).
type Generic struct {
	Name string
	Args []Type
}

func (Generic) isType() {}
func (g Generic) String() string {
	parts := make([]string, len(g.Args))
	for i, a := range g.Args {
		parts[i] = a.String()
	}
	return g.Name + "<" + strings.Join(parts, ", ") + ">"
}

// NoneType is the absence prelude value (§3.4) — only ever meaningful as a
// member of a Union; a field/param/return type is never bare None.
type NoneType struct{}

func (NoneType) isType()        {}
func (NoneType) String() string { return "None" }

// Union is `A | B | ...` (§3.4), most commonly `T | None`.
type Union struct{ Members []Type }

func (Union) isType() {}
func (u Union) String() string {
	parts := make([]string, len(u.Members))
	for i, m := range u.Members {
		parts[i] = m.String()
	}
	return strings.Join(parts, " | ")
}

// Record is an anonymous structural type — legal only as a local binding's
// inferred type (§3.3); appearing anywhere in a public signature is bug
// class A8. Fields are sorted by name for String() so output and equality
// are deterministic.
type Record struct{ Fields map[string]Type }

func (Record) isType() {}
func (r Record) String() string {
	names := make([]string, 0, len(r.Fields))
	for n := range r.Fields {
		names = append(names, n)
	}
	sort.Strings(names)
	parts := make([]string, len(names))
	for i, n := range names {
		parts[i] = n + ": " + r.Fields[n].String()
	}
	return "{ " + strings.Join(parts, ", ") + " }"
}

// Unknown is returned wherever the checker doesn't model something —
// a capability call (no capability-contract system exists to type-check
// one against, as opposed to Milestone 5's declarative capability
// *provider* configuration, ADR 0027), or a qualified/dotted type name
// from an aliased package when no package context was given at all
// (Resolve, as opposed to ResolveWithImports, Milestone 6, ADR 0032).
// It's deliberately compatible with everything in Equal/AssignableTo,
// so the checker doesn't cascade false positives into territory it
// hasn't modeled, rather than refusing to check anything that touches a
// capability at all.
type Unknown struct{}

func (Unknown) isType()        {}
func (Unknown) String() string { return "?" }

// Equal is exact nominal identity — no implicit conversions (ADR 0019
// already settled this for arithmetic; this is the same rule for every
// type). Unknown is equal to anything, by design.
func Equal(a, b Type) bool {
	if _, ok := a.(Unknown); ok {
		return true
	}
	if _, ok := b.(Unknown); ok {
		return true
	}
	switch a := a.(type) {
	case Primitive:
		b, ok := b.(Primitive)
		return ok && a.Name == b.Name
	case Named:
		b, ok := b.(Named)
		return ok && a.Decl == b.Decl
	case Generic:
		b, ok := b.(Generic)
		if !ok || a.Name != b.Name || len(a.Args) != len(b.Args) {
			return false
		}
		for i := range a.Args {
			if !Equal(a.Args[i], b.Args[i]) {
				return false
			}
		}
		return true
	case NoneType:
		_, ok := b.(NoneType)
		return ok
	case Opaque:
		b, ok := b.(Opaque)
		return ok && a.Name == b.Name
	case Union:
		b, ok := b.(Union)
		if !ok || len(a.Members) != len(b.Members) {
			return false
		}
		for i := range a.Members {
			if !Equal(a.Members[i], b.Members[i]) {
				return false
			}
		}
		return true
	case Record:
		b, ok := b.(Record)
		if !ok || len(a.Fields) != len(b.Fields) {
			return false
		}
		for name, at := range a.Fields {
			bt, ok := b.Fields[name]
			if !ok || !Equal(at, bt) {
				return false
			}
		}
		return true
	}
	return false
}

// AssignableTo reports whether a value of type `value` may be used where
// `target` is required — Equal, except a bare value is also assignable to
// a Union target when it matches one of the union's members (returning a
// plain Customer where Customer | None is expected, the ordinary case).
// The one other admission is ADR 0043's: a capability group is assignable
// to an interface its package's manifest says it provides — one hop,
// nominal against the manifest, no variance and no hierarchy. Nothing else
// about "no implicit conversions" bends.
func AssignableTo(value, target Type) bool {
	if Equal(value, target) {
		return true
	}
	if group, ok := CapabilityGroup(value); ok {
		if iface, ok := Interface(target); ok {
			for _, p := range group.Provides {
				if p == iface {
					return true
				}
			}
		}
	}
	// A response with no body fits any HttpResponse<B> (ADR 0069 §3), as
	// None fits B | None.
	if vb, ok := IsHttpResponse(value); ok {
		if _, isNone := vb.(NoneType); isNone {
			if _, ok := IsHttpResponse(target); ok {
				return true
			}
		}
	}
	if v, ok := value.(Generic); ok && v.Name == "List" && len(v.Args) == 1 {
		// A list of groups is a list of an interface they all provide; lists
		// are values here, never mutated through an alias, so this is sound.
		if t, ok := target.(Generic); ok && t.Name == "List" && len(t.Args) == 1 {
			if _, isGroup := CapabilityGroup(v.Args[0]); isGroup && AssignableTo(v.Args[0], t.Args[0]) {
				return true
			}
		}
	}
	if u, ok := target.(Union); ok {
		for _, m := range u.Members {
			if AssignableTo(value, m) {
				return true
			}
		}
	}
	return false
}
