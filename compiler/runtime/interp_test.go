package runtime

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"fiig-lang/compiler/checker"
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

// loadUniverse parses every .fiig file under the given examples/-relative
// subdirectories and builds one shared Universe spanning all of them —
// mirroring checker.CheckDir's cross-file lookup, but without re-running
// the checker (the corpus's own checker tests already guarantee it's
// clean; these tests exercise execution, not checking).
func loadUniverse(t *testing.T, subdirs ...string) *types.Universe {
	t.Helper()
	root := findExamplesDir(t)
	var files []*syntax.File
	for _, sub := range subdirs {
		dir := filepath.Join(root, sub)
		err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || filepath.Ext(path) != ".fiig" {
				return nil
			}
			src, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			f, errs := parser.Parse(string(src))
			if len(errs) > 0 {
				t.Fatalf("parse %s: %v", path, errs)
			}
			files = append(files, f)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	universe, conflicts := types.BuildUniverse(files)
	if len(conflicts) > 0 {
		t.Fatalf("unexpected name conflicts: %+v", conflicts)
	}
	checker.Check(files, nil) // types constants; see universeFromSource
	return universe
}

func newTestInterp(universe *types.Universe, caps *CapabilityRegistry) *Interp {
	if caps == nil {
		caps = NewCapabilityRegistry()
	}
	return New(universe, caps)
}

func mustRecord(t *testing.T, v Value) Record {
	t.Helper()
	r, ok := v.(Record)
	if !ok {
		t.Fatalf("got %T (%s), want Record", v, v)
	}
	return r
}

func mustResult(t *testing.T, v Value) Result {
	t.Helper()
	r, ok := v.(Result)
	if !ok {
		t.Fatalf("got %T (%s), want Result", v, v)
	}
	return r
}

func TestCallRuleWithCopy(t *testing.T) {
	// rename_customer.fiig: `return copy(customer, name: newName)`.
	u := loadUniverse(t, "customer-service/models", "customer-service/rules", "customer-service/formulas")
	in := newTestInterp(u, nil)

	customer := Record{TypeName: "Customer", Fields: map[string]Value{
		"id":    Str("c1"),
		"name":  Str("Ada"),
		"email": Str("ada@example.com"),
	}}
	v, fault := in.Call(context.Background(), "renameCustomer", map[string]Value{
		"customer": customer,
		"newName":  Str("Grace"),
	})
	if fault != nil {
		t.Fatalf("Call: %v", fault)
	}
	rec := mustRecord(t, v)
	if rec.Fields["name"] != Str("Grace") {
		t.Fatalf("name = %v, want Grace", rec.Fields["name"])
	}
	if !Equal(rec.Fields["id"], customer.Fields["id"]) {
		t.Fatalf("id changed: got %v, want %v", rec.Fields["id"], customer.Fields["id"])
	}
}

func TestCallConversion(t *testing.T) {
	// customer_to_summary.fiig: `return CustomerSummary(id: customer.id, name: customer.name)`.
	u := loadUniverse(t, "customer-service/models", "customer-service/conversions")
	in := newTestInterp(u, nil)

	customer := Record{TypeName: "Customer", Fields: map[string]Value{
		"id":    Str("c1"),
		"name":  Str("Ada"),
		"email": Str("ada@example.com"),
	}}
	v, fault := in.Call(context.Background(), "customerToSummary", map[string]Value{"customer": customer})
	if fault != nil {
		t.Fatalf("Call: %v", fault)
	}
	rec := mustRecord(t, v)
	if rec.TypeName != "CustomerSummary" || rec.Fields["id"] != Str("c1") || rec.Fields["name"] != Str("Ada") {
		t.Fatalf("got %s", rec)
	}
}

// TestRegionDisplayNameMatchesZeroFieldEnumVariant covers a zero-field enum
// variant (`Region.US`) as a match pattern, not just as a construction
// expression — it parses as a NamePattern, not a ConstructorPattern (no
// call parens), a distinct code path from the field-accessed error variants
// every other runtime match test exercises.
func TestRegionDisplayNameMatchesZeroFieldEnumVariant(t *testing.T) {
	u := loadUniverse(t, "order-processing/models", "order-processing/rules")
	in := newTestInterp(u, nil)

	for region, want := range map[string]string{"US": "United States", "EU": "Europe", "APAC": "Asia-Pacific"} {
		v, fault := in.Call(context.Background(), "regionDisplayName", map[string]Value{
			"region": EnumValue{TypeName: "Region", Variant: region},
		})
		if fault != nil {
			t.Fatalf("Call(%s): %v", region, fault)
		}
		if v.String() != want {
			t.Fatalf("regionDisplayName(%s) = %q, want %q", region, v, want)
		}
	}
}

// TestQualifiesForFreeShippingBooleanLogic covers `bool` as a real,
// declarable return type (ADR 0041): a stored boolean read twice, `and`,
// `==` over an enum, and the `true` literal — none of which had a static
// type or a literal spelling before this.
func TestQualifiesForFreeShippingBooleanLogic(t *testing.T) {
	u := loadUniverse(t, "order-processing/models", "order-processing/rules")
	in := newTestInterp(u, nil)

	region := func(variant string) Value { return EnumValue{TypeName: "Region", Variant: variant} }

	cases := []struct {
		cents  int64
		region string
		want   bool
	}{
		{10000, "US", true},
		{10000, "EU", true},
		{10000, "APAC", false},
		{999, "US", false},
	}
	for _, tc := range cases {
		v, fault := in.Call(context.Background(), "qualifiesForFreeShipping", map[string]Value{
			"total":  Int(tc.cents),
			"region": region(tc.region),
		})
		if fault != nil {
			t.Fatalf("Call(%d, %s): %v", tc.cents, tc.region, fault)
		}
		b, ok := v.(Bool)
		if !ok || bool(b) != tc.want {
			t.Fatalf("qualifiesForFreeShipping(%d, %s) = %v, want %v", tc.cents, tc.region, v, tc.want)
		}
	}
}

// TestCorpusForLoopRules runs the two attested ADR 0053 loops in the
// corpus: a field sum through a reassignable local, and a validate-all with
// an early `return` from inside the loop.
func TestCorpusForLoopRules(t *testing.T) {
	u := loadUniverse(t, "order-processing/models", "order-processing/rules")
	in := newTestInterp(u, nil)

	orders := func(cents ...int64) Value {
		elems := make([]Value, len(cents))
		for i, c := range cents {
			elems[i] = Record{TypeName: "Order", Fields: map[string]Value{
				"total": Int(c),
			}}
		}
		return List{Elems: elems}
	}

	v, fault := in.Call(context.Background(), "totalOfOrders", map[string]Value{"orders": orders(1500, 250, 4000)})
	if fault != nil {
		t.Fatalf("totalOfOrders: %v", fault)
	}
	if v != Int(5750) {
		t.Fatalf("totalOfOrders = %v, want 5750", v)
	}

	cases := []struct {
		cents []int64
		want  bool
	}{
		{[]int64{1500, 250}, true},
		{[]int64{1500, 0, 250}, false},
		{nil, true},
	}
	for _, tc := range cases {
		v, fault := in.Call(context.Background(), "allOrdersHavePositiveTotals", map[string]Value{"orders": orders(tc.cents...)})
		if fault != nil {
			t.Fatalf("allOrdersHavePositiveTotals(%v): %v", tc.cents, fault)
		}
		if b, ok := v.(Bool); !ok || bool(b) != tc.want {
			t.Fatalf("allOrdersHavePositiveTotals(%v) = %v, want %v", tc.cents, v, tc.want)
		}
	}
}

// TestCorpusLargeOrderIdsBuildsAList runs ADR 0057's attested building
// loop: an empty typed list, appended to in a `for`.
func TestCorpusLargeOrderIdsBuildsAList(t *testing.T) {
	u := loadUniverse(t, "order-processing/models", "order-processing/rules")
	in := newTestInterp(u, nil)

	order := func(id string, total int64) Value {
		return Record{TypeName: "Order", Fields: map[string]Value{"id": Str(id), "total": Int(total)}}
	}
	orders := List{Elems: []Value{order("A1", 500), order("A2", 12000), order("A3", 9999), order("A4", 10000)}}
	v, fault := in.Call(context.Background(), "largeOrderIds", map[string]Value{"orders": orders, "threshold": Int(10000)})
	if fault != nil {
		t.Fatalf("largeOrderIds: %v", fault)
	}
	if !Equal(v, List{Elems: []Value{Str("A2"), Str("A4")}}) {
		t.Fatalf("largeOrderIds = %s, want [A2, A4]", v)
	}
}

func TestValidateNewCustomerBuiltinMethods(t *testing.T) {
	// validate_new_customer.fiig exercises `.isBlank()` and `.contains("@")`,
	// the two builtin string methods — neither modeled by the checker at
	// all (compiler/checker/expr.go's inferQualifiedCall falls through to
	// Unknown for a FieldAccess target that isn't a bare Ident), so this is
	// the one place their actual behavior is defined and tested.
	u := loadUniverse(t, "customer-service/models", "customer-service/errors", "customer-service/rules", "customer-service/formulas", "customer-service/activities")
	in := newTestInterp(u, nil)

	newCustomer := func(name, email string) Value {
		return Record{TypeName: "NewCustomer", Fields: map[string]Value{
			"name":  Str(name),
			"email": Str(email),
		}}
	}

	v, fault := in.Call(context.Background(), "validateNewCustomer", map[string]Value{"input": newCustomer("Ada", "ada@example.com")})
	if fault != nil {
		t.Fatalf("Call: %v", fault)
	}
	if rv := mustResult(t, v); !rv.Ok {
		t.Fatalf("got Err(%s), want Ok", rv.Value)
	}

	v, fault = in.Call(context.Background(), "validateNewCustomer", map[string]Value{"input": newCustomer("  ", "ada@example.com")})
	if fault != nil {
		t.Fatalf("Call: %v", fault)
	}
	rv := mustResult(t, v)
	if rv.Ok {
		t.Fatalf("got Ok for blank name, want Err")
	}
	ev, ok := rv.Value.(EnumValue)
	if !ok || ev.Variant != "Invalid" {
		t.Fatalf("got %s, want CustomerError.Invalid", rv.Value)
	}

	v, fault = in.Call(context.Background(), "validateNewCustomer", map[string]Value{"input": newCustomer("Ada", "not-an-email")})
	if fault != nil {
		t.Fatalf("Call: %v", fault)
	}
	rv = mustResult(t, v)
	if rv.Ok {
		t.Fatalf("got Ok for invalid email, want Err")
	}
}

// checkCheckerClean is a sanity guard: these tests execute the real
// corpus, so if the corpus itself ever stops checking clean, that's a
// prerequisite failure worth distinguishing from an interpreter bug.
func checkCheckerClean(t *testing.T, root string) {
	t.Helper()
	diags, err := checker.CheckDir(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(diags) > 0 {
		t.Fatalf("corpus under %s has checker diagnostics: %+v", root, diags)
	}
}

// TestCorpusShippingCostUsesPrivateHelpers runs the corpus's ADR 0054
// example: a rule whose rate table lives in a private helper returning a
// private model.
func TestCorpusShippingCostUsesPrivateHelpers(t *testing.T) {
	u := loadUniverse(t, "order-processing/models", "order-processing/rules")
	in := newTestInterp(u, nil)
	for _, tc := range []struct {
		total  int64
		region string
		want   Value
	}{
		{2500, "US", Int(500)},
		{15000, "EU", Int(0)},
		{15000, "APAC", Int(1200)},
		{20000, "APAC", Int(0)},
	} {
		v, fault := in.Call(context.Background(), "shippingCost", map[string]Value{
			"total": Int(tc.total), "region": EnumValue{TypeName: "Region", Variant: tc.region},
		})
		if fault != nil {
			t.Fatalf("shippingCost(%d, %s): %v", tc.total, tc.region, fault)
		}
		if v != tc.want {
			t.Fatalf("shippingCost(%d, %s) = %v, want %v", tc.total, tc.region, v, tc.want)
		}
	}
}
