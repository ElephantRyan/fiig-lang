# ADR 0014: Idempotency Declaration and Explicit-Only Retry

Status: Accepted
Date: 2026-09-22

## Context

Two coupled decision records: what marks a step as idempotent (surfaced by
`docs/specification/grammar.md`, since `invalid-programs/retry_error.fiig`
only shows the *rejected* case and no step in the corpus is idempotent), and
decision record #9 (`fiig-lang.md` §8) — whether retry is explicit-only or
may be supplied by capability configuration.

## Decision

**Idempotency** is a modifier on the `step` declaration itself, alongside
`requires`:

```
step getCustomer(id: CustomerId) -> Result<Customer, CustomerError>
    requires Database
    idempotent
{
    ...
}
```

This isn't really a new decision so much as syntax for one already implied
by §3.11, which lists "purity, idempotency, and repeatability properties" as
existing registry contract fields — every step needed a way to state this
regardless of the retry question.

**Retry stays explicit-only**, at the call site, via `retry(attempts: N):`.
No capability provider configuration may supply retry automatically. This
follows the same reasoning as ADR 0006: whether an operation gets retried is
a decision with real consequences (cost, latency, side-effect risk if
idempotency was assumed incorrectly), and burying that decision in provider
configuration the workflow author never sees contradicts the same
"decision-making should be pretty explicit" principle ADR 0006 settled on.
Retry isn't purely an infrastructure concern separable from business logic
either — *which* operations are worth retrying, and how many times, is a
per-call-site judgment a capability's generic config can't make well.

## Consequences

- `docs/specification/grammar.md`'s `step-decl` production needs an optional
  `idempotent` modifier.
- `getCustomer` (`examples/customer-service/steps/get_customer.fiig`) is a
  natural candidate to mark `idempotent` — it's a plain read with no side
  effect — which also finally allows a *valid* `retry` example, something no
  existing example demonstrates (`retry_error.fiig` only shows the rejected
  case).
- The bug-class catalog's E1 entry (`retry_error.fiig`) is unaffected — it
  still correctly rejects retrying `chargePayment`, which stays
  non-idempotent (charging twice is unsafe).
