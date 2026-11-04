package checker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fiig-lang/compiler/parser"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

func findExamplesDir(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		candidate := filepath.Join(dir, "examples")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find examples/ directory above " + dir)
		}
		dir = parent
	}
}

// diagsByFile groups CheckDir's output by File, for per-file assertions.
func diagsByFile(diags []Diagnostic) map[string][]Diagnostic {
	byFile := map[string][]Diagnostic{}
	for _, d := range diags {
		byFile[d.File] = append(byFile[d.File], d)
	}
	return byFile
}

func rules(diags []Diagnostic) map[string]int {
	counts := map[string]int{}
	for _, d := range diags {
		counts[d.Rule]++
	}
	return counts
}

// TestValidCorpusHasNoDiagnostics is the exit criterion that actually
// matters: every valid example in the corpus must pass clean under every
// check this package implements. A single false positive here would mean
// the checker rejects a program fiig-lang.md's own examples establish as
// correct.
func TestValidCorpusHasNoDiagnostics(t *testing.T) {
	root := findExamplesDir(t)
	diags, err := CheckDir(root, nil)
	if err != nil {
		t.Fatalf("CheckDir: %v", err)
	}
	for file, fileDiags := range diagsByFile(diags) {
		if filepath.Dir(filepath.ToSlash(file)) == "invalid-programs" {
			continue
		}
		for _, d := range fileDiags {
			t.Errorf("unexpected diagnostic in valid file %s: %s", file, d)
		}
	}
}

