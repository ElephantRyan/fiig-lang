package adapter

import (
	"encoding/json"
	"testing"
)

func TestOpenAPIDefaultMappingRoute(t *testing.T) {
	u, files := loadProgram(t, "customer-service/models", "customer-service/errors",
		"customer-service/gateways", "customer-service/workflows", "customer-service/adapters")
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
		"customer-service/gateways", "customer-service/workflows", "customer-service/conversions",
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
	if _, ok := responses["default"]; !ok {
		t.Fatalf("missing \"default\" response for the ExprResponse arm (customerErrorToHttpResponse): %#v", responses)
	}
}

func TestOpenAPIRequestBodyAndQueryParameter(t *testing.T) {
	u, files := loadProgram(t, "customer-service/models", "customer-service/errors",
		"customer-service/rules", "customer-service/formulas", "customer-service/gateways", "customer-service/workflows",
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
		"customer-service/rules", "customer-service/formulas", "customer-service/gateways", "customer-service/workflows",
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
