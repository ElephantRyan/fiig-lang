# Ownership

Status: Milestone 2 deliverable ("Initial ownership design document;
implementation may remain deferred," `fiig-lang.md` line 292) — resolved.
No language-level ownership feature is being built. This document records
why, and what replaces it.

## 1. The problem

`fiig-lang.md` §3.5 says models are immutable by default, and flagged "a
later ownership feature" that "may permit temporary local mutation of
exclusively owned values" as a possible escape hatch. `docs/decisions/
0005-owned-mutation-release-timing.md` already committed to shipping
*some* such escape hatch by the first release — not because any current
example needs one, but because leaving immutability with no sanctioned
relief valve risks the guarantee eroding some other way instead (an
`Any`-like workaround, or exposing host-language mutation directly).

Concretely, the pain this is about: updating one field of an existing model
means reconstructing every field, touched or not. Using the real `Customer`
model (`id`, `name`, `email`):

```
rule renameCustomer(customer: Customer, newName: string) -> Customer {
    return Customer(id: customer.id, name: newName, email: customer.email)
}
```

Three fields is mildly annoying; `Order` already has four, and it only
gets worse as models grow or as a workflow chains several one-field
updates in a row.

## 2. Precedent

| Language | Mechanism | How soundness is achieved |
|---|---|---|
| Rust | ownership + moves, `&mut` | compile-time borrow checker proves exclusivity; no GC |
| Swift | copy-on-write | **runtime** check (`isUniquelyReferenced`), not compile-time |
| Clean / Pony | uniqueness types (`*World`, `iso`) | compile-time — a type-level "exactly one reference" guarantee |
| Kotlin / Scala | `.copy(field = value)` on a data/case class | no mutation at all — plain reconstruction, sugared |
| Haskell / F# records | `r { field = value }` / `{ r with Field = value }` | same — reconstruction, sugared |

The first three solve a problem Fiig doesn't strictly have: Rust needs
compile-time ownership specifically because it has no garbage collector to
fall back on, and Fiig is already committed to a garbage-collected backend
(`fiig-lang.md` §4 — initially emitting Go, no zero-GC goal anywhere in the
charter). Clean/Pony's uniqueness types are the same idea, generalized.
Kotlin/Scala/Haskell/F#'s record-update sugar solves a *different*,
narrower problem — pure ergonomics, no mutation, no soundness question at
all, because it never touches memory a second time.

## 3. Why no language-level ownership feature

Two things initially look like they need real ownership tracking, and
don't:

**"How would in-place mutation even work under immutability?"** It
wouldn't, at the language level — and it isn't meant to. Every update is
still, semantically, "produce a new value." What ownership/uniqueness
would buy, in any of the precedent languages that have it, is narrower: a
license for the *implementation* to produce that new value by overwriting
the old value's memory instead of allocating fresh memory, legal only when
the compiler can prove nothing else could still be looking at the old
memory. That's an allocation-avoidance technique, not a new capability a
Fiig program would ever observe or write code to use.

**"Doesn't reconstruction cost more memory?"** Marginally, yes — even with
structural sharing of untouched fields (already promised unconditionally
by §3.5, independent of this document), reconstruction still allocates one
new top-level shell per update. For a single call this is nothing; for a
tight loop doing thousands of updates, it's thousands of small allocations
instead of zero. But closing that gap doesn't require putting ownership
into the *language*: given a GC-backed runtime is already the plan, the
standard answer is Swift's — track a reference count, and when a value's
count is exactly 1 at the moment of an update, reuse its memory in place
instead of allocating; otherwise allocate fresh. That's entirely a
runtime/backend decision, invisible to the Fiig programmer. No new
keyword, no type-system change, nothing the type checker needs to verify.

So the ergonomic problem (§1) and the memory problem are separable, and
only the first one needs anything in the language now.

## 4. Decision: `copy(base, field: value, ...)`

A prelude builtin, checked specially (like `Ok`/`Err`), not a real
declaration:

```
rule renameCustomer(customer: Customer, newName: string) -> Customer {
    return copy(customer, name: newName)
}
```

Rules:

- The first argument is the base value and must be positional (unnamed).
  It must resolve to a `model` type — primitive bases have no fields to
  override, so they're rejected the same way a primitive is rejected as an
  A7 request-body binding target, for the same underlying reason (nothing to
  decode/override against).
- Every remaining argument must be named, and the name must match one of
  the base's own model fields. Positional overrides are rejected outright
  — unlike ordinary construction, where positional args fill fields in
  declared order, a `copy` call is usually overriding an arbitrary subset
  of fields scattered through the declaration, and "which field does
  position 2 mean, when I'm skipping most of them" has no good answer.
  Named-only keeps every override attributable at a glance, consistent
  with the project's existing preference for named-argument calls.
- Each override's value is checked against that field's real declared
  type, exactly as `Customer(...)` construction already checks it — a
  mismatch is an ordinary A1, not a new bug class.
- Any field not named among the overrides keeps its value from `base`,
  unchanged.
- The result's type is exactly `base`'s own type.

This needed no new grammar: `copy(...)` already parses as an ordinary call
expression. The only new code is one `inferCall` branch
(`compiler/checker/expr.go`'s `checkCopy`) recognizing the name `copy` the
same way `Ok`/`Err` are already recognized, reusing the same per-field
type-checking `checkFieldArgs` already does for construction. Unlike
`Ok`/`Err`, `copy`'s result type never depends on an expected type from
context — it's always exactly `base`'s type — so it needs no
`checkExpected` special case, only the one `inferCall` branch.

Attested: `customer-service/rules/rename_customer.fiig`. Error cases
(non-model base, positional override, unknown override field, override
type mismatch) are covered by unit tests in `compiler/checker/
checker_test.go` rather than `invalid-programs/*.fiig` fixtures, since none
of them are numbered bug-classes.md entries — they're ordinary
infrastructure diagnostics (untagged), the same treatment "undefined name"
or "too many arguments" already get.

## 5. What's explicitly not here

- **No `owned` parameter modifier, no `mutate` block, no move semantics.**
  An earlier draft of this design proposed exactly that (Rust-shaped:
  an `owned` parameter consumed at the call site, plus a `mutate` block
  permitting direct field assignment within it). It was dropped once
  `copy(...)` turned out to solve the actual motivating problem (§1) with
  no new type-system machinery at all, and once it was clear the
  remaining memory concern doesn't need language-level ownership either
  (§3). Nothing is unresolved here waiting on it.
- **No in-place mutation, now or later, at the language level.** If
  reference-counted in-place reuse (§3) is ever implemented, it stays a
  Milestone 4 runtime/backend technique, invisible to Fiig source — not a
  future extension of this document's scope.
- **Model invariants** (§3.5's "stable, pure, context-independent
  invariants may be declared on models") aren't implemented at all yet,
  for `copy`-produced values or any other construction — not a gap this
  document introduces, just not yet built, uniformly.

## 6. Consequences

- `fiig-lang.md` §3.5's ownership bullet and §5's Milestone 2 deliverable
  line are updated to point here instead of describing a feature that
  doesn't exist.
- `docs/decisions/0005-owned-mutation-release-timing.md` isn't reversed —
  it asked "does *some* escape hatch for immutable-by-default ship by v1,"
  and the answer is still yes. What's resolved here is *which* mechanism,
  which ADR 0005 explicitly left open.
