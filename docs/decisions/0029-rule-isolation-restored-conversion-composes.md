# ADR 0029: Rules Call Nothing; Conversions May Call Rules and Conversions

Status: Accepted; narrowed by ADR 0049 (implemented 2026-11-02): a rule may
call formulas, so "rules call nothing" now reads "rules call nothing that
carries judgment or effects." Rules still may not call rules, conversions,
activities, gateways, or workflows.
Date: 2026-10-08
Supersedes: ADR 0028

## Context

ADR 0028 let `Rule`/`Conversion` call each other freely, reasoning that
purity composes safely. Revisiting it while designing a replacement for
`step` surfaced a cleaner distinction: **recursive structural type mapping**
(a `Conversion` for a complex nested model calling the `Conversion` for
each nested field — `orderToSummary` calling `customerToSummary` for
`order.customer`) is mechanical, judgment-free, and inherent to any model
with nested model fields. **Composing business decisions** (`validateCustomer`
calling `validatePhoneNumber`/`validateEmail`) is a fundamentally different
kind of composition — it's coordinating decisions, not reshaping data —
and ADR 0030's new `Activity` kind exists specifically to be that
coordinator. Treating both as "purity composes, so both are fine" missed
that distinction.

## Decision

- `Rule` may call nothing. Fully isolated, as originally argued before
  ADR 0028 — any composition of rules now belongs to `Activity` (ADR 0030).
- `Conversion` may call `Rule` and other `Conversion`s — restoring the
  original pre-0028 `Conversion -> Rule` allowance and adding
  `Conversion -> Conversion` for recursive structural mapping.

```go
kindRule:       {},
kindConversion: {kindRule: true, kindConversion: true},
```

## Rationale

The line that actually matters is "is this composition reshaping data, or
composing decisions." A `Conversion` calling another `Conversion` is still
doing exactly `Conversion`'s one job, one level deeper in an object graph —
no new kind of behavior appears. A `Rule` calling another `Rule` was always
really coordination wearing a `Rule`'s name, which is why ADR 0028's fix
felt like it was patching a symptom: it made composition *possible* again
without asking whether `Rule` was the right place for it. It wasn't —
`Activity` is.

## Consequences

- `compiler/checker/checker.go`: `allowedCallees[kindRule]` becomes empty;
  `kindConversion` gains `kindConversion: true`.
- `examples/customer-service/rules/validate_new_customer.fiig`: moves back
  out of being a rule that calls `validateEmail` — see ADR 0030, it becomes
  an `Activity`.
- New bug class B6 (`docs/specification/bug-classes.md`): a rule calls
  another rule or a conversion.
- Conversion-calls-conversion (nested structural mapping) isn't exercised
  by a real example yet — no model in the corpus currently has a nested
  model field needing it. Legal, not yet attested; flagged rather than
  forced with a manufactured example.
