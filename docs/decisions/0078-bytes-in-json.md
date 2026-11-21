# ADR 0078: The JSON Form of `bytes`

Status: Accepted and implemented (2026-11-15; see Implementation notes);
§6 added and implemented 2026-11-21
Date: 2026-11-15
Related: ADR 0058 (`bytes`), ADR 0062 (play nice with foreign data), ADR
0068 §3 (`toBase64`, `toBase64Url`), ADR 0069 §7 (`fromBase64`,
`fromBase64Url`), ADR 0072 (the JSON shape of enums), ADR 0073 §8 (J1),
ADR 0077 (a JSON body bound as `bytes`), charter principle 10.

## Context

JSON has no binary type, so every language that writes bytes into JSON
picks a text form. fiig hasn't: ADR 0073 §8 made `bytes` J1 ("no ADR gives
it a JSON form"), so a model with a `bytes` field can't be decoded from a
request, encoded into a response, or passed to `json.encode` or
`json.decode`. Real APIs send binary fields all the time, and each picks a
form:

- **GitHub's contents API** returns a file's `content` as standard base64,
  with a line break every 60 characters.
- **Gmail's API** takes and returns a message's `raw` as base64url.
- **Kubernetes Secrets** hold `data` values as standard base64, padded.

There is also a bug in the meantime: `fiig run -json` prints a `bytes`
value as `null`. Its tagged encoder has no case for `bytes` and falls
through to its default, so the value is lost without a word.

### Precedent

| | Writes | Reads |
|---|---|---|
| Go `encoding/json` (`[]byte`) | standard base64, padded | standard only; CR and LF skipped |
| Protocol Buffers' JSON mapping (`bytes`) | standard base64, padded | standard **or** URL-safe, padded or not |
| Java, Jackson (`byte[]`) | standard base64, padded | standard |
| .NET `System.Text.Json` (`byte[]`) | standard base64, padded | standard |
| Rust, serde (`Vec<u8>`) | an array of numbers, unless `serde_bytes` or `serde_with` picks base64 | the same |
| Elixir, Jason | no bytes type: a binary is a string, and one that isn't UTF-8 is an error | — |
| OpenAPI 3.0 | `type: string, format: byte` is base64 (RFC 4648) | — |

Everyone that writes base64 writes the same thing: the standard alphabet,
padded. Protobuf, which has to read what every one of those languages
sends, accepts either alphabet with or without padding.

## Decision

### 1. `bytes` is a JSON string of standard base64, padded

Every JSON encoder writes it the same way: `json.encode`, a response body,
and `fiig run -json`. That is RFC 4648 §4, `toBase64()`'s output (ADR
0068 §3), and what Go, Jackson, .NET, and Protobuf send.

```fiig
model Attachment {
    name: string
    content: bytes
}
// {"name": "logo.png", "content": "iVBORw0KGgo="}
```

### 2. Reading accepts what real producers send

A JSON string decodes to `bytes` when it is base64 by ADR 0069 §7's rules
(padding optional, CR and LF skipped anywhere, leftover bits ignored),
with one change: **either alphabet is accepted**, the decoder choosing by
the characters it finds. §7 has each method read only its own alphabet
because the method names one, so text in the other alphabet is
mislabeled. A JSON member names none, so there is nothing for the text to
disagree with; Protobuf reads both for the same reason.

- `+` or `/` alongside `-` or `_` in one string is a
  `JsonError.Mismatch`: no producer writes that.
- Anything that isn't a string, including an array of numbers, is a
  `Mismatch` naming what's expected: `expected a base64 string, got an
  array`.
- The same rules hold for `json.decode` from a `JsonValue` (ADR 0073 §4)
  and for a request body decoded by the adapter.

### 3. J1 no longer covers `bytes`

J1 keeps `Secret`, `Connection`, capability and interface values,
`BatchResult`, and (to decode) a `Map` without `string` keys. The
generated OpenAPI writes `bytes` in a JSON body as `{"type": "string",
"format": "byte"}`, OpenAPI's own name for base64.

### 4. A JSON route's whole body as `bytes` stays raw, in both directions

ADR 0077 binds a JSON request body declared `bytes` as it arrived, not as
a base64 string. The response side matches it: a JSON route whose
response body is `bytes` (the workflow's return value, or an
`HttpResponse<bytes>`) sends those bytes as they are, not as a quoted
base64 string. A program that returns `bytes` to a JSON route has built
the JSON itself, with `json.encode` or by passing a stored document
through, and base64 there would wrap a correct body in a string nobody
asked for.

Because the route declares JSON (charter principle 10, strict about what
it says), the adapter checks those bytes are one JSON value in UTF-8
before sending them, and faults (500) when they aren't, rather than
sending a body that contradicts its own `Content-Type`.

Only the whole body is raw. A `bytes` anywhere inside it (a field, a list
element, an enum payload) is §1's base64, and `json.encode(b)` of a
`bytes` value is a base64 JSON string: the call says to encode it.

### 5. The corpus

`examples/event-inbox` gains an event whose payload carries a `bytes`
field, received as GitHub sends it (standard base64 wrapped at 60
characters) and answered as standard base64, with a test that the
base64url form of the same bytes decodes too.

### 6. A field can be written as base64url (added 2026-11-21)

*(The owner chose a field clause over a Base64Url type, 2026-11-21.)*

Some APIs require base64url for a field and refuse the standard alphabet:
Gmail's `raw`, JWT's three segments (RFC 7515 §2), and WebAuthn's
credential IDs and client data. §1 still decides how every other field
is written. A field that has to be base64url says so where it already
says how it looks in JSON, in its `from json` clause:

```fiig
model GmailMessage {
    id: string
    raw: bytes from json base64url
}
// {"id": "18c2", "raw": "RnJvbTogYUBiLmM"}

