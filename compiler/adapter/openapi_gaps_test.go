package adapter

import (
	"strings"
	"testing"

	"fiig-lang/compiler/parser"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

func universeOf(t *testing.T, srcs ...string) (*types.Universe, []*syntax.File) {
	t.Helper()
	var files []*syntax.File
	for _, src := range srcs {
		f, errs := parser.Parse(src)
		if len(errs) > 0 {
			t.Fatalf("parse: %v", errs)
		}
		files = append(files, f)
	}
	u, conflicts := types.BuildUniverse(files)
	if len(conflicts) > 0 {
		t.Fatalf("conflicts: %v", conflicts)
	}
	return u, files
}

// responseSchema is the JSON schema of path's GET response with code.
func responseSchema(t *testing.T, doc map[string]any, path, code string) map[string]any {
	t.Helper()
	get := doc["paths"].(map[string]any)[path].(map[string]any)["get"].(map[string]any)
	resp, ok := get["responses"].(map[string]any)[code].(map[string]any)
	if !ok {
		t.Fatalf("GET %s has no %s response: %#v", path, code, get["responses"])
	}
	content, ok := resp["content"].(map[string]any)
	if !ok {
		t.Fatalf("GET %s %s has no content: %#v", path, code, resp)
	}
	return content["application/json"].(map[string]any)["schema"].(map[string]any)
}

// abs-recommendations G38: a workflow that returns a plain value and maps it
// with a name (`status -> 200 status`) describes that value, not `{}`.
func TestAPlainValueWorkflowHasItsSchema(t *testing.T) {
	u, files := universeOf(t, `model Health {
    ok: bool
}`, `workflow healthCheck() -> Health {
    return Health(ok: true);
}`, `adapter http healthApi {
    GET "/health" -> healthCheck() {
        status -> 200 status
    }
}`)
	doc, err := GenerateOpenAPI(findAdapter(t, files, "healthApi"), u)
	if err != nil {
		t.Fatal(err)
	}
	schema := responseSchema(t, doc, "/health", "200")
	props, _ := schema["properties"].(map[string]any)
	if _, ok := props["ok"]; !ok {
		t.Fatalf("200 schema = %#v, want Health's object with an ok property", schema)
	}
}

// abs-recommendations G42: an arm answered by another package's conversion
// still lists the statuses that conversion builds.
func TestAResponseBuiltInAnotherPackageKeepsItsStatuses(t *testing.T) {
	common, _ := universeOf(t, `enum Refusal {
    Missing
    Denied
}`, `conversion refusalResponse(error: Refusal) -> HttpResponse<string> {
    return match (error) {
        Refusal.Missing -> {
            return HttpResponse(status: 404, body: "missing");
        }
        Refusal.Denied -> {
            return HttpResponse(status: 403, body: "denied");
        }
    }
}`)
	app, files := universeOf(t, `use shop.common

model Item {
    name: string
}`, `workflow readItem() -> Result<Item, common.Refusal> {
    return Ok(Item(name: "x"));
}`, `adapter http itemApi {
    GET "/item" -> readItem() {
        Ok(item) -> 200 item
        Err(error) -> common.refusalResponse(error)
    }
}`)
	pkg := &types.Package{Path: "shop.items", Universe: app, Imports: map[string]*types.Universe{"common": common}}
	doc, err := GenerateOpenAPIFor([]Served{{Decl: findAdapter(t, files, "itemApi"), Package: pkg}}, app, nil)
	if err != nil {
		t.Fatal(err)
	}
	responses := doc["paths"].(map[string]any)["/item"].(map[string]any)["get"].(map[string]any)["responses"].(map[string]any)
	for _, code := range []string{"200", "403", "404"} {
		if _, ok := responses[code]; !ok {
			t.Errorf("responses = %v, want %s among them", keys(responses), code)
		}
	}
	if _, only := responses["default"]; only && len(responses) == 1 {
		t.Errorf("only default: %s", strings.Join(keys(responses), ","))
	}
}

func keys(m map[string]any) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

// Hearsay G57: an arm's conversion that hands a variant's carried error to
// another conversion (`RatingError.Session(c) -> identityErrorResponse(c)`)
// answers with that conversion's statuses too.
func TestAResponseFromANestedConversionKeepsItsStatus(t *testing.T) {
	u, files := universeOf(t,
		"enum IdentityError {\n    NotSignedIn\n}",
		"enum RatingError {\n    Session(cause: IdentityError)\n    NoRating\n}",
		"workflow deleteMine(token: string) -> Result<string, RatingError> {\n    return Err(RatingError.NoRating);\n}",
		"conversion identityErrorResponse(error: IdentityError) -> HttpResponse<string> {\n    return HttpResponse(status: 401, body: \"sign in first\");\n}",
		`conversion ratingErrorResponse(error: RatingError) -> HttpResponse<string> {
    return match (error) {
        RatingError.Session(cause) -> identityErrorResponse(cause)
        RatingError.NoRating -> HttpResponse(status: 404, body: "no rating")
    }
}`,
		`adapter http api {
    GET "/mine/{token}" -> deleteMine(token: string)
        produces "application/json"
    {
        Ok(who) -> 200 who
        Err(error) -> ratingErrorResponse(error)
    }
}`)
	doc, err := GenerateOpenAPI(findAdapter(t, files, "api"), u)
	if err != nil {
		t.Fatal(err)
	}
	responses := doc["paths"].(map[string]any)["/mine/{token}"].(map[string]any)["get"].(map[string]any)["responses"].(map[string]any)
	for _, code := range []string{"200", "401", "404"} {
		if _, ok := responses[code]; !ok {
			t.Errorf("responses = %v, want %s", keys(responses), code)
		}
	}
}
