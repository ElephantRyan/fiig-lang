package adapter

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"fiig-lang/compiler/checker"
	"fiig-lang/compiler/parser"
	"fiig-lang/compiler/runtime"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// ADR 0072: an enum with no payloads is its variant's name, an enum with
// payloads (and a Result) an object naming its variant, and a request body
// decodes the same shapes it is sent in.

var enumProgram = []string{
	`enum Region {
    US,
    EU
}`,
	`enum Problem {
    NotFound(id: string),
    Late(id: string, days: int | None),
    Unknown
}`,
	`enum Source(key code: string) {
    Acme(code: "ACME"),
    Globex(code: "GLBX")
}`,
	`model Shipment {
    region: Region;
    problem: Problem;
    outcome: Result<int, Problem>;
    source: Source;
}`,
	`workflow echoShipment(shipment: Shipment) -> Shipment {
    return shipment;
}`,
	`workflow sourceCode(shipment: Shipment) -> string {
    return shipment.source.code;
}`,
	`adapter http enumApi {
    POST "/echo" -> echoShipment(shipment: Shipment)
        consumes "application/json"
        produces "application/json";
    POST "/code" -> sourceCode(shipment: Shipment)
        consumes "application/json"
        produces "text/plain";
}`,
}

func TestEnumsAndResultsRoundTripThroughABody(t *testing.T) {
	h := handlerFor(t, enumProgram...)
	cases := []struct{ in, out string }{
		{
			`{"region": "EU", "problem": {"variant": "Late", "id": "s1", "days": 3}, "outcome": {"variant": "Ok", "value": 7}, "source": "Globex"}`,
			`{"region":"EU","problem":{"variant":"Late","id":"s1","days":3},"outcome":{"variant":"Ok","value":7},"source":"Globex"}`,
		},
		{
			// A payload-free variant of a payload enum is still an object; a
			// missing optional payload field is None; unknown members are ignored.
			`{"region": "US", "problem": {"variant": "Unknown", "extra": 1}, "outcome": {"variant": "Err", "error": {"variant": "Late", "id": "s2"}}, "source": "Acme"}`,
			`{"region":"US","problem":{"variant":"Unknown"},"outcome":{"variant":"Err","error":{"variant":"Late","id":"s2","days":null}},"source":"Acme"}`,
		},
	}
	for _, c := range cases {
		req := httptest.NewRequest("POST", "/echo", strings.NewReader(c.in))
		req.Header.Set("Content-Type", "application/json")
		code, _, body := serve(h, req)
		if code != 200 || strings.TrimSpace(body) != c.out {
			t.Errorf("POST /echo %s\n got %d %s\nwant 200 %s", c.in, code, body, c.out)
		}
	}
}

func TestADecodedHeadedEnumReadsItsHeader(t *testing.T) {
	h := handlerFor(t, enumProgram...)
	req := httptest.NewRequest("POST", "/code", strings.NewReader(
		`{"region": "US", "problem": {"variant": "Unknown"}, "outcome": {"variant": "Ok", "value": 1}, "source": "Globex"}`))
	req.Header.Set("Content-Type", "application/json")
	if code, _, body := serve(h, req); code != 200 || body != "GLBX" {
		t.Errorf("got %d %q, want 200 \"GLBX\"", code, body)
	}
}

func TestEnumDecodingRefusals(t *testing.T) {
	h := handlerFor(t, enumProgram...)
	valid := map[string]string{
		"region":  `"US"`,
		"problem": `{"variant": "Unknown"}`,
		"outcome": `{"variant": "Ok", "value": 1}`,
		"source":  `"Acme"`,
	}
	cases := []struct{ field, value, want string }{
		{"region", `"us"`, `\"us\" is not a variant of Region (US, EU)`},
		{"region", `{"variant": "US"}`, "expected a string naming a variant of Region"},
		{"problem", `"Unknown"`, `expected an object with a \"variant\" member for Problem`},
		{"problem", `{"id": "s1"}`, `Problem needs a \"variant\" member`},
		{"problem", `{"variant": "Gone"}`, `\"Gone\" is not a variant of Problem (NotFound, Late, Unknown)`},
		{"problem", `{"variant": "NotFound"}`, `Problem.NotFound missing required field \"id\"`},
		{"outcome", `{"variant": "Maybe"}`, "naming Ok or Err"},
		{"source", `"ACME"`, `\"ACME\" is not a variant of Source (Acme, Globex)`},
	}
	for _, c := range cases {
		members := []string{}
		for _, name := range []string{"region", "problem", "outcome", "source"} {
			v := valid[name]
			if name == c.field {
				v = c.value
			}
			members = append(members, `"`+name+`": `+v)
		}
		req := httptest.NewRequest("POST", "/echo", strings.NewReader("{"+strings.Join(members, ", ")+"}"))
		req.Header.Set("Content-Type", "application/json")
		code, _, body := serve(h, req)
		if code != 400 || !strings.Contains(body, c.want) {
			t.Errorf("%s = %s: got %d %s, want 400 containing %s", c.field, c.value, code, body, c.want)
		}
	}
}

