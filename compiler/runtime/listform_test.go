package runtime

import (
	"context"
	"strings"
	"testing"
)

// ADR 0093 at run time: the list forms, sorted's keys, and Map.atOr.
func TestListForms(t *testing.T) {
	model := "model Rating {\n    itemId: string\n    score: int\n    recommended: bool\n    updatedAt: int\n}"
	rule := `rule summary(ratings: List<Rating>) -> string {
    recommended = filter(r in ratings): r.recommended
    groups = groupBy(r in recommended): r.itemId
    out: string = ""
    for itemId, group in groups {
        total = sum(r in group): r.score
        latest = max(r in group): r.updatedAt
        out = "${out}${itemId}:${total}/${group.length()}@${latest} "
    }
    ids = map(r in sorted(r in ratings): r.score descending then r.updatedAt): r.itemId
    tally: Map<string, int> = [:]
    for r in ratings {
        tally = tally.put(r.itemId, tally.atOr(r.itemId, 0) + 1)
    }
    empty: List<int> = []
    none = maxOrNone(r in filter(r in ratings): r.score > 99): r.score
    return "${out}| ${ids} | ${count(r in ratings): r.recommended} ${any(r in ratings): r.score == 5} ${all(r in ratings): r.score > 1} ${sum(r in empty): 1} ${min(r in ratings): r.itemId} ${tally} ${none}"
}`
	checkClean(t, model, rule)
	in := newTestInterp(universeFromSource(t, model, rule), nil)
	rating := func(item string, score int64, rec bool, at int64) Value {
		return Record{TypeName: "Rating", Fields: map[string]Value{"itemId": Str(item), "score": Int(score), "recommended": Bool(rec), "updatedAt": Int(at)}}
	}
	ratings := List{Elems: []Value{rating("b", 4, true, 10), rating("a", 5, true, 30), rating("b", 2, true, 20), rating("c", 4, false, 5), rating("a", 4, true, 1)}}
	got, fault := in.Call(context.Background(), "summary", map[string]Value{"ratings": ratings})
	// Groups are in first-appearance order; sorted ties on score 4 go to
	// the earlier updatedAt.
	want := `b:6/2@20 a:9/2@30 | [a, a, c, b, b] | 4 true true 0 a [b: 2, a: 2, c: 1] None`
	if fault != nil || got.String() != want {
		t.Fatalf("got %v, %v\nwant %s", got, fault, want)
	}
}

func TestListFormFaults(t *testing.T) {
	rule := "rule r(xs: List<int>) -> int {\n    return max(x in xs): x\n}"
	checkClean(t, rule)
	in := newTestInterp(universeFromSource(t, rule), nil)
	_, fault := in.Call(context.Background(), "r", map[string]Value{"xs": List{}})
	if fault == nil || !strings.Contains(fault.Error(), "use maxOrNone") {
		t.Fatalf("fault = %v, want max of an empty list", fault)
	}
	sum := "rule s(xs: List<int>) -> int {\n    return sum(x in xs): x\n}"
	checkClean(t, sum)
	in = newTestInterp(universeFromSource(t, sum), nil)
	_, fault = in.Call(context.Background(), "s", map[string]Value{"xs": List{Elems: []Value{Int(1 << 62), Int(1 << 62)}}})
	if fault == nil || !strings.Contains(fault.Error(), "overflow") {
		t.Fatalf("fault = %v, want overflow", fault)
	}
}
