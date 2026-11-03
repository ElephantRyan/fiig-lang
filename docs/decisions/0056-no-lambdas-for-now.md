# ADR 0056: No Lambdas or Function Values, For Now

Status: **Accepted** (2026-10-19).
Date: 2026-10-19
Amends: `fiig-lang.md` §8 item 14 (lambdas stop being an open question).
Related: ADR 0046 (enum dispatch; list literals deferred in §7), ADR 0043
(capability contracts), ADR 0047 (cites first-class functions as "a closed,
unplanned axis"), ADR 0049 (rejected the keyword `function` for the same
reason), ADR 0053 (`for` loops), ADR 0054 (private helpers).

## Context

`fiig-lang.md` §8 item 14 asks whether lambdas belong in the language. ADR
0054 answered the other half of that item, private helpers, and left lambdas
open.

A lambda is usually wanted for one of two things:

1. **Naming a small piece of logic close to where it is used.** ADR 0054's
   private helpers now do this. A helper is named, visible only in its file,
   and takes on its file's kind and effects.
2. **Passing behaviour as a value.** A predicate given to `filter`, a key
   given to `sort`, a callback. Nothing in fiig does this today, and it is the
   question this ADR answers.

Several records already assume the answer is no. ADR 0047 rejected generators
partly because "fiig has no first-class functions (recorded as a closed,
unplanned axis)", but no ADR actually recorded it. ADR 0049 rejected the
keyword `function` because it "implies first-class functions, which fiig does
not have". §8 item 13 settled extension through enum dispatch (ADR 0046) and
capability packages (ADR 0043), "not first-class functions". Item 14 is the
only place that still lists lambdas as open. This ADR makes the records agree
and states the reasons in one place.

### Corpus sweep (2026-10-19)

64 `.fiig` files, 5 of which use a `List`.

**Two `for` loops** (ADR 0053), with how each would read with lambdas:

| Today | With lambdas |
|---|---|
| `total: int = 0`, then `for order in orders { total = total + order.total }` | `orders.map(o -> o.total).sum()` |
| `for order in orders { if order.total <= 0 { return false } }`, then `return true` | `orders.all(o -> o.total > 0)` |

The lambda forms are shorter, not clearer. The loops need nothing new in the
type system.

**Three workflows already map without a lambda:**

```
concurrent(customerId in customerIds):
    getCustomer(customerId)
```

The body is an expression over the loop variable: the shape of a lambda
body, built into the syntax instead of passed as a value.

**No case passes behaviour as a value.** No predicate parameter, no sort
key, no callback.

The absence is weak evidence on its own: iteration only arrived with ADR 0053
two days before the sweep, so the corpus was written by someone who could not
loop. The case rests as much on the costs below as on the count.

## Decision

1. **Fiig has no lambdas and no function values.** No function literals, no
   function types, no passing a declaration as an argument. A call names its
   callee.
2. **Local named logic is a private helper** (ADR 0054).
3. **Per-element work is a statement or a syntax form, not a function
   argument.** `for` (ADR 0053) covers sequential work: sums, validation,
   early exit. `concurrent(x in xs): ...` covers parallel mapping. If a
   common list operation becomes painful, the first answer is a syntax form
   whose body is an expression over a named element, like `concurrent` and
   like the precedent below, before function values are reconsidered.
4. **Dispatch over an owned set is an enum** (ADR 0046). **Effectful
   extension by a third party is a capability** (ADR 0043).

## Rationale

**Function types carry the hardest question in fiig: effects.** Every call
in fiig is judged by the kind of its callee (§3.2). A function value hides
the callee. A rule that takes a function argument is either allowed to call
whatever is passed, which breaks rule isolation, or its parameter type must
carry an effect ceiling (`(Order) -> bool` that is pure, versus one that may
reach a gateway). That is an effect system in the type system, which fiig has
avoided by putting effects on declaration kinds instead.

**Function values need generics to be worth having.** `map`, `filter`, and
`sort` are generic in their element type. Fiig has no user-declarable
generics. Monomorphic function values (`(Order) -> bool` only) would cover
almost nothing.

**Capture collides with ADR 0053.** A lambda that captures a reassignable
local must either see later reassignments (shared mutable state, which
`concurrent` bodies already forbid) or copy at creation (Java's "effectively
final"). Either is a rule to learn for no attested benefit.

**The registry stops being a complete call graph.** Today a card's
dependencies are the names its file calls (ADR 0054). A call through a
function value has no name at the call site, so the registry, derived
purity (ADR 0044), and an LLM reading the catalog would all lose the edge.

**Precedent: business-rule languages replace lambdas with syntax forms.**
SQL has no function values: `WHERE`, `ORDER BY`, and the select list are
filter, sort, and map written as clauses over a row. DMN's FEEL, the
nearest relative of fiig's rule tier, has `for x in xs return e`,
`some x in xs satisfies p`, `every x in xs satisfies p`, and filters written
as `xs[p]`. It reaches for a function literal in one place: `sort`, which
takes a comparison function. Excel went 35 years without `LAMBDA`. So the
domain's own experience points the same way, and also names the likely
pressure point.

## Alternatives considered

**A. Lambdas with function types, effect-annotated.** Rejected now for the
reasons above: an effect system in types, blocked on generics, and no
attested use.

**B. Monomorphic function values without generics.** Rejected: pays the
effect and registry costs while covering almost no real case.

**C. Leave the question open.** Rejected: three ADRs already reason as if it
were closed, and an open item invites a feature to slip in through a review
comment.

## When to revisit

Revisit this ADR when either of these is attested in `examples/`:

1. **Sort by key** (orders by total, customers by name) needed more than
   once, where no fixed ordering on the element type works. FEEL's one
   function literal is here, so it is the most likely trigger. The first
   option to weigh even then is a syntax form (`sort orders by total`), not a
   general function value.
2. **Pure policy supplied by a third party**, §8 item 13's still-open case: a
   discount policy shipped by a package that cannot edit the caller's enum.
   That is interface work (ADR 0043's `capability` → `interface` direction),
   and bounded generics will force the question there.

## Consequences

- No grammar, checker, or runtime change.
- On acceptance, `fiig-lang.md` §8 item 14 is marked resolved for lambdas,
  pointing here; private enums remain its only open part.
- ADR 0047's "recorded as a closed, unplanned axis" now has a record to
  point to.
- The gap the sweep did find, building a list in a loop (ADR 0053 open
  question 1), is list literals and append (ADR 0046 §7). That is next and
  is independent of this ADR: a lambda-based `map` would need it too.
