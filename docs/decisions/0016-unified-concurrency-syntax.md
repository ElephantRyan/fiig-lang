# ADR 0016: One `concurrent` Syntax for I/O and CPU-Bound Work

Status: Accepted
Date: 2026-09-22

## Context

Decision record #8 (`fiig-lang.md` §8): does pure CPU-bound parallel rule
execution reuse `concurrent(...)`, or does it need a distinct `parallel`
keyword/mode?

## Decision

One syntax. `concurrent(x in xs): expr` is used for both — there is no
`parallel` construct. `concurrent` is already generic enough to wrap a call
to a rule, conversion, step, or workflow (nothing in the grammar restricts
it to steps), so `concurrent(x in xs): pureTransform(x)` over a rule is
already legal without adding anything.

The real difference between the two cases isn't syntax, it's which resource
bounds the budget: §3.7's budget list (application default/maximum,
*capability* maximum, local limit, parent workflow budget) assumes something
with a capability. Pure rules have none (§3.1 scopes capabilities to steps).
So a `concurrent` block's budget dimension is inferred from what it wraps —
a step/workflow call bounds against that capability's existing limits as
already specified; a rule/conversion-only body (no I/O) bounds against a
compute-pool budget instead (e.g. core count), same shape, different
resource, no keyword change.

## Rationale

This follows the same pattern already established for `?` (ADR 0006) and
`mapErr` (rejected in ADR 0006): don't add a second construct when the
existing one already generalizes and the only real difference is an
inferable runtime detail, not something the syntax itself needs to
distinguish.

## Consequences

- No grammar changes — `concurrent-expr` in `docs/specification/grammar.md`
  already covers this; worth a note there rather than a new production.
- The type/effect checker (Milestone 2) needs a rule for classifying a
  `concurrent` block's body as I/O-bound vs. compute-bound (by whether it
  calls a step/workflow anywhere in its body), to pick the right budget
  dimension — not decided here, just identified as necessary follow-up work.
- No current example exercises the CPU-bound case (every `concurrent` block
  in the corpus wraps a step call) — worth adding one once it's useful to
  demonstrate, not required to close this decision.
