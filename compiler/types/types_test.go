package types

import (
	"testing"

	"fiig-lang/compiler/parser"
	"fiig-lang/compiler/syntax"
)

func buildTestUniverse(t *testing.T, src string) (*Universe, *syntax.File) {
	t.Helper()
	f, errs := parser.Parse(src)
	if len(errs) > 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}
	u, conflicts := BuildUniverse([]*syntax.File{f})
	if len(conflicts) != 0 {
		t.Fatalf("unexpected conflicts: %+v", conflicts)
	}
	return u, f
}

func TestResolvePrimitives(t *testing.T) {
	u, f := buildTestUniverse(t, `rule r(a: int, b: string) -> int {
    return a;
}`)
	rd := f.Decls[0].(*syntax.RuleDecl)
	at, ok := Resolve(rd.Params[0].Type, u)
	if !ok || !Equal(at, Primitive{Name: "int"}) {
		t.Fatalf("got %v, ok=%v", at, ok)
	}
	bt, ok := Resolve(rd.Params[1].Type, u)
	if !ok || !Equal(bt, Primitive{Name: "string"}) {
		t.Fatalf("got %v, ok=%v", bt, ok)
	}
}

func TestResolvePreludeGenericsAndNone(t *testing.T) {
	u, f := buildTestUniverse(t, `rule r(a: List<int>, b: Result<int, string>, c: int | None) -> int {
    return a;
}`)
	rd := f.Decls[0].(*syntax.RuleDecl)

	listT, ok := Resolve(rd.Params[0].Type, u)
	if !ok {
		t.Fatal("List<int> did not resolve")
	}
	want := Generic{Name: "List", Args: []Type{Primitive{Name: "int"}}}
	if !Equal(listT, want) {
		t.Fatalf("got %v, want %v", listT, want)
	}

	resultT, ok := Resolve(rd.Params[1].Type, u)
	if !ok {
		t.Fatal("Result<int, string> did not resolve")
	}
	wantResult := Generic{Name: "Result", Args: []Type{Primitive{Name: "int"}, Primitive{Name: "string"}}}
	if !Equal(resultT, wantResult) {
		t.Fatalf("got %v, want %v", resultT, wantResult)
	}

	unionT, ok := Resolve(rd.Params[2].Type, u)
	if !ok {
		t.Fatal("int | None did not resolve")
	}
	wantUnion := Union{Members: []Type{Primitive{Name: "int"}, NoneType{}}}
	if !Equal(unionT, wantUnion) {
		t.Fatalf("got %v, want %v", unionT, wantUnion)
	}
}

func TestResolveNamedDeclarationsAreNominal(t *testing.T) {
	var files []*syntax.File
	for _, src := range []string{"model CustomerRef {\n    id: string\n}\n", "model OrderRef {\n    id: string\n}\n"} {
		f, errs := parser.Parse(src)
		if len(errs) > 0 {
			t.Fatalf("unexpected parse errors: %v", errs)
		}
		files = append(files, f)
	}
	u, _ := BuildUniverse(files)

	// CustomerRef and OrderRef have identical fields, but are distinct
	// nominal types — models are never compared structurally (§3.3).
	custRef, ok := u.Lookup("CustomerRef")
	if !ok {
		t.Fatal("CustomerRef not in universe")
	}
	orderRef, ok := u.Lookup("OrderRef")
	if !ok {
		t.Fatal("OrderRef not in universe")
	}
	custType := Named{Name: "CustomerRef", Decl: custRef}
	orderType := Named{Name: "OrderRef", Decl: orderRef}
	if Equal(custType, orderType) {
		t.Fatal("CustomerRef and OrderRef must not be equal, even though their fields match")
	}
	if !Equal(custType, Named{Name: "CustomerRef", Decl: custRef}) {
		t.Fatal("a Named type must equal itself")
	}
}

func TestAssignableToUnion(t *testing.T) {
	customer := Named{Name: "Customer", Decl: &syntax.ModelDecl{Name: "Customer"}}
	optional := Union{Members: []Type{customer, NoneType{}}}

	if !AssignableTo(customer, optional) {
		t.Fatal("a bare Customer should be assignable to Customer | None")
	}
	if !AssignableTo(NoneType{}, optional) {
		t.Fatal("None should be assignable to Customer | None")
	}
	other := Named{Name: "Order", Decl: &syntax.ModelDecl{Name: "Order"}}
	if AssignableTo(other, optional) {
		t.Fatal("an unrelated Named type should not be assignable to Customer | None")
	}
}

