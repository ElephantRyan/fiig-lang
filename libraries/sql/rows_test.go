package sql

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"fiig-lang/compiler/checker"
	"fiig-lang/compiler/decimal"
	"fiig-lang/compiler/parser"
	"fiig-lang/compiler/runtime"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// ADR 0082 against real SQLite: values bound by fiig read back into a
// declared row type, and the refusals fault at the query.

var parcelSources = []string{
	`model Parcel {
    id: string
    weight: decimal
    note: string | None
    state: ParcelState
    fragile: bool
    label: bytes
}`,
	`enum ParcelState {
    Packed
    Sent from json "sent"
}`,
	`gateway saveParcel(p: Parcel) -> int
    requires Database
{
    return Database.execute(
        "INSERT INTO parcels (id, weight, note, state, fragile, label) VALUES (?, ?, ?, ?, ?, ?)",
        p.id,
        p.weight,
        p.note,
        p.state,
        p.fragile,
        p.label,
    )
}`,
	`gateway findParcel(id: string) -> Parcel | None
    requires Database
    idempotent
{
    return Database.queryOne("SELECT id, weight, note, state, fragile, label FROM parcels WHERE id = ?", id)
}`,
	`gateway parcelIds() -> List<string>
    requires Database
    idempotent
{
    return Database.queryMany("SELECT id FROM parcels ORDER BY id")
}`,
	`gateway readBoth() -> List<string>
    requires Database
    idempotent
{
    return Database.queryMany("SELECT a.id, b.id FROM parcels a JOIN parcels b ON a.id = b.id")
}`,
}

func parcelInterp(t *testing.T, schema string) (*runtime.Interp, *types.Universe, *sql.DB) {
	t.Helper()
	files := make([]*syntax.File, len(parcelSources))
	for i, src := range parcelSources {
		f, errs := parser.Parse(src)
		if len(errs) > 0 {
			t.Fatalf("parse: %v", errs)
		}
		files[i] = f
	}
	if diags := checker.Check(files, nil); len(diags) > 0 {
		t.Fatalf("check: %v", diags)
	}
	u, _ := types.BuildUniverse(files)
	db := openTestDB(t)
	if _, err := db.Exec(schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Exec("DROP TABLE parcels") })
	caps := runtime.NewCapabilityRegistry()
	caps.Register("Database", New(db))
	return runtime.New(u, caps), u, db
}

func TestRowsReadIntoTheirDeclaredType(t *testing.T) {
	in, u, _ := parcelInterp(t, `CREATE TABLE parcels (id TEXT, weight TEXT, note TEXT, state TEXT, fragile INTEGER, label BLOB)`)
	decl, _ := u.Lookup("Parcel")
	stateDecl, _ := u.Lookup("ParcelState")
	d, _ := decimal.Parse("19.90")
	weight := runtime.Decimal{Decimal: d}
	parcel := runtime.Record{TypeName: "Parcel", Decl: decl.(*syntax.ModelDecl), Fields: map[string]runtime.Value{
		"id":      runtime.Str("p1"),
		"weight":  weight,
		"note":    runtime.None{},
		"state":   runtime.EnumValue{TypeName: "ParcelState", Variant: "Sent", Decl: stateDecl.(*syntax.EnumDecl)},
		"fragile": runtime.Bool(true),
		"label":   runtime.Bytes{B: []byte{0xff, 0x00, 0x7f}},
	}}
	ctx := context.Background()
	if _, f := in.Call(ctx, "saveParcel", map[string]runtime.Value{"p": parcel}); f != nil {
		t.Fatalf("saveParcel: %v", f)
	}
	got, f := in.Call(ctx, "findParcel", map[string]runtime.Value{"id": runtime.Str("p1")})
	if f != nil {
		t.Fatalf("findParcel: %v", f)
	}
	if !runtime.Equal(got, parcel) {
		t.Fatalf("got %v, want %v", got, parcel)
	}
	if w := got.(runtime.Record).Fields["weight"].(runtime.Decimal).String(); w != "19.90" {
		t.Errorf("weight = %s, want its scale kept", w)
	}
	ids, f := in.Call(ctx, "parcelIds", nil)
	if f != nil || !runtime.Equal(ids, runtime.List{Elems: []runtime.Value{runtime.Str("p1")}}) {
		t.Fatalf("parcelIds = %v, %v", ids, f)
	}
	if _, f := in.Call(ctx, "readBoth", nil); f == nil || !strings.Contains(f.Error(), `two columns are named "id"`) {
		t.Fatalf("readBoth fault = %v, want two columns named id", f)
	}
}

// §7: SQLite's NUMERIC affinity turns '19.90' into a REAL in a DECIMAL
// column, and reading that into a decimal faults at the first read.
func TestADecimalStoredAsRealFaults(t *testing.T) {
	in, _, db := parcelInterp(t, `CREATE TABLE parcels (id TEXT, weight DECIMAL(10,2), note TEXT, state TEXT, fragile INTEGER, label BLOB)`)
	if _, err := db.Exec(`INSERT INTO parcels VALUES ('p2', '19.90', NULL, 'sent', 0, x'00')`); err != nil {
		t.Fatal(err)
	}
	_, f := in.Call(context.Background(), "findParcel", map[string]runtime.Value{"id": runtime.Str("p2")})
	if f == nil || !strings.Contains(f.Error(), `column "weight" holds REAL 19.9, but Parcel.weight is decimal`) {
		t.Fatalf("fault = %v, want the REAL refused", f)
	}
}
