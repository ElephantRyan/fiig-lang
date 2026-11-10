package checker

import (
	"testing"

	"fiig-lang/compiler/syntax"
)

// ADR 0073: json.decode, json.encode, J1, and J2.

const eventModel = `model Event {
    type: string
    data: JsonValue
}`

func TestJsonDecodeTakesItsTypeFromContext(t *testing.T) {
	diags := checkSrc(t, eventModel, `rule parse(body: bytes) -> Result<Event, JsonError> {
    event: Event = json.decode(body)?
    tree: JsonValue = json.decode(event.data)?
    text: string = json.decode("\"x\"")?
    return json.decode(tree)
}`, `rule send(event: Event) -> bytes {
    return json.encode(event)
}`)
	if len(diags) > 0 {
		t.Fatalf("diags = %v", diags)
	}

	// The call records what it decodes into, for the runtime.
	f := mustParse(t, `rule parse(body: bytes) -> Result<Event, JsonError> {
    event: Event = json.decode(body)?
    return Ok(event)
}`)
	Check([]*syntax.File{mustParse(t, eventModel), f}, nil)
	call := f.Decls[0].(*syntax.RuleDecl).Body.Stmts[0].(*syntax.Binding).Value.(*syntax.TryExpr).Operand.(*syntax.CallExpr)
	if call.DecodeInto == nil || call.DecodeInto.(interface{ String() string }).String() != "Event" {
		t.Errorf("DecodeInto = %v", call.DecodeInto)
	}
}

func TestJsonDecodeRefusals(t *testing.T) {
	cases := []struct {
		name, src, rule, fragment string
	}{
		{"no context", `rule r(body: bytes) -> int {
    match json.decode(body) {
        Ok(_) -> { return 1 }
        Err(_) -> { return 0 }
    }
}`, "A12", "json.decode has no type to decode into"},
		{"no ?", `rule r(body: bytes) -> Event {
    return json.decode(body)
}`, "A1", "add `?`"},
		{"wrong error type", `rule r(body: bytes) -> Result<Event, string> {
    return json.decode(body)
}`, "A1", "Result<Event, JsonError>"},
		{"wrong input", `rule r(n: int) -> Result<Event, JsonError> {
    return json.decode(n)
}`, "A1", "takes a string, bytes, or a JsonValue, got int"},
		{"no member", `rule r(body: bytes) -> bytes {
    return json.parse(body)
}`, "", "has no member \"parse\""},
		{"local named json", `rule r(json: int) -> int {
    return json
}`, "G4", "prelude json group"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			wantDiag(t, checkSrc(t, eventModel, c.src), c.rule, c.fragment)
		})
	}
	wantDiag(t, checkSrc(t, `model JsonValue {
    a: int
}`), "K2", "a prelude type")
	wantDiag(t, checkSrc(t, `rule json(a: int) -> int {
    return a
}`), "K2", "the prelude json group")
}

// §8: J1, at the call and at a request body.
func TestJ1(t *testing.T) {
	session := `model Session {
    user: string
    token: Secret
}`
	cases := []struct {
		name, src, fragment string
	}{
		{"secret field", `rule r(body: bytes) -> Result<Session, JsonError> {
    return json.decode(body)
}`, "json.decode into Session: field token is a Secret"},
		{"encode secret", `rule r(s: Session) -> bytes {
    return json.encode(s)
}`, "json.encode of Session: field token is a Secret"},
		{"bytes", `rule r(body: string) -> Result<List<bytes>, JsonError> {
    return json.decode(body)
}`, "it is bytes"},
		{"map keys", `rule r(body: string) -> Result<Map<int, string>, JsonError> {
    return json.decode(body)
}`, "a Map with int keys"},
		{"nested in an enum", `rule r(body: string) -> Result<Holder, JsonError> {
    return json.decode(body)
}`, "field Has.session.token is a Secret"},
	}
	holder := `enum Holder {
    Has(session: Session)
    Empty
}`
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			wantDiag(t, checkSrc(t, session, holder, c.src), "J1", c.fragment)
		})
	}
	// Encoding a Map with int keys is allowed: it is a list of pairs.
	if diags := checkSrc(t, `rule r(m: Map<int, string>) -> bytes {
    return json.encode(m)
}`); len(diags) > 0 {
		t.Errorf("encode Map<int, string>: %v", diags)
	}
	wantDiag(t, checkSrc(t, `model Upload {
    name: string
    content: bytes
}`, `workflow upload(file: Upload) -> string {
    return file.name
}`, `adapter http api {
    POST "/uploads" -> upload(file: Upload)
        consumes "application/json"
        produces "text/plain"
}`), "J1", `request body parameter "file" decodes into Upload: field content is bytes`)
}

// §7b: J2, and W3 on the member's JSON name.
func TestJ2AndW3OnJsonNames(t *testing.T) {
	wantDiag(t, checkSrc(t, `model Upload {
    contentType: string from json "type"
    type: string
}`), "J2", `Upload.type reads the JSON member "type", which Upload.contentType already reads`)
	wantDiag(t, checkSrc(t, `enum Change {
    Moved(from: string from json "at", at: string)
}`), "J2", `Change.Moved.at reads the JSON member "at"`)
	wantDiag(t, checkSrc(t, `model Base {
    id: string
}`, `model Item {
    Base
    key: string from json "id"
}`), "J2", `fields id and key of Item all read the JSON member "id"`)
	wantDiag(t, checkSrc(t, `enum Change {
    Renamed(name: string from json "variant")
}`), "W3", "from json \"variant\"")
	if diags := checkSrc(t, `enum Change {
    Renamed(variant: string from json "newVariant")
}`); len(diags) > 0 {
		t.Errorf("a payload field named variant with another JSON name: %v", diags)
	}
}
