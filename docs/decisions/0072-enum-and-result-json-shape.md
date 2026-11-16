# ADR 0072: The JSON Shape of an Enum and a `Result`

Status: Accepted and implemented (2026-11-08; see Implementation notes)
Date: 2026-11-08
Related: ADR 0017 (adapter response mapping), ADR 0021 (request-body
binding), ADR 0040 (`enum` as a general sum type), ADR 0046 (headed
enums), ADR 0062 (JSON numbers; its library is alpha item 7), ADR 0069
(`produces`, `consumes`, W1/W2), charter principle 10.

## Context

An HTTP body is JSON, and fiig has no decided JSON form for an enum. What
happens today:

- **Responses** send an enum in the tagged form `fiig run -json` prints,
  `{"$enum": "Region", "variant": "US", "fields": []}`, and a `Result` as
  `{"$result": "Ok", "value": ...}`. That is a debugging view of fiig
  values, not something a client should code against. Models are already
  plain objects (`EncodeBodyJSON`).
- **Requests** can't carry an enum at all. `DecodeJSON` has no enum case,
  so a request model with a `Region` field fails every request with
  "cannot be decoded from a plain JSON value", and nothing reports it
  before the program runs.
- **OpenAPI** describes every enum as
  `{"type": "object", "description": "... (a tagged enum variant)"}`,
  which tells a client nothing.

`docs/known-limitations.md` lists the response half; the request half was
found while drafting this ADR.

### Precedent

The examples use these declarations:

```fiig
enum Region { US  EU  APAC }
enum OrderError {
    NotFound(id: string)
    PaymentDeclined(reason: string)
    OutOfStock
}
```

**Rust (serde)** offers four shapes for each type. The default
("externally tagged") sends `NotFound { id }` as
`{"NotFound": {"id": "o1"}}` and `OutOfStock` as `"OutOfStock"`.
`#[serde(tag = "type")]` ("internally tagged") sends
`{"type": "NotFound", "id": "o1"}` and `{"type": "OutOfStock"}`. A
payload-free enum is `"US"` either way, and `Result` is
`{"Ok": 5}` / `{"Err": ...}`. axum doesn't send a handler's `Result`: the
error type implements `IntoResponse` and chooses a status.

**Java (Jackson)** sends `enum Region` as `"US"`. A sealed interface of
records with no annotations loses the tag (`{"id": "o1"}`) and can't be
decoded back. With `@JsonTypeInfo(use = NAME, property = "type")` and
`@JsonSubTypes` it is `{"type": "NotFound", "id": "o1"}`, the internally
tagged shape. Spring maps errors to statuses in exception handlers.

**Go** has no sum types. An `iota` enum sends `0`, `1`, `2`, so reordering
the constants changes the API; the usual fix is `type Region string`,
which sends `"US"`. A payload sum is an interface with a hand-written
`MarshalJSON` adding `"type"`, decoded in two passes through
`json.RawMessage`. `json.Marshal(errors.New("x"))` is `{}`.

**Elixir (Jason, Phoenix)** sends atoms as strings (`:us` is `"us"`).
`Jason.encode!({:ok, 1})` raises: tuples have no JSON form, so a result
tuple can't reach the wire by accident. Structs need
`@derive Jason.Encoder` and carry no tag. Phoenix's `FallbackController`
maps `{:error, :not_found}` to a 404 by hand.

**OpenAPI 3** writes a payload-free enum as
`{"type": "string", "enum": ["US", "EU", "APAC"]}`, and a payload sum as
`oneOf` with a `discriminator`, whose `propertyName` must be a property
inside each object. **GraphQL** sends enums as strings and puts
`__typename` inside a union member's object. **Protobuf's JSON mapping**
sends enums as name strings and a `oneof` keyed by the field that is set,
the externally tagged shape.

Every source agrees on the first case: an enum of names is a string. For
payload sums, the internally tagged shape is the one OpenAPI's
discriminator describes, GraphQL uses, annotated Jackson writes, and
TypeScript clients narrow on (`switch (e.variant)`). The externally tagged
shape is the zero-configuration default in serde and protobuf, but a
client has to test `typeof` and then probe keys, and OpenAPI can only
describe it as a `oneOf` of single-key objects.

