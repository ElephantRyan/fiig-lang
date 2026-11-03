# ADR 0015: Transaction Binding Is Implicit by Enclosing Atomic Scope

Status: Accepted
Date: 2026-09-22

## Context

Decision record #10 (`fiig-lang.md` §8): how do transaction-compatible read
steps bind to an active transaction — implicitly because they're called
inside an `atomic` block, or via an explicit parameter/handle?

## Decision

Implicit. A step's capability-backed I/O automatically participates in the
active transaction of the nearest enclosing `atomic` block whose named
capability matches the step's own `requires` capability. No transaction
handle is threaded through step signatures or call sites.

## Rationale

This is already the de facto behavior of every existing example, not a new
choice: `getCustomer(id)` has the exact same call shape in
`read_customer.fiig` (no active transaction) and in
`change_customer_email.fiig` (inside `atomic Database { ... }`, where it
must see the preceding write). An explicit-parameter design would require
threading a transaction handle through every Database-capability step and
every workflow that calls one, breaking that consistency retroactively for
no observed benefit.

This doesn't conflict with the explicit-decision principle behind ADR
0006/0014, because there's no hidden judgment call to make: the step already
declares `requires Database`, and the enclosing block already declares
`atomic Database`. Wiring two matching, already-explicit declarations
together is mechanical — unlike picking a conversion or a retry policy,
nothing is being decided on the workflow author's behalf that they didn't
already state.

## Consequences

- No grammar changes — this is a semantic/runtime rule, not new syntax.
  `docs/specification/grammar.md`'s atomic-block section should note it.
- A step called with no active matching `atomic` block in scope still works
  for reads (every example does this today) via whatever ambient
  non-transactional connection the capability provider uses by default —
  that default isn't specified further here, and doesn't need to be for this
  decision.
- Sequential atomic blocks (`place_order.fiig`) stay separate transactions:
  a step in the second `atomic Database { ... }` does not see uncommitted
  writes from the first, consistent with §3.8's model of two independently
  committed blocks, not one spanning both.
- No ambiguity about *which* enclosing atomic block a step joins: §3.8
  already forbids nesting/overlapping atomic blocks, and only
  rules/conversions/matching-capability steps may appear inside one at all,
  so at most one matching atomic scope can ever be active at a call site.
