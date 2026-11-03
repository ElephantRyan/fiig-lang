package types

import (
	"fmt"
	"strings"

	"fiig-lang/compiler/lexer"
	"fiig-lang/compiler/syntax"
)

// ADR 0043: interfaces, capability groups, and the manifest's `provides`.

// Interface reports whether t is an interface type, and which one.
func Interface(t Type) (*syntax.InterfaceDecl, bool) {
	n, ok := t.(Named)
	if !ok {
		return nil, false
	}
	d, ok := n.Decl.(*syntax.InterfaceDecl)
	return d, ok
}

// CapabilityGroup reports whether t is the type of a capability group used
// as a value (`return Stripe`), and which group.
func CapabilityGroup(t Type) (*syntax.CapabilityDecl, bool) {
	n, ok := t.(Named)
	if !ok {
		return nil, false
	}
	d, ok := n.Decl.(*syntax.CapabilityDecl)
	return d, ok
}

// HoldsCapability reports whether a value of type t is, or contains, a
// capability: an interface, a capability group, or a list or union of one.
// ADR 0043 §6b capitalizes such a binding, and §6c keeps it out of models.
func HoldsCapability(t Type) bool {
	switch t := t.(type) {
	case Named:
		switch t.Decl.(type) {
		case *syntax.InterfaceDecl, *syntax.CapabilityDecl:
			return true
		}
	case Generic:
		for _, a := range t.Args {
			if HoldsCapability(a) {
				return true
			}
		}
	case Union:
		for _, m := range t.Members {
			if HoldsCapability(m) {
				return true
			}
		}
	}
	return false
}

// GroupRef is a group imported by path tail: a capability group (`use
// acme.http.Client`, ADR 0043 §5) in Decl, or a formulas group (`use
// standard.geometry.vector`, ADR 0049 §2.5) in Formulas, never both. Package
// declares it, and its members' signatures resolve in that package's scope;
// Package is nil for a formulas group of the importing package itself, which
// §2.5 imports like any other.
type GroupRef struct {
	Package  *Package
	Decl     *syntax.CapabilityDecl
	Formulas *syntax.FormulasDecl
}

// ProvidesError is one problem found verifying a manifest's `provides` map:
// P1 (the group does not cover the interface), P2 (a member's signature
// differs), or an untagged infrastructure error (an unknown interface or
// group).
type ProvidesError struct {
	Rule string
	Msg  string
	At   lexer.Pos
	// Decl is the declaration the error is about, when there is one, so a
	// caller with per-file attribution can say which file it is in.
	Decl syntax.Decl
}

func (e ProvidesError) Error() string {
	if e.Rule == "" {
		return e.Msg
	}
	return fmt.Sprintf("%s: [%s] %s", e.At, e.Rule, e.Msg)
}

// BindProvides verifies one package's `provides` map (ADR 0043 §2) and, for
// every entry that holds, records the interface on the group's Provides so
// AssignableTo admits the group where the interface is expected.
//
// own is the package being bound and ownPath its canonical path; deps are
// its built dependencies, keyed by path. A key names an interface by package
// path (`acme.payments.PaymentProvider`), which must be own or a dependency;
// a value names a capability group in own. Signatures are compared member by
// member, each side resolved in its own package's scope, so `Money` matches
// only when both sides mean the same declaration — never a same-shaped
// look-alike. Parameter names must match too: a provider call binds its
// arguments by the interface's names and dispatches to the group's gateway.
func BindProvides(ownPath string, own *Package, deps Packages, provides map[string]string) []ProvidesError {
	var errs []ProvidesError
	for key, groupName := range provides {
		dot := strings.LastIndex(key, ".")
		if dot <= 0 {
			errs = append(errs, ProvidesError{Msg: fmt.Sprintf("provides key %q must name an interface by package path, e.g. acme.payments.PaymentProvider", key)})
			continue
		}
		ifacePath, ifaceName := key[:dot], key[dot+1:]
		ifacePkg := own
		if ifacePath != ownPath {
			ifacePkg = deps[ifacePath]
		}
		if ifacePkg == nil {
			errs = append(errs, ProvidesError{Msg: fmt.Sprintf("provides %q: package %q is not this package or one of its dependencies", key, ifacePath)})
			continue
		}
		ifaceDecl, _ := ifacePkg.Universe.Lookup(ifaceName)
		iface, ok := ifaceDecl.(*syntax.InterfaceDecl)
		if !ok {
			errs = append(errs, ProvidesError{Msg: fmt.Sprintf("provides %q: package %q declares no interface %q", key, ifacePath, ifaceName)})
			continue
		}
		groupDecl, _ := own.Universe.Lookup(groupName)
		group, ok := groupDecl.(*syntax.CapabilityDecl)
		if !ok {
			errs = append(errs, ProvidesError{Msg: fmt.Sprintf("provides %q: this package declares no capability group %q", key, groupName)})
			continue
		}
		before := len(errs)
		for _, m := range iface.Members {
			gw := group.Gateway(m.Name)
			if gw == nil {
				errs = append(errs, ProvidesError{Rule: "P1", At: group.Start, Decl: group, Msg: fmt.Sprintf("capability %s is declared to provide %s but has no gateway %q", group.Name, iface.Name, m.Name)})
				continue
			}
			if why := signatureMismatch(m, ifacePkg, gw, own); why != "" {
				errs = append(errs, ProvidesError{Rule: "P2", At: gw.Start, Decl: group, Msg: fmt.Sprintf("gateway %s.%s does not match %s.%s: %s", group.Name, gw.Name, iface.Name, m.Name, why)})
			}
		}
		if len(errs) == before && !providesAlready(group, iface) {
			group.Provides = append(group.Provides, iface)
		}
	}
	return errs
}

// providesAlready keeps binding idempotent: a host that checks a package
// and then builds it again binds the same AST twice.
func providesAlready(g *syntax.CapabilityDecl, iface *syntax.InterfaceDecl) bool {
	for _, p := range g.Provides {
		if p == iface {
			return true
		}
	}
	return false
}

func signatureMismatch(m syntax.InterfaceMember, ifacePkg *Package, gw *syntax.GatewayDecl, groupPkg *Package) string {
	if len(m.Params) != len(gw.Params) {
		return fmt.Sprintf("%d parameter(s), want %d", len(gw.Params), len(m.Params))
	}
	for i, want := range m.Params {
		got := gw.Params[i]
		if got.Name != want.Name {
			return fmt.Sprintf("parameter %d is named %q, want %q", i+1, got.Name, want.Name)
		}
		if !sameType(want.Type, ifacePkg, got.Type, groupPkg) {
			return fmt.Sprintf("parameter %q has type %s, want %s", got.Name, SyntaxString(got.Type), SyntaxString(want.Type))
		}
	}
	if !sameType(m.ReturnType, ifacePkg, gw.ReturnType, groupPkg) {
		return fmt.Sprintf("returns %s, want %s", SyntaxString(gw.ReturnType), SyntaxString(m.ReturnType))
	}
	return ""
}

// sameType compares two written types, each resolved in its own package.
// An unresolvable side is a mismatch here, not a pass: Unknown equals
// anything, which is right for an unmodeled expression and wrong for a
// signature someone is relying on to match.
func sameType(a syntax.Type, aPkg *Package, b syntax.Type, bPkg *Package) bool {
	at, aok := ResolveWithImports(a, aPkg.Universe, aPkg.Imports)
	bt, bok := ResolveWithImports(b, bPkg.Universe, bPkg.Imports)
	if !aok || !bok {
		return false
	}
	return Equal(at, bt) && Equal(bt, at)
}
