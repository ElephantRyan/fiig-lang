# ADR 0019: Arithmetic Operators Are Fixed, Int-Only, and Non-Overloadable

Status: Accepted
Date: 2026-10-01

> **Note (2026-10-17):** ADR 0055 removed `value` declarations. References to `value` types below are historical.

## Context

`docs/specification/grammar.md`'s "Known gaps" flagged this directly: "no
arithmetic operators appear in the corpus, and unlike comparison/boolean
(decided per ADR 0011) nothing has forced a decision here yet." Nothing in
the examples adds, subtracts, multiplies, divides, or negates anything,
even though `Money` arithmetic and date offsets are obvious real needs.

## Decision

- `+ - * / %` are binary arithmetic operators; `-` is additionally a unary
  negation operator (`-x`).
- They apply only to `int` — the only numeric primitive the language has.
  *(Amended by ADR 0048 §2.2: they also apply to `float`, never to one of
  each. The addendum below is unchanged and governs that.)*
  They are **not user-overloadable**: there is no mechanism, now or
  planned, for a `model` or `value` declaration to redefine what any of
  these operators do. Typed arithmetic over a domain value like `Money` is
  not an operator overload — it's an ordinary named `rule`, e.g.
  `addMoney(a: Money, b: Money) -> Money`, discoverable through the
  registry exactly like any other declaration. This is stricter than Java,
  which still special-cases `+` for `String`; Fiig has no built-in-type
  special case at all.
- Precedence extends ADR 0011's existing chain, slotting standard
  arithmetic precedence between `not` and comparison:
  `not > unary '-' > ('*' '/' '%') > ('+' '-') > comparison > and > or`.
- Division and modulo by zero are faults (unexpected runtime defects, §3.6
  of `fiig-lang.md`), not typed errors — a divisor of zero is a programming
  mistake to surface and crash on, not a domain condition a workflow should
  `match` against, matching Java's own unchecked `ArithmeticException`.
- **Addendum, same decision moment:** no numeric type ever mixes with
  another under an arithmetic operator without an explicit conversion —
  there is no implicit widening between numeric types, the way Java, C#,
  and C++ silently promote `int` to `double` in mixed arithmetic. This
  isn't new scope beyond "int-only, for now" below; it's closing off the
  implicit-promotion shortcut *before* a second numeric type exists and
  someone has to guess whether it applies. When a fractional type is
  eventually added, multiplying an `int` by it directly is a type error;
  crossing between them goes through an explicit `conversion`, or — more
  likely, given the domains this language targets — the cross-type
  operation is a named `rule` that owns the actual business rule (rounding,
  what the result type is), the same pattern `addMoney` already uses for
  same-type `Money` arithmetic.

## Rationale

**Non-overloadable.** Fiig already has the right mechanism for a typed,
named operation between domain values — a `rule` — and the registry already
makes it discoverable without reading its source. Operator overloading
would be a second, parallel path to the same goal, and specifically the
path that reintroduces the problem the registry exists to avoid: to know
what `a + b` means, an LLM or developer would have to go look up which
overload applies to `a`'s and `b`'s types. A fixed, non-overloadable meaning
means `+` means the same one thing in every Fiig program that will ever
exist — no lookup required, ever. Java is the nearest mainstream precedent
for disallowing this; Fiig goes further by not even keeping Java's one
built-in exception.

**Standard precedence.** Every mainstream language and every math class
already agrees on `*`/`/`/`%` binding tighter than `+`/`-`, both binding
tighter than comparisons. An LLM's prior from training data is correct by
default here; inventing a different order would be pure risk for no
benefit.

**Int-only, for now — but not a ceiling.** `int` and `string` are the only
primitives attested anywhere in the grammar today, so restricting
arithmetic to `int` is mostly just a description of what already exists,
not a real constraint. It is deliberately *not* a statement that Fiig will
stay int-only. `money.fiig` avoids a decimal type today only "to avoid
pulling a decimal type into a Milestone 0 sketch" — real billing systems
routinely need fractional-cent precision (fee splits, tax, pro-rated
charges), so a fractional numeric type is an anticipated future need, not
a rejected one. What that type's syntax looks like is a separate decision,
left for when an example actually forces it, per `fiig-lang.md` §9's
guardrail against building things before they're needed — but the
addendum above (no implicit widening) is written now specifically so that
decision doesn't quietly default to Java-style promotion just because
nobody wrote down the alternative first.

**Why not IEEE float, when the time comes.** Not a decision here, but worth
leaving as a steer for whoever makes it: `money.fiig`'s own reasoning for
staying on whole-cent `int` — avoiding float's binary representation error
for values like `0.1` — is exactly why a future fractional type for money
should almost certainly be fixed-point/decimal, not IEEE `float`/`double`.
The classic "floating-point money bug" category is a direct consequence of
reaching for the wrong representation, independent of anything this
language's syntax does right or wrong.

**Java isn't actually the strict model here.** Worth being precise about
this, since Java is the explicitness precedent cited for the no-overloading
decision above: Java's own numeric promotion rules (`int` silently becoming
`double` in mixed arithmetic) *are* an implicit conversion, just a narrower
one than, say, JavaScript's. Rust, Kotlin, and Swift are the actual strict
precedent for "every conversion is written down, even widening ones" — and
that's the one this addendum follows, not Java's.

**Unary minus.** A necessary companion to binary `-` — without it, `-x`
(e.g. a negative offset) has no spelling at all.

**Division/modulo by zero as a fault.** Consistent with `fiig-lang.md`
§3.6's existing split between typed, expected failures and unexpected
runtime defects: a zero divisor is essentially always a bug at the call
site, not a condition a workflow should be built to branch on.

## Consequences

- `docs/specification/grammar.md`: new lexical tokens for `+`, `-`, `/`,
  `%` (`*` already exists, reused from the wildcard-import/generic-args
  positions — the same symbol-reuse-by-parse-position pattern already used
  for `<`/`>`); new `additive`/`multiplicative`/`unary` productions in the
  expression grammar, inserted between `comparison` and `postfix-expression`.
- `compiler/lexer`: four new punctuation token kinds.
- `compiler/parser`: `parseAdditive`/`parseMultiplicative`/`parseUnary`
  inserted into the precedence chain; a new `NegExpr` AST node for unary
  minus (mirroring the existing `NotExpr`).
- `compiler/format`: print support for the new `BinaryOp` values and
  `NegExpr`.
- Nothing in the corpus attests arithmetic yet, so an example needs adding
  alongside this ADR — `addMoney` in `order-processing`, unwrapping `Money`
  through its `.value` field, the same unwrap convention already attested
  by `EmailAddress.value` in `validate_new_customer.fiig`.
- Division/modulo-by-zero-as-fault isn't actionable until a runtime exists
  (Milestone 4) — recorded now so it isn't re-litigated later as if it were
  still open.
