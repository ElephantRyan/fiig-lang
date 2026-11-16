# ADR 0083: Models Declare No Invariants; a Rule Makes the Valid Model

Status: Accepted and implemented (2026-11-16)
Date: 2026-11-16
Related: fiig-lang.md §3.5, charter principle 4, ADR 0029 (rules are
isolated), ADR 0030 (activities compose), ADR 0055 (no wrapper types), ADR
0079 (an obvious path, not enforcement). Alpha item 11.

## Context

§3.5 says "stable, pure, context-independent invariants may be declared on
models", and charter principle 4 reads "immutable models with declared
invariants". Nothing implements it, and `known-limitations.md` says to
validate in a rule.

Eiffel, D, and Ada attach invariants to a type and check them at every
construction; Kotlin and Java records check in the constructor; Pydantic
checks field constraints when it builds or decodes a value. In each, a
violation is an exception.

## Decision

**A model declares no invariants, now or later.** A model is the shape of
data. Whether a value is acceptable is business logic, and business logic
belongs in rules.

**A rule makes the valid model from the input model.** §3.5 already prefers
separate lifecycle models (`NewCustomer` and `Customer`). The rule between
them validates and returns the second:

```fiig
rule createValidRating(input: NewRating) -> Result<Rating, RatingError> {
    if input.score < 1 or input.score > 10 {
        return Err(RatingError.Invalid("score", "must be from 1 to 10"))
    }
    if input.item.isBlank() {
        return Err(RatingError.Invalid("item", "must not be blank"))
    }
    return Ok(Rating(item: input.item, score: input.score, note: input.note))
}
```

Code that needs a valid rating takes a `Rating`. A gateway that stores
ratings takes a `Rating`, not a `NewRating`, so its signature says it
expects the validated form, and the obvious way to get one is the rule.
When validation needs another rule, an activity composes them (ADR 0030),
as `validateNewCustomer` does. When it needs the database, a gateway does
that part (§3.5).

This is a path, not a guarantee: any code may still construct a `Rating`
directly, and a JSON body or a database row is read into one without the
rule running. As with ADR 0079, fiig gives the shape and makes it visible
rather than enforcing it.

## Rationale

- **One place for business logic.** With invariants, "is a score of 11
  allowed?" would have two possible homes, the model and a rule, and a
  reader would have to check both. Rules already answer it, with a typed
  error a caller can handle; an invariant could only fault or produce the
  adapter's generic 400.
- **Acceptability depends on context more often than it looks.** A limit
  that seems fixed (a score out of 10) is a product decision that changes,
  and may differ by caller. In a rule, it changes in one function; on a
  model, it changes what every stored value and every client must be.
- **Models stay data.** fiig has no methods on models (ADR 0049 §2.6) and
  isn't object-oriented (charter); invariants would be the first behavior
  attached to a model.

**Considered and rejected:**

- **Boolean invariants on the model** (Eiffel, Ada, Kotlin), checked at
  every construction, decode, and database read. Rejected for the reasons
  above.
- **Declarative field constraints** (`score: int in 1..10`, as Pydantic or
  JSON Schema). The same business logic in the model, in a smaller
  vocabulary.
- **Only the rule may construct the model** (Rust/Haskell smart
  constructors). Needs package-internal visibility (ADR 0079 open question
  1) and adds construction ceremony for callers, ADR 0055's objection.

## Consequences

- `fiig-lang.md` §3.5: the invariants bullet is replaced by this ADR's rule;
  the section is renamed "Models". Charter principle 4 becomes "immutable
  models; validation in rules".
- `known-limitations.md` drops "No model invariants"; `ownership.md`'s note
  about them points here.
- Corpus: getting-started takes this shape. `validateRating` becomes
  `createValidRating(input: NewRating) -> Result<Rating, RatingError>`,
  `recordRating` passes its result on, and `saveRating` takes a `Rating`.
  `docs/getting-started.md` follows.
- Alpha item 11 is closed by this decision; nothing is built in the
  compiler.
