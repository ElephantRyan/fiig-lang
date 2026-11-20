package types

import (
	"fmt"

	"fiig-lang/compiler/lexer"
	"fiig-lang/compiler/migration"
	"fiig-lang/compiler/syntax"
)

// Universe is every declaration in one package, keyed by bare name — a
// package is every file being checked together, not one file at a time,
// since declarations constantly reference each other across files under
// §3.9's one-declaration-per-file rule (a workflow in one file calls a
// gateway declared in a different one). Milestone 6 (ADR 0032): one
// application or library is one Universe; two different packages never
// share one, even when built together, so the same bare name in two
// packages is never a conflict — see Package/Packages below for how a
// file resolves a *qualified* reference into another package's Universe.
type Universe struct {
	Decls map[string]syntax.Decl
	// Ambiguous is every name BuildUniverse saw declared more than once.
	// Decls still holds the first one for each — removing it would turn one
	// duplicate name into an "undefined name" at every call site — but
	// *which* one survives is an artifact of the order files happened to be
	// walked, so any judgement that depends on the winner's kind is as
	// likely to be wrong as right. Consumers that make such a judgement
	// check here first and say nothing instead (compiler/checker's own
	// (*checker).lookup); the duplicate itself is reported once, from the
	// Conflict BuildUniverse returned alongside this.
	Ambiguous map[string]bool

	// Parent is set on a file scope (ADR 0054): the Universe of one file's
	// private members, whose lookups fall back to the package Universe in
	// Parent. nil on a package Universe.
	Parent *Universe
	// scopes maps every declaration whose file has a private block (its
	// public entry and each private member) to that file's scope. Only a
	// package Universe has it; see ScopeOf.
	scopes map[syntax.Decl]*Universe
	// Private maps a private member's name to the public entry of every
	// file that declares one by that name, so a reference to it from
	// another file can be reported as an escape (H4) rather than as an
	// undefined name. Only a package Universe has it.
	Private map[string][]syntax.Decl
}

// Package bundles one package's own Universe with its own import
// aliases. Resolving one of its declarations' signatures needs both: the
// Universe for the names it declares, and Imports for any further-
// qualified type or call inside one of those declarations (e.g.
// `company.time`'s `addBusinessDays` returns `time.Instant`, resolved
// against company.time's *own* "time" alias for standard.time — not
// whatever alias the file calling `addBusinessDays` happens to use for
// company.time itself). Imports is one level deep (alias -> that
// package's bare Universe) rather than recursing into further Packages,
// since nothing attested chains an aliased reference more than two
// segments deep (grammar.md §2).
type Package struct {
	// Path is the package's canonical dotted path, set by compiler/loader;
	// "" for a package built without one.
	Path     string
	Universe *Universe
	Imports  map[string]*Universe
	// Groups is the package's own capability-group imports (`use
	// acme.http.Client`, ADR 0043 §5), alias -> group, which its gateways'
	// bodies reach through `requires`.
	Groups map[string]*GroupRef
	// Dependencies is the packages this one was built against, by path:
	// what its Imports' universes belong to.
	Dependencies Packages
	// Provides is the package manifest's `provides`, interface path ->
	// the capability group fulfilling it (ADR 0043 §2), already verified
	// by the checker; the runtime finds a database driver's group here
	// (ADR 0084 §1). nil for a package built without a manifest.
	Provides map[string]string
	// Migrations is the package's schema migrations (ADR 0096), in number
	// order; nil when it has none.
	Migrations []migration.File
}

// Packages maps a canonical dotted package path (manifest.Manifest.Path)
// to that package's own Package. compiler/loader is what actually
// populates one, by walking a dependency's manifest/lockfile and parsing
// its source (ADR 0032's consequences, built out across ADR 0033); this
// package only defines the shape resolution itself needs.
type Packages map[string]*Package

// Conflict is a duplicate declaration name found while building a
// Universe — not itself one of bug-classes.md's numbered entries in the
// general case, but necessary infrastructure: without a unique name per
// declaration, nothing else the checker does is well-defined. First and
// Second are the two colliding declarations (in registration order), kept
// so callers can single out a specific numbered bug class when the
// collision has one — e.g. A6, two `conversion` declarations sharing a
// name (bug-classes.md: "otherwise indistinguishable canonical identity"
// narrowed, post-ADR-0006, to a plain naming collision).
type Conflict struct {
	Name   string
	At     lexer.Pos
	First  syntax.Decl
	Second syntax.Decl
}

