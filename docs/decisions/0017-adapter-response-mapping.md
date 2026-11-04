# ADR 0017: Explicit Adapter Response Mapping, No Dedicated Reuse Syntax

Status: Accepted
Date: 2026-09-22

## Context

Two related gaps, both about the adapter boundary and neither addressed by
any prior decision: how a workflow's `Result` becomes a protocol-specific
response (surfaced during ADR 0006), and how external/untyped input is
decoded before entering domain code (bug-class catalog entry A7).

## Decision

Every adapter route carries an explicit mapping from its workflow's `Result`
to a protocol response — a `match`-shaped block attached to the route,
mapping `Ok`/`Err` cases to a status/exit code and optional body:

```
adapter http customerHttpApi {
    GET "/customers/{id}" -> readCustomer(id: CustomerId) {
        Ok(customer) -> 200 customer
        Err(CustomerError.NotFound(_)) -> 404
        Err(error) -> 500 error
    }
}
```

**Reuse of a mapping across routes is not a language feature.** There is no
`using`-style syntax to reference a shared interpreter. A shared mapping is
just an ordinary `conversion` declaration, called like any other reusable
declaration from a route's `Err` arm:

```
conversion customerErrorToHttpResponse(error: CustomerError) -> HttpResponse {
    match error {
        CustomerError.NotFound(_) -> HttpResponse(status: 404)
        CustomerError.EmailAlreadyInUse(_) -> HttpResponse(status: 409)
        error -> HttpResponse(status: 500, body: error)
    }
}

adapter http customerHttpApi {
    GET "/customers/{id}" -> readCustomer(id: CustomerId) {
        Ok(customer) -> 200 customer
        Err(error) -> customerErrorToHttpResponse(error)
    }
}
```

A route that wants different behavior just doesn't call the shared
conversion and writes its own mapping inline instead — the opt-out is
ordinary composition, not a special mechanism.

**Inbound decoding (A7):** a path/route parameter binding directly into a
`value` type (e.g. `{id}` into `CustomerId`) already *is* the decode step by
construction — `value` types are thin nominal wrappers with no invariants of
their own, so there's nothing further to validate at that layer. Richer
validation already belongs in an explicitly-called `rule`
(`validateNewCustomer` is the existing pattern). Structural decoding of a
whole request body into a multi-field model (e.g. a POST body into
`NewCustomer`) is real and distinct, but left as a deferred implementation
detail — not blocking this decision.

## Rationale

This deliberately chooses convention over a language feature: the pattern
you described from experience (a reusable error interpreter, with the
ability for one call site to opt out of it) doesn't need dedicated syntax to
exist — `conversion` declarations already are ordinary, callable, reusable
things. Adding a `using` keyword would grow the language's surface area to
express something composition of existing primitives already provides, which
runs against §9's guardrail against building mechanisms before they're
proven necessary. Good structure here is expected to emerge from engineers
choosing to factor out a shared conversion, the same way anyone would factor
out any other repeated logic — not from the compiler knowing about "response
interpreters" as their own concept.

## Consequences

- `docs/specification/grammar.md`'s adapter-route productions need extending
  so a route can carry a trailing `match`-shaped mapping block. This is the
  only new syntax this decision requires.
- `HttpResponse` (and an analogous CLI response shape) is an implied new
  type this introduces — whether it's a genuine built-in or just an ordinary
  model applications define for themselves isn't resolved here, flagged as
  follow-up. Settled by ADR 0069 §3 (2026-11-04): a builtin generic
  `HttpResponse<B>` in the prelude, checked as a type, with optional
  `headers` and `cookies`.
- The bug-class catalog's A7 entry can be updated: path-parameter decoding is
  now settled (decode-by-construction via `value` types); only structural
  body decoding remains genuinely open.
- Worth adding a real example once the route-mapping grammar is nailed down,
  grounding this the way every other decision here has been.
