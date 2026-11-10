# ADR 0073: JSON in Fiig Code

Status: Accepted and implemented (2026-11-09; see Implementation notes)
Date: 2026-11-08
Related: ADR 0006 (`?` propagates unchanged), ADR 0021 (request-body
binding), ADR 0049 (prelude `math` group, K2), ADR 0052 (structural `==`),
ADR 0057 (`[]` typed by context), ADR 0062 (JSON numbers; `JsonValue`),
ADR 0065 (`Map`), ADR 0069 (`from header`), ADR 0071 (`from env`, `Secret`), ADR 0072 (enum and `Result`
shapes), charter principle 10.

## Context

A fiig program can read JSON only when an HTTP adapter decodes a request
body into a route's parameter. Any other JSON is out of reach:

- a webhook whose signature has to be checked over the raw bytes before
  the body is trusted, so it is bound as `bytes` (ADR 0069 §5);
- a reply from another service, which the HTTP client package (alpha
  item 8) will hand back as `bytes`;
- a JSON column read from a database, or a file.

ADR 0062 decided what a JSON number is and defined `JsonValue`, the
lossless untyped tree, and left typed decoding to "a separate ADR". ADR
0072 decided the shapes enums and `Result` take. This ADR is the library
that uses both.

### Precedent

```java
// Java (Jackson): the type is passed as a value
Event e = mapper.readValue(body, Event.class);
```
```go
// Go: decode into a variable declared first
var e Event
err := json.Unmarshal(body, &e)
```
```elixir
# Elixir (Jason): untyped only; the result is maps, read by pattern matching
{:ok, %{"type" => type}} = Jason.decode(body)
```
```rust
// Rust (serde): the type comes from context
let e: Event = serde_json::from_slice(&body)?;
```

Java's form needs types to be values, which fiig's aren't. Go's needs a
variable to be filled in place, and fiig's values don't change in place
(ADR 0023). Elixir has no typed decoding, so every caller reads a tree.
Rust's form fits fiig, which already takes a type from context for
`Ok(...)`, `Err(...)`, and `[]` (ADR 0057 §2).

## Decision

### 1. `json` is a prelude group, like `math`

`json.decode` and `json.encode` are members of a prelude group named
`json`, available in every file without an import, as `math` is (ADR
0049). Both are pure, so rules, conversions, and formulas can call them:
parsing does no I/O. A declaration named `json` is K2, as one named `math`
is.

### 2. `json.decode` takes its type from context

```fiig
rule parseEvent(body: bytes) -> Result<Event, JsonError> {
    event: Event = json.decode(body)?
    return Ok(event)
}
```

`json.decode(text)` returns `Result<T, JsonError>`, where `T` is what the
context expects: a typed local (`event: Event = ...`), a declared return
type (`-> Result<Event, JsonError>`), a parameter, or a field. With no
expected type it is an error, reported as A12 is for `[]`:
`json.decode(body)` alone can't be decoded into anything. The fix is the
same as A12's, a typed declaration.

Its argument is `string`, `bytes`, or a `JsonValue` (§4). `bytes` must be
UTF-8, as RFC 8259 §8.1 requires of JSON exchanged between systems;
invalid UTF-8 is a `Syntax` error, not something replaced with U+FFFD.

### 3. One decoder, with the adapter's rules

`json.decode` and an HTTP request body are the same decoder, so moving
JSON from a route parameter to a `json.decode` call changes nothing a
client can see (ADR 0062 §6's goal):

- numbers follow ADR 0062: `3.0` and `1e3` are an `int`, `2.5` is an
  error, a `decimal` keeps its scale, a `float` rounds to the nearest
  binary64;
- a duplicate member name is an error (ADR 0062 §1);
- a model's missing `T | None` field is `None`, and any other missing
  field is an error;
- members a model or a variant doesn't declare are ignored (charter
  principle 10);
- enums and `Result` take ADR 0072's shapes;
- trailing content after the value, other than whitespace, is an error.

### 4. `JsonValue` is the untyped tree, and also a decodable type

