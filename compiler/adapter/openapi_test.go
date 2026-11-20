package adapter

import (
	"encoding/json"
	"strings"
	"testing"

	"fiig-lang/compiler/parser"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

func TestOpenAPIDefaultMappingRoute(t *testing.T) {
	u, files := loadProgram(t, "customer-service/models", "customer-service/errors",
		"customer-service/gateways", "customer-service/activities", "customer-service/workflows", "customer-service/adapters")
	doc, err := GenerateOpenAPI(findAdapter(t, files, "customerHttpApi"), u)
	if err != nil {
		t.Fatalf("GenerateOpenAPI: %v", err)
	}
	paths := doc["paths"].(map[string]any)
	get, ok := paths["/customers/{id}"].(map[string]any)["get"].(map[string]any)
	if !ok {
		t.Fatalf("got %#v, want a GET operation at /customers/{id}", paths)
	}
	params := get["parameters"].([]any)
	if len(params) != 1 {
		t.Fatalf("got %d parameters, want 1 (id)", len(params))
	}
	p0 := params[0].(map[string]any)
	if p0["name"] != "id" || p0["in"] != "path" {
		t.Fatalf("got %#v", p0)
	}
	if schema := p0["schema"].(map[string]any); schema["type"] != "string" {
		t.Fatalf("id's schema = %#v, want a plain string (CustomerId unwraps)", schema)
	}

	responses := get["responses"].(map[string]any)
	ok200 := responses["200"].(map[string]any)
	schema := ok200["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
	if schema["type"] != "object" {
		t.Fatalf("200 schema = %#v, want a Customer object schema", schema)
	}
	props := schema["properties"].(map[string]any)
	if _, ok := props["name"]; !ok {
		t.Fatalf("Customer schema missing \"name\" property: %#v", props)
	}
	if _, ok := responses["500"]; !ok {
		t.Fatalf("missing default Err->500 response: %#v", responses)
	}
}

func TestOpenAPIExplicitMappingRoute(t *testing.T) {
	u, files := loadProgram(t, "customer-service/models", "customer-service/errors",
		"customer-service/gateways", "customer-service/activities", "customer-service/workflows", "customer-service/conversions",
		"customer-service/adapters")
	doc, err := GenerateOpenAPI(findAdapter(t, files, "customerHttpApiWithMapping"), u)
	if err != nil {
		t.Fatalf("GenerateOpenAPI: %v", err)
	}
	paths := doc["paths"].(map[string]any)
	get := paths["/customers/{id}"].(map[string]any)["get"].(map[string]any)
	responses := get["responses"].(map[string]any)

	// http_customer_api_with_mapping.fiig has exactly two arms:
	// `Ok(customer) -> 200 customer` and
	// `Err(error) -> customerErrorToHttpResponse(error)`.
	if _, ok := responses["200"]; !ok {
		t.Fatalf("missing explicit 200 response: %#v", responses)
	}
	// customerErrorToHttpResponse builds its responses with constant
	// statuses, so each is described by its own (ADR 0098 §1).
	for _, code := range []string{"404", "409", "500"} {
		if _, ok := responses[code]; !ok {
			t.Errorf("missing the %s that customerErrorToHttpResponse builds: %#v", code, responses)
		}
	}
	if _, ok := responses["default"]; ok {
		t.Errorf("a default response beside the conversion's own statuses: %#v", responses)
	}
}

func TestOpenAPIRequestBodyAndQueryParameter(t *testing.T) {
	u, files := loadProgram(t, "customer-service/models", "customer-service/errors",
		"customer-service/rules", "customer-service/formulas", "customer-service/gateways", "customer-service/activities", "customer-service/workflows",
		"customer-service/adapters")
	doc, err := GenerateOpenAPI(findAdapter(t, files, "customerCreateApi"), u)
	if err != nil {
		t.Fatalf("GenerateOpenAPI: %v", err)
	}
	paths := doc["paths"].(map[string]any)
	post := paths["/customers"].(map[string]any)["post"].(map[string]any)
	body, ok := post["requestBody"].(map[string]any)
	if !ok {
		t.Fatalf("missing requestBody: %#v", post)
	}
	schema := body["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
	if schema["type"] != "object" {
		t.Fatalf("requestBody schema = %#v, want a NewCustomer object schema", schema)
	}

	doc2, err := GenerateOpenAPI(findAdapter(t, files, "customerEmailApi"), u)
	if err != nil {
		t.Fatalf("GenerateOpenAPI: %v", err)
	}
	paths2 := doc2["paths"].(map[string]any)
	patch := paths2["/customers/{id}"].(map[string]any)["patch"].(map[string]any)
	var queryParam map[string]any
	for _, p := range patch["parameters"].([]any) {
		pm := p.(map[string]any)
		if pm["in"] == "query" {
			queryParam = pm
		}
	}
	if queryParam == nil || queryParam["name"] != "newEmail" {
		t.Fatalf("got %#v, want a query parameter named newEmail", patch["parameters"])
	}
}

// TestOpenAPIDocumentIsValidJSON is a cheap sanity check that the whole
// generated structure actually serializes — the real consumer of this
// output (an OpenAPI viewer, codegen tool, etc.) only ever sees JSON.
func TestOpenAPIDocumentIsValidJSON(t *testing.T) {
	u, files := loadProgram(t, "customer-service/models", "customer-service/errors",
		"customer-service/rules", "customer-service/formulas", "customer-service/gateways", "customer-service/activities", "customer-service/workflows",
		"customer-service/conversions", "customer-service/adapters")
	for _, name := range []string{"customerHttpApi", "customerHttpApiWithMapping", "customerCreateApi", "customerEmailApi", "customerQueryApi"} {
		doc, err := GenerateOpenAPI(findAdapter(t, files, name), u)
		if err != nil {
			t.Fatalf("GenerateOpenAPI(%s): %v", name, err)
		}
		if _, err := json.MarshalIndent(doc, "", "  "); err != nil {
			t.Fatalf("MarshalIndent(%s): %v", name, err)
		}
	}
}

// ADR 0069: header and cookie parameters, and request and response content
// under each route's declared types.
func TestOpenAPIHeadersCookiesAndMediaTypes(t *testing.T) {
	var files []*syntax.File
	for _, src := range sourcesProgram {
		f, errs := parser.Parse(src)
		if len(errs) > 0 {
			t.Fatalf("parse: %v", errs)
		}
		files = append(files, f)
	}
	u, _ := types.BuildUniverse(files)
	doc, err := GenerateOpenAPI(findAdapter(t, files, "api"), u)
	if err != nil {
		t.Fatal(err)
	}
	paths := doc["paths"].(map[string]any)
	me := paths["/me"].(map[string]any)["get"].(map[string]any)
	got, _ := json.Marshal(me["parameters"])
	want := `[{"in":"header","name":"Authorization","required":true,"schema":{"type":"string"}},` +
		`{"in":"header","name":"X-Request-Id","required":false,"schema":{"type":"string"}},` +
		`{"in":"header","name":"X-Page","required":false,"schema":{"type":"integer"}}]`
	if string(got) != want {
		t.Fatalf("parameters = %s\nwant %s", got, want)
	}
	// ADR 0098 §2: the cookie is a security scheme, optional here since
	// the argument is `string | None`.
	if sec, _ := json.Marshal(me["security"]); string(sec) != `[{"sid":[]},{}]` {
		t.Errorf("security = %s", sec)
	}
	if schemes, _ := json.Marshal(doc["components"]); string(schemes) != `{"securitySchemes":{"sid":{"in":"cookie","name":"sid","type":"apiKey"}}}` {
		t.Errorf("components = %s", schemes)
	}

	csv := paths["/csv"].(map[string]any)["post"].(map[string]any)
	body, _ := json.Marshal(csv["requestBody"])
	if string(body) != `{"content":{"text/csv":{"schema":{"type":"string"}}},"required":true}` {
		t.Fatalf("requestBody = %s", body)
	}
	resp, _ := json.Marshal(csv["responses"].(map[string]any)["200"])
	if string(resp) != `{"content":{"text/csv":{"schema":{"type":"string"}}},"description":""}` {
		t.Fatalf("200 = %s", resp)
	}
	bin, _ := json.Marshal(paths["/bin"].(map[string]any)["post"].(map[string]any)["requestBody"])
	if !strings.Contains(string(bin), `"application/octet-stream":{"schema":{"format":"binary","type":"string"}}`) {
		t.Fatalf("bytes requestBody = %s", bin)
	}
	nothing, _ := json.Marshal(paths["/nothing"].(map[string]any)["get"].(map[string]any)["responses"].(map[string]any)["200"])
	if string(nothing) != `{"description":""}` {
		t.Fatalf("a None body = %s, want no content", nothing)
	}
}