func TestUnknownIsAssignableEverywhere(t *testing.T) {
	if !Equal(Unknown{}, Primitive{Name: "int"}) {
		t.Fatal("Unknown should compare equal to anything, by design")
	}
	if !AssignableTo(Unknown{}, Named{Name: "Customer"}) {
		t.Fatal("Unknown should be assignable anywhere")
	}
}

func TestResolveUnresolvedNameFails(t *testing.T) {
	u, f := buildTestUniverse(t, "rule r(a: DoesNotExist) -> int {\n    return 1;\n}\n")
	rd := f.Decls[0].(*syntax.RuleDecl)
	_, ok := Resolve(rd.Params[0].Type, u)
	if ok {
		t.Fatal("expected an unresolved name to fail, not silently succeed")
	}
}

func TestResolveQualifiedNameIsUnknownNotError(t *testing.T) {
	// Resolve alone has no package context (Milestone 6's cross-package
	// resolution needs ResolveWithImports, below) — a dotted/aliased type
	// reference resolves to Unknown, deliberately unmodeled rather than a
	// false-positive error.
	u, f := buildTestUniverse(t, "rule r(a: systemTime.Instant) -> int {\n    return 1;\n}\n")
	rd := f.Decls[0].(*syntax.RuleDecl)
	got, ok := Resolve(rd.Params[0].Type, u)
	if !ok {
		t.Fatal("a qualified name should resolve ok (to Unknown), not fail outright")
	}
	if _, isUnknown := got.(Unknown); !isUnknown {
		t.Fatalf("got %v, want Unknown", got)
	}
}

// TestResolveWithImportsResolvesKnownAlias is ADR 0032's actual
// cross-package resolution: a qualified reference resolves for real once
// its alias is a known entry in imports, instead of staying Unknown.
func TestResolveWithImportsResolvesKnownAlias(t *testing.T) {
	standardTime, _ := buildTestUniverse(t, "model Instant {\n    days: int\n}\n")
	u, f := buildTestUniverse(t, "rule r(a: systemTime.Instant) -> int {\n    return 1;\n}\n")
	rd := f.Decls[0].(*syntax.RuleDecl)

	got, ok := ResolveWithImports(rd.Params[0].Type, u, map[string]*Universe{"systemTime": standardTime})
	if !ok {
		t.Fatal("a known alias's real member should resolve, not fail")
	}
	named, isNamed := got.(Named)
	if !isNamed {
		t.Fatalf("got %v, want a Named type", got)
	}
	instantDecl, _ := standardTime.Lookup("Instant")
	if named.Decl != instantDecl {
		t.Fatal("resolved to a different declaration than standard.time's own Instant")
	}
}

// TestResolveWithImportsUnknownAliasStaysUnknown: an alias this call
// wasn't given (no package context for it, or a capability-like
// qualified reference) stays Unknown, exactly like Resolve alone —
// nothing regresses for a dependency that simply isn't loaded.
func TestResolveWithImportsUnknownAliasStaysUnknown(t *testing.T) {
	u, f := buildTestUniverse(t, "rule r(a: systemTime.Instant) -> int {\n    return 1;\n}\n")
	rd := f.Decls[0].(*syntax.RuleDecl)
	got, ok := ResolveWithImports(rd.Params[0].Type, u, map[string]*Universe{"other": u})
	if !ok {
		t.Fatal("an alias with no loaded package should stay Unknown, not fail outright")
	}
	if _, isUnknown := got.(Unknown); !isUnknown {
		t.Fatalf("got %v, want Unknown", got)
	}
}

// TestResolveWithImportsKnownAliasMissingMemberFails: once an alias
// really is a loaded package, a name that doesn't exist in it is a real
// unresolved-reference error, the same as a bad unqualified name — not
// silently Unknown.
func TestResolveWithImportsKnownAliasMissingMemberFails(t *testing.T) {
	standardTime, _ := buildTestUniverse(t, "model Instant {\n    days: int\n}\n")
	u, f := buildTestUniverse(t, "rule r(a: systemTime.DoesNotExist) -> int {\n    return 1;\n}\n")
	rd := f.Decls[0].(*syntax.RuleDecl)
	_, ok := ResolveWithImports(rd.Params[0].Type, u, map[string]*Universe{"systemTime": standardTime})
	if ok {
		t.Fatal("a real package missing the referenced member should fail, not silently succeed")
	}
}