```
enum JsonValue {
    Null
    Bool(value: bool)
    Number(text: string)
    Text(value: string)
    Array(items: List<JsonValue>)
    Object(members: Map<string, JsonValue>)
}
```

`JsonValue` is ADR 0062 §1's enum, made a prelude type. Decoding into it
keeps everything, numbers as their text and members in the order they
arrived:

```fiig
rule eventType(body: bytes) -> Result<string, JsonError> {
    tree: JsonValue = json.decode(body)?
    match tree {
        JsonValue.Object(members) -> {
            match members.atOrNone("type") {
                JsonValue.Text(value) -> { return Ok(value) }
                None -> { return Err(JsonError.Mismatch(path: "type", reason: "missing")) }
                _ -> { return Err(JsonError.Mismatch(path: "type", reason: "expected a string")) }
            }
        }
        _ -> { return Err(JsonError.Mismatch(path: "", reason: "expected an object")) }
    }
}
```

A model field may be a `JsonValue`, to carry part of a document through
without deciding its shape, as Go's `json.RawMessage` and
`serde_json::Value` are used:

```fiig
model Event {
    type: string
    data: JsonValue
}
```

**`json.decode` also takes a `JsonValue`**, so a subtree is turned into a
typed value the same way bytes are. This is how ADR 0062 §4's conversions
are invoked, which that ADR left to this one:

```fiig
amount: int = json.decode(event.data)?
```

There is one set of rules, so `Number("3.0")` decodes to an `int` here
exactly as `3.0` does in a request body.

`JsonValue` is sent as the JSON it holds, not in ADR 0072's enum shape: a
`JsonValue.Text(value: "a")` encodes as `"a"`, not
`{"variant": "Text", "value": "a"}`. It is the one enum that does, because
its variants are the JSON types themselves.

### 5. `JsonError` says where and why

```
enum JsonError {
    Syntax(offset: int, reason: string)
    Mismatch(path: string, reason: string)
}
```

- `Syntax` is input that isn't JSON: `offset` is the byte offset of the
  problem, and `reason` says what was expected (`expected ',' or '}'`,
  `invalid UTF-8`, `duplicate name "id"`).
- `Mismatch` is JSON that doesn't fit the type: `path` is where, written
  as `data.items[2].amount` (`""` for the whole value), and `reason` is the
  message an HTTP request body's 400 gives today
  (`2.5 has a fractional part`).

A duplicate name is `Syntax` because the document is ambiguous whatever
type it is read as (ADR 0062 §1). The adapter's 400 becomes these errors'
text, so the two report the same thing in the same words.

### 6. `json.encode` returns `bytes`

```fiig
payload: bytes = json.encode(event)
```

It writes any encodable value in the shapes a response body uses (ADR
0072): a model as an object of its fields, enums and `Result` as ADR 0072
sends them, `JsonValue` as the JSON it holds. Member order is the model's
declared field order, and a `Map`'s insertion order. A `Number`'s text is
written verbatim (ADR 0062 §5), and invalid text is a fault, as that ADR
decided. No other value can fail to encode, so the return type is `bytes`,
not a `Result`.

`bytes` rather than `string` because the encoded value is usually sent
(a request body, a socket write, a signature input); `.utf8()` turns it
into a `string` where one is wanted.

The adapter's response bodies currently write a model's members in name
order. They move to declared order with this change, so a response and
`json.encode` produce the same bytes.

### 7. Member names fiig can't spell

A model decodes a member only through a field of the same name, and the
names third-party APIs use don't all fit. Tried against the checker:

| A producer sends | As a field | Today |
| --- | --- | --- |
| `"created_at"` (Stripe, GitHub) | `created_at: string` | works: `_` is legal after the first letter |
| `"InstanceId"` (AWS) | `InstanceId: string` | works |
| `"limit"`, `"in"`, `"for"`, `"use"`, `"as"`, `"match"`, `"config"` | `limit: int` | parse error: a keyword |
| `"content-type"`, `"@type"`, `"$ref"`, `"2fa"` | none | not an identifier |

