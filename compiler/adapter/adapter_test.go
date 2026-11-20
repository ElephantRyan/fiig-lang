package adapter

import (
	"context"
	"encoding/json"
	"fiig-lang/compiler/checker"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fiig-lang/compiler/parser"
	"fiig-lang/compiler/runtime"
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

// loadProgram parses every .fiig file under the given examples/-relative
// subdirectories, returning both the Universe (for planning routes) and
// the parsed files themselves (to pull a specific *syntax.AdapterDecl out
// of, since Universe only indexes declarations with a name and adapters
// aren't looked up by name anywhere else in this package).
func loadProgram(t *testing.T, subdirs ...string) (*types.Universe, []*syntax.File) {
	t.Helper()
	root := findExamplesDir(t)
	var files []*syntax.File
	for _, sub := range subdirs {
		dir := filepath.Join(root, sub)
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if e.IsDir() || filepath.Ext(e.Name()) != ".fiig" {
				continue
			}
			src, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				t.Fatal(err)
			}
			f, errs := parser.Parse(string(src))
			if len(errs) > 0 {
				t.Fatalf("parse %s: %v", e.Name(), errs)
			}
			files = append(files, f)
		}
	}
	u, conflicts := types.BuildUniverse(files)
	if len(conflicts) > 0 {
		t.Fatalf("unexpected conflicts: %+v", conflicts)
	}
	// Checked, so a Database query reads into its row type (ADR 0082 §5).
	checker.Check(files, nil)
	return u, files
}

func findAdapter(t *testing.T, files []*syntax.File, name string) *syntax.AdapterDecl {
	t.Helper()
	for _, f := range files {
		for _, d := range f.Decls {
			if ad, ok := d.(*syntax.AdapterDecl); ok && ad.Name == name {
				return ad
			}
		}
	}
	t.Fatalf("adapter %q not found", name)
	return nil
}

func newFakeDatabaseForAdapter() (*runtime.CapabilityRegistry, *adapterFakeDB) {
	db := &adapterFakeDB{rows: map[string]runtime.Value{}}
	caps := runtime.NewCapabilityRegistry()
	caps.Register("Database", db)
	caps.Register("Random", runtime.NewSeededRandomProvider(1))
	return caps, db
}

// adapterFakeDB is this package's own minimal simulated Database — a
// FuncProvider-shaped fake would work too, but a named type lets Begin
// implement runtime.TransactionalProvider for change_customer_email.fiig's
// `transaction Database { ... }`.
type adapterFakeDB struct {
	rows map[string]runtime.Value
}

func (db *adapterFakeDB) Call(_ context.Context, method string, args []runtime.Value) (runtime.Value, *runtime.Fault) {
	switch method {
	case "queryOne":
		key := keyOf(args[1])
		if row, ok := db.rows[key]; ok {
			return row, nil
		}
		return runtime.None{}, nil
	case "execute":
		if len(args) >= 3 {
			// update_customer_email.fiig: execute(sql, email, id) — keep
			// the simulated row's email in sync so a reread sees it.
			id := keyOf(args[2])
			if row, ok := db.rows[id].(runtime.Record); ok {
				row.Fields["email"] = runtime.Str(keyOf(args[1]))
				db.rows[id] = row
			}
		}
		return runtime.Int(1), nil
	default:
		return nil, runtime.Faultf("adapterFakeDB has no method %q", method)
	}
}

func (db *adapterFakeDB) Begin(context.Context, string) (runtime.Tx, *runtime.Fault) {
	return adapterFakeTx{db}, nil
}

type adapterFakeTx struct{ db *adapterFakeDB }

func (tx adapterFakeTx) Call(ctx context.Context, method string, args []runtime.Value) (runtime.Value, *runtime.Fault) {
	return tx.db.Call(ctx, method, args)
}
func (tx adapterFakeTx) Commit() *runtime.Fault   { return nil }
func (tx adapterFakeTx) Rollback() *runtime.Fault { return nil }

func keyOf(v runtime.Value) string {
	if r, ok := v.(runtime.Record); ok {
		if inner, ok := r.Fields["value"]; ok {
			return keyOf(inner)
		}
	}
	return v.String()
}

