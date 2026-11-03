# ADR 0013: `if` Supports `else` and `else if`

Status: Accepted
Date: 2026-09-22

## Context

A gap surfaced by `docs/specification/grammar.md`: every attested `if` in
the examples is a single-sided guard (`if cond { return Err(...) }`), never
paired with `else`. Worth deciding explicitly rather than by default,
including the alternative of a guard-only `if` that routes every multi-branch
decision through `match` instead.

## Decision

`if` supports an optional `else`, including `else if` chains, matching
familiar mainstream syntax.

## Rationale

The guard-only alternative is a real, defensible minimalism (one way to
branch, not two competing ones), but the evidence for it here is thin — 11
examples all happening to be single-sided guards isn't proof a two-sided
branch is never needed, and forcing plain binary conditions through
`match true/false` is more ceremony than `if/else` for no corresponding
gain. `if/else` is universally understood, costs nothing against the rest of
the design, and is cheap to simplify later if it ever proves redundant.

## Consequences

- `docs/specification/grammar.md`'s `if-statement` production needs
  extending to allow `else if` chains and a trailing `else`.
- No current example exercises `else`; not required to retrofit one for
  Milestone 0's exit criteria, but worth using naturally in a future example
  rather than forcing it in artificially.