`limit` alone is in the pagination of most APIs. Precedent: Go needs a
struct tag on nearly every field anyway (``Limit int `json:"limit"` ``),
since an exported field must be capitalized; Jackson has
`@JsonProperty("content-type")`; serde has `#[serde(rename = "type")]`,
needed because `type` is a Rust keyword, and raw identifiers (`r#type`);
Elixir's Jason decodes to string-keyed maps, so any name is read as
`body["content-type"]`, and Ecto maps a field to a differently named
column with `source:`.

Two problems, two fixes.

**7a. A keyword may be a field name.** A field's name appears only where
no keyword can mean anything else: in its declaration, after a `.`, and as
a named argument:

```fiig
model Page {
    limit: int
    in: string
}

page.limit
Page(limit: 10, in: "inbox")
copy(page, limit: 20)
```

This holds for model fields and enum payload fields. Parameters, locals,
and declaration names stay unable to be keywords, since they are read bare
in expressions, where `in` or `match` already means something. Kotlin's
soft keywords and Python's `match` and `case` work the same way. It is
right without JSON too: `limit` is an ordinary name for a field. One
position needs care: in a `concurrent` header, `limit:` stays the clause
(ADR 0016), as no named argument can appear there.

**7b. `from json "name"` names the member a field reads and writes.** For
names that aren't identifiers, the field says the member it maps to,
written as routes already say `from header "Authorization"` (ADR 0069)
and config says `from env "NAME"` (ADR 0071):

```fiig
model Upload {
    contentType: string from json "content-type"
    schema: string from json "$schema"
}
```

- It applies wherever the value meets JSON: `json.decode`, `json.encode`,
  request and response bodies, and OpenAPI, which lists the member's JSON
  name. In fiig code the field is `upload.contentType`.
- It is allowed on model fields and enum payload fields, and the name is
  any JSON string.
- Two fields of one model, or of one variant, reading the same member
  (`a: string from json "x"` beside `x: string`) is **J2**, at the second.
- W3 (ADR 0072 §3) is about the member's JSON name: a payload field
  `from json "variant"` is W3, and a payload field named `variant` with a
  `from json` name of its own is not.

**Rejected: a naming policy for a whole model** (Jackson's
`PropertyNamingStrategies.SNAKE_CASE`, serde's `rename_all`). It makes the
field `createdAt` silently mean `"created_at"`, so a reader looking for the
member `created_at` finds nothing in the source (charter principle 10:
strict about what the program says). `created_at` is already a legal field
name, so the program can say the real one.

**Rejected: escaped identifiers** (Rust's `r#limit`, Kotlin's backticks).
They fix keywords but not `content-type`, and 7a fixes keywords without
new syntax.

### 8. Types that can't be decoded or encoded are refused

The checker refuses `json.decode` into, or `json.encode` of, a type that
includes:

- `Secret` (ADR 0071 §4): decoding one would make a secret from data, and
  encoding one would send it;
- `Connection` (ADR 0060), a capability, or an interface value;
- `BatchResult`, which only a `concurrent` block produces;
- a `Map` whose keys aren't `string`, for `decode` only, since a JSON
  object's names are strings. (Encoding one as `[key, value]` pairs, as
  response bodies already do, stays.)

