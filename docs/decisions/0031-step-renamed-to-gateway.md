# ADR 0031: The `step` Declaration Is Renamed to `gateway`

Status: Accepted
Date: 2026-10-08

## Context

`step` never quite fit alongside `rule`, `conversion`, `workflow`, and
`adapter` — every one of those names carries real, specific weight
("rule" implies a constraint, "workflow" implies orchestration, "adapter"
names a specific architectural role); "step" just means "one item in a
sequence," which is true of nearly everything in this language and
therefore doesn't distinguish anything.

Several candidates were weighed and rejected along the way:
`Operation`/`Procedure`/`Routine` (too generic, and `Operation` now
collides with `compiler/adapter`'s own OpenAPI `operationId` generation);
`Transaction`/`Process`/`Task` (direct collisions with this language's
own existing vocabulary — a real `transaction` block, and the structured
concurrency "task tree"); `Port` (backwards relative to Ports & Adapters —
the capability, not this declaration, is closer to the abstract "port");
`Interpreter` (collides with `compiler/runtime`, which literally is one);
`Boundary` (named the edge but not the work done at it); `Interaction`
(already fiig-lang.md's own word, safe, but soft); `Liaison`/`Steward`
(precise about the judgment/resilience work involved, but unconventional
as a language keyword).

The deciding realization came from working out what this declaration
actually guarantees once `Activity` (ADR 0030) exists to hold pure
composition: every real example — `getCustomer`, `chargePayment`,
`reserveInventory`, all ten of them — has exactly one capability, always.
That's not a sometimes-present attribute riding along inside a more
general coordinator; it's the entire, load-bearing identity of this kind.
Once that's clear, `Gateway` (Fowler's *Patterns of Enterprise Application
Architecture*: "an object that encapsulates access to an external system
or resource") stops being a narrow, hedged-on choice and becomes an exact
match — and it's not a cold import: `PaymentGateway` is already a
capability name in this project's own corpus.

## Decision

Rename `step` to `gateway` everywhere — lexer, parser/AST, checker,
registry, runtime, formatter, every example, and every current spec
document. This is a pure rename, same treatment ADR 0020 gave
`atomic -> transaction`: every rule ADR 0010/0014/0015/0017/0021 already
established is unchanged, only the name changes.

- ADR 0010 ("a step requires exactly one capability") still holds exactly
  as written, now naming `gateway`.
- ADR 0014 (idempotency/explicit retry), ADR 0015 (implicit transaction
  binding), ADR 0017 (response mapping calling a step), ADR 0021 (adapter
  request binding) are all unaffected in substance.

## Rationale

See Context — `Gateway` is the word that was true the entire time, once
`Activity` relieved it of ever needing to justify a capability-less case.

## Consequences

- `compiler/lexer`: `KwStep` -> `KwGateway`, keyword text `step` -> `gateway`.
- `compiler/syntax`: `StepDecl` -> `GatewayDecl` (same fields: `Name`,
  `Params`, `ReturnType`, `Requires`, `Idempotent`, `Body`).
- `compiler/parser`: `parseStepDecl` -> `parseGatewayDecl`.
- `compiler/checker`: `kindStep` -> `kindGateway`; bug-class diagnostic
  text updated (B1, B3, B4, B5 keep their numbers and meaning, just name
  "gateway" instead of "step").
- `compiler/registry`: `Card.Kind == "gateway"` instead of `"step"`.
- `compiler/runtime`: `SpanStep` -> `SpanGateway`.
- `compiler/format`: emits `gateway` instead of `step`.
- Every `examples/**/*.fiig` step declaration and every reference to
  "step" in `docs/specification/grammar.md`, `bug-classes.md`,
  `docs/terminology.md`, and `fiig-lang.md` updated to `gateway`.
- ADRs 0009, 0010, 0014, 0015, 0017, 0018, 0021 predate this decision and
  still say `step` — left as-is as historical record, per this project's
  established convention (ADR 0020's own consequences section). This ADR
  supersedes their terminology; their reasoning is otherwise unchanged.
