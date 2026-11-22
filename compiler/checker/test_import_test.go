package checker

import (
	"strings"
	"testing"

	"fiig-lang/compiler/parser"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// abs-recommendations GAPS G14: a fake of a config declared in a
// dependency the test file doesn't import names the `use` to add, not
// T7's "declare it first".
func TestFakeOfAnUnimportedDeclarationNamesTheUse(t *testing.T) {
	dep, errs := parser.Parse("wire Abs {\n    url: string from env \"ABS_URL\"\n}\n")
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	u, _ := types.BuildUniverse([]*syntax.File{dep})
	packages := types.Packages{"abs.client": {Path: "abs.client", Universe: u}}
	diags, _ := CheckSources(map[string]string{
		"tests/abs_url.fiig": "test absUrl {\n    fake Abs {\n        url: \"https://abs.test\"\n    }\n    expect 1 == 1\n}\n",
	}, packages, "abs.app", nil)
	if len(diags) != 1 || diags[0].Rule != "T4" || !strings.Contains(diags[0].Msg, "add `use abs.client.Abs`") {
		t.Fatalf("diags = %v, want one T4 naming the use", diags)
	}
}

// abs-recommendations GAPS G18: a rule calling a conversion is told to
// become a conversion.
func TestRuleCallingAConversionNamesTheFix(t *testing.T) {
	diags := checkSrc(t, "rule tokenRequest(code: string) -> string {\n    return formEncode(code)\n}", "conversion formEncode(s: string) -> string {\n    return s\n}")
	wantDiag(t, diags, "B7", "so make tokenRequest a conversion")
}

// abs-recommendations GAPS G22: an Ok arm on a route whose workflow
// returns a plain value is a check-time error, not a fault per request.
func TestRouteArmThatCantMatchTheWorkflowIsAnError(t *testing.T) {
	diags := checkSrc(t, "workflow endSession(sid: string) -> int {\n    return 1\n}", "adapter http api {\n    DELETE \"/session/{sid}\" -> endSession(sid: string)\n        produces \"application/json\"\n    {\n        Ok(n) -> 204\n    }\n}")
	wantDiag(t, diags, "A1", "workflow endSession returns int, which this arm's pattern can never match; bind the value with a name instead")
}

// abs-recommendations GAPS G25: a test file's private helpers return
// their own type; only the test itself returns nothing.
func TestATestFilesHelpersReturn(t *testing.T) {
	diags, _ := CheckSources(map[string]string{
		"models/row.fiig":      "model Row {\n    score: int\n}\n",
		"tests/uses_rows.fiig": "test usesRows {\n    expect row(3).score == 3\n}\n\nprivate {\n    row(score: int) -> Row {\n        return Row(score: score)\n    }\n}\n",
	}, nil, "", nil)
	if len(diags) != 0 {
		t.Fatalf("diags = %v, want none", diags)
	}
	diags, _ = CheckSources(map[string]string{
		"tests/returns.fiig": "test returns {\n    return 1\n}\n",
	}, nil, "", nil)
	if len(diags) != 1 || !strings.Contains(diags[0].Msg, "a test returns nothing") {
		t.Fatalf("diags = %v, want the test's own return refused", diags)
	}
}

// abs-recommendations GAPS G32: a query key that differs from its
// placeholder's name is an error, not a key that's never read.
func TestQueryKeyMustMatchItsPlaceholder(t *testing.T) {
	diags := checkSrc(t, "workflow page(size: int | None) -> int {\n    return 1\n}", "adapter http api {\n    GET \"/items?limit={size}\" -> page(size: int | None)\n        produces \"application/json\"\n}")
	wantDiag(t, diags, "", "query key \"limit\" takes its placeholder's name")
}

// abs-recommendations GAPS G35: a bare arm after Err(..) on a Result is
// told to be Ok(..), once.
func TestBareArmAfterErrOnAResult(t *testing.T) {
	diags := checkSrc(t, "rule r(x: Result<string, int>) -> string {\n    match x {\n        Err(e) -> \"\"\n        id -> id\n    }\n}")
	if len(diags) != 1 || !strings.Contains(diags[0].Msg, "write `Ok(id)` to bind the value") {
		t.Fatalf("diags = %v, want one naming Ok(id)", diags)
	}
}
