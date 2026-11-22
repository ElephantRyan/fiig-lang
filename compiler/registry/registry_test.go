package registry

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fiig-lang/compiler/parser"
)

func TestBuildCardGateway(t *testing.T) {
	file, errs := parser.Parse(`
gateway getCustomer(id: CustomerId) -> Result<Customer, CustomerError> requires Database idempotent {
    return Ok(Customer(id: id, name: "x", email: "y"));
}`)
	if len(errs) > 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}
	card := BuildCard(file.Decls[0], "gateways/get_customer.fiig")
	if card.Name != "getCustomer" || card.Kind != "gateway" || card.Catalog != Default {
		t.Fatalf("got %+v", card)
	}
	if card.Requires != "Database" || !card.Idempotent {
		t.Fatalf("requires/idempotent wrong: %+v", card)
	}
	if len(card.Params) != 1 || card.Params[0] != (Param{Name: "id", Type: "CustomerId"}) {
		t.Fatalf("params wrong: %+v", card.Params)
	}
	if card.Returns != "Result<Customer, CustomerError>" {
		t.Fatalf("returns wrong: %q", card.Returns)
	}
	if card.Error != "CustomerError" {
		t.Fatalf("error wrong: %q", card.Error)
	}
	if card.Pure {
		t.Fatalf("a gateway must not be reported as pure: %+v", card)
	}
}

// TestBuildCardRuleIsPureWithErrorType covers Error/Pure for a rule, the
// other half of the gateway/rule asymmetry TestBuildCardGateway checks —
// a gateway is never pure (ADR 0031), a rule always is (ADR 0029: a rule
// may call nothing at all).
func TestBuildCardRuleIsPureWithErrorType(t *testing.T) {
	file, errs := parser.Parse(`
rule validate(input: NewCustomer) -> Result<NewCustomer, CustomerError> {
    return Ok(input);
}`)
	if len(errs) > 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}
	card := BuildCard(file.Decls[0], "")
	if !card.Pure {
		t.Fatalf("a rule must be reported as pure: %+v", card)
	}
	if card.Error != "CustomerError" {
		t.Fatalf("error wrong: %q", card.Error)
	}
}

// TestBuildCardNoErrorWhenReturnIsNotResult covers the common case of a
// return type with no typed error at all — Error must stay empty, not
// guess at one.
func TestBuildCardNoErrorWhenReturnIsNotResult(t *testing.T) {
	file, errs := parser.Parse(`
rule greet(name: string) -> string {
    return name;
}`)
	if len(errs) > 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}
	card := BuildCard(file.Decls[0], "")
	if card.Error != "" {
		t.Fatalf("expected no error type, got %q", card.Error)
	}
}

func TestBuildCardModelAndError(t *testing.T) {
	file, errs := parser.Parse("model Customer {\n    id: CustomerId;\n    name: string;\n}")
	if len(errs) > 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}
	card := BuildCard(file.Decls[0], "")
	if card.Kind != "model" || len(card.Fields) != 2 {
		t.Fatalf("got %+v", card)
	}

	file, errs = parser.Parse(`
enum CustomerError {
    NotFound(id: CustomerId),
    Invalid,
}`)
	if len(errs) > 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}
	card = BuildCard(file.Decls[0], "")
	if card.Kind != "enum" || len(card.Variants) != 2 {
		t.Fatalf("got %+v", card)
	}
	if card.Variants[1].Name != "Invalid" || len(card.Variants[1].Fields) != 0 {
		t.Fatalf("got %+v", card.Variants[1])
	}
}

func TestBuildCardWorkflowIsCompositionCatalog(t *testing.T) {
	file, errs := parser.Parse(`
workflow readCustomer(id: CustomerId) -> Result<Customer, CustomerError> {
    return getCustomer(id);
}`)
	if len(errs) > 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}
	card := BuildCard(file.Decls[0], "")
	if card.Kind != "workflow" || card.Catalog != Composition {
		t.Fatalf("got %+v", card)
	}
}

func TestGenerateOverCorpusIsStableAndNonEmpty(t *testing.T) {
	root := findExamplesDir(t)
	cards, fileErrs, err := Generate(root)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	// The invalid-programs fixtures pack several declarations into one
	// file on purpose, which ADR 0054 made a parse error (G1), and one
	// holds a wildcard import, a parse error since ADR 0050 §2.4 (G2).
	// Nothing else may fail to parse.
	for _, fe := range fileErrs {
		for _, e := range fe.Errors {
			if (e.Rule != "G1" && e.Rule != "G2") || !strings.HasPrefix(fe.Source, "invalid-programs/") {
				t.Fatalf("unexpected file error in %s: %+v", fe.Source, e)
			}
		}
	}
	if len(cards) == 0 {
		t.Fatal("no cards generated")
	}

	again, _, err := Generate(root)
	if err != nil {
		t.Fatalf("Generate (second run): %v", err)
	}
	if len(again) != len(cards) {
		t.Fatalf("non-deterministic card count: %d vs %d", len(cards), len(again))
	}
	for i := range cards {
		if cards[i].Name != again[i].Name || cards[i].Source != again[i].Source {
			t.Fatalf("non-deterministic order at %d: %+v vs %+v", i, cards[i], again[i])
		}
	}
}

