package runtime

import (
	"context"
	"strings"
	"testing"

	"fiig-lang/compiler/checker"
	"fiig-lang/compiler/parser"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// ADR 0073: json.decode and json.encode in fiig code.

var jsonProgram = []string{
	`model Page {
    limit: int
    in: string
    contentType: string from json "content-type"
    tags: Map<string, int>
    extra: JsonValue
    next: string | None
    region: Region
}`,
	`enum Region {
    US
    EU
}`,
	`model Order {
    items: List<Item>
}`,
	`model Item {
    amount: int
}`,
	`rule parsePage(body: string) -> Result<Page, JsonError> {
    page: Page = json.decode(body)?;
    return Ok(page);
}`,
	`rule parseBytes(body: bytes) -> Result<Page, JsonError> {
    return json.decode(body);
}`,
	`rule parseOrder(body: string) -> Result<Order, JsonError> {
    return json.decode(body);
}`,
	`rule encodePage(page: Page) -> bytes {
    return json.encode(page);
}`,
	`rule tree(body: string) -> Result<JsonValue, JsonError> {
    return json.decode(body);
}`,
	`rule encodeTree(v: JsonValue) -> bytes {
    return json.encode(v);
}`,
	`rule amount(v: JsonValue) -> Result<int, JsonError> {
    n: int = json.decode(v)?;
    return Ok(n);
}`,
	`rule badNumber(text: string) -> bytes {
    return json.encode(JsonValue.Number(text: text));
}`,
}

// checkedInterp checks srcs, failing on any diagnostic, and runs them.
func checkedInterp(t *testing.T, srcs ...string) *Interp {
	t.Helper()
	files := make([]*syntax.File, len(srcs))
	for i, src := range srcs {
		f, errs := parser.Parse(src)
		if len(errs) > 0 {
			t.Fatalf("parse %q: %v", src, errs)
		}
		files[i] = f
	}
	if diags := checker.Check(files, nil); len(diags) > 0 {
		t.Fatalf("checker: %+v", diags)
	}
	u, _ := types.BuildUniverse(files)
	return newTestInterp(u, nil)
}

func callJSON(t *testing.T, in *Interp, name string, args map[string]Value) Value {
	t.Helper()
	v, fault := in.Call(context.Background(), name, args)
	if fault != nil {
		t.Fatalf("%s: fault %v", name, fault)
	}
	return v
}

const pageJSON = `{"limit": 3.0, "in": "inbox", "content-type": "text/csv", "tags": {"z": 1, "a": 2}, "extra": {"b": [1.50, null], "a": true}, "region": "EU", "ignored": 1}`

func TestJsonDecodeIntoAModel(t *testing.T) {
	in := checkedInterp(t, jsonProgram...)
	for _, name := range []string{"parsePage", "parseBytes"} {
		var arg Value = Str(pageJSON)
		if name == "parseBytes" {
			arg = Bytes{B: []byte(pageJSON)}
		}
		r := callJSON(t, in, name, map[string]Value{"body": arg}).(Result)
		if !r.Ok {
			t.Fatalf("%s: %s", name, r.Value)
		}
		page := r.Value.(Record)
		// 3.0 is an int (ADR 0062 §2), a keyword names a field (§7a), a
		// `from json` field reads its member (§7b), a missing optional
		// field is None, and an unknown member is ignored.
		if page.Fields["limit"] != Int(3) || page.Fields["in"] != Str("inbox") || page.Fields["contentType"] != Str("text/csv") || page.Fields["next"] != (None{}) {
			t.Errorf("%s: page = %s", name, page)
		}
		// A Map and a JsonValue keep the members' arrival order.
		if got := page.Fields["tags"].String(); got != "[z: 1, a: 2]" {
			t.Errorf("tags = %s", got)
		}
		out := callJSON(t, in, "encodePage", map[string]Value{"page": page}).(Bytes)
		want := `{"limit":3,"in":"inbox","content-type":"text/csv","tags":{"z":1,"a":2},"extra":{"b":[1.50,null],"a":true},"next":null,"region":"EU"}`
		if string(out.B) != want {
			t.Errorf("encode:\n got %s\nwant %s", out.B, want)
		}
	}
}

func TestJsonDecodeErrors(t *testing.T) {
	in := checkedInterp(t, jsonProgram...)
	cases := []struct {
		name, body, want string
	}{
		{"parseOrder", `{"items": [{"amount": 1}, {"amount": 2}, {"amount": 2.5}]}`, `Mismatch items[2].amount: 2.5 has a fractional part`},
		{"parseOrder", `{"items": "none"}`, `Mismatch items: expected an array, got a string`},
		{"parseOrder", `{}`, `Mismatch : Order missing required field "items"`},
		{"parsePage", `{"limit": 1, "in": "x", "content-type": "y", "tags": {}, "extra": 1, "region": "us"}`, `Mismatch region: "us" is not a variant of Region (US, EU)`},
		{"parsePage", `{"limit": 1, "in": "x", "contentType": "y", "tags": {}, "extra": 1, "region": "US"}`, `Mismatch : Page missing required field "content-type"`},
		{"parseOrder", `{"items": [], "items": []}`, `Syntax 14: duplicate name "items"`},
		{"parseOrder", `{"items": [1,]}`, `Syntax 13: expected a value, got ']'`},
		{"parseOrder", `{"items": []} x`, `Syntax 14: unexpected 'x' after the JSON value`},
		{"parseOrder", `{"items": [01]}`, `Syntax 12: expected ',' or ']', got '1'`},
		{"parseOrder", "{\"items\": \"a\tb\"}", `Syntax 12: control character '\t' in a string; write it escaped`},
		{"parseOrder", ``, `Syntax 0: expected a value, got end of input`},
	}
	for _, c := range cases {
		r := callJSON(t, in, c.name, map[string]Value{"body": Str(c.body)}).(Result)
		if r.Ok || jsonErrText(r.Value) != c.want {
			t.Errorf("%s(%s)\n got %v %s\nwant %s", c.name, c.body, r.Ok, jsonErrText(r.Value), c.want)
		}
	}
	// Bytes that aren't UTF-8 are a Syntax error, not U+FFFD (§2).
	r := callJSON(t, in, "parseBytes", map[string]Value{"body": Bytes{B: []byte("{\"in\": \"a\xffb\"}")}}).(Result)
	if r.Ok || jsonErrText(r.Value) != `Syntax 9: invalid UTF-8` {
		t.Errorf("invalid UTF-8: %s", r.Value)
	}
	// Nesting is bounded.
	deep := strings.Repeat("[", maxJSONDepth+2)
	r = callJSON(t, in, "tree", map[string]Value{"body": Str(deep)}).(Result)
	if r.Ok || !strings.Contains(r.Value.String(), "nested more than") {
		t.Errorf("deep nesting: %s", r.Value)
	}
}

// §4: a JsonValue keeps everything, encodes as the JSON it holds, and
// decodes into a type by the same rules bytes do.
func TestJsonValueTree(t *testing.T) {
	in := checkedInterp(t, jsonProgram...)
	src := ` {"b": [1.50, 1e400, -0, "\u00e9\ud83d\ude00"], "a": {"x": null, "y": false}} `
	r := callJSON(t, in, "tree", map[string]Value{"body": Str(src)}).(Result)
	if !r.Ok {
		t.Fatal(r.Value)
	}
	out := callJSON(t, in, "encodeTree", map[string]Value{"v": r.Value}).(Bytes)
	if want := `{"b":[1.50,1e400,-0,"é😀"],"a":{"x":null,"y":false}}`; string(out.B) != want {
		t.Errorf("tree round trip:\n got %s\nwant %s", out.B, want)
	}
	for text, want := range map[string]string{
		"3.0": "Ok(3)",
		"2.5": `Err(JsonError.Mismatch(, 2.5 has a fractional part))`,
	} {
		got := callJSON(t, in, "amount", map[string]Value{"v": jsonValue("Number", Str(text))})
		if got.String() != want {
			t.Errorf("amount(%s) = %s, want %s", text, got, want)
		}
	}
	// A Number built with text that isn't a number is the program's defect.
	if _, fault := in.Call(context.Background(), "badNumber", map[string]Value{"text": Str("abc")}); fault == nil || !strings.Contains(fault.Message, `JsonValue.Number(text: "abc") is not a JSON number`) {
		t.Errorf("badNumber: fault = %v", fault)
	}
	if _, fault := in.Call(context.Background(), "amount", map[string]Value{"v": jsonValue("Number", Str("1x"))}); fault == nil {
		t.Error("decoding a hand-built invalid Number didn't fault")
	}
}

// jsonErrText is a JsonError as "Syntax 14: reason" or
// "Mismatch items[2].amount: reason".
func jsonErrText(v Value) string {
	ev, ok := v.(EnumValue)
	if !ok || ev.TypeName != "JsonError" || len(ev.Fields) != 2 {
		return v.String()
	}
	where := ev.Fields[0].String()
	if s, ok := ev.Fields[0].(Str); ok {
		where = string(s)
	}
	return ev.Variant + " " + where + ": " + string(ev.Fields[1].(Str))
}

func TestJsonEncodeEscapesStrings(t *testing.T) {
	b, err := EncodeBodyJSON(List{Elems: []Value{Str("a\"b\\c\n\x01<>&\u2028é"), Str("\xff")}})
	if err != nil {
		t.Fatal(err)
	}
	if want := `["a\"b\\c\n\u0001<>&\u2028é","\ufffd"]`; string(b) != want {
		t.Errorf("got %s, want %s", b, want)
	}
}

// ADR 0073 §9 (amended 2026-11-21): json.encodePretty is json.encode's
// members in its order, indented two spaces, with no newline at the end.
func TestJsonEncodePretty(t *testing.T) {
	in := checkedInterp(t, append(jsonProgram,
		`rule prettyPage(page: Page) -> bytes {
    return json.encodePretty(page);
}`,
		`rule prettyTree(v: JsonValue) -> bytes {
    return json.encodePretty(v);
}`,
		`rule prettyList(items: List<string>) -> bytes {
    return json.encodePretty(value: items);
}`,
		`rule prettyBadNumber(text: string) -> bytes {
    return json.encodePretty(JsonValue.Number(text: text));
}`)...)
	r := callJSON(t, in, "parsePage", map[string]Value{"body": Str(pageJSON)}).(Result)
	if !r.Ok {
		t.Fatal(r.Value)
	}
	r.Value.(Record).Fields["tags"] = Map{} // an empty object
	out := callJSON(t, in, "prettyPage", map[string]Value{"page": r.Value}).(Bytes)
	want := `{
  "limit": 3,
  "in": "inbox",
  "content-type": "text/csv",
  "tags": {},
  "extra": {
    "b": [
      1.50,
      null
    ],
    "a": true
  },
  "next": null,
  "region": "EU"
}`
	if string(out.B) != want {
		t.Errorf("encodePretty:\n got %s\nwant %s", out.B, want)
	}
	// Strings are written as encode writes them, punctuation and escapes
	// included; an empty list is [].
	items := List{Elems: []Value{Str(`a, "b": {c}`), Str("line\nbreak \u2028")}}
	out = callJSON(t, in, "prettyList", map[string]Value{"items": items}).(Bytes)
	if want := `[
  "a, \"b\": {c}",
  "line\nbreak \u2028"
]`; string(out.B) != want {
		t.Errorf("encodePretty of strings:\n got %s\nwant %s", out.B, want)
	}
	out = callJSON(t, in, "prettyList", map[string]Value{"items": List{}}).(Bytes)
	if string(out.B) != "[]" {
		t.Errorf("encodePretty of [] = %s", out.B)
	}
	out = callJSON(t, in, "prettyTree", map[string]Value{"v": jsonValue("Number", Str("1e400"))}).(Bytes)
	if string(out.B) != "1e400" {
		t.Errorf("encodePretty of a Number = %s", out.B)
	}
	if _, fault := in.Call(context.Background(), "prettyBadNumber", map[string]Value{"text": Str("abc")}); fault == nil || !strings.Contains(fault.Message, `json.encodePretty: JsonValue.Number(text: "abc") is not a JSON number`) {
		t.Errorf("prettyBadNumber: fault = %v", fault)
	}
}