model Draft {
    raw: bytes from json "raw_data" base64url
}
```

```
field ::= field-name ':' type [ 'from' 'json' ( string-literal [ 'base64url' ] | 'base64url' ) ]
```

`base64url` is a contextual word, like `from` and `json`, so it isn't
reserved. The member name, if any, comes first: the clause reads as
"from the JSON member `raw_data`, as base64url". The clause is allowed on
model and payload fields, the fields that already take `from json`. A
record field has no JSON form of its own and a variant's `from json` is
its spelling, so neither takes it.

**What it writes.** RFC 4648 §5, `-` and `_`, **with no padding**: what
`toBase64Url()` returns (ADR 0068 §3), so a field and the method can't
disagree. JWT and WebAuthn require base64url without padding and refuse
`=`. Google's APIs read it padded or not. Unpadded is the one form every
consumer that asks for base64url accepts.

**What it reads.** §2's rules, the same as every other `bytes` field:
either alphabet, padded or not, CR and LF skipped. The clause changes
what fiig says, never what it accepts (charter principle 10). A
`base64url` field that receives standard base64 decodes it, and so does a
plain field that receives base64url, as it has since §2. The two
refusals §2 already made stay, because each is a case where guessing
could give the wrong bytes:

- **Both alphabets in one string** (`+` or `/` beside `-` or `_`) is a
  `Mismatch`. Each character could be decoded on its own, but no producer
  writes that string. It is two values joined, or one damaged in transit,
  and decoding it would return bytes nobody sent.
- **A space or tab** is a `Mismatch`, as now. Only CR and LF are
  skipped, because GitHub and MIME wrap lines with them. A space inside
  base64 is most often a `+` that was form-URL-decoded on the way in
  (`a+b` became `a b`). Decoding around the space would drop six bits and
  shift every byte after it.

Errors keep their shape: `JsonError.Mismatch` with the member's path and
`base64 mixes the standard alphabet (+ /) with the URL-safe one (- _)` or
`isn't base64: ...`, and an HTTP request body answers 400 with the
`Mismatch`, as before.

**Which types.** The clause describes the field's own bytes, so the
field's type must be bytes all the way down: `bytes`, `bytes | None`, a
`List` of them, a `Map` whose values are, and any nesting of those
(`List<bytes | None> | None`). On any other type it is **J4**. On
`string` or `int` the clause would say nothing. On a model or enum it
would reach into fields that have their own `from json` clauses, and a
model used in two places would be written two ways. A `Map` written as
`[key, value]` pairs (keys that aren't `string`) writes only its values
as base64url. A key is never the field's content.

**Everywhere fiig writes JSON.** `json.encode`, `json.encodePretty`
(ADR 0073 §9), an HTTP response body, and an enum payload field all go
through the same encoder. `fiig run -json` is a tool's tagged view keyed
by fiig field names (ADR 0072 §7). It already ignores `from json` names,
so it ignores `base64url` too and prints §1's form. A whole `bytes` body
is still raw (§4).

**OpenAPI.** The field's schema is `{"type": "string", "format":
"base64url"}`, the OpenAPI Format Registry's name for RFC 4648 §5. The
generated document is OpenAPI 3.0.3, whose Schema Object has no
`contentEncoding`. OpenAPI 3.1 would write JSON Schema's
`contentEncoding: base64url` instead. The other fields keep `format:
byte`.

#### Precedent

| | How a field picks base64url |
|---|---|
| Go `encoding/json` | It can't: `[]byte` is always standard base64, padded. A JWT library declares its own type with a `MarshalJSON` method (go-jose's `byteBuffer`). |
| Rust, serde | A field attribute: `#[serde_as(as = "Base64<UrlSafe, Unpadded>")]` (serde_with), with the alphabet and padding on the field. |
| Python, Pydantic | A type: `Base64UrlBytes` beside `Base64Bytes`. |
| Java, Jackson | `Base64Variants.MODIFIED_FOR_URL` (unpadded), chosen for the whole `ObjectMapper`. A single field needs a custom serializer. |
| Protocol Buffers' JSON mapping | It can't choose: it writes standard, and reads both alphabets, padded or not. |

serde puts the choice on the field, as fiig does. Pydantic uses a type,
and Jackson a mapper-wide setting. Go and Protobuf offer no way to choose
on the writing side.