func TestFindScopesToDefaultCatalogByDefault(t *testing.T) {
	root := findExamplesDir(t)
	cards, _, err := Generate(root)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	results := Find(cards, Query{Text: "getCustomer"})
	if len(results) != 1 || results[0].Kind != "gateway" {
		t.Fatalf("got %+v", results)
	}

	// retrieveCustomers is a workflow (composition catalog) — invisible to a
	// default-scope search by name.
	none := Find(cards, Query{Text: "retrieveCustomers"})
	if len(none) != 0 {
		t.Fatalf("expected no default-catalog match, got %+v", none)
	}
	found := Find(cards, Query{Text: "retrieveCustomers", Composition: true})
	if len(found) == 0 {
		t.Fatal("expected a composition-catalog match")
	}
}

func TestFindByInputAndOutputType(t *testing.T) {
	root := findExamplesDir(t)
	cards, _, err := Generate(root)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	results := Find(cards, Query{Input: "string", Output: "Result<Customer, CustomerError>"})
	if len(results) == 0 {
		t.Fatal("expected at least getCustomer")
	}
	for _, c := range results {
		if !hasParamType(c.Params, "string") || c.Returns != "Result<Customer, CustomerError>" {
			t.Fatalf("got non-matching card: %+v", c)
		}
	}
}

func TestFindByRequires(t *testing.T) {
	root := findExamplesDir(t)
	cards, _, err := Generate(root)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	results := Find(cards, Query{Requires: "Database"})
	if len(results) == 0 {
		t.Fatal("expected at least one Database-requiring gateway")
	}
	for _, c := range results {
		if c.Requires != "Database" {
			t.Fatalf("got non-matching card: %+v", c)
		}
	}
}

func TestFindByError(t *testing.T) {
	root := findExamplesDir(t)
	cards, _, err := Generate(root)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	results := Find(cards, Query{Error: "OrderError"})
	if len(results) == 0 {
		t.Fatal("expected at least one OrderError-returning declaration")
	}
	for _, c := range results {
		if c.Error != "OrderError" {
			t.Fatalf("got non-matching card: %+v", c)
		}
	}
}

func TestFindByPureAndIdempotent(t *testing.T) {
	root := findExamplesDir(t)
	cards, _, err := Generate(root)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	pure := Find(cards, Query{Pure: true})
	if len(pure) == 0 {
		t.Fatal("expected at least one pure declaration")
	}
	for _, c := range pure {
		if c.Kind != "rule" && c.Kind != "conversion" && c.Kind != "activity" && c.Kind != "formula" {
			t.Fatalf("--pure returned a non-rule/conversion/activity/formula card: %+v", c)
		}
	}

	idempotent := Find(cards, Query{Idempotent: true})
	if len(idempotent) == 0 {
		t.Fatal("expected at least one idempotent gateway")
	}
	for _, c := range idempotent {
		if !c.Idempotent {
			t.Fatalf("--idempotent returned a non-idempotent card: %+v", c)
		}
	}
}

// TestFindByLeafDistinguishesAtomicFromComposedRules is the registry-
// visibility deliverable from the ADR 0029/0030 design discussion:
// validateEmail (a rule, no dependencies) should show up under --leaf;
// validateNewCustomer (now an activity, depends on validateEmail) should
// not.
func TestFindByLeafDistinguishesAtomicFromComposedRules(t *testing.T) {
	root := findExamplesDir(t)
	cards, _, err := Generate(root)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	leaves := Find(cards, Query{Leaf: true})
	if len(leaves) == 0 {
		t.Fatal("expected at least one leaf declaration")
	}
	var sawValidateEmail, sawValidateNewCustomer bool
	for _, c := range leaves {
		if len(c.Dependencies) > 0 {
			t.Fatalf("--leaf returned a card with dependencies: %+v", c)
		}
		if c.Name == "validateEmail" {
			sawValidateEmail = true
		}
		if c.Name == "validateNewCustomer" {
			sawValidateNewCustomer = true
		}
	}
	if !sawValidateEmail {
		t.Fatal("expected validateEmail (no dependencies) under --leaf")
	}
	if sawValidateNewCustomer {
		t.Fatal("validateNewCustomer depends on validateEmail — should not be a leaf")
	}
}

