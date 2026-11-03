# ADR 0011: Standard Comparison and Boolean Operators

Status: Accepted. Precedence confirmed by ADR 0041's real example.
Date: 2026-09-22

## Context

A gap surfaced by `docs/specification/grammar.md`: no example uses a
comparison or boolean operator — every condition so far is a method call
(`.isBlank()`, `.contains()`) or the prefix `not` already in the grammar.

## Decision

The language has standard infix comparison operators (`==`, `!=`, `<`, `<=`,
`>`, `>=`) and boolean operators (`and`, `or`), alongside the already-attested
prefix `not`.

## Rationale

Comparison and boolean logic are common enough in ordinary rule/step bodies
that routing them through method calls (`.equals()`, `.and()`) would be
unusual friction with no stated benefit — near-universal convention here,
and nothing about the language's goals (registry-first reuse, effect
tracking, concurrency safety) is in tension with ordinary infix operators.

## Consequences

- `docs/specification/grammar.md`'s expression grammar needs a
  binary-expression production and a precedence table, added there.
- No current example exercises this, so the precedence order is a
  first-cut guess (standard C-like: `not` > comparisons > `and` > `or`),
  not something validated against a real program yet — worth confirming
  once an example needs actual boolean composition (e.g. two conditions
  combined with `and`).