#### Alternatives considered for §6

**F. A `Base64Url` type** (Pydantic's `Base64UrlBytes`). Then every
`bytes` method, `crypto` call, and binary pattern needs a conversion in
and out of it, for a difference that only exists in JSON. A type is how
a value behaves, and these bytes behave like any other bytes. Only their
JSON text differs, and the field is where a JSON member's text is
decided (its name, ADR 0073 §7b). Rejected; the owner chose the clause.

**G. One setting for a whole program or adapter** (Jackson's mapper
variant). Gmail's `raw` would change every other `bytes` field the
service sends, including fields a standard-base64 consumer reads.
Rejected.

**H. Keep the workaround**, a `string` field filled with `toBase64Url()`.
The field's type then stops saying it holds bytes, and every reader
decodes by hand. That was open question 1's interim answer, not a
design. Rejected.

**I. Pad by default, or add `base64url padded`.** No attested API needs
padded base64url, and JWT and WebAuthn refuse it. A `padded` word can be
added when an API needs it. Not added now.

**J. `as base64url`**, the open question's sketch. `as` already renames,
in a use line (ADR 0043). One `from json` clause holding both the name
and the form keeps everything about a field's JSON in one place.
Rejected.

**K. A `base64url` field refuses standard base64.** The sender can't do
anything about that refusal, and nothing about the text is ambiguous.
Rejected: charter principle 10, and §2's reason.

## Alternatives considered

**A. An array of numbers** (serde's default). Four times the size of the
bytes in the best case, and no API in the list above sends it. Rejected.

**B. Write base64url.** URL-safe characters matter in URLs and cookies,
not inside a JSON string, and everyone else writes the standard alphabet,
so a fiig service would be the odd one out to every client. Rejected; a
field that must be base64url is open question 1.

**C. Hex.** Twice the size, and unusual for JSON APIs. Rejected.

**D. Read only the standard alphabet** (Go, Jackson, .NET), as ADR 0069
§7's `fromBase64` does. Gmail-style base64url would then be refused for no
reason the receiver can act on. Rejected: ADR 0062, and charter principle
10's lenient about what it receives.

**E. A whole `bytes` body on a JSON route as base64 too**, the same rule
everywhere. It makes `return json.encode(report)` send `"eyJ..."` instead
of the report, the trap §4 closes, and breaks the symmetry with ADR 0077.
Rejected.

## Consequences

- A model can carry binary data through every JSON path: request and
  response bodies, `json.encode`, `json.decode`, and a `JsonValue`.
- `fiig run -json` prints `bytes` as base64 instead of `null`.
- A JSON route can return a JSON document the program built, checked
  before it's sent.
- `docs/known-limitations.md` drops "`bytes` has no JSON form".

## Open questions

1. ~~Writing a field as base64url, for an API that demands it (Gmail's
   `raw`). Today the field is a `string` and the program calls
   `toBase64Url()`; a field-level form, such as `from json "raw" as
   base64url`, can wait for a second need.~~ Done 2026-11-21 as §6:
   `raw: bytes from json base64url`, unpadded, and J4 for any other type.
   JWT and WebAuthn were the second need.

## Implementation notes (2026-11-15)

- `compiler/runtime/json.go`: `bytes` decodes through
  `decodeJSONBase64`, which picks the alphabet from the text and reuses
  ADR 0069 §7's `decodeBase64`; both encoders (`EncodeBodyJSON` and
  `fiig run -json`'s `EncodeJSON`) write `base64.StdEncoding`. The tagged
  encoder had fallen through to `null`.
- `checker.refusal` no longer refuses `bytes`; J1's route test now uses a
  `Map<int, string>` field.
- §4 is in `writeResponse`: a `bytes` body on a JSON route is checked with
  the JSON reader the adapter decodes with (one value, UTF-8) and sent
  as it is, or faults.
- OpenAPI: `schemaFor` writes `bytes` as `format: byte`; a new
  `bodySchema` handles a whole body of `bytes`: any JSON value on a JSON
  route, `format: binary` on any other (ADR 0077 §5's case moved there).
- `examples/event-inbox` gained `document.attached`
  (`models/document_attached.fiig`); `inbox-sender`'s `InboxEvent`
  mirrors the new variant.

## Implementation notes for §6 (2026-11-21)

- `compiler/syntax`: `Field.Base64URL` (and `FlatField`'s, carried
  through embedding by `compiler/types`); `compiler/parser`'s
  `parseFieldJSON` reads the clause, and `compiler/format` prints it
  after the member name.
- `compiler/checker/json.go`: J4 in `checkJsonNames`, beside J2 and W3.
- `compiler/runtime/json.go`: `writeJSONValue` carries a member's
  base64url through `| None`, `List`, and a `Map`'s values, and writes
  `base64.RawURLEncoding`. `decodeJSONBase64` is unchanged: §2 already
  read both alphabets.
- `compiler/adapter/openapi.go`: `fieldSchema` rewrites the field's
  `format: byte` to `base64url`.
