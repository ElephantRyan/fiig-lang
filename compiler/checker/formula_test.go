package checker

import (
	"strings"
	"testing"

	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// ADR 0049: formulas groups.

const vectorModel = `model Vector {
	x: int;
	y: int;
}`

const vectorGroup = `formulas vector {
	formula dot(a: Vector, b: Vector) -> int {
		return a.x * b.x + a.y * b.y;
	}

	formula lengthSquared(v: Vector) -> int {
		return dot(v, v);
	}

	formula scaled(v: Vector, k: int) -> Vector {
		return Vector(x: v.x * k, y: v.y * k);
	}

	formula power(base: int, exp: int) -> int {
		if (exp == 0) {
			return 1;
		}
		return base * power(base, exp - 1);
	}
}`

// checkGeo checks srcs as the package `geo`, alongside Vector and the
// vector group.
func checkGeo(t *testing.T, srcs ...string) []Diagnostic {
	t.Helper()
	files := parseAll(t, append([]string{vectorModel, vectorGroup}, srcs...)...)
	return CheckPackage(files, nil, "geo", nil)
}

func TestFormulasAreCallableFromEveryKind(t *testing.T) {
	diags := checkGeo(t,
		"use geo.vector;\n\nrule isLong(v: Vector) -> bool {\n\treturn vector.lengthSquared(v) > 100;\n}",
		"use geo.vector;\n\nconversion doubled(v: Vector) -> Vector {\n\treturn vector.scaled(v, k: 2);\n}",
		"use geo.vector;\n\nactivity area(v: Vector) -> int {\n\treturn vector.dot(a: v, b: v);\n}",
		"use geo.vector;\n\nworkflow cube(n: int) -> int {\n\treturn vector.power(n, 3);\n}",
		"use geo.vector as vec;\n\ngateway probe(v: Vector) -> int\n\trequires Socket\n{\n\treturn vec.lengthSquared(v);\n}",
	)
	if len(diags) != 0 {
		t.Errorf("unexpected diagnostics: %+v", diags)
	}
}

func TestFormulaCallsAreTyped(t *testing.T) {
	diags := checkGeo(t,
		"use geo.vector;\n\nrule bad(v: Vector) -> string {\n\treturn vector.lengthSquared(v);\n}",
		"use geo.vector;\n\nrule badArg(n: int) -> int {\n\treturn vector.lengthSquared(n);\n}",
		"use geo.vector;\n\nrule missing(v: Vector) -> int {\n\treturn vector.norm(v);\n}",
	)
	if got := rules(diags)["A1"]; got != 2 {
		t.Errorf("got %d A1, want 2 (wrong return use, wrong argument): %+v", got, diags)
	}
	if !hasMessage(diags, `formulas group vector has no formula "norm"`) {
		t.Errorf("missing member not reported: %+v", diags)
	}
}

func TestFormulaRestrictionsAreK3K4K5(t *testing.T) {
	diags := checkGeo(t, `enum GeoError {
	Bad
}`, `formulas checks {
	formula parse(s: string) -> Result<int, GeoError> {
		return Ok(1);
	}

	formula fetch(s: string) -> int requires Socket {
		return 1;
	}

	formula sqrt(x: int) -> int;
}`)
	got := rules(diags)
	for _, rule := range []string{"K3", "K4", "K5"} {
		if got[rule] != 1 {
			t.Errorf("got %d %s, want 1: %+v", got[rule], rule, diags)
		}
	}
}

func TestFormulaCallsOnlyFormulasB11(t *testing.T) {
	diags := checkGeo(t,
		"rule positive(n: int) -> bool {\n\treturn n > 0;\n}",
		"capability Store {\n\tgateway get(id: string) -> int\n\t\trequires Socket\n\t{\n\t\treturn 1;\n\t}\n}",
		`formulas leaky {
	formula viaRule(n: int) -> bool {
		return positive(n);
	}

	formula viaGroup(id: string) -> int {
		return Store.get(id);
	}
}`)
	if got := rules(diags)["B11"]; got != 2 {
		t.Errorf("got %d B11, want 2 (a rule, a capability group): %+v", got, diags)
	}
}

func TestFormulaMayCallAnotherGroup(t *testing.T) {
	diags := checkGeo(t, "use geo.vector;\n\nformulas shapes {\n\tformula diagonal(v: Vector) -> int {\n\t\treturn vector.lengthSquared(v);\n\t}\n}")
	if len(diags) != 0 {
		t.Errorf("unexpected diagnostics: %+v", diags)
	}
}

// TestGroupMustBeImportedEvenInItsOwnPackage is §2.5: a group's visibility
// is stated at the top of each file that uses it, and with the package's
// path known, the import must name it.
func TestGroupMustBeImportedEvenInItsOwnPackage(t *testing.T) {
	diags := checkGeo(t,
		"rule isLong(v: Vector) -> bool {\n\treturn vector.lengthSquared(v) > 100;\n}",
		"use elsewhere.vector;\n\nrule isShort(v: Vector) -> bool {\n\treturn vector.lengthSquared(v) < 100;\n}",
	)
	if n := countMessages(diags, "formulas group vector is not imported"); n != 2 {
		t.Errorf("got %d not-imported reports, want 2: %+v", n, diags)
	}
}

// TestMembersAreUnqualifiedOnlyInsideTheirGroup is §2.1: no formula is ever
// reachable by a bare name outside its own group.
func TestMembersAreUnqualifiedOnlyInsideTheirGroup(t *testing.T) {
	diags := checkGeo(t, "use geo.vector;\n\nrule isLong(v: Vector) -> bool {\n\treturn lengthSquared(v) > 100;\n}")
	if !hasMessage(diags, `undefined name "lengthSquared"`) {
		t.Errorf("bare member call outside its group not rejected: %+v", diags)
	}
}

// TestGroupNameOnlyCollidesWhereImported is §2.5's reason for importing
// every group: a local name only meets a group name in a file that imports
// it, and there it is G4.
func TestGroupNameOnlyCollidesWhereImported(t *testing.T) {
	clean := checkGeo(t, "rule count(vector: List<int>) -> int {\n\treturn vector.length();\n}")
	if len(clean) != 0 {
		t.Errorf("a local named like an unimported group was rejected: %+v", clean)
	}
	diags := checkGeo(t, "use geo.vector;\n\nrule count(vector: List<int>) -> int {\n\treturn vector.length();\n}")
	if got := rules(diags)["G4"]; got != 2 {
		t.Errorf("got %d G4, want 2 (the parameter and the import): %+v", got, diags)
	}
}

func TestGroupNamesAreLowercaseN1(t *testing.T) {
	diags := checkGeo(t,
		"formulas Shapes {\n\tformula Area(n: int) -> int {\n\t\treturn n;\n\t}\n}",
		"use geo.vector as Vec;\n\nrule isLong(v: Vector) -> bool {\n\treturn Vec.lengthSquared(v) > 100;\n}",
	)
	got := rules(diags)
	if got["N1"] != 2 || got["N2"] != 1 {
		t.Errorf("got N1=%d N2=%d, want 2 and 1: %+v", got["N1"], got["N2"], diags)
	}
}

func TestGatewayMayNotRequireAFormulasGroup(t *testing.T) {
	diags := checkGeo(t, "use geo.vector;\n\ngateway probe(v: Vector) -> int\n\trequires vector\n{\n\treturn 1;\n}")
	if !hasMessage(diags, "which is a formulas group, not a capability") {
		t.Errorf("requires naming a formulas group not rejected: %+v", diags)
	}
}

func TestFormulasGroupAcrossPackages(t *testing.T) {
	geometry := buildDep(t, "acme.geometry", nil, nil, vectorModel, vectorGroup)
	app := types.Packages{"acme.geometry": geometry}
	clean := mustParse(t, "use acme.geometry;\nuse acme.geometry.vector;\n\nrule isLong(v: geometry.Vector) -> bool {\n\treturn vector.lengthSquared(v) > 100;\n}")
	if diags := Check([]*syntax.File{clean}, app); len(diags) != 0 {
		t.Errorf("unexpected diagnostics: %+v", diags)
	}
	bad := mustParse(t, "use acme.geometry.vector;\n\nrule isLong(n: int) -> bool {\n\treturn vector.lengthSquared(n) > 100;\n}")
	if got := rules(Check([]*syntax.File{bad}, app))["A1"]; got != 1 {
		t.Errorf("got %d A1 for a wrong argument across packages, want 1", got)
	}
	missing := mustParse(t, "use acme.geometry.matrix;\n\nrule isLong(n: int) -> bool {\n\treturn n > 1;\n}")
	if !hasMessage(Check([]*syntax.File{missing}, app), `has no capability or formulas group "matrix"`) {
		t.Errorf("import of a missing group not reported")
	}
}

func hasMessage(diags []Diagnostic, substr string) bool {
	return countMessages(diags, substr) > 0
}

func countMessages(diags []Diagnostic, substr string) int {
	n := 0
	for _, d := range diags {
		if strings.Contains(d.Msg, substr) {
			n++
		}
	}
	return n
}