// BuildUniverse collects every declaration across files into one lookup
// table. The first declaration with a given name wins; every later one
// with the same name is reported as a Conflict rather than silently
// shadowing it, and the name is recorded in Ambiguous so the winner's
// arbitrariness doesn't get treated as a fact downstream.
//
// A file's private members (ADR 0054) go into a file scope of their own
// instead, reachable through ScopeOf. A private name that matches a
// package-level declaration, or another private member of the same file,
// is a Conflict too (§3a), but it is ambiguous only inside that file: the
// package-level declaration stays usable everywhere else.
func BuildUniverse(files []*syntax.File) (*Universe, []Conflict) {
	u := &Universe{
		Decls: map[string]syntax.Decl{}, Ambiguous: map[string]bool{},
		scopes: map[syntax.Decl]*Universe{}, Private: map[string][]syntax.Decl{},
	}
	var conflicts []Conflict
	for _, f := range files {
		for _, d := range f.Decls {
			name := DeclName(d)
			if name == "" {
				continue
			}
			if existing, exists := u.Decls[name]; exists {
				start, _ := d.(syntax.Spanned).Bounds()
				conflicts = append(conflicts, Conflict{Name: name, At: start, First: existing, Second: d})
				u.Ambiguous[name] = true
				continue
			}
			u.Decls[name] = d
		}
	}
	flattenModels(u)
	for _, f := range files {
		if f.Private == nil || len(f.Decls) == 0 {
			continue
		}
		conflicts = append(conflicts, u.addFileScope(f)...)
	}
	return u, conflicts
}

// addFileScope builds f's file scope from its private block and registers
// it for f's public entry and every private member.
func (u *Universe) addFileScope(f *syntax.File) []Conflict {
	entry := f.Decls[0]
	fs := &Universe{Decls: map[string]syntax.Decl{}, Ambiguous: map[string]bool{}, Parent: u}
	u.scopes[entry] = fs
	// ADR 0102 §2: a test file's tests share its private block.
	for _, d := range f.Decls[1:] {
		if _, ok := d.(*syntax.TestDecl); ok {
			u.scopes[d] = fs
		}
	}
	var conflicts []Conflict
	for _, d := range f.Private.Members {
		u.scopes[d] = fs
		name := DeclName(d)
		if name == "" {
			continue
		}
		start, _ := d.(syntax.Spanned).Bounds()
		if existing, exists := fs.Decls[name]; exists {
			conflicts = append(conflicts, Conflict{Name: name, At: start, First: existing, Second: d})
			fs.Ambiguous[name] = true
			continue
		}
		fs.Decls[name] = d
		u.Private[name] = append(u.Private[name], entry)
		if existing, exists := u.Decls[name]; exists {
			conflicts = append(conflicts, Conflict{Name: name, At: start, First: existing, Second: d})
			fs.Ambiguous[name] = true
		}
	}
	flattenModels(fs)
	return conflicts
}

// ScopeOf returns the Universe that d's body and signature resolve names
// in: d's file scope when its file has a private block (ADR 0054), else
// the package Universe itself. Safe on a file scope too; it defers to the
// package Universe.
func (u *Universe) ScopeOf(d syntax.Decl) *Universe {
	root := u
	for root.Parent != nil {
		root = root.Parent
	}
	if fs, ok := root.scopes[d]; ok {
		return fs
	}
	return root
}

// IsPrivate reports whether d is a private member of some file in this
// package (ADR 0054), as opposed to a public entry.
func (u *Universe) IsPrivate(d syntax.Decl) bool {
	fs := u.ScopeOf(d)
	return fs.Parent != nil && fs.Decls[DeclName(d)] == d
}

// PrivateOwners returns the public entry of every file in this package
// that has a private member named name (ADR 0054), for reporting a
// reference to one from outside its file.
func (u *Universe) PrivateOwners(name string) []syntax.Decl {
	if u == nil {
		return nil
	}
	for u.Parent != nil {
		u = u.Parent
	}
	return u.Private[name]
}

// Root is the package Universe u belongs to: u itself, or for a file scope
// (ADR 0054), its package.
func (u *Universe) Root() *Universe {
	for u.Parent != nil {
		u = u.Parent
	}
	return u
}

// Lookup finds a declaration by name, in this scope and then, for a file
// scope, in its package, and last in the prelude (ADR 0060's SocketError).
func (u *Universe) Lookup(name string) (syntax.Decl, bool) {
	if d, ok := u.Decls[name]; ok {
		return d, ok
	}
	if u.Parent != nil {
		return u.Parent.Lookup(name)
	}
	d, ok := preludeDecls[name]
	return d, ok
}

// IsAmbiguous reports whether name was declared more than once anywhere
// this scope can see it: in the package, or (for a file scope) as a
// private member that collides with something.
func (u *Universe) IsAmbiguous(name string) bool {
	if u.Ambiguous[name] {
		return true
	}
	return u.Parent != nil && u.Parent.IsAmbiguous(name)
}

// DeclName returns a declaration's name, generically over every kind.
func DeclName(d syntax.Decl) string { return syntax.DeclName(d) }

// DeclKindName returns a declaration's kind as the keyword that declares
// it; see syntax.DeclKindName.
func DeclKindName(d syntax.Decl) string { return syntax.DeclKindName(d) }

