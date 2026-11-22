package runtime

import (
	"context"
	"testing"

	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// TestEmbeddedModelIsFlatAtRuntime is ADR 0045's runtime half: construction
// names the embedded type, but the resulting record is flat, so a promoted
// field reads as the outer model's own, an explicit field shadows a promoted
// one, and JSON encodes promoted fields at the top level.
func TestEmbeddedModelIsFlatAtRuntime(t *testing.T) {
	srcs := []string{
		"model Address {\n    line1: string\n    city: string\n}",
		"model ContactInfo {\n    Address\n    email: string\n}",
		"model Customer {\n    ContactInfo\n    id: string\n    city: string\n}",
		"rule build(a: Address, email: string) -> Customer {\n    return Customer(ContactInfo: ContactInfo(Address: a, email: email), id: \"c1\", city: \"Shadow\");\n}",
		"rule plain(a: Address, email: string) -> ContactInfo {\n    return ContactInfo(Address: a, email: email);\n}",
	}
	files := make([]*syntax.File, len(srcs))
	for i, s := range srcs {
		files[i] = mustParseFile(t, s)
	}
	u, _ := types.BuildUniverse(files)
	in := newTestInterp(u, nil)

	addr := Record{TypeName: "Address", Fields: map[string]Value{"line1": Str("1 Main"), "city": Str("Oslo")}}
	args := map[string]Value{"a": addr, "email": Str("a@example.com")}

	v, fault := in.Call(context.Background(), "build", args)
	if fault != nil {
		t.Fatalf("build: %v", fault)
	}
	rec := mustRecord(t, v)
	if rec.TypeName != "Customer" {
		t.Fatalf("TypeName = %q, want Customer", rec.TypeName)
	}
	want := map[string]Value{"line1": Str("1 Main"), "city": Str("Shadow"), "email": Str("a@example.com"), "id": Str("c1")}
	if len(rec.Fields) != len(want) {
		t.Fatalf("fields = %v, want exactly %v (flat, no ContactInfo/Address key)", rec.Fields, want)
	}
	for k, w := range want {
		if rec.Fields[k] != w {
			t.Errorf("field %q = %v, want %v (explicit city must shadow the promoted one)", k, rec.Fields[k], w)
		}
	}

	// EncodeJSON wraps a record as {"$type": ..., "fields": {...}}; the
	// promoted fields must sit directly in "fields", not under an embedded
	// model's name.
	obj, _ := EncodeJSON(rec).(map[string]any)
	fields, _ := obj["fields"].(map[string]any)
	if fields["email"] != "a@example.com" || fields["line1"] != "1 Main" || fields["ContactInfo"] != nil {
		t.Fatalf("JSON must carry promoted fields flat, got %v", EncodeJSON(rec))
	}

	// Decoding goes through the same flat field set.
	decoded, err := DecodeJSON(map[string]any{"line1": "x", "city": "c", "email": "e", "id": "i"}, types.Named{Name: "Customer", Decl: u.Decls["Customer"]}, u)
	if err != nil {
		t.Fatalf("DecodeJSON: %v", err)
	}
	if mustRecord(t, decoded).Fields["email"] != Str("e") {
		t.Fatalf("decoded = %v", decoded)
	}
}
