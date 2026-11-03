# ADR 0053: Sequential `for` Loops over `List<T>` and Typed Local Reassignment

Status: Accepted (2026-10-16, on the author's approval of the open questions;
see "Decisions taken at review"). Supersedes the earlier `fold` expression
draft of this record (rejected in review). Amends ADR 0023 (see Relation to
ADR 0023).
Date: 2026-10-16
Related: ADR 0016 (one `concurrent` syntax), ADR 0023 (no language-level
mutation of values), ADR 0040 (`enum`), ADR 0046 §7 (list literals open).
Milestone 7 deliverable: "Sequential iteration over `List<T>`."

## Context

`concurrent(x in xs): body` is the only construct that walks a `List`. It is
bounded-parallel I/O over a capability call, so a pure `rule` cannot validate
every item in a list or sum a list of amounts. The spec records this as a gap.

The design goal is a plain sequential loop, shaped like Java's enhanced `for`
(and C#'s `foreach`, Python's `for`, Go's `range`, Rust's `for`). These
languages all pair a for-each statement with a mutable local for
accumulation. Fiig has no reassignment today, so accumulation is impossible
without one of the decisions below.

## Decision

### 1. The loop is a statement over `List<T>`

```
for amount in amounts {
    total = total + amount
}
```

- The iterable must be a `List<T>`. The loop variable (`amount`) is a
  read-only binding of type `T`.
- Iteration is sequential, left to right, once, in list order.
- A `for` is a statement, so it is legal in any block. It is legal in
  `rule`, `conversion`, and `formula`, because a loop over local bindings is
  pure whenever its body is pure. In a `step` or `activity` the body may call
  capabilities. Sequential I/O inside a `for` carries no concurrency budget;
  `concurrent` remains the only bounded-parallel form.
- `for` carries no `BatchResult`. It is not a substitute for `concurrent`.

### 2. Locals are reassignable only when declared with a type

There is no `var`. A local is declared with a type annotation and an
initializer:

```
total: int = 0
```

Reassignment is a separate statement that names an existing reassignable
local:

```
total = total + amount
```

Rules for reassignment:

- Only a local declared with `name: Type = expr` may be reassigned. A plain
  `name = expr` binding stays immutable, as today, and reassigning it is an
  error.
- The assigned expression's type must equal the declared type. The checker
  reports a mismatch as it does for any binding.
- Reassignment targets a local in the same function. It cannot reassign a
  parameter, a model field, or anything in the registry.
- Reassignment inside a `concurrent` body is an error. Concurrent bodies run
  in parallel, so a write to a shared local is a race.

### 3. Model values never change

A `Customer` value is still immutable. Changing one still means `copy(...)`
(ADR 0023). Reassignment changes which value a *name* refers to. It never
changes a value in place. `total` goes from one `int` to the next `int`; the
old `int` is untouched.

Reading a field of a model value is always allowed, including on a loop
variable. Models are immutable records, as in Java, so a loop over
`List<Order>` can read `order.amount` and `order.status` freely. Summing a
field and validating a property are the primary use cases, and both are
reads plus a reassignment of a local:

```
rule orderTotal(orders: List<Order>) -> int:
    total: int = 0
    for order in orders {
        total = total + order.amount
    }
    return total

rule allAmountsPositive(orders: List<Order>) -> bool:
    ok: bool = true
    for order in orders {
        if order.amount < 0 {
            ok = false
        }
    }
    return ok
```

Neither example uses `return` inside the loop, so both work whatever the
answer to open question 2.

## Relation to ADR 0023

ADR 0023's title is "No Language-Level Mutation." Its decision body is about
ownership machinery: no `owned` parameters, no `mutate` block, no move
semantics, and in-place update left to the runtime. It does not address a
local name being rebound to a new value, and it did not intend to.

This ADR therefore narrows 0023 as follows: **values are immutable; a
function-local name may be rebound, and only when declared with a type.**
The title of 0023 is misleading after this change and should be amended to
match. No ownership or uniqueness concept is added.

## Alternatives considered

**A. `var` declarations.** Rejected by the user. A type annotation is the
marker of a reassignable local instead.

**B. Expression form (`fold`).** The earlier draft. Rejected: it isn't a
for-each, and it is not how the languages above shape the construct.

**C. Effects-only loop, no reassignment.** Keeps 0023 untouched, but pure
rules still cannot sum or validate a list, which is the gap this ADR closes.

### 4. Early exit: `return`, `break`, `continue`, and labels

- `return expr` inside a `for` returns from the enclosing declaration, as it
  does anywhere else in a block. Nothing is returned from the loop itself.
- `break` exits the innermost enclosing `for`. `continue` skips to the next
  element of the innermost enclosing `for`.
- Labels follow Java: a `for` may be prefixed with `label:`, and
  `break label` or `continue label` targets that loop from any depth inside
  it.

```
outer: for order in orders {
    for line in order.lines {
        if line.quantity < 0 {
            break outer
        }
    }
}
```

- `break` and `continue` are statements. They are legal only inside a `for`.
  A label must name an enclosing `for` in the same function. Labels are
  unique within a function, and a label may not repeat an enclosing label.
- A `break` or `continue` may not cross a `concurrent` body boundary. The
  body of a `concurrent` block runs in parallel, so a jump out of it is
  rejected by the checker. `return` inside a `concurrent` body is governed by
  the existing `concurrent` rules and is not changed here.

### Decisions taken at review

- Reassignment is spelled `name: Type = expr` to declare and `name = expr` to
  reassign. There is no keyword.
- Early `return` from a `for` is allowed.
- `break` and labeled `break`, Java-style, are allowed.
- `continue` is included with labels, for the same reason.
- The loop variable is read-only.
- The ADR 0023 title is amended (see Relation to ADR 0023).

## Open questions

1. **Map and build a list.** Resolved by ADR 0057 (2026-10-20): start
   from `ids: List<string> = []` and `ids = append(ids, x)` in the loop.
   Original note: building a `List<R>` needs list literals or an
   append operation (ADR 0046 §7). Not in this ADR.

## Consequences

- Grammar: a `for-statement` production and an `assignment` statement. The
  typed declaration `name: Type = expr` is a new binding form.
- Parser, syntax, and formatter gain `ForStmt`, `AssignStmt`, and a typed
  binding. The formatter prints the block form.
- Checker: element type from `List<T>`; reassignability from the declaration;
  reassignment type must match; reassignment in a `concurrent` body is an
  error.
- Runtime: a sequential loop over `List.Elems` in `compiler/runtime`, with
  local frames updated in place for reassignable names only. No worker pool.
- Corpus: an attested example in `examples/` (a sum in a rule, and a
  validate-all loop) before the Milestone 7 exit criterion is claimed.
  Done (2026-10-17): `order-processing/rules/total_of_orders.fiig` and
  `all_orders_have_positive_totals.fiig`, both run by
  `compiler/runtime`'s `TestCorpusForLoopRules`.
- ADR 0023's title and status line to be amended when this is accepted.