The new code is **J1**, reported at the call with the part of the type
that can't be decoded or encoded: `json.decode into Session: field token
is a Secret`. A request body parameter of such a type is J1 too, at the
route, where today it is found only at run time.

### 9. Scope

In: `json.decode`, `json.encode`, `JsonValue`, `JsonError`, keywords as
field names, `from json`, J1, J2, and moving the adapter's decoding and
encoding onto them.

Out:

- **A decoder written in fiig.** Typed decoding needs a declaration's
  fields, and fiig code can't inspect a type; a fiig-written `JsonValue`
  parser beside the native one would be a second decoder that can drift
  from the adapter's. Revisit if fiig gains a way to write a decoder over
  types.
- **Streaming** large documents. A decode reads the whole input.
- **Pretty-printed output**; `json.encode` writes no whitespace.

## Alternatives considered

- **The type as an argument** (`json.decode(body, as: Event)`, Java's
  form). Types aren't values in fiig, and adding a type-argument syntax for
  one function is more grammar than context typing, which fiig already
  has.
- **Untyped decoding only** (Elixir's form). Every caller would match its
  way through a `JsonValue`, and an LLM would have to write that code
  correctly for every shape; a declared model says the same thing once.
- **`json.parse` for the tree and `json.decode` for types.** Two names for
  one decoder; with `JsonValue` as an ordinary decodable type, one name
  covers both.
- **`string` from `json.encode`.** Every sender would call `.utf8()` back
  to bytes; the decision follows where the value usually goes.

## Consequences

- Fiig code can read and write JSON from any source, not only route
  bodies, with the same rules as the adapter.
- `JsonValue`, `JsonError`, and `json` become prelude names, so a
  declaration with one of those names is K2. No corpus declaration uses
  them.
- Two new bug classes: J1, which also moves the adapter's unsupported
  body types from a run-time failure to a check, and J2.
- The parser accepts keywords as field names (§7a), which also lets a
  model say `limit` where it means a limit.
- Response bodies' member order changes from name order to declared order.
- ADR 0062 §1-§5 and §7 are implemented by this ADR's build.

## Open questions

None. Renaming fields, the first draft's open question, is §7.

## Implementation notes (2026-11-09)

- **One reader.** `runtime/jsonread.go` is a small RFC 8259 reader written
  for this: exact byte offsets for `Syntax`, objects that keep their
  members' order (so a `Map` and a `JsonValue` decode in arrival order, as
  ADR 0062 §1 wants), invalid UTF-8 refused before anything is read,
  duplicate names refused at the second one, and nesting capped at 10,000
  levels, encoding/json's limit, so a body of a million `[` can't exhaust
  memory. A `\u` escape of a lone surrogate becomes U+FFFD, as in
  encoding/json. Tooling's `runtime.ParseJSON` reads through it too.
- **One decoder.** `runtime.DecodeJSON` reports a `*JsonMismatchError`
  with a path; the adapter's 400 is its text, prefixed with the body
  parameter (`body "hook": items[1]: 2.5 has a fractional part`).
  `json.decode` resolves each field's type in the package that declares
  the model, so a dependency's model decodes by that dependency's names.
  A path is relative to what was decoded: decoding `envelope.data` reports
  `amount`, not `data.amount`.
- **One encoder.** `runtime.EncodeBodyJSON` writes bytes directly, for
  `json.encode` and response bodies alike: declared field order (models
  now carry their declaration, `Record.Decl`), `from json` names, `<`, `>`
  and `&` unescaped, and U+2028 and U+2029 escaped. A `JsonValue.Number`
  built with text that isn't a number faults, whether encoded or decoded
  (ADR 0062 §5). `fiig run -json` keeps its tagged form.
- **Context typing.** The checker records the type a `json.decode` call
  decodes into on the call itself (`syntax.CallExpr.DecodeInto`), and the
  runtime reads it there; a program that wasn't checked faults at the call
  rather than guessing. The positions that give a type are every position
  `checkExpected` sees: a typed local, a `return`, an argument, a field, and
  `Ok(...)`. `json.decode(x)?` takes the expected type itself;
  `json.decode(x)` takes the `T` of an expected `Result<T, JsonError>`.
- **J1 adds `bytes`** to §8's list. Before this ADR a `bytes` field in a
  JSON body was refused when decoding and sent as `null` when encoding;
  choosing base64 or an array of numbers is a decision of its own, so for
  now it is refused at check time (`docs/known-limitations.md`). At a
  route, a `Secret` is left to S1, which already reports it at the
  workflow's parameter.
- **Anonymous record types** decode too, by their field names, since a
  typed local may have one.
- `examples/event-inbox` is the corpus example, dogfooded with
  `fiig serve`. Dogfooding found that a JSON webhook can't be bound raw: a
  route that consumes `application/json` must bind a model (A7), so the
  raw route consumes `application/octet-stream`
  (`docs/known-limitations.md`).
