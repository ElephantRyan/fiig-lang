package runtime

import (
	"context"
	"testing"
)

// ADR 0065 §3: sorted(x in xs): key at run time.
func TestSortedForm(t *testing.T) {
	model := "model Parcel {\n    id: string\n    kilograms: decimal\n    region: string\n}"
	rule := `activity order(parcels: List<Parcel>, descending: bool) -> string {
    if (descending) {
        heavy = sorted(p in parcels): p.kilograms descending;
        return "${ids(heavy)}";
    }
    light = sorted(p in parcels): p.kilograms;
    byRegionThenWeight = sorted(p in light): p.region;
    return "${ids(light)} ${ids(byRegionThenWeight)}";
}`
	ids := `rule ids(parcels: List<Parcel>) -> string {
    out: string = "";
    for (p in parcels) {
        out = "${out}${p.id}";
    }
    return out;
}`
	checkClean(t, model, rule, ids)
	in := newTestInterp(universeFromSource(t, model, rule, ids), nil)
	parcel := func(id, kg, region string) Value {
		return Record{TypeName: "Parcel", Fields: map[string]Value{"id": Str(id), "kilograms": dec(t, kg), "region": Str(region)}}
	}
	// b and c weigh the same (1.50 and 1.5), so stability keeps b first in
	// both directions; the region sort keeps the weight order within west.
	parcels := List{Elems: []Value{parcel("a", "2", "west"), parcel("b", "1.50", "east"), parcel("c", "1.5", "west"), parcel("d", "0.5", "west")}}
	for _, c := range []struct {
		descending bool
		want       string
	}{{false, "dbca bdca"}, {true, "abcd"}} {
		got, fault := in.Call(context.Background(), "order", map[string]Value{"parcels": parcels, "descending": Bool(c.descending)})
		if fault != nil || got.String() != c.want {
			t.Errorf("descending=%v: got %v, %v; want %s", c.descending, got, fault, c.want)
		}
	}
}