func TestSiblingsAreSignatureBasedNotJudgmental(t *testing.T) {
	root := findExamplesDir(t)
	cards, _, err := Generate(root)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	retrieve := ByName(cards, "retrieveCustomers")
	if retrieve == nil {
		t.Fatal("retrieveCustomers not found")
	}
	siblings := Siblings(cards, retrieve)
	if len(siblings) == 0 {
		t.Fatal("expected at least one same-signature sibling in the corpus")
	}

	// A model has no call signature; it never reports siblings.
	customer := ByName(cards, "Customer")
	if customer == nil {
		t.Fatal("Customer not found")
	}
	if got := Siblings(cards, customer); got != nil {
		t.Fatalf("expected no siblings for a model, got %+v", got)
	}
}

// TestDependenciesThroughNestedMatch covers the recursive AST walk through
// a workflow calling other workflows inside nested match statements
// (checkout.fiig, ADR 0006's cross-domain composition example) — the
// deepest nesting any corpus declaration actually exercises.
func TestDependenciesThroughNestedMatch(t *testing.T) {
	root := findExamplesDir(t)
	cards, _, err := Generate(root)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	checkout := ByName(cards, "checkout")
	if checkout == nil {
		t.Fatal("checkout not found")
	}
	want := map[string]bool{"registerCustomer": true, "placeOrder": true}
	if len(checkout.Dependencies) != len(want) {
		t.Fatalf("got %+v, want exactly %v", checkout.Dependencies, want)
	}
	for _, d := range checkout.Dependencies {
		if !want[d] {
			t.Fatalf("unexpected dependency %q in %+v", d, checkout.Dependencies)
		}
	}
}

// TestAdapterDependenciesIncludeResponseMapping covers adapterCallNames:
// both a route's target workflow and a conversion called from inside its
// response-mapping block (ADR 0017) must show up, but the route's own
// call arguments (type references, not value expressions — see
// checker.checkAdapterRoutes) must not.
func TestAdapterDependenciesIncludeResponseMapping(t *testing.T) {
	root := findExamplesDir(t)
	cards, _, err := Generate(root)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	adapter := ByName(cards, "customerHttpApiWithMapping")
	if adapter == nil {
		t.Fatal("customerHttpApiWithMapping not found")
	}
	want := map[string]bool{"readCustomer": true, "customerErrorToHttpResponse": true}
	if len(adapter.Dependencies) != len(want) {
		t.Fatalf("got %+v, want exactly %v", adapter.Dependencies, want)
	}
	for _, d := range adapter.Dependencies {
		if !want[d] {
			t.Fatalf("unexpected dependency %q in %+v (route type-reference arguments must not leak in)", d, adapter.Dependencies)
		}
	}
}

// TestCallersIsInverseOfDependencies covers Callers — loadCustomer, an
// activity (ADR 0107: the step workflows reach getCustomer through), is
// called from workflows in the composition catalog, so this also confirms Callers doesn't
// scope to one catalog the way Siblings does.
func TestCallersIsInverseOfDependencies(t *testing.T) {
	root := findExamplesDir(t)
	cards, _, err := Generate(root)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	loadCustomer := ByName(cards, "loadCustomer")
	if loadCustomer == nil {
		t.Fatal("loadCustomer not found")
	}
	callers := Callers(cards, loadCustomer)
	if len(callers) == 0 {
		t.Fatal("expected loadCustomer to have at least one caller")
	}
	retrieveCustomers := ByName(cards, "retrieveCustomers")
	if retrieveCustomers == nil {
		t.Fatal("retrieveCustomers not found")
	}
	var sawRetrieveCustomers bool
	for _, name := range callers {
		if name == "retrieveCustomers" {
			sawRetrieveCustomers = true
		}
	}
	if !sawRetrieveCustomers {
		t.Fatalf("expected retrieveCustomers (composition catalog) among loadCustomer's callers, got %+v", callers)
	}

	// Cross-check the inverse directly: loadCustomer must appear in
	// retrieveCustomers' own Dependencies.
	var sawLoadCustomer bool
	for _, dep := range retrieveCustomers.Dependencies {
		if dep == "loadCustomer" {
			sawLoadCustomer = true
		}
	}
	if !sawLoadCustomer {
		t.Fatalf("retrieveCustomers.Dependencies should include loadCustomer, got %+v", retrieveCustomers.Dependencies)
	}
}