// preludeGenerics are the prelude's generic names (grammar.md §1: "Result
// Ok Err BatchResult List None ... distinguished only by convention").
// Arity isn't enforced here — Resolve just maps however many args were
// written; the checker reports a mismatch separately if it matters.
var preludeGenerics = map[string]bool{
	"List":         true,
	"Result":       true,
	"BatchResult":  true,
	"Map":          true, // ADR 0065 §4
	"HttpResponse": true, // ADR 0069 §3
}

// Resolve converts a parsed syntax.Type into a semantic Type, looking up
// named references in u. Returns ok=false when the name can't be resolved
// at all (distinct from Unknown, which means "deliberately not modeled" —
// an unresolved name is a real error the caller should report). A
// qualified/aliased reference (e.g. `systemTime.Instant`) is always
// Unknown here, since Resolve alone has no package context — see
// ResolveWithImports for the package-aware version every other behavior
// here is otherwise identical to.
func Resolve(t syntax.Type, u *Universe) (Type, bool) {
	return resolve(t, u, nil)
}

// ResolveWithImports is Resolve, but also resolves a two-segment
// qualified reference (`alias.Name`) against imports when alias is a
// key it recognizes — Milestone 6 cross-package type resolution (ADR
// 0032). Every caller that doesn't yet have package context (every one
// before this milestone) keeps using Resolve, unaffected.
func ResolveWithImports(t syntax.Type, u *Universe, imports map[string]*Universe) (Type, bool) {
	return resolve(t, u, imports)
}

func resolve(t syntax.Type, u *Universe, imports map[string]*Universe) (Type, bool) {
	switch t := t.(type) {
	case *syntax.QualifiedType:
		return resolveQualified(t, u, imports)
	case *syntax.RecordType:
		fields := make(map[string]Type, len(t.Fields))
		ok := true
		for _, f := range t.Fields {
			ft, fok := resolve(f.Type, u, imports)
			fields[f.Name] = ft
			ok = ok && fok
		}
		return Record{Fields: fields}, ok
	case *syntax.UnionType:
		members := make([]Type, len(t.Members))
		ok := true
		for i, m := range t.Members {
			mt, mok := resolve(m, u, imports)
			members[i] = mt
			ok = ok && mok
		}
		return Union{Members: members}, ok
	default:
		return Unknown{}, false
	}
}

func resolveQualified(t *syntax.QualifiedType, u *Universe, imports map[string]*Universe) (Type, bool) {
	if len(t.Path) > 1 {
		if len(t.Path) == 2 {
			if pkg, ok := imports[t.Path[0]]; ok {
				if decl, found := pkg.Lookup(t.Path[1]); found {
					switch decl.(type) {
					case *syntax.ModelDecl, *syntax.EnumDecl, *syntax.InterfaceDecl:
						return Named{Name: t.Path[0] + "." + t.Path[1], Decl: decl}, true
					}
				}
				// alias is a real, loaded package, but the name doesn't
				// exist in it — a genuine unresolved reference, same as an
				// unqualified bad name.
				return Unknown{}, false
			}
		}
		// Either three-or-more segments (unattested — grammar.md §2 never
		// chains an alias this deep) or alias isn't a package this checker
		// has loaded (no package context at all, or a capability-like
		// qualified reference) — deliberately unmodeled, not an error.
		return Unknown{}, true
	}
	name := t.Path[0]
	switch name {
	case "string", "int", "float", "decimal", "bool", "bytes":
		return Primitive{Name: name}, true
	case "None":
		return NoneType{}, true
	case "Connection":
		return ConnectionType, true
	case "Secret":
		return SecretType, true
	}
	if preludeGenerics[name] {
		args := make([]Type, len(t.Args))
		ok := true
		for i, a := range t.Args {
			at, aok := resolve(a, u, imports)
			args[i] = at
			ok = ok && aok
		}
		return Generic{Name: name, Args: args}, ok
	}
	if decl, found := u.Lookup(name); found {
		switch decl.(type) {
		case *syntax.ModelDecl, *syntax.EnumDecl, *syntax.InterfaceDecl:
			return Named{Name: name, Decl: decl}, true
		}
	}
	return Unknown{}, false
}

// String renders a syntax.Type for diagnostics without needing it resolved
// first — used when reporting an unresolved-name error, where Resolve
// itself returned ok=false and there's nothing else to print.
func SyntaxString(t syntax.Type) string {
	switch t := t.(type) {
	case *syntax.QualifiedType:
		s := t.Path[0]
		for _, seg := range t.Path[1:] {
			s += "." + seg
		}
		if len(t.Args) > 0 {
			s += "<"
			for i, a := range t.Args {
				if i > 0 {
					s += ", "
				}
				s += SyntaxString(a)
			}
			s += ">"
		}
		return s
	case *syntax.UnionType:
		s := ""
		for i, m := range t.Members {
			if i > 0 {
				s += " | "
			}
			s += SyntaxString(m)
		}
		return s
	default:
		return fmt.Sprintf("%v", t)
	}
}
