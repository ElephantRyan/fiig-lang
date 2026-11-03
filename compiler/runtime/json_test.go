package runtime

import (
	"testing"

	"fiig-lang/compiler/types"
)

func decodeRawJSON(t *testing.T, raw string) any {
	t.Helper()
	v, err := ParseJSON([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestDecodeJSONModelAndValueWrapper(t *testing.T) {
	u := loadUniverse(t, "customer-service/models")
	customerDecl, ok := u.Lookup("Customer")
	if !ok {
		t.Fatal("Customer not found")
	}
	pt := types.Named{Name: "Customer", Decl: customerDecl}

	raw := decodeRawJSON(t, `{"id": "c1", "name": "Ada", "email": "ada@example.com"}`)
	v, err := DecodeJSON(raw, pt, u)
	if err != nil {
		t.Fatalf("DecodeJSON: %v", err)
	}
	rec, ok := v.(Record)
	if !ok || rec.TypeName != "Customer" {
		t.Fatalf("got %#v, want Customer Record", v)
	}
	if rec.Fields["id"] != Str("c1") {
		t.Fatalf("id field = %#v, want \"c1\"", rec.Fields["id"])
	}
}

func TestDecodeJSONOptionalFieldDefaultsToNone(t *testing.T) {
	u := loadUniverse(t, "customer-service/models")
	decl, _ := u.Lookup("Customer")
	optional := types.Union{Members: []types.Type{types.Named{Name: "Customer", Decl: decl}, types.NoneType{}}}
	if !IsOptional(optional) {
		t.Fatal("IsOptional(Customer | None) = false, want true")
	}
}

func TestDecodeJSONListOfPrimitives(t *testing.T) {
	u := loadUniverse(t, "customer-service/models")
	listType := types.Generic{Name: "List", Args: []types.Type{types.Primitive{Name: "string"}}}

	raw := decodeRawJSON(t, `["c1", "c2"]`)
	v, err := DecodeJSON(raw, listType, u)
	if err != nil {
		t.Fatalf("DecodeJSON: %v", err)
	}
	list, ok := v.(List)
	if !ok || len(list.Elems) != 2 {
		t.Fatalf("got %#v, want a 2-element List", v)
	}
	if list.Elems[0] != Str("c1") {
		t.Fatalf("got %s", list.Elems[0])
	}
}

func TestEncodeJSONRoundTripsTaggedShapes(t *testing.T) {
	// A one-field model whose field happens to be named "value" encodes like
	// any other model — ADR 0055 removed the value-wrapper special case.
	rec := Record{TypeName: "Box", Fields: map[string]Value{"value": Str("c1")}}
	got := EncodeJSON(rec)
	m, ok := got.(map[string]any)
	if !ok || m["$type"] != "Box" {
		t.Fatalf("got %#v", got)
	}
	if fields, ok := m["fields"].(map[string]any); !ok || fields["value"] != "c1" {
		t.Fatalf("got %#v", got)
	}

	result := Result{Ok: false, Value: EnumValue{TypeName: "E", Variant: "X", Fields: []Value{Int(1)}}}
	got = EncodeJSON(result)
	m, ok = got.(map[string]any)
	if !ok || m["$result"] != "Err" {
		t.Fatalf("got %#v", got)
	}
	inner, ok := m["value"].(map[string]any)
	if !ok || inner["$enum"] != "E" || inner["variant"] != "X" {
		t.Fatalf("got %#v", m["value"])
	}
}

func TestDecodeUntypedJSON(t *testing.T) {
	raw := decodeRawJSON(t, `{"id": "c1", "count": 3, "ok": true, "missing": null, "tags": ["a", "b"]}`)
	v, err := DecodeUntypedJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	rec, ok := v.(Record)
	if !ok {
		t.Fatalf("got %T, want Record", v)
	}
	if rec.Fields["id"] != Str("c1") || rec.Fields["count"] != Int(3) || rec.Fields["ok"] != Bool(true) {
		t.Fatalf("got %s", rec)
	}
	if _, isNone := rec.Fields["missing"].(None); !isNone {
		t.Fatalf("missing field = %#v, want None", rec.Fields["missing"])
	}
	tags, ok := rec.Fields["tags"].(List)
	if !ok || len(tags.Elems) != 2 {
		t.Fatalf("tags = %#v, want a 2-element List", rec.Fields["tags"])
	}
}