// TestInvalidProgramsCatchEveryAttestedBugClass checks each
// invalid-programs/*.fiig fixture against bug-classes.md's own
// attestation table — every numbered bug class this package implements
// (A1-A4, A6-A9, B1-B11, E1, G1, G2, H5, K3-K5) must fire on the file that attests it.
// G3 is attested by TestDuplicateImportAliasIsG3 instead — it needs two
// imports in one file, which no corpus fixture has, and the bug it guards
// against is specifically that both `use` lines look individually fine.
// Some fixtures also incidentally trip a category they weren't built to
// test (e.g. concurrency_error.fiig packs two declarations into one file,
// which is also a real G1 violation) — that's correct, not a test bug, and
// is asserted here rather than ignored.
func TestInvalidProgramsCatchEveryAttestedBugClass(t *testing.T) {
	root := findExamplesDir(t)
	diags, err := CheckDir(root, nil)
	if err != nil {
		t.Fatalf("CheckDir: %v", err)
	}
	byFile := diagsByFile(diags)

	cases := []struct {
		file          string
		wantRuleCount map[string]int
	}{
		{"invalid-programs/type_error.fiig", map[string]int{
			"A1": 3, "A2": 1, "A3": 1, "A7": 1, "A8": 1, "A9": 2,
			"A6": 2, // one duplicate name, reported at both colliding conversions
		}},
		{"invalid-programs/effect_error.fiig", map[string]int{
			"B1": 1, "B2": 1, "B3": 1, "B4": 1, "B5": 1, "B6": 1, "B7": 1, "B8": 1,
			"B9": 2,  // pingCustomer + pongCustomer, each reported at its own declaration
			"G1": 10, // eleven declarations packed into one file: a parse error at each extra one (ADR 0054), incidental but real
		}},
		{"invalid-programs/retry_error.fiig", map[string]int{"E1": 1}},
		{"invalid-programs/module_structure_error.fiig", map[string]int{"G1": 1}},
		{"invalid-programs/concurrency_error.fiig", map[string]int{
			"D1": 1, // retrieveAllCustomers: limit: unbounded
			"D2": 1, // retrieveCustomersNested: inner limit 50 > outer budget 5
			"G1": 1, // two declarations packed into one file, incidental but real
		}},
		{"invalid-programs/transaction_error.fiig", map[string]int{
			"C2": 1, // doubleReserve: nested transaction blocks
			"C4": 2, // reserveAndCharge (chargePayment/PaymentGateway) + createAndEmailInTransaction (sendEmail/EmailSender), both mismatched against transaction Database — C1 is attested by the first of these, per bug-classes.md's own framing as "one instance of" C4, not a separate check
			"C5": 1, // changeEmailAndReread: workflow call inside transaction
			"G1": 3, // four declarations packed into one file, one per extra declaration, incidental but real
		}},
		{"invalid-programs/error_handling_error.fiig", map[string]int{
			"F1": 1, // createCustomerIgnoringFailure: Result dropped as a bare statement
		}},
		{"invalid-programs/socket_error.fiig", map[string]int{
			"B10": 1, // peekAtDatabase: a rule calling Socket.connect
			"H5":  1, // resumeSession: a Connection as a workflow parameter
			"G1":  1, // two declarations packed into one file, incidental but real
		}},
		{"invalid-programs/wire_error.fiig", map[string]int{
			"W1": 1, // htmlPage: sets Content-Type, which produces owns
			"W2": 1, // embedCookie: SameSite=None over plain HTTP
			"G1": 1, // two declarations packed into one file, incidental but real
		}},
		{"invalid-programs/random_error.fiig", map[string]int{
			"B10": 1, // rollDie: a rule drawing a random number
			"P8":  1, // newApiKey: requires Random
			"G1":  1, // two declarations packed into one file, incidental but real
		}},
		{"invalid-programs/capability_error.fiig", map[string]int{
			"P3": 1, // refundInRule: a rule invoking a provider
			"P5": 2, // RefundPing/RefundPong require each other, reported at both
			"P6": 1, // RefundPolicy.provider: a capability in a model
			"P7": 1, // refundDirect: requires Socket, uses RefundPing
			"N1": 1, // refundDesk: a lowercase capability
			"N2": 1, // refundDesk.Issue: a capitalized operation
			"N3": 1, // refundLowercase: a lowercase binding holding a provider
			"G1": 7, // eight declarations packed into one file, incidental but real
		}},
		{"invalid-programs/formula_error.fiig", map[string]int{
			"K3":  1, // parseEmail: returns a Result
			"K4":  1, // lookupDomain: requires Socket
			"K5":  1, // normalize: no body
			"B11": 1, // shipsFree: calls a rule
		}},
		{"invalid-programs/float_error.fiig", map[string]int{
			"A1": 1, // total / count mixes float and int
			"M1": 1, // average == expected compares floats
		}},
		{"invalid-programs/decimal_error.fiig", map[string]int{
			"A1": 2, // 1.0 / 3.0 as a decimal, and total / units
		}},
		{"invalid-programs/math_error.fiig", map[string]int{
			"A1": 1, // math.max(spent, budget): no (int, float) form
			"":   1, // math.closeTo's relative tolerance passed by position
			"K2": 1, // a private model named Result
		}},
	}

	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			got := rules(byFile[tc.file])
			for rule, want := range tc.wantRuleCount {
				if got[rule] != want {
					t.Errorf("%s: got %d %s diagnostics, want %d (all: %+v)", tc.file, got[rule], rule, want, byFile[tc.file])
				}
			}
		})
	}
}

// TestWildcardImportErrorCatchesG2 checks the fixture's G2, which is a
// parse error since ADR 0050 §2.4 and reaches CheckDir's diagnostics the
// same way G1 does. It is kept out of the table above because the
// fixture's bare `Instant` may also report as unresolved, depending on how
// CheckDir's flat walk sees standard.time; that is incidental to G2.
func TestWildcardImportErrorCatchesG2(t *testing.T) {
	root := findExamplesDir(t)
	diags, err := CheckDir(root, nil)
	if err != nil {
		t.Fatalf("CheckDir: %v", err)
	}
	got := rules(diagsByFile(diags)["invalid-programs/wildcard_import_error.fiig"])
	if got["G2"] != 1 {
		t.Errorf("got %d G2 diagnostics, want 1", got["G2"])
	}
}

func mustParse(t *testing.T, src string) *syntax.File {
	t.Helper()
	f, errs := parser.Parse(src)
	if len(errs) > 0 {
		t.Fatalf("parse error: %v", errs)
	}
	return f
}

