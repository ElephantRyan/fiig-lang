# ADR 0055: `value` Declarations Are Removed

Status: **Accepted** (2026-10-17).
Date: 2026-10-17
Amends: `fiig-lang.md` §3 ("named models and distinct domain values are
nominally typed"), ADR 0019 (operators over domain values), ADR 0052
(comparability of `value` wrappers).
Related: ADR 0022 (transparent aliases dropped; named-argument calls
preferred), ADR 0025 (SQL ids as `value CustomerId: string`), ADR 0054.

## Context

`value Name: T` declares a nominal wrapper over one type, a newtype:

```
value CustomerId: string
value Money: int
```

A `CustomerId` holds a string but is not interchangeable with `string` or with
any other wrapper over `string`. The intent was to make swapped arguments a
compile error:

```
rule refund(orderId: OrderId, customerId: CustomerId) -> ...
refund(customer.id, order.id)      // compile error under value types
```

The corpus has six `value` declarations: `CustomerId`, `EmailAddress`,
`OrderId`, `PaymentReference`, `Money`, and `standard.time`'s `Instant`. All
six wrap a primitive.

## Decision

The `value` declaration kind is removed. Domain identifiers, amounts, and
similar quantities are written as primitives:

```
rule refund(orderId: string, customerId: string) -> ...

refund(orderId: order.id, customerId: customer.id)
```

**Named-argument calls are fiig's answer to the swapped-argument bug.** A call
site that names every argument reads by name rather than by position, so a
swap is visible in the source, in a diff, in review, and in a registry
contract card. Nothing new is added for this. Named arguments already exist
and ADR 0022 already prefers them.

If a type really needs more than one primitive's worth of meaning, it is an
ordinary `model`. No shorthand for a one-field model replaces `value`.

## Rationale

**Wrapper types spread to every caller.** A rule's signature is its contract.
If a rule, and especially a library rule, takes `CustomerId`, every caller in
every application that uses it has to obtain or construct a `CustomerId`. One
library author's choice becomes everyone's ceremony. Fiig's package model
(Milestone 6) makes libraries the normal way to share rules, so this cost
isn't occasional.

**The remaining protection doesn't justify a declaration kind.** With named
arguments, the bug that a wrapper still catches is narrow: the right parameter
name with the wrong value, as in `refund(orderId: customer.id, ...)`. That's a
real mistake, but it's rare, and it is visible at the call site because the
names sit right next to the values. A whole declaration kind, plus construction
and `.value` unwrapping at every boundary, costs more than that.

**Readers get nothing extra.** `customerId: string` and
`customerId: CustomerId` tell a human or an LLM the same thing, because the
parameter name already carries the meaning. The wrapper adds a type to look
up and nothing new to learn from it.

**Familiar precedent.** Java has no built-in newtype, and large Java codebases
handle this with parameter names and IDE parameter hints. Fiig gets the same
result more strongly, because named arguments put the names in the source
rather than in an IDE overlay.

**It shrinks the language.** One fewer declaration kind, one fewer reserved
word, and several special cases go away: the runtime's one-field `"value"`
record, the JSON bare-scalar encoding, the OpenAPI scalar schema, ADR 0052's
recursion through `value` wrappers, and ADR 0052's open question about whether
a `value`'s underlying type must be a primitive.

**Considered and rejected:**

- **Keep `value` as an opt-in tool for domain edges.** The cost lands on
  callers rather than on the author who opted in, so "optional" isn't optional
  for anyone downstream.
- **Spell it as a one-field model** (`model CustomerId { value: string }`).
  Same caller cost, more verbose.
- **Rename the keyword** (for example `type CustomerId: string`). Keeps the
  cost and also reads as an alias, which ADR 0022 rejected.

## Consequences

- **Grammar.** `value` is no longer a keyword. `parseValueDecl` and
  `syntax.ValueDecl` are removed. A file starting with `value` is a parse
  error whose diagnostic suggests the underlying primitive, or a `model`.
- **Checker.** The `ValueDecl` cases in `checker.go` and `expr.go` go,
  including the `.value` field access on wrappers. ADR 0052's comparability
  table loses its `Named -> value V: P` row.
- **Registry.** The `"value"` card kind is removed (`registry.go`,
  `diff.go`). Removing a `value` declaration from a published package is a
  breaking change under ADR 0034's bump check, as removing any declaration is.
- **Runtime and adapters.** The single-field `"value"` record form
  (`callexpr.go`, `json.go`) and the adapter and OpenAPI scalar handling
  (`adapter.go`, `openapi.go`) are removed.
- **Formatter.** The `value` case in `format.go` is removed.
- **Corpus migration.**
  - `CustomerId`, `EmailAddress`, `OrderId`, `PaymentReference` become
    `string`.
  - `Money` becomes `int`. `addMoney` becomes plain `+` and is deleted, and
    the `.value` unwraps in `total_of_orders`,
    `all_orders_have_positive_totals`, and `qualifies_for_free_shipping` go
    away.
  - `standard.time`'s `Instant` becomes `model Instant { days: int }`. That
    package exists to give ADR 0032's cross-package resolution a real
    imported *type*, so it can't simply become `int`. `addBusinessDays`
    constructs `time.Instant(days: from.days + days)`.
  - `validateEmail` takes a `string`.
  - Every call site in the corpus that passes one of these values uses named
    arguments, if it doesn't already.
- **Spec and docs.** `fiig-lang.md` §3 drops "distinct domain values,"
  `docs/specification/tour.md` drops its `value` section, and ADRs 0019, 0025,
  and 0052 get a note pointing here.

## Closed questions

1. **Should named arguments be required, not just preferred?** Closed
   2026-10-17: **no, they stay preferred.** Requiring them, even only for
   same-typed arguments, would be a new checker rule, and the user judged the
   preference enough.

## Implementation notes (2026-10-17)

Implemented as specified. Two things came up:

- **A latent bug, now fixed.** With `.value` gone, `validateEmail`'s
  `email.contains("@")` became the first method call whose receiver is a
  bare local name. Both the checker (`inferQualifiedCall`) and the runtime
  (`evalQualifiedCall`) treated any `name.method(...)` whose `name` wasn't a
  declaration or import alias as a **capability call**, so the runtime
  faulted with "no provider registered for capability "email"" and the
  checker silently accepted it. A local binding now takes precedence over
  the capability fallback in both.
- **A cost the ADR didn't name.** Type-based registry search loses
  precision. `catalog find --input CustomerId` used to pick out exactly the
  declarations taking a customer ID; `--input string` now matches every
  declaration with any string parameter.
