package checker

import "testing"

// ADR 0093: the list forms, sorted's keys, and Map.atOr.

const ratingModel = "model Rating {\n    itemId: string;\n    score: int;\n    weight: float;\n    price: decimal;\n    recommended: bool;\n    updatedAt: int;\n    note: string | None;\n}"

func TestListFormsCheck(t *testing.T) {
	diags := checkSrc(t, `rule r(ratings: List<Rating>, count: int) -> int {
    recommended: List<Rating> = filter(r in ratings): r.recommended;
    ids: List<string> = map(r in ratings): r.itemId;
    groups: Map<string, List<Rating>> = groupBy(r in recommended): r.itemId;
    many: int = count(r in ratings): r.score > 3;
    some: bool = any(r in ratings): r.recommended;
    every: bool = all(r in ratings): r.score >= 0;
    total: int = sum(r in ratings): r.score;
    ones: int = sum(r in ratings): 1;
    weight: float = sum(r in ratings): r.weight;
    price: decimal = sum(r in ratings): r.price;
    latest: int = max(r in ratings): r.updatedAt;
    first: string = min(r in ratings): r.itemId;
    maybe: int | None = maxOrNone(r in ratings): r.score;
    ranked: List<Rating> = sorted(r in ratings): r.score descending then r.updatedAt descending then r.itemId;
    seen = groups.atOr("a", []);
    tally: Map<string, int> = [:];
    tally = tally.put("a", tally.atOr("a", 0) + 1);
    return total + count + many + latest + seen.length() + ranked.length();
}`, ratingModel)
	if len(diags) != 0 {
		t.Fatalf("diags = %v, want none", diags)
	}
	// The names are contextual: declarations named after forms are called
	// as usual.
	diags = checkSrc(t, "rule count(n: int) -> int {\n    return n;\n}", "activity a(n: int) -> int {\n    return count(n) + math.max(n, 1);\n}")
	if len(diags) != 0 {
		t.Fatalf("call to a rule named count: diags = %v, want none", diags)
	}
}

func TestListFormsMisuse(t *testing.T) {
	rule := func(body string) string {
		return "rule r(ratings: List<Rating>) -> int {\n    " + body + "\n    return 0\n}"
	}
	cases := []struct{ src, rule, fragment string }{
		{rule("x = filter(r in ratings): r.score"), "A1", "a filter body must be bool, got int"},
		{rule("x = count(r in ratings): r.itemId"), "A1", "a count body must be bool, got string"},
		{rule("x = sum(r in ratings): r.itemId"), "A1", "a sum body must be int, float, or decimal, got string"},
		{rule("x = max(r in ratings): r.note"), "M3", "decide where None goes"},
		{rule("x = min(r in ratings): r.recommended"), "M3", "a min key must be int, float, decimal, or string, got bool"},
		{rule("x = groupBy(r in ratings): r.weight"), "M1", "float can't be a map key"},
		{rule("x = map(r in ratings.first()): r.score"), "A1", "map takes a List, got Rating"},
		{rule("filter(r in ratings): r.recommended"), "A13", "the result of filter(...) is discarded"},
		{rule("x = sorted(r in ratings, descending): r.score"), "A1", "`descending` now follows the key"},
		{rule("x = sorted(r in ratings): r.score then r.note"), "M3", "decide where None goes"},
		{rule("m: Map<string, int> = [:];\n    x = m.atOr(\"a\", \"none\");"), "A1", ""},
	}
	for _, c := range cases {
		wantDiag(t, checkSrc(t, c.src, ratingModel), c.rule, c.fragment)
	}
	// A body may call only what a rule may, even inside an activity.
	diags := checkSrc(t, `activity a(ratings: List<Rating>) -> List<Rating> {
    return filter(r in ratings): isBanned(r.itemId);
}`, "gateway isBanned(id: string) -> bool\n    requires Database\n{\n    return false;\n}", ratingModel)
	wantDiag(t, diags, "B5", `a filter body may not call gateway "isBanned"; it may call what the enclosing declaration may, but nothing with an effect`)
}

// ADR 0100 (abs-recommendations GAPS G31): in a conversion a body may
// call a conversion; in a rule it still can't.
func TestFormBodiesCallWhatTheirDeclarationMay(t *testing.T) {
	toId := "conversion idOf(r: Rating) -> string {\n    return r.itemId;\n}"
	diags := checkSrc(t, "conversion ids(ratings: List<Rating>) -> List<string> {\n    return map(r in ratings): idOf(r);\n}", toId, ratingModel)
	if len(diags) != 0 {
		t.Fatalf("in a conversion: diags = %v, want none", diags)
	}
	diags = checkSrc(t, "rule ids(ratings: List<Rating>) -> List<string> {\n    return map(r in ratings): idOf(r);\n}", toId, ratingModel)
	wantDiag(t, diags, "B7", `may not call conversion "idOf"`)
}
