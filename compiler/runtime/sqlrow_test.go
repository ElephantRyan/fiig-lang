package runtime

import (
	"strings"
	"testing"

	"fiig-lang/compiler/types"
)

// ADR 0082 §6-§7: a query's rows read into the row type by exact column
// name, each value by its field's type.

func rowUniverse(t *testing.T) (*types.Universe, types.Type) {
	t.Helper()
	u := universeFromSource(t, `model Parcel {
    id: string
    weight: decimal
    grams: float
    count: int
    note: string | None
    state: ParcelState
    fragile: bool
    label: bytes
}`, `enum ParcelState {
    Packed
    Sent from json "sent"
}`)
	d, _ := u.Lookup("Parcel")
	return u, types.Named{Name: "Parcel", Decl: d}
}

func parcelRow() Record {
	return Record{Fields: map[string]Value{
		"id":      Str("p1"),
		"weight":  Str("19.90"),
		"grams":   Int(250),
		"count":   Int(2),
		"note":    None{},
		"state":   Bytes{B: []byte("sent")},
		"fragile": Int(1),
		"label":   Bytes{B: []byte{0xff, 0x00}},
	}}
}

func TestReadRowFillsAModel(t *testing.T) {
	u, parcel := rowUniverse(t)
	v, err := readRow(parcelRow(), parcel, universeFields(u))
	if err != nil {
		t.Fatal(err)
	}
	rec := v.(Record)
	if rec.TypeName != "Parcel" || rec.Decl == nil {
		t.Fatalf("got %#v, want a Parcel", v)
	}
	if got := rec.Fields["weight"].(Decimal).String(); got != "19.90" {
		t.Errorf("weight = %s, want 19.90 with its scale", got)
	}
	if got := rec.Fields["grams"]; got != Value(Float(250)) {
		t.Errorf("grams = %v, want 250.0", got)
	}
	if got := rec.Fields["state"].(EnumValue).Variant; got != "Sent" {
		t.Errorf("state = %s, want Sent", got)
	}
	if got := rec.Fields["fragile"]; got != Value(Bool(true)) {
		t.Errorf("fragile = %v", got)
	}
	if _, isNone := rec.Fields["note"].(None); !isNone {
		t.Errorf("note = %v, want None", rec.Fields["note"])
	}
}

func TestReadRowRefusals(t *testing.T) {
	u, parcel := rowUniverse(t)
	cases := []struct {
		col      string
		v        Value
		fragment string
	}{
		{"weight", Float(19.9), `column "weight" holds REAL 19.9, but Parcel.weight is decimal; a REAL can't be read as a decimal exactly`},
		{"count", Float(3), `holds REAL 3.0, but Parcel.count is int`},
		{"count", Str("3"), `holds TEXT "3", but Parcel.count is int`},
		{"id", None{}, `holds NULL, but Parcel.id is string; only a ` + "`T | None`" + ` field reads NULL`},
		{"id", Bytes{B: []byte{0xff}}, `holds a BLOB of 1 bytes`},
		{"fragile", Int(2), `holds INTEGER 2, but Parcel.fragile is bool`},
		{"state", Str("Sent"), `its variants are spelled Packed, sent`},
		{"weight", Str("heavy"), `it isn't a number`},
		{"grams", Int(1 << 60), `it isn't exact in binary64`},
	}
	for _, tc := range cases {
		row := parcelRow()
		row.Fields[tc.col] = tc.v
		_, err := readRow(row, parcel, universeFields(u))
		if err == nil || !strings.Contains(err.Error(), tc.fragment) {
			t.Errorf("%s = %v: err = %v, want %q", tc.col, tc.v, err, tc.fragment)
		}
	}
}

func TestReadRowMatchesColumnsExactly(t *testing.T) {
	u, parcel := rowUniverse(t)
	missing := parcelRow()
	delete(missing.Fields, "label")
	if _, err := readRow(missing, parcel, universeFields(u)); err == nil || !strings.Contains(err.Error(), "Parcel.label has no column") {
		t.Errorf("err = %v, want a missing column", err)
	}
	extra := parcelRow()
	extra.Fields["key_hash"] = Str("x")
	if _, err := readRow(extra, parcel, universeFields(u)); err == nil || !strings.Contains(err.Error(), `column "key_hash" has no field in Parcel`) {
		t.Errorf("err = %v, want an extra column", err)
	}
}

func TestReadRowOneColumn(t *testing.T) {
	v, err := readRow(Record{Fields: map[string]Value{"COUNT(*)": Int(3)}}, types.Primitive{Name: "int"}, nil)
	if err != nil || v != Value(Int(3)) {
		t.Fatalf("got %v, %v", v, err)
	}
	_, err = readRow(Record{Fields: map[string]Value{"a": Int(1), "b": Int(2)}}, types.Primitive{Name: "int"}, nil)
	if err == nil || !strings.Contains(err.Error(), "returns 2 columns") {
		t.Fatalf("err = %v, want two columns refused", err)
	}
}
