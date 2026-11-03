# ADR 0041: `bool` Is a Real, Declarable Primitive Type

Status: Accepted
Date: 2026-10-10

## Context

ADR 0011 added comparison and boolean operators (`==`, `!=`, `<`, `<=`,
`>`, `>=`, `and`, `or`, already-attested `not`) but deliberately left two
things open: its precedence guess was unvalidated (no example combined
two conditions with `and`/`or`), and — more fundamentally — the checker
never modeled a boolean *type* at all. `inferBinary` returned
`types.Unknown{}` for every comparison/boolean expression, and `NotExpr`'s
operand went unchecked. A runtime `Bool` value already existed (something
concrete has to flow through `if`, `and`, `or`, `not`), but nothing let a
program *declare* a value as one, bind it to a name and reuse it, or
return it from a rule — the exact gap an outside critique of the language
surfaced: "you can't bind `isValid = x and y` and use it twice."

## Decision

`bool` joins `string`/`int` as a real primitive type (`types.Primitive`),
usable anywhere a type is written — fields, parameters, return types.
`true`/`false` are literals, resolved the same way `None` already is: a
bare `Ident` special-cased by name, not a new lexer token or grammar
production (no new syntax was needed — ADR 0011 already added every
operator this type needed to make sense of).

```
rule qualifiesForFreeShipping(total: Money, region: Region) -> bool {
    isLargeOrder = total.value >= 10000
    if isLargeOrder and region == Region.US {
        return true
    }
    return isLargeOrder and region == Region.EU
}
```

Every comparison operator and `and`/`or`/`not` now actually produces and
expects `bool`: `and`/`or` check both operands against it the same way
arithmetic already checks `int`; `not`'s operand is checked the same way;
comparisons return `bool` unconditionally (operand well-definedness for
ordering stays a runtime concern — `Less`/`Equal` already guard that). An
`if` condition (and every `else if`) is now checked against `bool` too,
the same `checkExpected` machinery every other expected-type position
already uses — previously it was inferred but never checked against
anything.

The real example above also validates ADR 0011's precedence guess for the
first time: `isLargeOrder and region == Region.US` parses as `isLargeOrder
and (region == Region.US)` — comparisons bind tighter than `and`, exactly
the untested first-cut guess that ADR said needed confirming. ADR 0011's
Status line is updated to drop "precedence unconfirmed" accordingly.

## Rationale

This was never a hard problem — every mainstream language ships a boolean
type in week one — it was a side effect of this project's own "resolve
syntax only where the examples require it" discipline colliding with the
fact that no example had ever needed to store a condition's result. That
discipline is right for genuinely undesigned syntax; a storable boolean
isn't undesigned, it's a load-bearing primitive every other decision
(comparisons, `and`/`or`, `if`) already assumed existed. Closing it now
costs nothing new syntactically and removes a real expressiveness gap
before more business logic depends on it.

## Consequences

- `compiler/types`: `Primitive{Name: "bool"}` is a resolvable type
  (`universe.go`'s `resolve`, alongside `"string"`/`"int"`).
- `compiler/checker`: `inferExpr`'s `Ident` case resolves `true`/`false` to
  `bool`; `NotExpr` checks its operand against `bool` and returns it;
  `inferBinary` checks `and`/`or` operands against `bool` and returns
  `bool` for them and for every comparison operator (previously
  `Unknown{}`); `IfStmt`'s condition and every `else if` condition are
  now checked against `bool` via `checkExpected` (previously inferred
  only, never checked).
- `compiler/runtime`: `evalExpr`'s `Ident` case evaluates `true`/`false`
  to `Bool(true)`/`Bool(false)`, mirroring `None`'s existing special case.
- `compiler/runtime/json.go`: `DecodeJSON` gains a `"bool"` case (JSON
  `true`/`false` → `Bool`) — needed the moment a `bool` field/parameter can
  appear in a request body or `-args` input.
- `compiler/adapter/openapi.go` and `compiler/adapter/adapter.go`: `bool`
  maps to OpenAPI `{"type": "boolean"}`; a path/query placeholder decodes
  a raw `"true"`/`"false"` string the same way an `int` placeholder
  already decodes a numeric string.
- **Real bug found and fixed along the way, not introduced by this
  change**: writing the first real expression-position use of a
  zero-field enum variant outside a match pattern
  (`region == Region.US`) surfaced that `evalFieldAccess` had no case for
  a `FieldAccess` target naming an enum type — it unconditionally tried
  (and failed, with "undefined name") to look the type name up as a local
  variable. This was already latent in the corpus: `checkout.fiig`'s
  `return Err(CheckoutError.OutOfStock)` is the exact same shape and had
  never actually been exercised by any runtime test. Fixed by giving
  `evalFieldAccess` the same target-resolution special-case
  `inferFieldAccess` already had on the checker side; a new regression
  test (`TestCheckoutWorkflowReinterpretsOutOfStock`) exercises it for the
  first time.
- New example: `examples/order-processing/rules/qualifies_for_free_shipping.fiig`
  — a stored boolean read twice, `and`, `==` over an enum, and the first
  attested `true` literal in the corpus.
- New tests: `TestQualifiesForFreeShippingBooleanLogic` (the new example),
  `TestCheckoutWorkflowReinterpretsOutOfStock` (the dormant bug above).
- Documentation: `docs/specification/grammar.md`'s "no boolean literal"
  note and "Known gaps" section updated; ADR 0011's Status line updated
  to remove "precedence unconfirmed".