func TestOpenAPIDescribesEnumAndResultShapes(t *testing.T) {
	var files []*syntax.File
	var ad *syntax.AdapterDecl
	for _, src := range enumProgram {
		f, errs := parser.Parse(src)
		if len(errs) > 0 {
			t.Fatalf("parse: %v", errs)
		}
		files = append(files, f)
		for _, d := range f.Decls {
			if a, ok := d.(*syntax.AdapterDecl); ok {
				ad = a
			}
		}
	}
	u, _ := types.BuildUniverse(files)
	doc, err := GenerateOpenAPI(ad, u)
	if err != nil {
		t.Fatal(err)
	}
	post := doc["paths"].(map[string]any)["/echo"].(map[string]any)["post"].(map[string]any)
	schema := post["requestBody"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
	got, err := json.Marshal(schema["properties"])
	if err != nil {
		t.Fatal(err)
	}
	want := `{"outcome":{"discriminator":{"propertyName":"variant"},"oneOf":[` +
		`{"properties":{"value":{"type":"integer"},"variant":{"enum":["Ok"],"type":"string"}},"required":["variant","value"],"type":"object"},` +
		`{"properties":{"error":{"discriminator":{"propertyName":"variant"},"oneOf":[` +
		`{"properties":{"id":{"type":"string"},"variant":{"enum":["NotFound"],"type":"string"}},"required":["variant","id"],"type":"object"},` +
		`{"properties":{"days":{"nullable":true,"type":"integer"},"id":{"type":"string"},"variant":{"enum":["Late"],"type":"string"}},"required":["variant","id"],"type":"object"},` +
		`{"properties":{"variant":{"enum":["Unknown"],"type":"string"}},"required":["variant"],"type":"object"}]},` +
		`"variant":{"enum":["Err"],"type":"string"}},"required":["variant","error"],"type":"object"}]},` +
		`"problem":{"discriminator":{"propertyName":"variant"},"oneOf":[` +
		`{"properties":{"id":{"type":"string"},"variant":{"enum":["NotFound"],"type":"string"}},"required":["variant","id"],"type":"object"},` +
		`{"properties":{"days":{"nullable":true,"type":"integer"},"id":{"type":"string"},"variant":{"enum":["Late"],"type":"string"}},"required":["variant","id"],"type":"object"},` +
		`{"properties":{"variant":{"enum":["Unknown"],"type":"string"}},"required":["variant"],"type":"object"}]},` +
		`"region":{"enum":["US","EU"],"type":"string"},` +
		`"source":{"enum":["Acme","Globex"],"type":"string"}}`
	if string(got) != want {
		t.Errorf("properties:\n got %s\nwant %s", got, want)
	}
}

// TestCorpusShippingQuotes serves examples/shipping-quotes: enums in a
// request body, sent back in the same shapes, and an error enum sent as
// the 422 body itself.
func TestCorpusShippingQuotes(t *testing.T) {
	if diags, err := checker.CheckDir(filepath.Join(findExamplesDir(t), "shipping-quotes"), nil); err != nil || len(diags) > 0 {
		t.Fatalf("checker: %v %+v", err, diags)
	}
	u, files := loadProgram(t, "shipping-quotes/models", "shipping-quotes/errors", "shipping-quotes/rules",
		"shipping-quotes/workflows", "shipping-quotes/adapters")
	h, err := HTTPHandler(findAdapter(t, files, "shippingApi"), u, runtime.New(u, runtime.NewCapabilityRegistry()))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		in   string
		code int
		out  string
	}{
		{`{"weightGrams": 2500, "speed": "Express", "destination": {"variant": "Domestic", "postcode": "2000"}}`,
			200, `{"speed":"Express","destination":{"variant":"Domestic","postcode":"2000"},"cents":1600}`},
		{`{"weightGrams": 40000, "speed": "Standard", "destination": {"variant": "Domestic", "postcode": "2000"}}`,
			422, `{"variant":"TooHeavy","weightGrams":40000,"limitGrams":30000}`},
		{`{"weightGrams": 100, "speed": "Express", "destination": {"variant": "International", "country": "NZ", "postcode": "6011"}}`,
			422, `{"variant":"NoExpressAbroad"}`},
	}
	for _, c := range cases {
		req := httptest.NewRequest("POST", "/quotes", strings.NewReader(c.in))
		req.Header.Set("Content-Type", "application/json")
		if code, _, body := serve(h, req); code != c.code || strings.TrimSpace(body) != c.out {
			t.Errorf("POST /quotes %s\n got %d %s\nwant %d %s", c.in, code, body, c.code, c.out)
		}
	}

	// ADR 0080 §3-§4: Speed read from the path, exactly, and an unknown
	// one a 400 naming the ones it may be.
	for path, want := range map[string]string{
		"/rates/Express?weightGrams=2500":   `200 1600`,
		"/rates/Standard?weightGrams=2500":  `200 800`,
		"/rates/express?weightGrams=2500":   `400 {"error":"path value \"speed\": \"express\" isn't a Speed; expected Standard or Express"}`,
		"/rates/Overnight?weightGrams=2500": `400 {"error":"path value \"speed\": \"Overnight\" isn't a Speed; expected Standard or Express"}`,
	} {
		code, _, body := serve(h, httptest.NewRequest("GET", path, nil))
		if got := fmt.Sprintf("%d %s", code, strings.TrimSpace(body)); got != want {
			t.Errorf("GET %s\n got %s\nwant %s", path, got, want)
		}
	}

	// §5: the path parameter's schema lists the variants.
	doc, err := GenerateOpenAPI(findAdapter(t, files, "shippingApi"), u)
	if err != nil {
		t.Fatal(err)
	}
	op := doc["paths"].(map[string]any)["/rates/{speed}"].(map[string]any)["get"].(map[string]any)
	speed, _ := json.Marshal(op["parameters"].([]any)[0])
	if !strings.Contains(string(speed), `"schema":{"enum":["Standard","Express"],"type":"string"}`) {
		t.Errorf("speed's parameter doesn't list the variants: %s", speed)
	}
}