// TestExhaustiveMatchResult covers bug class A9 against a Result subject —
// no real corpus example exercises a missing Ok/Err arm (every attested
// match over a Result either names both or uses a catch-all), so this is
// built directly rather than found in examples/.
func TestExhaustiveMatchResult(t *testing.T) {
	enumDecl := mustParse(t, "enum MyError {\n\tBad\n}")
	risky := mustParse(t, "rule risky(x: int) -> Result<int, MyError> {\n\treturn Ok(x)\n}")

	missingErr := mustParse(t, "activity useRisky(x: int) -> int {\n\tmatch risky(x) {\n\t\tOk(value) -> value\n\t}\n}")
	diags := Check([]*syntax.File{enumDecl, risky, missingErr}, nil)
	if got := rules(diags)["A9"]; got != 1 {
		t.Errorf("got %d A9 diagnostics, want 1 (all: %+v)", got, diags)
	}

	full := mustParse(t, "activity useRisky(x: int) -> int {\n\tmatch risky(x) {\n\t\tOk(value) -> value\n\t\tErr(_) -> 0\n\t}\n}")
	diags = Check([]*syntax.File{enumDecl, risky, full}, nil)
	if len(diags) != 0 {
		t.Errorf("unexpected diagnostics for fully-covered Result match: %+v", diags)
	}
}

// TestExhaustiveMatchNoneUnion covers bug class A9 against a `T | None`
// union subject — the one case grammar.md §5 documents specially (a bare
// binding name covers only the non-None remainder, not None itself). No
// corpus example resolves this match shape today: every `T | None` match in
// examples/ matches against a capability call's result, which this checker
// can't type (Unknown, no capability-contract system yet), so the special
// case is only reachable through a directly-typed model field here.
func TestExhaustiveMatchNoneUnion(t *testing.T) {
	box := mustParse(t, "model Box {\n\tvalue: int | None\n}")

	missingNone := mustParse(t, "rule useBox(b: Box) -> int {\n\tmatch b.value {\n\t\tfound -> 1\n\t}\n}")
	diags := Check([]*syntax.File{box, missingNone}, nil)
	if got := rules(diags)["A9"]; got != 1 {
		t.Errorf("got %d A9 diagnostics, want 1 (all: %+v)", got, diags)
	}

	full := mustParse(t, "rule useBox(b: Box) -> int {\n\tmatch b.value {\n\t\tfound -> 1\n\t\tNone -> 0\n\t}\n}")
	diags = Check([]*syntax.File{box, full}, nil)
	if len(diags) != 0 {
		t.Errorf("unexpected diagnostics for fully-covered None-union match: %+v", diags)
	}
}

// TestCopyReturnsBaseModelType covers the valid case of the `copy(base,
// field: value, ...)` prelude builtin (docs/specification/ownership.md):
// the result is the same model type as base, and an override's value is
// checked against the real field type. The corpus also attests this
// (rename_customer.fiig), but a self-contained model keeps this test from
// depending on the real Customer model's shape.
func TestCopyReturnsBaseModelType(t *testing.T) {
	widget := mustParse(t, "model Widget {\n\tid: string\n\tcount: int\n}")
	rule := mustParse(t, "rule rename(w: Widget, n: int) -> Widget {\n\treturn copy(w, count: n)\n}")
	diags := Check([]*syntax.File{widget, rule}, nil)
	if len(diags) != 0 {
		t.Errorf("unexpected diagnostics: %+v", diags)
	}
}

func TestCopyRejectsNonModelBase(t *testing.T) {
	rule := mustParse(t, "rule bad(n: int) -> int {\n\treturn copy(n, count: n)\n}")
	diags := Check([]*syntax.File{rule}, nil)
	if len(diags) == 0 {
		t.Errorf("expected a diagnostic for copy(...) on a non-model base, got none")
	}
}

