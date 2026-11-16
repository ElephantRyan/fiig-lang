# ADR 0077: A JSON Request Body Read Raw

Status: Accepted and implemented (2026-11-15; see Implementation notes)
Date: 2026-11-15
Related: ADR 0021 (request bodies bind a model, A7), ADR 0069 §5
(`consumes`; raw `string`/`bytes` bodies for other types), ADR 0071 §4
(`Secret`), ADR 0073 (`json.decode`, `JsonError`), ADR 0075 §1 (verifying
a webhook's signature), ADR 0076 §1 (the body limit), charter principle
10.

## Context

ADR 0075 gave fiig `crypto.hmacSha256`, so a program can check a
webhook's signature. Stripe, GitHub, Shopify, and Slack sign the **exact
bytes** of the body they send, and the body is JSON, sent as
`Content-Type: application/json`. A receiver has to have those bytes:
re-encoding a decoded value doesn't reproduce them (member order, spacing,
`1.50` versus `1.5`, escapes).

A fiig route can't give them to it. A route that consumes
`application/json` must bind its body to a model (A7, ADR 0021), which the
adapter decodes; the bytes are gone by the time the workflow runs. ADR
0069 §5 lets a route bind the body raw, as `string` or `bytes`, but only
when it consumes a type other than JSON. `examples/event-inbox` works
around it by declaring its raw route `application/octet-stream`, which a
real sender doesn't send: GitHub's delivery would get a 415.

### Precedent

How a framework lets one route take a JSON body undecoded:

| | How | Cost |
|---|---|---|
| Go `net/http` | the body is always raw; `json.Unmarshal` is the handler's call | none |
| Rust, axum | the handler's parameter type picks it: `body: Bytes` instead of `Json<T>` | none |
| Java, Spring | the parameter type: `@RequestBody String payload` (Stripe's Java sample) or `byte[]` | none |
| Node, Express | a different parser on that route: `express.raw({type: "application/json"})`, as Stripe's docs instruct | easy to get wrong: a global `express.json()` registered first has already consumed the body |
| Elixir, Plug | a custom `:body_reader` on `Plug.Parsers` that keeps a copy | the most-asked webhook question in Phoenix; the parser consumes the body otherwise |

Where the declared parameter type decides (axum, Spring), the raw form
needs no extra step; where a parser runs first (Express, Plug), it's a
known trap. fiig's route already binds by the workflow parameter's type,
so it fits the first pattern.

## Decision

### 1. A JSON route may bind its body as `bytes`

A route that consumes a JSON media type binds its body argument to either
a model, as today, or `bytes`. With `bytes`, the adapter passes the body
as it arrived, and the workflow decodes it with `json.decode` (ADR 0073)
when it chooses:

```fiig
adapter http inboxApi {
    POST "/events/signed" -> receiveSignedEvent(body: bytes, signature: string | None from header "X-Hub-Signature-256")
        consumes "application/json"
        produces "application/json"
    {
        Ok(event) -> 200 event
        Err(error) -> inboxErrorResponse(error) // 401 for BadSignature, 422 for Undecodable
    }
}

workflow receiveSignedEvent(body: bytes, signature: string | None) -> Result<Event, InboxError> {
    if not signatureMatches(body: body, header: signature) {
        return Err(InboxError.BadSignature)
    }
    return readSignedEvent(body) // json.decode, a JsonError becoming InboxError.Undecodable
}
```

The signature is checked before a byte of the body is decoded. (The
helpers are sketched; the corpus example, §6, fixes their exact form.)

### 2. Only `bytes`, not `string`

A non-JSON route may bind `string` (ADR 0069 §5) because a text format
like CSV is parsed as text. A JSON route binding `string` would be what
A7 exists to stop: JSON handed around as text, open to `contains` and
`split` instead of being decoded. `bytes` has no text operations: a
program can hash it, store it, or decode it, and getting text from it
takes an explicit `body.utf8()`, which a reader sees.
A signature is over bytes, and `json.decode` takes `bytes`, so nothing a
webhook needs is lost.

### 3. The adapter doesn't parse it

A JSON route binding `bytes` gets no 400 for a body that isn't JSON. The
workflow decides what an undecodable body is answered with, through
`JsonError` (ADR 0073), as it does for any `json.decode`. Two reasons:

- **The program checks the signature first.** A forged request with a
  junk body should be answered as forged (401), not as malformed (400);
  the order is the program's to choose.
- **One read, not two.** Parsing for a 400 and decoding again in the
  workflow does the work twice, on every delivery.

What stays: `Content-Type` must match the route's `consumes` (the 415 of
ADR 0069 §5), and ADR 0076 §1's body limit applies.

