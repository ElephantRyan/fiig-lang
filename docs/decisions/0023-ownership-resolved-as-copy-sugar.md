# ADR 0023: Ownership Resolved as `copy(...)` Sugar; Values Are Immutable

Status: Accepted. Amended by ADR 0053 (2026-10-16): function-local names may
be rebound when declared with a type (`total: int = 0`). Values never change.
Date: 2026-10-05

## Context

`docs/decisions/0005-owned-mutation-release-timing.md` settled *when*
"temporary local mutation of exclusively owned values" (`fiig-lang.md`
§3.5) ships — by the first release — but explicitly left *which mechanism*
open, noting only that design could land at Milestone 2 while
implementation waited.

Working out that mechanism went through two rounds:

1. An initial proposal mirrored Rust: an `owned` parameter modifier
   (consumed/moved at the call site) plus a `mutate` block permitting
   direct field assignment on an owned value. This is sound, but it's real
   machinery — a move-tracking discipline added to every call involving an
   owned parameter, on top of a language where no call currently consumes
   its arguments.
2. That was dropped once a much simpler alternative surfaced: a universal
   `copy(base, field: value, ...)` builtin — sugar for exactly the full
   reconstruction a programmer would otherwise write by hand, resolving
   every field not named in the overrides from `base`. Full reasoning and
   the rejected mechanism are in `docs/specification/ownership.md`.

A second question came up alongside the first: given models are immutable,
how would in-place mutation even work, and wouldn't reconstruction increase
memory use? Both are answered in `docs/specification/ownership.md` §3 —
short version: "in-place" was never a language-level capability in any of
the precedent languages that have it, only a licensed implementation
technique; and the memory question has a known answer (Swift-style
reference-counted reuse) that lives entirely in a future runtime, not in
the type system.

## Decision

1. Add `copy(base, field: value, ...)` as a prelude builtin, checked
   specially like `Ok`/`Err` (`compiler/checker/expr.go`'s `checkCopy`).
   No new grammar — it already parses as an ordinary call.
2. Do not add `owned` parameters, `mutate` blocks, move semantics, or any
   other ownership/uniqueness concept to the language.
3. Do not implement in-place mutation now. If reference-counted in-place
   reuse is ever built, it's a Milestone 4 runtime/backend optimization
   invisible to Fiig source, not a language feature — no further decision
   needed here to permit that later.

## Rationale

See `docs/specification/ownership.md` in full. The short version: Rust-style
compile-time ownership earns its complexity by letting a language run
without a garbage collector. Fiig already committed to a GC-backed runtime
(`fiig-lang.md` §4), so paying for ownership tracking in the type system
would be buying a guarantee the language doesn't need for the reason Rust
needs it. `copy(...)` fully answers ADR 0005's actual worry — immutability
having no sanctioned relief valve — without any of that machinery, since
it never mutates anything; it's just reconstruction with a shorter spelling.

## Consequences

- `fiig-lang.md` §3.5's ownership bullet is replaced, and §5's Milestone 2
  deliverable line now points to `docs/specification/ownership.md` instead
  of describing a feature that was never built.
- `compiler/checker/expr.go` gains `checkCopy`; `compiler/checker/
  checker_test.go` gains direct unit tests for its error cases (non-model
  base, positional override, unknown field, type mismatch — the last one
  is an ordinary A1, not a new bug class).
- `examples/customer-service/rules/rename_customer.fiig` attests the valid
  case.
- `docs/decisions/0005-owned-mutation-release-timing.md` is not reversed —
  it answered "does some escape hatch ship by v1" (yes), which still
  stands; this ADR answers "which one" (`copy`, not ownership).