// TestDependenciesExcludeCapabilityCallsAndConstructions is a synthetic,
// self-contained check (rather than relying on corpus specifics that could
// change) that a capability call, a model construction, an error-variant
// construction, and the Ok/copy prelude forms are never recorded as
// dependencies. Goes through Generate rather than BuildCard directly,
// since telling a model-constructor call (Widget(...)) apart from a real
// dependency is exactly what Generate's post-pass does — BuildCard alone,
// given one declaration in isolation, can't yet know Widget names a model.
func TestDependenciesExcludeCapabilityCallsAndConstructions(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"widget.fiig": "model Widget {\n\tid: string;\n\tcount: int;\n}\n",
		"get_widget.fiig": `gateway getWidget(id: string) -> Result<Widget, WidgetError>
    requires Database
{
    row = Database.queryOne("...", id);
    return match (row) {
        found -> Ok(copy(Widget(id: id, count: 0), count: 1)),
        None -> Err(WidgetError.NotFound(id)),
    }
}
`,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cards, _, err := Generate(dir)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	card := ByName(cards, "getWidget")
	if card == nil {
		t.Fatal("getWidget not found")
	}
	if len(card.Dependencies) != 0 {
		t.Fatalf("expected no dependencies (capability call + Ok/copy/model/error constructions only), got %+v", card.Dependencies)
	}
}

// TestGenerateDropsDependenciesOnUnknownOrNonCallableNames confirms
// Generate's post-pass: a call to a name that either doesn't exist in this
// build, or resolves to a model/error (never callable), is dropped
// rather than kept as a dangling or wrong-kind edge.
func TestGenerateDropsDependenciesOnUnknownOrNonCallableNames(t *testing.T) {
	dir := t.TempDir()
	widget := "model Widget {\n\tid: string;\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "widget.fiig"), []byte(widget), 0o644); err != nil {
		t.Fatal(err)
	}
	// bogus() doesn't exist anywhere in this build; Widget(...) names a
	// model, never a callable dependency.
	rule := "rule makeWidget(id: string) -> Widget {\n\tbogus();\n\treturn Widget(id: id);\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "make_widget.fiig"), []byte(rule), 0o644); err != nil {
		t.Fatal(err)
	}
	cards, _, err := Generate(dir)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	card := ByName(cards, "makeWidget")
	if card == nil {
		t.Fatal("makeWidget not found")
	}
	if len(card.Dependencies) != 0 {
		t.Fatalf("expected no surviving dependencies, got %+v", card.Dependencies)
	}
}

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

// TestActivityPurityIsDerivedFromCallees is ADR 0044: an activity is pure
// exactly when everything it reaches is, so validateNewCustomer (rules
// only) reports Pure while registerCustomer (reaches gateways) and
// summarizeCustomer (a gateway directly) do not.
func TestActivityPurityIsDerivedFromCallees(t *testing.T) {
	cards, _, err := Generate(findExamplesDir(t))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	for name, want := range map[string]bool{"validateNewCustomer": true, "registerCustomer": false, "summarizeCustomer": false, "placeAndShipOrder": false} {
		c := ByName(cards, name)
		if c == nil {
			t.Fatalf("%s not found", name)
		}
		if c.Pure != want {
			t.Errorf("%s.Pure = %v, want %v", name, c.Pure, want)
		}
	}
}

// TestPrivateMembersGetNoCardButTheirCallsCount is ADR 0054 §3: a private
// member never reaches the registry, and the public entry's card lists
// what its helpers call (but not the helpers), so purity still sees a
// gateway reached only through a helper.
func TestPrivateMembersGetNoCardButTheirCallsCount(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"save.fiig": "gateway save(n: int) -> int\n\trequires Database\n{\n\treturn n;\n}\n",
		"coordinate.fiig": "activity coordinate(n: int) -> int {\n\treturn persist(n);\n}\n\n" +
			"private {\n\tpersist(n: int) -> int {\n\t\treturn save(n);\n\t}\n\n\tmodel Scratch {\n\t\tn: int;\n\t}\n}\n",
	}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cards, fileErrs, err := Generate(dir)
	if err != nil || len(fileErrs) > 0 {
		t.Fatalf("Generate: %v %+v", err, fileErrs)
	}
	if ByName(cards, "persist") != nil || ByName(cards, "Scratch") != nil {
		t.Fatalf("private members must not get cards, got %+v", cards)
	}
	c := ByName(cards, "coordinate")
	if c == nil {
		t.Fatal("coordinate not found")
	}
	if len(c.Dependencies) != 1 || c.Dependencies[0] != "save" {
		t.Fatalf("coordinate.Dependencies = %v, want [save]", c.Dependencies)
	}
	if c.Pure {
		t.Fatal("coordinate reaches a gateway through its helper; it is not pure")
	}
}