## Decision

### 1. An enum with no payloads is a string

An enum none of whose variants carries a payload is sent and received as
its variant's name:

```json
"US"
```

This covers headed enums (ADR 0046), whose variants never carry a payload:
`DeductionSource.Acme` is `"Acme"`. Header values are the program's own
constants and are not sent; a client that holds a key value gets the
variant through the enum's lookup, `DeductionSource(code: raw)`, in the
program.

### 2. An enum with any payload is an object naming its variant

If any variant of an enum carries a payload, **every** variant is sent as
a JSON object with a `variant` member naming it, and the payload's fields
beside it:

```json
{"variant": "NotFound", "id": "o1"}
{"variant": "OutOfStock"}
```

Nesting is the same rule applied again:

```json
{"variant": "Order", "error": {"variant": "NotFound", "id": "o1"}}
```

The name is `variant` because that is fiig's word for what it holds (ADR
0040). `kind` was rejected because fiig already uses it for the kinds of
declaration (model, rule, activity, and the rest), and `type` because
domain models often have a field named `type`. Payload fields are always
named in fiig, so the internally tagged shape works for every enum; serde
can't use it for tuple variants, and fiig has none.

One type has one JSON shape. A payload-free variant of a payload enum is
`{"variant": "OutOfStock"}`, not `"OutOfStock"`, so a client never has to
test whether it got a string or an object.

**What changes the contract.** Adding a variant never changes how the
other variants are sent: adding `CA` to `Region` adds `"CA"`, and adding
`Expired(at: string)` to `OrderError` adds one more `variant` value. The
one change that does is giving the first payload to an enum that had none,
which turns `"US"` into `{"variant": "US"}`. That is a breaking change to
every client, and the bump check (ADR 0033) reports it as one.

### 3. A payload field may not be named `variant`

`NotFound(variant: string)` would send two `variant` members. A payload
field named `variant` is **W3**, reported at the field. The check is on
the declaration, not only on enums an adapter happens to reach, because
item 7's JSON library will encode any value, and an enum that is safe
today shouldn't become an error when a route first sends it.

### 4. `Result` is the two-variant enum it is

A `Result` inside a body (a `BatchResult` item, or a model field) is sent
as the enum with payloads it is, with fields named as the corpus names its
patterns:

```json
{"variant": "Ok", "value": {"id": "c1", "name": "Ada"}}
{"variant": "Err", "error": {"variant": "NotFound", "id": "c2"}}
```

A route's top-level `Result` is unchanged: its arms map `Ok` and `Err` to
statuses and bodies (ADR 0017), as axum, Phoenix, and Spring each do by
hand. A `BatchResult` keeps its current shape, a list of
`{"key": ..., "outcome": ...}`, with each outcome in this form.

Refusing a nested `Result` at check time, as Elixir refuses tuples, was
the alternative. A batch response needs a per-item outcome, and the
refusal would leave it nowhere to go but a hand-written wrapper enum with
the same two variants, so one rule is used instead.

### 5. Decoding reads the same shapes