### 4. A7 narrows again

A7 reads: a body bound to a JSON route is a model or `bytes`; a body
bound to any other route is `string` or `bytes`. `bytes | None`, `string`,
and other primitives on a JSON route stay A7, with a message that names
`bytes` as the raw form.

### 5. OpenAPI says JSON, with no schema

The generated document lists the route's body as `application/json` with
an empty schema (`{}`, any JSON value), not `bytes`'
`{"type": "string", "format": "binary"}`, which is wrong for JSON. Naming
the type the workflow decodes into is open question 1.

### 6. The corpus

`examples/event-inbox`'s raw route moves from `application/octet-stream`
to `application/json`, checks an `X-Hub-Signature-256` HMAC against a
`Secret` from its config (ADR 0071), and answers 401 for a bad signature.
`examples/inbox-sender` signs what it sends, so the two dogfood each
other, as ADR 0074's did.

## Alternatives considered

**A. Bind both: the raw bytes and the decoded model.** A second body
argument, or a prelude `RawBody<T>` holding both. It decodes input the
program hasn't authenticated yet, gives a route two body arguments where
ADR 0021 has one, and the workflow can already get the model with one
`json.decode` call. Rejected.

**B. Check JSON syntax in the adapter, then pass the bytes.** A 400
before the workflow runs, as a model route gets. But it parses twice, and
answers an unsigned junk body 400 before the program can answer it 401.
Rejected; §3.

**C. A route-level marker instead of the type** (`consumes
"application/json" raw`). It says the same thing the parameter's type
already says, and the two could disagree. Rejected: the type decides, as
in axum and Spring.

**D. Leave it, and have senders use octet-stream.** Real senders don't
let the receiver choose their `Content-Type`. Rejected.

## Consequences

- A fiig service can verify Stripe, GitHub, Shopify, and Slack webhooks
  as they're actually sent.
- A JSON route binding `bytes` answers a malformed body however its
  workflow says to; the adapter's 400 for invalid JSON applies only to
  routes that bind a model.
- `docs/known-limitations.md` drops "A JSON request body can't be read
  raw".

## Open questions

1. Whether a raw JSON route should name the type it decodes into, for the
   OpenAPI document (`consumes "application/json" as Envelope`), and
   whether the checker should then hold the workflow's `json.decode` to
   it.

## Implementation notes (2026-11-15)

- `checker.checkBodyBinding` accepts `bytes` on a JSON route; A7's
  message for a primitive there now names `bytes` as the raw form.
- The adapter decides at the route: a JSON route whose body parameter is
  `bytes` skips the parse and passes the body through `decodeBody`'s raw
  path. The 415 and ADR 0076's body limit run first, unchanged.
- §5's schema is `{"description": "any JSON value"}`, the schema the
  generator already writes for `JsonValue`, rather than a bare `{}`: both
  mean any JSON, and one spelling reads better than two.
- `examples/event-inbox`'s `/events/raw` (octet-stream) became
  `/events/signed` (JSON, `X-Hub-Signature-256`), with `config Inbox`, an
  `InboxError` enum, and a conversion answering 401 or 422.
  `examples/inbox-sender` signs with `config Outbox` and sends there.
  Dogfooded with `fiig serve` and `fiig run`: the same secret is
  accepted, a wrong one is `SendError.Rejected(401)`, an unsigned
  request is 401, and octet-stream is 415.
