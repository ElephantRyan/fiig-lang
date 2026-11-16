# ADR 0078: The JSON Form of `bytes`

Status: Accepted and implemented (2026-11-15; see Implementation notes)
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

1. Writing a field as base64url, for an API that demands it (Gmail's
   `raw`). Today the field is a `string` and the program calls
   `toBase64Url()`; a field-level form, such as `from json "raw" as
   base64url`, can wait for a second need.

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