func TestCopyRejectsUnknownField(t *testing.T) {
	widget := mustParse(t, "model Widget {\n\tid: string\n\tcount: int\n}")
	rule := mustParse(t, "rule bad(w: Widget) -> Widget {\n\treturn copy(w, bogus: 1)\n}")
	diags := Check([]*syntax.File{widget, rule}, nil)
	if len(diags) == 0 {
		t.Errorf("expected a diagnostic for an unknown override field, got none")
	}
}

func TestCopyRejectsPositionalOverride(t *testing.T) {
	widget := mustParse(t, "model Widget {\n\tid: string\n\tcount: int\n}")
	rule := mustParse(t, "rule bad(w: Widget, n: int) -> Widget {\n\treturn copy(w, n)\n}")
	diags := Check([]*syntax.File{widget, rule}, nil)
	if len(diags) == 0 {
		t.Errorf("expected a diagnostic for a positional copy override, got none")
	}
}

func TestCopyOverrideTypeMismatchIsA1(t *testing.T) {
	widget := mustParse(t, "model Widget {\n\tid: string\n\tcount: int\n}")
	rule := mustParse(t, "rule bad(w: Widget, s: string) -> Widget {\n\treturn copy(w, count: s)\n}")
	diags := Check([]*syntax.File{widget, rule}, nil)
	if got := rules(diags)["A1"]; got != 1 {
		t.Errorf("got %d A1 diagnostics, want 1 (all: %+v)", got, diags)
	}
}

// TestUnhandledResultInNonProducingMatchArmIsF1 locks in a fix found while
// running the LLM composition trial
// (docs/specification/llm-composition-trial.md): a Result dropped inside a
// non-producing match arm's bare-expression body used to escape F1
// entirely, even though the exact same expression as a bare top-level
// statement was already correctly rejected — not a real distinction, both
// are "this value's result is silently dropped."
func TestUnhandledResultInNonProducingMatchArmIsF1(t *testing.T) {
	enumDecl := mustParse(t, "enum MyError {\n\tBad\n}")
	risky := mustParse(t, "rule risky(x: int) -> Result<int, MyError> {\n\treturn Ok(x)\n}")
	activity := mustParse(t, "activity useRisky(x: int) -> int {\n\tmatch x {\n\t\t_ -> risky(x)\n\t}\n\treturn 0\n}")
	diags := Check([]*syntax.File{enumDecl, risky, activity}, nil)
	if got := rules(diags)["F1"]; got != 1 {
		t.Errorf("got %d F1 diagnostics, want 1 (all: %+v)", got, diags)
	}
}

// parsePackageFiles parses every .fiig file under dir, recursively — a
// test-local stand-in for the directory-walking a real package loader
// will eventually do (ADR 0032's consequences list this as not yet
// built); good enough to parse one package root's own kind-named
// subdirectories (rules/, values/, activities/, ...).
func parsePackageFiles(t *testing.T, dir string) []*syntax.File {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var files []*syntax.File
	for _, e := range entries {
		if e.IsDir() {
			files = append(files, parsePackageFiles(t, filepath.Join(dir, e.Name()))...)
			continue
		}
		if filepath.Ext(e.Name()) != ".fiig" {
			continue
		}
		src, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		file, errs := parser.Parse(string(src))
		if len(errs) > 0 {
			t.Fatalf("parse errors in %s: %v", e.Name(), errs)
		}
		files = append(files, file)
	}
	return files
}

// buildPackage parses one package root and wraps it as a *types.Package,
// via the production BuildPackage — this test lives in package checker
// and could reach BuildUniverse/resolveFileImports directly, but going
// through the exported function keeps this test exercising the same path
// compiler/loader uses, not a parallel hand-rolled one.
func buildPackage(t *testing.T, dir string, packages types.Packages) *types.Package {
	t.Helper()
	files := parsePackageFiles(t, dir)
	pkg, diags := BuildPackage(files, packages)
	if len(diags) > 0 {
		t.Fatalf("unexpected diagnostics building %s: %+v", dir, diags)
	}
	return pkg
}

