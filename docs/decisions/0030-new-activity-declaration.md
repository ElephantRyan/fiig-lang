# ADR 0030: New `Activity` Declaration — Pure Coordination of Rules and Conversions

Status: Accepted
Date: 2026-10-08

## Context

ADR 0029 removed `Rule`'s ability to call other rules, which reopens a
real need: composing several small, independently-tested rules/conversions
into one named, reusable, registry-discoverable decision — e.g.
`validateCustomer` calling `validatePhoneNumber` and `validateEmail` — has
nowhere to live if `Rule` is a pure leaf and the only other kinds
(`Step`/`Gateway`, `Workflow`) either require a capability or are reserved
for top-level application composition.

A fourth kind for exactly this ("pure composition, no capability") was
considered and rejected once already, during the ADR 0028 discussion, on
the reasoning that composition-with-capability and composition-without
didn't need separating. That reasoning didn't survive contact with a
second concrete case (see ADR 0031): the capability-bound kind turned out
to be the overwhelmingly common, load-bearing case (every existing example
has exactly one capability), not a sometimes-present attribute of a more
general coordinator. Once that was clear, "coordinates rules/conversions,
capability optional" was the wrong shape for *either* case — cleaner to
split along the same axis `Rule`/`Conversion` already split along
(structural narrowing, not a flag) than to leave one kind with an optional
property.

## Decision

A new declaration kind, `Activity`: coordinates `Rule` and `Conversion`
calls into a reusable, named unit. Never touches a capability — no
`requires` clause exists in its grammar at all, not even an optional one.

```
activity validateCustomer(email: EmailAddress, phone: PhoneNumber) -> Result<None, CustomerError> {
    validateEmail(email)?
    validatePhoneNumber(phone)?
    return Ok(None)
}
```

Composition matrix: `Activity` may call `Rule`/`Conversion`, same as a
`Gateway` (ADR 0031) may. `Activity` may not call another `Activity`, a
`Gateway`, or a `Workflow` — coordinating multiple of *those* is a
`Workflow`'s job, the same "may not coordinate" restriction a `Gateway`
already had for other `Gateway`s.

```go
kindActivity: {kindRule: true, kindConversion: true},
```

`Workflow`'s own allowed callees gain `kindActivity: true`.

An `Activity` is `Pure` in the registry, same as `Rule`/`Conversion` —
it never reaches a capability, so it's exactly as deterministic.

## Rationale

This is the same precedent `Conversion` already set for `Rule`: a common,
specific, narrower case gets its own name and its own enforceable rules
rather than living as an unconstrained capability of a broader kind.
"Coordinates pure things, forbidden from touching a capability" is exactly
specific enough to be worth naming, the same way "relates two types" was.

## Consequences

- Grammar: new `activity` keyword (`KwActivity`), new `activity-decl`
  production — identical shape to `rule-decl`/`conversion-decl` (name,
  params, return type, body), no `requires`/`idempotent` clause.
- `compiler/syntax`: new `ActivityDecl` (`Span`, `Name`, `Params`,
  `ReturnType`, `Body`).
- `compiler/checker`: new `kindActivity`; `checkDecl` handles
  `*syntax.ActivityDecl`; new bug class B7 — an activity calls a gateway,
  another activity, or a workflow.
- `compiler/registry`: new `Card.Kind == "activity"`; `Pure: true`;
  `Dependencies`/`Callers` work identically to every other kind (no
  special-casing needed, per `compiler/registry/graph.go`'s existing
  kind-agnostic walk).
- `compiler/runtime`: `Interp.Call` handles `*syntax.ActivityDecl` the same
  way it handles `*syntax.RuleDecl`; new `SpanActivity` trace kind.
- `compiler/format`: prints `activity name(...) -> T { ... }`.
- New example: `examples/customer-service/activities/validate_new_customer.fiig`
  — `validateNewCustomer` moves here from `rules/`, now calling the
  `validateEmail` rule directly (the real motivating case from the
  `Rule`/`Activity` discussion).
