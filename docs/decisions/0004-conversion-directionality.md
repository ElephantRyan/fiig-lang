# ADR 0004: Two-Way Conversions Are One Declaration

Status: Accepted, syntax not yet designed
Date: 2026-09-22

## Context

Decision record #3 (`fiig-lang.md` §8): whether a two-way conversion (A→B and
B→A, e.g. Celsius↔Fahrenheit) is one `conversion` declaration carrying two
directed contracts, or two separate `conversion` declarations.

## Decision

One declaration. A→B and B→A are the same idea — one relationship between two
types — so they're declared together, under one canonical registry identity.

## Rationale

Splitting the two directions into separate declarations would give one
conceptual relationship two separate canonical identities in the registry,
and would make it easy for the directions to drift out of sync (e.g. one
side's rounding/formatting rule gets updated and the other is forgotten,
with nothing tying them together to catch it).

## Consequences

- §3.9 ("one primary semantic declaration per file") still holds — a
  bidirectional conversion is one declaration, hence one file, even though it
  carries two directed contracts.
- The registry contract card format (backlog item 9, `fiig-lang.md` §8) needs
  to represent a declaration with two typed directions, rather than assuming
  every declaration has exactly one input type and one output type.
- Exact syntax for expressing both directions inside one `conversion`
  declaration is undesigned — added to `docs/specification/grammar.md`
  "Known gaps." `examples/customer-service/conversions/customer_to_summary.fiig`
  is one-directional and doesn't exercise this; a bidirectional example (e.g.
  Celsius↔Fahrenheit) should be added once the syntax exists, the same way
  every other construct here is validated against a real example.