// TestCrossPackageResolution dogfoods Milestone 6's package-scoped
// Universe (ADR 0032) against the real example packages it was built
// for: examples/standard-time (a plain value), examples/company-time (a
// rule depending on it, under its own "time" alias, deliberately
// different from the alias used below), and examples/time-library (the
// long-attested aliased-import example, now an activity calling into
// company.time for real instead of opaquely). Proves the mechanism two
// ways: the valid program checks clean, and a real mistake inside it (an
// argument of the wrong type on the imported call) is actually caught —
// not silently Unknown, which is what would happen if resolution weren't
// real.
func TestCrossPackageResolution(t *testing.T) {
	root := findExamplesDir(t)
	standardTime := buildPackage(t, filepath.Join(root, "standard-time"), nil)
	packages := types.Packages{"standard.time": standardTime}
	packages["company.time"] = buildPackage(t, filepath.Join(root, "company-time"), packages)

	demoFiles := parsePackageFiles(t, filepath.Join(root, "time-library"))
	if diags := Check(demoFiles, packages); len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %+v", diags)
	}

	broken := mustParse(t, `
use company.time as businessTime
use standard.time as systemTime

activity computeFollowUpDate(from: systemTime.Instant) -> systemTime.Instant {
    return businessTime.addBusinessDays(3, 3)
}`)
	diags := Check([]*syntax.File{broken}, packages)
	if got := rules(diags)["A1"]; got != 1 {
		t.Fatalf("want exactly 1 A1 diagnostic for addBusinessDays' mismatched argument type, got %d (all: %+v)", got, diags)
	}
}

// TestDuplicateImportAliasIsG3 covers both ways two imports in one file
// can end up bound to the same alias. The implicit form is the dangerous
// one: neither `use` line mentions the alias they're fighting over, so
// the only visible symptom used to be an "unresolved type" on whichever
// package lost — a diagnostic pointing at correct code. Checked without
// package context on purpose: G3 is a property of the file's own import
// list, same as G2, and must not depend on whether the packages are
// loaded.
func TestDuplicateImportAliasIsG3(t *testing.T) {
	implicit := mustParse(t, "use company.time\nuse standard.time\n\nrule noop(x: int) -> int {\n\treturn x\n}")
	if got := rules(Check([]*syntax.File{implicit}, nil))["G3"]; got != 1 {
		t.Errorf("implicit last-segment aliases: got %d G3 diagnostics, want 1", got)
	}

	explicit := mustParse(t, "use company.time as t\nuse standard.time as t\n\nrule noop(x: int) -> int {\n\treturn x\n}")
	if got := rules(Check([]*syntax.File{explicit}, nil))["G3"]; got != 1 {
		t.Errorf("explicit duplicate `as` aliases: got %d G3 diagnostics, want 1", got)
	}

	distinct := mustParse(t, "use company.time as businessTime\nuse standard.time\n\nrule noop(x: int) -> int {\n\treturn x\n}")
	if diags := Check([]*syntax.File{distinct}, nil); len(diags) != 0 {
		t.Errorf("unexpected diagnostics for distinct aliases: %+v", diags)
	}
}