func (db *adapterFakeDB) insert(id, name, email string) {
	db.rows[id] = runtime.Record{Fields: map[string]runtime.Value{
		"id": runtime.Str(id), "name": runtime.Str(name), "email": runtime.Str(email),
	}}
}

func TestHTTPDefaultMappingGET(t *testing.T) {
	u, files := loadProgram(t, "customer-service/models", "customer-service/errors",
		"customer-service/gateways", "customer-service/activities", "customer-service/workflows", "customer-service/adapters")
	caps, db := newFakeDatabaseForAdapter()
	db.insert("c1", "Ada", "ada@example.com")
	in := runtime.New(u, caps)

	h, err := HTTPHandler(findAdapter(t, files, "customerHttpApi"), u, in)
	if err != nil {
		t.Fatalf("HTTPHandler: %v", err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/customers/c1", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	// A model is a plain object of its fields, as the OpenAPI document
	// describes it, not run-cli's tagged {"$type", "fields"}.
	if body["id"] != "c1" || body["$type"] != nil {
		t.Fatalf("got %#v", body)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/customers/missing", nil))
	if rec.Code != 500 {
		t.Fatalf("status = %d, want 500 for the default Err mapping", rec.Code)
	}
}

func TestHTTPExplicitMapping(t *testing.T) {
	u, files := loadProgram(t, "customer-service/models", "customer-service/errors",
		"customer-service/gateways", "customer-service/activities", "customer-service/workflows", "customer-service/conversions",
		"customer-service/adapters")
	caps, db := newFakeDatabaseForAdapter()
	db.insert("c1", "Ada", "ada@example.com")
	in := runtime.New(u, caps)

	h, err := HTTPHandler(findAdapter(t, files, "customerHttpApiWithMapping"), u, in)
	if err != nil {
		t.Fatalf("HTTPHandler: %v", err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/customers/c1", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	// Err(CustomerError.NotFound(_)) -> customerErrorToHttpResponse(error) -> HttpResponse(status: 404, body: None)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/customers/missing", nil))
	if rec.Code != 404 {
		t.Fatalf("status = %d, want 404 via the explicit mapping's shared conversion", rec.Code)
	}
}

func TestHTTPPathAndQueryBinding(t *testing.T) {
	u, files := loadProgram(t, "customer-service/models", "customer-service/errors",
		"customer-service/gateways", "customer-service/activities", "customer-service/workflows", "customer-service/adapters")
	caps, db := newFakeDatabaseForAdapter()
	db.insert("c1", "Ada", "ada@example.com")
	in := runtime.New(u, caps)

	h, err := HTTPHandler(findAdapter(t, files, "customerEmailApi"), u, in)
	if err != nil {
		t.Fatalf("HTTPHandler: %v", err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/customers/c1?newEmail=ada2@example.com", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["email"] != "ada2@example.com" {
		t.Fatalf("got %#v, want the updated (and reread, inside the transaction) email", body)
	}
}

func TestHTTPRequestBodyBinding(t *testing.T) {
	u, files := loadProgram(t, "customer-service/models", "customer-service/errors",
		"customer-service/rules", "customer-service/formulas", "customer-service/activities", "customer-service/gateways", "customer-service/workflows",
		"customer-service/adapters")
	caps, _ := newFakeDatabaseForAdapter()
	in := runtime.New(u, caps)

	h, err := HTTPHandler(findAdapter(t, files, "customerCreateApi"), u, in)
	if err != nil {
		t.Fatalf("HTTPHandler: %v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/customers", strings.NewReader(`{"name": "Ada", "email": "ada@example.com"}`))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body)
	}
}

// TestHTTPRequestBodyDuplicateNameIs400 is ADR 0062 §6: Go's map decoding
// kept the last duplicate silently; the boundary now rejects it.
func TestHTTPRequestBodyDuplicateNameIs400(t *testing.T) {
	u, files := loadProgram(t, "customer-service/models", "customer-service/errors",
		"customer-service/rules", "customer-service/formulas", "customer-service/activities", "customer-service/gateways", "customer-service/workflows",
		"customer-service/adapters")
	caps, _ := newFakeDatabaseForAdapter()
	in := runtime.New(u, caps)

	h, err := HTTPHandler(findAdapter(t, files, "customerCreateApi"), u, in)
	if err != nil {
		t.Fatalf("HTTPHandler: %v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/customers", strings.NewReader(`{"name": "Ada", "name": "Eve", "email": "ada@example.com"}`))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "duplicate name") {
		t.Fatalf("status = %d, body %s; want 400 naming the duplicate", rec.Code, rec.Body)
	}
}

func TestHTTPQueryMethodRequestBodyBinding(t *testing.T) {
	// customer_query_api.fiig: `QUERY "/customers/lookup" -> lookupCustomer(criteria: CustomerLookup)`
	// — QUERY carries a body like POST but stays safe/idempotent like GET (RFC 10008).
	u, files := loadProgram(t, "customer-service/models", "customer-service/errors",
		"customer-service/gateways", "customer-service/activities", "customer-service/workflows", "customer-service/adapters")
	caps, db := newFakeDatabaseForAdapter()
	db.insert("c1", "Ada", "ada@example.com")
	in := runtime.New(u, caps)

	h, err := HTTPHandler(findAdapter(t, files, "customerQueryApi"), u, in)
	if err != nil {
		t.Fatalf("HTTPHandler: %v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("QUERY", "/customers/lookup", strings.NewReader(`{"id": "c1"}`))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body)
	}
}

func TestCLIDispatch(t *testing.T) {
	u, files := loadProgram(t, "customer-service/models", "customer-service/errors",
		"customer-service/gateways", "customer-service/activities", "customer-service/workflows", "customer-service/adapters")
	caps, db := newFakeDatabaseForAdapter()
	db.insert("c1", "Ada", "ada@example.com")
	in := runtime.New(u, caps)

	ad := findAdapter(t, files, "customerCli")
	resp, err := Dispatch(context.Background(), ad, u, in, []string{"customer", "get", "c1"})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if resp.Status != 200 {
		t.Fatalf("Status = %d, want 200", resp.Status)
	}
	rec, ok := resp.Body.(runtime.Record)
	if !ok || rec.TypeName != "Customer" {
		t.Fatalf("got %#v, want a Customer record", resp.Body)
	}

	_, err = Dispatch(context.Background(), ad, u, in, []string{"customer", "list"})
	if err == nil {
		t.Fatal("got nil error, want one: no route matches \"customer list\"")
	}
}

// TestDecodeStringFloat is ADR 0048 §2.3 at the HTTP boundary: a path or
// query float decodes, and NaN or Inf, which strconv reads, is a bad
// request rather than a value.
func TestDecodeStringFloat(t *testing.T) {
	floatT := types.Primitive{Name: "float"}
	if v, err := decodeString("-12.5", floatT, nil); err != nil || v != runtime.Float(-12.5) {
		t.Fatalf("decode -12.5 = %v, %v", v, err)
	}
	for _, raw := range []string{"NaN", "Inf", "-Infinity", "1e400", "abc"} {
		if v, err := decodeString(raw, floatT, nil); err == nil {
			t.Errorf("decode %q = %v, want an error", raw, v)
		}
	}
	if schema := schemaFor(floatT, resolver{}); schema["type"] != "number" {
		t.Errorf("schema = %v, want a number", schema)
	}
}

// TestDecodeStringDecimal is ADR 0064 §8: a path or query decimal reads
// the text number grammar (widened by ADR 0067 §7), keeping its scale.
func TestDecodeStringDecimal(t *testing.T) {
	decT := types.Primitive{Name: "decimal"}
	for raw, want := range map[string]string{"19.90": "19.90", "+1": "1", "1.": "1", ".50": "0.50"} {
		if v, err := decodeString(raw, decT, nil); err != nil || v.String() != want {
			t.Errorf("decode %q = %v, %v; want %s", raw, v, err, want)
		}
	}
	for _, raw := range []string{"NaN", "Infinity", "1e34", "abc", ".", "1,5"} {
		if v, err := decodeString(raw, decT, nil); err == nil {
			t.Errorf("decode %q = %v, want an error", raw, v)
		}
	}
	if schema := schemaFor(decT, resolver{}); schema["type"] != "number" || schema["format"] != "decimal" {
		t.Errorf("schema = %v, want a decimal number", schema)
	}
}
