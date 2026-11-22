package runtime

import (
	"context"
	"strings"
	"testing"

	"fiig-lang/compiler/checker"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// ADR 0049: calls into formulas groups.

const runtimeVectorGroup = `formulas vector {
    formula dot(a: Vector, b: Vector) -> int {
        return a.x * b.x + a.y * b.y;
    }

    formula lengthSquared(v: Vector) -> int {
        return dot(v, v);
    }

    formula power(base: int, exp: int) -> int {
        if (exp == 0) {
            return 1;
        }
        return base * power(base, exp - 1);
    }
}`

const runtimeVectorModel = "model Vector {\n    x: int\n    y: int\n}"

func TestFormulaRunsAndReachesItsSiblings(t *testing.T) {
	u := universeFromSource(t, runtimeVectorModel, runtimeVectorGroup,
		"use geo.vector\n\nrule lengthOf(v: Vector) -> int {\n    return vector.lengthSquared(v);\n}",
		"use geo.vector\n\nrule cube(n: int) -> int {\n    return vector.power(n, 3);\n}",
	)
	in := newTestInterp(u, nil)
	v := Record{TypeName: "Vector", Fields: map[string]Value{"x": Int(3), "y": Int(4)}}
	if got, fault := in.Call(context.Background(), "lengthOf", map[string]Value{"v": v}); fault != nil || got != Int(25) {
		t.Fatalf("lengthOf = %v, %v; want 25", got, fault)
	}
	if got, fault := in.Call(context.Background(), "cube", map[string]Value{"n": Int(2)}); fault != nil || got != Int(8) {
		t.Fatalf("cube = %v, %v; want 8", got, fault)
	}
}

// TestFormulaRecursionIsBoundedByTheCallDepth is ADR 0054 §6 applied to
// formulas: recursion is legal, and a runaway one faults.
func TestFormulaRecursionIsBoundedByTheCallDepth(t *testing.T) {
	u := universeFromSource(t, runtimeVectorModel, runtimeVectorGroup,
		"use geo.vector\n\nrule forever(n: int) -> int {\n    return vector.power(n, -1);\n}",
	)
	in := newTestInterp(u, nil)
	in.MaxCallDepth = 50
	_, fault := in.Call(context.Background(), "forever", map[string]Value{"n": Int(2)})
	if fault == nil || !strings.Contains(fault.Error(), "call depth limit") {
		t.Fatalf("got %v, want a call depth fault", fault)
	}
}

func TestFormulaAcrossPackages(t *testing.T) {
	geometry := buildTestPackage(t, []*syntax.File{
		mustParseFile(t, runtimeVectorModel),
		mustParseFile(t, runtimeVectorGroup),
		// A group calling a group of its own package, through its own import.
		mustParseFile(t, "use acme.geometry.vector\n\nformulas shapes {\n    formula diagonal(v: Vector) -> int {\n        return vector.lengthSquared(v);\n    }\n}"),
	}, nil)
	packages := types.Packages{"acme.geometry": geometry}
	app := mustParseFile(t, "use acme.geometry\nuse acme.geometry.shapes\n\nrule diag(v: geometry.Vector) -> int {\n    return shapes.diagonal(v);\n}")
	universe, _ := types.BuildUniverse([]*syntax.File{app})
	in := New(universe, NewCapabilityRegistry())
	in.Imports = map[string]map[string]*types.Package{"diag": checker.ResolveFileImports(app, packages)}
	in.Groups = map[string]map[string]*types.GroupRef{"diag": checker.ResolveFileGroups(app, packages)}

	v := Record{TypeName: "Vector", Fields: map[string]Value{"x": Int(1), "y": Int(2)}}
	if got, fault := in.Call(context.Background(), "diag", map[string]Value{"v": v}); fault != nil || got != Int(5) {
		t.Fatalf("diag = %v, %v; want 5", got, fault)
	}
}