// TestDuplicateDeclNameReportsBothSitesAndSuppressesCascade locks in two
// properties of a duplicate name across files. First, both declarations
// are reported: BuildUniverse keeps whichever one the file walk reached
// first, which is not something the author can see, so blaming only the
// other one sends them to a file that may be perfectly correct. Second,
// nothing downstream is reported against the name at all — here an
// adapter route calling `createCustomer` must not draw a B6 ("not an
// exported workflow"), because the workflow *does* exist and the only
// reason the activity won the lookup is walk order.
func TestDuplicateDeclNameReportsBothSitesAndSuppressesCascade(t *testing.T) {
	model := mustParse(t, "model NewCustomer {\n\tname: string\n}")
	activity := mustParse(t, "activity createCustomer(input: NewCustomer) -> NewCustomer {\n\treturn input\n}")
	workflow := mustParse(t, "workflow createCustomer(input: NewCustomer) -> NewCustomer {\n\treturn input\n}")
	adapter := mustParse(t, "adapter http CustomerApi {\n\tPOST \"/customers\" -> createCustomer(input: NewCustomer)\n\t\tconsumes \"application/json\"\n\t\tproduces \"application/json\"\n}")

	// Both orderings: the result must not depend on which one is seen first.
	for _, order := range [][]*syntax.File{
		{model, activity, workflow, adapter},
		{model, workflow, activity, adapter},
	} {
		diags := Check(order, nil)
		if got := rules(diags)["B6"]; got != 0 {
			t.Errorf("got %d B6 diagnostics, want 0 — a duplicate name must not cascade (all: %+v)", got, diags)
		}
		if len(diags) != 2 {
			t.Errorf("want exactly 2 diagnostics (one per colliding declaration), got %d: %+v", len(diags), diags)
		}
		for _, d := range diags {
			if !strings.Contains(d.Msg, `duplicate declaration name "createCustomer"`) {
				t.Errorf("unexpected diagnostic: %s", d)
			}
		}
	}
}

// TestWorkflowIsUncallableAndOnlyAdapterBoundary locks in ADR 0044's two
// properties of a workflow: nothing may call one (B8 — including another
// workflow), and an adapter may attach to nothing else (B6 — including an
// activity, even though an activity may now be effectful).
func TestWorkflowIsUncallableAndOnlyAdapterBoundary(t *testing.T) {
	model := mustParse(t, "model NewCustomer {\n\tname: string\n}")
	inner := mustParse(t, "workflow inner(input: NewCustomer) -> NewCustomer {\n\treturn input\n}")
	outer := mustParse(t, "workflow outer(input: NewCustomer) -> NewCustomer {\n\treturn inner(input)\n}")
	act := mustParse(t, "activity coordinate(input: NewCustomer) -> NewCustomer {\n\treturn input\n}")
	api := mustParse(t, "adapter http Api {\n\tPOST \"/c\" -> coordinate(input: NewCustomer)\n}")

	got := rules(Check([]*syntax.File{model, inner, outer, act, api}, nil))
	if got["B8"] != 1 || got["B6"] != 1 {
		t.Fatalf("want one B8 (workflow calls workflow) and one B6 (adapter exposes an activity), got %v", got)
	}
}

// TestActivityMayCallGatewayAndActivityButNotCycle covers ADR 0044's
// widened activity matrix: gateway and activity callees are legal, and a
// cycle through them is B9 at every member. An activity calling itself
// stays inside one file, which ADR 0054 §6 allows (activities may call
// activities, and the runtime's call depth limit bounds it), so it is not
// B9.
func TestActivityMayCallGatewayAndActivityButNotCycle(t *testing.T) {
	model := mustParse(t, "model NewCustomer {\n\tname: string\n}")
	gw := mustParse(t, "gateway save(input: NewCustomer) -> NewCustomer\n\trequires Database\n{\n\treturn input\n}")
	a := mustParse(t, "activity a(input: NewCustomer) -> NewCustomer {\n\treturn save(input)\n}")
	b := mustParse(t, "activity b(input: NewCustomer) -> NewCustomer {\n\treturn a(input)\n}")

	if got := rules(Check([]*syntax.File{model, gw, a, b}, nil)); len(got) != 0 {
		t.Fatalf("activity calling a gateway and another activity must be legal, got %v", got)
	}

	loopA := mustParse(t, "activity loopA(input: NewCustomer) -> NewCustomer {\n\treturn loopB(input)\n}")
	loopB := mustParse(t, "activity loopB(input: NewCustomer) -> NewCustomer {\n\treturn loopA(input)\n}")
	self := mustParse(t, "activity self(input: NewCustomer) -> NewCustomer {\n\treturn self(input)\n}")
	if got := rules(Check([]*syntax.File{model, loopA, loopB, self}, nil)); got["B9"] != 2 {
		t.Fatalf("want 2 B9 (loopA, loopB; not self), got %v", got)
	}
}
