# ADR 0006: Workflow Error Sets and Cross-Workflow Propagation

Status: Accepted
Date: 2026-09-22

## Context

Two coupled decision records from `fiig-lang.md` §8:

- #5: are a workflow's public errors explicit, inferred, or inferred-then-
  checked against an explicit contract?
- #7: when workflow A calls workflow B, how do B's errors reach A — merged
  automatically, or must A convert them?

No existing example exercises this, because every workflow in `examples/`
only ever calls things from its own error domain (`customer-service/` stays
inside `CustomerError`, `order-processing/` inside `OrderError`). The
scenario below is new: a `checkout` workflow that calls both a
`customer-service` workflow and an `order-processing` workflow, so the two
child error types genuinely differ and the decision actually shows up.

```
workflow checkout(input: NewCustomer, total: Money) -> Result<Order, ???> {
    customer = createCustomer(input)?      // Result<Customer, CustomerError>
    orderId = OrderId.generate()
    order = placeOrder(orderId, customer.id, total)?  // Result<Order, OrderError>
    return Ok(order)
}
```

Three options were explored below and superseded by the decision that
follows them — kept for the record since each taught something the final
answer keeps.

## Decision

Workflow error types stay explicit — every workflow declares its own, as
every example already does (settles §8 item #5). `?` only ever propagates an
error whose type the current declaration already accepts *unchanged* — it
never wraps, converts, or reinterprets. The moment a workflow needs to
interpret a child error as something else for its own purposes, that's an
explicit `match` on the child's `Result`, written out in the workflow body,
with every `Err` arm stating exactly what happens (settles §8 item #7). This
directly answers Question 3: composing within one domain (child already
returns the caller's own error type) can keep using `?`; composing across
domains, or whenever a failure needs reinterpreting, requires `match`.

```
error CheckoutError {
    Customer(error: CustomerError)
    Order(error: OrderError)
    OutOfStock
}

workflow checkout(orderId: OrderId, input: NewCustomer, total: Money) -> Result<Order, CheckoutError> {
    match createCustomer(input) {
        Err(error) -> return Err(CheckoutError.Customer(error)) // explicit: wrapped unchanged
        found -> {
            match placeOrder(orderId, found.id, total) {
                Ok(order) -> return Ok(order)
                Err(OrderError.InventoryUnavailable(_)) -> return Err(CheckoutError.OutOfStock) // explicit: reinterpreted
                Err(error) -> return Err(CheckoutError.Order(error))                             // explicit: wrapped unchanged
            }
        }
    }
}
```

No new syntax: `match`, `error`, `Result`, and `return` inside a match arm
already cover this. `mapErr` (Option A, below) is dropped as unnecessary.
One small grammar extension falls out of the "reinterpret a specific case"
arm: `Err(OrderError.InventoryUnavailable(_))` qualifies the constructor by
its error type, which the current `pattern` grammar
(`docs/specification/grammar.md`) doesn't yet allow — noted there as a
consequence, not a blocker, since it's a natural extension of the
already-qualified construction syntax (`CustomerError.NotFound(id)`) to
pattern position.

## Options considered and rejected

## Option A — Explicit, with required conversion

`checkout` declares its own error type. Every child error must be converted
into it before `?` is allowed to propagate — the same way any other type
mismatch would be rejected.

```
error CheckoutError {
    Customer(error: CustomerError)
    Order(error: OrderError)
}

conversion customerErrorToCheckoutError(error: CustomerError) -> CheckoutError {
    return CheckoutError.Customer(error)
}

conversion orderErrorToCheckoutError(error: OrderError) -> CheckoutError {
    return CheckoutError.Order(error)
}

workflow checkout(input: NewCustomer, total: Money) -> Result<Order, CheckoutError> {
    customer = createCustomer(input) mapErr customerErrorToCheckoutError ?
    orderId = OrderId.generate()
    return placeOrder(orderId, customer.id, total) mapErr orderErrorToCheckoutError ?
}
```

This is close to the shape you described — a declared error type that other
errors get "caught" and reinterpreted into, success vs. failure vs. what kind
of failure. `CheckoutError` here plays that role directly, as an ordinary
tagged union (§3.6's existing `error` declaration), not a new language-level
concept — whether it needs a separate `WorkflowResultType` idea on top of
that, or whether a tagged `error` declaration already *is* that idea, is
itself part of what this option answers: no new construct, just discipline
about always declaring and converting.

**Tradeoff:** every workflow composition site is explicit about what can go
wrong and why, at the cost of writing a conversion (or `mapErr`, itself new
syntax — not yet attested anywhere) for every child error type a workflow
touches.

## Option B — Fully inferred

`checkout` declares no error type. The compiler computes it as the union of
every error type reachable via `?` in the body.

```
workflow checkout(input: NewCustomer, total: Money) -> Result<Order, CustomerError | OrderError> {
    customer = createCustomer(input)?
    orderId = OrderId.generate()
    return placeOrder(orderId, customer.id, total)?
}
```

(The signature shown is what the compiler would generate for the registry
contract — a developer would just write `-> Result<Order, _>` or omit it
entirely, depending on how far inference is allowed to reach into public
signatures, which is itself unresolved.)

**Tradeoff:** zero ceremony to compose workflows — this is what every
existing example already assumes, since none of them declare or convert
errors today. Cost: a caller three levels up can be surprised by a new
variant in its inferred error union just because some deeply nested step
changed, with no signal at the call site that changed. No "what kind of
failure, for this workflow specifically" story — callers see raw
implementation-detail unions.

## Option C — Inferred, then checked against a declared contract

`checkout` declares `CheckoutError` like Option A, but the compiler infers
the real union from the body and *checks* it against the declaration instead
of requiring hand-written conversions for every case.

```
error CheckoutError {
    Customer(error: CustomerError)
    Order(error: OrderError)
}

workflow checkout(input: NewCustomer, total: Money) -> Result<Order, CheckoutError> {
    customer = createCustomer(input)?              // CustomerError auto-wrapped into CheckoutError.Customer?
    orderId = OrderId.generate()
    return placeOrder(orderId, customer.id, total)? // OrderError auto-wrapped into CheckoutError.Order?
}
```

This only works if the compiler can automatically match `CustomerError` to
the `CheckoutError.Customer(error: CustomerError)` variant by matching the
wrapped type, without an explicit `conversion`. That's a real design cost
(ambiguous if two variants wrap the same type) in exchange for keeping
Option A's declared, meaningful error type without its per-call-site
conversion boilerplate.

## Why not A, B, or C

- **Option B** (fully inferred) is ruled out directly: a workflow that
  silently unions whatever child errors happen to occur isn't interpreting
  anything, which contradicts the stated principle that a workflow should
  actively decide what a failure means for itself.
- **Option C** (automatic wrapping by matching types) is ruled out because
  its whole appeal — skipping a written decision at each call site — is
  exactly what "decision making should be pretty explicit by the workflow"
  rejects. Silent, compiler-selected wrapping is still silent, even when
  it's unambiguous.
- **Option A** (named `conversion` + `mapErr`) is close, but redundant: the
  `conversion` functions it required were pure boilerplate (`return
  CheckoutError.Customer(error)`, no real logic), and `match` already
  expresses the same explicit per-case decision inline, without inventing
  `mapErr` or requiring a separate declaration elsewhere in the file tree
  just to look up what a call site does.

## Consequences

- `docs/specification/grammar.md`'s `pattern` production needs extending to
  allow a qualified constructor name (`OrderError.InventoryUnavailable(_)`),
  not just a bare identifier — a small, natural extension of the
  already-qualified construction syntax.
- The bug-class catalog's A6 ("ambiguous conversion") is no longer entangled
  with this ADR: there's no automatic type-directed selection anywhere in
  error propagation now, so that specific ambiguity risk doesn't arise here.
  Ambiguity between two explicitly-named `conversion` declarations (e.g. from
  ADR 0004's bidirectional conversions) is a separate, narrower question,
  still open on its own terms.
- Adapter-level error translation (how a finished workflow error type like
  `CheckoutError` becomes an HTTP status code or CLI exit code) is a
  genuinely separate, still-open question this ADR doesn't answer — flagged
  during this discussion, worth its own decision record later.
- The `checkout` example above is new ground (cross-domain composition) that
  none of the original eleven examples cover — worth adding to `examples/`
  now that the shape is decided.
