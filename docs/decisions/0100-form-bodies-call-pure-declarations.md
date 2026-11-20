# ADR 0100: A Form's Body May Call What Its Declaration May, Minus Effects

Status: Accepted (2026-11-20). Implemented 2026-11-20.
Date: 2026-11-20
Amends: ADR 0093 §2 (list form bodies), ADR 0065 §3 (sorted keys).
Related: ADR 0095 §3 (mapErr's rule, which this generalises),
abs-recommendations GAPS G31.

## Context

ADR 0093 let a list form's body call only what a rule may: formulas and
methods. A rule calls no rule or conversion (ADR 0029), so a body can't
call the conversion that already turns one thing into another:

```
books = map(item in found.results): bookOfListed(item)
// [B7] a map body may not call conversion "bookOfListed"
```

abs-recommendations wrote a loop-and-append conversion per element type
instead (`booksOfListed`, `booksOfHits`). ADR 0095 gave `mapErr` the wider
rule and left this as its open question 1; G31 is the case.

The point of ADR 0093's rule was that a form has no effect: a `filter`
in an activity can't call a gateway. That needs only "no effects", not
"only what a rule may".

## Decision

Every form's body or key — `filter`, `map`, `groupBy`, `count`, `any`,
`all`, `sum`, `max`, `min`, their `OrNone` forms, `sorted`, and `mapErr`
— may call **what the enclosing declaration may, minus anything with an
effect**:

| Enclosing | A body may call |
|---|---|
| rule, formula | formulas and methods (unchanged: what the rule may) |
| conversion, activity, gateway, workflow, test | rules, conversions, formulas, methods |

A gateway, an activity, a workflow, or a capability is never callable
from a body (B-class error, as today). So:

```
conversion booksOf(found: AbsSearch) -> List<Book> {
    return map(item in found.results): bookOfListed(item)
}
```

## Rationale

- **The guarantee that mattered is kept:** a form has no effect, and
  every call in it has a name in the call graph.
- **One rule for all forms**, the one `mapErr` already has, instead of
  two.
- **A rule stays a rule.** Inside a rule a body still calls only what the
  rule may; ADR 0029's tiers don't move.

## Alternatives considered

- **Let a rule call a rule** (G31's other suggestion). Reopens ADR 0029's
  tiers for every rule, not just forms.
- **Keep the narrow rule.** Costs a hand-written loop per element type.

## Consequences

- G31 closes; the app's loop-and-append conversions can go.
- A `sorted` key in an activity can now call a conversion. It's still
  evaluated once per element, before any comparison (ADR 0065 §3).