A request body (and item 7's library) decodes exactly what §1-§4 send:

- a payload-free enum from a string naming a variant;
- a payload enum from an object whose `variant` member names a variant,
  its payload fields decoded as a model's are: a missing field that isn't
  optional is an error, and a missing `T | None` field is `None`.

Names are matched exactly, case included. An unknown variant name, a
missing or non-string `variant`, or a string for a payload enum is a 400
that names the expected variants:

```
order.status: "Shipped" is not a variant of OrderStatus (Pending, Paid, Cancelled)
```

Members the enum doesn't declare are ignored, as unknown members of a
model already are (charter principle 10).

### 6. OpenAPI describes the shapes

- A payload-free enum: `{"type": "string", "enum": ["US", "EU", "APAC"]}`.
- A payload enum: a `oneOf` with one object schema per variant, each
  requiring `variant` with that variant's name as its only `enum` value
  and listing its payload fields as a model's are, plus
  `"discriminator": {"propertyName": "variant"}`.
- A `Result` inside a body: the same, for `Ok` with `value` and `Err` with
  `error`.

### 7. `fiig run -json` keeps its tagged form

`fiig run -json` prints a tool's view of fiig values, with `$type` on
models and `$enum` on enums, so its output says what each value is. It is
not a wire contract, and it keeps that form. The HTTP shape is the one
item 7's library uses.

## Alternatives considered

- **Keyed by variant, as serde's default and protobuf send it**
  (`{"NotFound": {"id": "o1"}}`, `"OutOfStock"`). It never collides with a
  field name, and adding a payload never changes a sibling's shape. It was
  rejected because one type mixes strings and objects, a client probes keys
  to find the variant, and OpenAPI can't attach a discriminator to it.
- **The tag beside a `value` object** (serde's "adjacently tagged":
  `{"variant": "NotFound", "value": {"id": "o1"}}`). It can't collide
  either, but every level of nesting adds a `.value` to read through, and
  fiig's named payload fields make the collision it avoids a single
  checkable rule (§3).
- **Payload-free variants as strings even inside a payload enum.** It
  keeps `"OutOfStock"` stable if a sibling gains a payload, at the cost of
  one type having two JSON shapes. Rejected for the same reason as the
  keyed form.
- **`$variant` as the member name.** It can't collide with a fiig field,
  since `$` can't start one, but it looks foreign to every client that
  isn't fiig, and §3 costs one field name.

## Consequences

- A request body can carry an enum, which it can't today.
- Clients get one predictable shape per type and an OpenAPI document that
  generators turn into string unions and discriminated unions.
- Response bodies that contain an enum change shape. No corpus adapter
  sends one in a body today (each maps errors to a status and a message),
  so nothing in the corpus migrates.
- `variant` is unavailable as a payload field name.
- The bump check gains one more breaking change to recognize: an enum
  gaining its first payload.

## Open questions

1. **A union of two enums with a variant name in common**, such as
   `OrderError | CustomerError` with `NotFound` in both. `{"variant":
   "NotFound", ...}` doesn't say which. Decoding tries members in order
   today, as it does for a union of models. Should the checker refuse such
   a union in a body, or should the shape name the enum?
2. **An enum in a path segment or query value**, such as
   `GET "/regions/{region}"` with `region: Region`. The natural form is the
   variant name, as in §1, but route arguments bind only primitives today.
   Resolved by ADR 0080: by its text form, exactly.

## Implementation notes (2026-11-08)

- `runtime.EnumValue` carries its `*syntax.EnumDecl`, so the encoder knows
  whether the enum has payloads and what its fields are called. Every
  constructor sets it, including `SocketError` from the socket provider.
- `EncodeBodyJSON` writes §1, §2, and §4; `EncodeJSON` (`fiig run -json`)
  is unchanged (§7). A model's members still come out in name order, as
  before; a payload enum's come out as declared, after `variant`.
- `DecodeJSON` gained enums and `Result` (§5). A headed enum decoded from
  JSON carries only its variant's name; its header constants are evaluated
  on the first field read, in the enum's own package's scope
  (`Interp.headerConsts`), so `request.source.code` works on a decoded
  value.
- W3 is checked in `checker.go` beside N2, on every enum.
- OpenAPI (§6) writes OpenAPI 3.0.3, which has no `const`, so each
  variant's `variant` property is a one-value `enum`. `BatchResult` is now
  described as a list of `{key, outcome}`, which it always was on the
  wire; before, its schema was a `oneOf` of the key and success types.
- The bump check reports an enum's first payload as Major
  (`registry.variantChanges`).
- `examples/shipping-quotes` is the corpus example, dogfooded with
  `fiig serve`: a `Speed` string and a `Destination` object in the
  request, both echoed back, and `QuoteError` as the 422 body, with 400s
  naming the variants for a wrong case, an unknown variant, a missing
  payload field, and a string where an object belongs.
