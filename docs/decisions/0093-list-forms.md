# ADR 0093: List Forms: `filter`, `map`, `groupBy`, and Aggregates

Status: Accepted (2026-11-19). Implemented 2026-11-19; see Implementation
notes.
Date: 2026-11-19
Amends: `grammar.md` (new forms; `sorted` keys), ADR 0065 §3 (more than
one key; `descending` moves to the key; open question 1 closed) and §6
(`m.atOr`).
Related: ADR 0023 (values are immutable), ADR 0049 §2.6 (closed method
tables), ADR 0052 (comparability and ordering), ADR 0056 (no lambdas; §3
names this answer), ADR 0057 (A13, discarded results), ADR 0063
(overflow faults), ADR 0064 (decimal), abs-recommendations GAPS G3 and G4.

## Context

abs-recommendations is the first program written in fiig by someone
trying to ship it. Its ranking rule has to "group ratings by item, sum,
count and take the newest per group, then sort by three keys". In fiig
today it takes three parallel maps, two private helpers that only say
"absent means 0", and three `sorted` forms written least important key
first (GAPS G3, G4):

```
sums: Map<string, int> = [:]
counts: Map<string, int> = [:]
latest: Map<string, int> = [:]
for rating in ratings {
    if rating.recommended {
        sums = sums.put(rating.itemId, sumSoFar(sums, rating.itemId) + rating.score)
        counts = counts.put(rating.itemId, sumSoFar(counts, rating.itemId) + 1)
        latest = latest.put(rating.itemId, newer(latest, rating.itemId, rating.updatedAt))
    }
}
...
byLatest = sorted(item in items, descending): item.latest
byCount = sorted(item in byLatest, descending): item.count
return sorted(item in byCount, descending): item.average
```

The app's log says every aggregation it has will repeat this. ADR 0056
turned down lambdas and said what to do when this happened: "the first
answer is a syntax form whose body is an expression over a named element,
before function values are reconsidered." fiig has two forms of that shape:
`concurrent(x in xs): ...` and `sorted(x in xs): key`. The owner chose this
route over function values (2026-11-19).

### How others do it

| | filter | transform | group | aggregate |
|---|---|---|---|---|
| Kotlin | `xs.filter { it.ok }` | `xs.map { it.id }` | `groupBy { it.k }` | `sumOf { }`, `maxOf { }` |
| C# LINQ | `where x.Ok` | `select x.Id` | `group x by x.K` | `Sum(x => x.V)` |
| Python | `[x for x in xs if x.ok]` | `[x.id for x in xs]` | `itertools.groupby` (sorted input) | `sum(x.v for x in xs)` |
| SQL | `WHERE` | `SELECT` | `GROUP BY` | `SUM`, `COUNT`, `MAX` |
| DMN FEEL | `xs[item.ok]` | `for x in xs return x.id` | none | `sum(for x in xs return x.v)` |

Python, SQL and FEEL have no function values in these places: the body is
written where it's used. Python's comprehensions and FEEL's `for ... return`
are the nearest to fiig's forms. Kotlin's names are the best known, so
this ADR uses them.

## Decision

### 1. Eleven forms, all shaped like `sorted`

```
filter(x in xs): cond        // List<T>: the elements where cond is true
map(x in xs): expr           // List<U>: expr for each element
groupBy(x in xs): key        // Map<K, List<T>>
count(x in xs): cond         // int: how many elements have cond true
any(x in xs): cond           // bool: true if cond holds for one; false on []
all(x in xs): cond           // bool: true if cond holds for each; true on []
sum(x in xs): expr           // int, float or decimal; 0 on []
max(x in xs): key            // the largest key; faults on []
min(x in xs): key            // the smallest key; faults on []
maxOrNone(x in xs): key      // the largest key, or None on []
minOrNone(x in xs): key      // the smallest key, or None on []
```

`max` and `min` each have an `OrNone` partner, as
`first` does (ADR 0065 §1, ADR 0063). The ranking rule becomes:

```
rule rankRecommendations(ratings: List<Rating>) -> List<RankedItem> {
    recommended = filter(r in ratings): r.recommended
    groups = groupBy(r in recommended): r.itemId
    items: List<RankedItem> = []
    for itemId, group in groups {
        items = append(items, RankedItem(
            itemId: itemId,
            average: math.divide(math.toDecimal(sum(r in group): r.score), math.toDecimal(group.length()), places: 2, rounding: Rounding.HalfUp),
            count: group.length(),
            latest: max(r in group): r.updatedAt,
        ))
    }
    return sorted(item in items): item.average descending then item.count descending then item.latest descending
}
```

No helpers, no parallel maps. A group from `groupBy` is never empty, so
`max` can't fault there.

### 2. What a body may do

The rules are `sorted`'s (ADR 0065 §3), for every form:

- **A body may call only what a rule body may**: formulas and
  closed-table methods, whatever kind of declaration it sits in. A `filter`
  in an activity can't call a gateway, so none of the forms has an effect,
  and the registry sees every call by name, as it does today.
- **A body is evaluated once per element**, in list order.
- **The names a body can see** are the element's and the enclosing
  scope's. A body can't assign. A form is a value, never a function, so
  nothing is captured and nothing outlives the expression. ADR 0056's four
  costs don't arise.
- **`?` in a body returns from the enclosing declaration**, as it does in a
  `sorted` key and a `for` body.

### 3. Types

- `filter`, `count`, `any`, `all`: the body is `bool` (A1).
- `map`: `List<U>`, where `U` is the body's type. `map(o in orders):
  check(o)` is a `List<Result<...>>`, and the A13/F1 checks apply to it as
  to any list.
- `groupBy`: the key must be `Comparable` (ADR 0052; `Map`'s rule, ADR 0065
  §4). Keys are in order of first appearance, and each group keeps list
  order (ADR 0065 §7).
- `sum`: the body is `int`, `float` or `decimal`. A constant body takes
  `int`, so `sum(x in xs): 1` counts. `int` overflow faults (ADR 0063), a
  decimal sum is exact or faults (ADR 0064), and a float sum adds left to
  right.
- `max`, `min`: the key must be ordered (M3), as a `sorted` key must. The
  result is the key, not the element: `max(r in group): r.updatedAt` is an
  `int`. To get the element, take the first of a `sorted`.
- A form whose result is dropped is A13, as `sorted` is today.

### 4. `sorted` takes keys joined by `then`, each with its own direction

```
sorted(o in orders): o.region then o.total descending
```

ADR 0065 §3 allowed one key, with `descending` in the header for the whole
sort. Now `then` joins keys, compared in order, and `descending` follows
the key it applies to. Mixed directions, like region A to Z and then the
largest total first, are common, and the header form couldn't say them.

The separator is `then` rather than a comma, because a comma would mean two
arguments when a `sorted` sits in a call: `f(sorted(x in xs): a, b)`. LINQ
uses a comma, but its `orderby` never sits inside an argument list.

The header's `descending` is removed. It still parses, so the checker can
say where the word goes now (A1), and `fiig fmt` rewrites
`sorted(x in xs, descending): k` as `sorted(x in xs): k descending`. The
two uses in fiig-lang's corpus and docs move in the same change; the five
in abs-recommendations are that repository's to run `fiig fmt` on.

### 5. `m.atOr(key, default)`

```
m.atOr(k, d)        // V: the value at k, or d when k is absent
```

GAPS G3's second request. Python's `dict.get(k, d)` and Kotlin's
`getOrDefault`. A count kept by hand reads `counts.put(id, counts.atOr(id,
0) + 1)`. `d` must be a `V` (A1). `List` doesn't get it: an index that's
out of range is a bug, not a default.

### 6. The names stay free

As with `sorted`, each form starts only at `name ( ident in`, which no call
can begin with, because `in` is a keyword. A rule named `count`, a local
named `max`, `xs.filter` and `math.max(a, b)` stay legal. The IntelliJ
keyword list doesn't change.

## Rationale

- **Every form is a `for` loop the compiler can see whole.** A body is an
  expression, checked where it's written, under the rules of a rule body.
  No function type, no generics in user code, nothing captured. That keeps
  everything ADR 0056 kept.
- **Kotlin's names**, because they are what most readers already know, and
  because an LLM writing fiig will reach for them first.
- **`max` returns the key, as SQL `MAX` and Kotlin `maxOf` do.** Returning
  the element (Kotlin `maxBy`) would need a rule for ties; `sorted` already
  has one (stable), so the element is `(sorted(...): k descending).first()`.

## Alternatives considered

- **Function values (lambdas).** ADR 0056's costs: effects in function
  types, generics, capture, call-graph edges. The owner chose this ADR's
  route instead. Still open later if a program has to pass its own policy.
- **Arrow spelling at the call: `xs.filter(r -> r.recommended)`.** It reads
  like a lambda, so readers expect to store one. The `in` form matches
  `sorted` and `concurrent`, and doesn't look like a value.
- **One general comprehension**, Python's `[f(x) for x in xs if c(x)]`. One
  form instead of eleven, but group and aggregates still need names, and
  `for` already means a statement in fiig.
- **A comma between `sorted` keys.** Ambiguous inside a call (§4).
- **Keeping the header `descending` beside the per-key one.** Two ways to
  say the same thing. Seven uses is a small move.
- **`groupBy` returning `List<Group>`**, a list of records. It needs a
  prelude record type; a `Map` already iterates as `key, value` (ADR 0065
  §7).

## Consequences

- GAPS G3 and G4 close. The ranking rule loses its two helpers and three maps.
- New contextual words: eleven form names and `then`. No new reserved words.
- The runtime evaluates each form eagerly, like `sorted`. A chain like
  `sum(x in filter(...))` builds the inner list; nothing streams.
- `grammar.md`, `tour.md`, and the IntelliJ plugin's highlighting of
  `sorted` gain the new forms. *(Highlighting them in IntelliJ was never
  done; it is improvement idea B7. This line named "ADR 0092's open item
  C", an ADR that was never written; see ADR 0092.)*

## Open questions

1. **Two names over a `Map`**: `map(k, v in m): ...`, `filter(k, v in m):
   ...`. `for` already takes two names (ADR 0065 §7). Waits for a real case.
2. **An index**: `map(i, x in xs)`. ADR 0065's open question 2, still waiting.
3. **`distinct` / `distinctBy`.** Not needed by any program yet.

## Implementation notes

Built 2026-11-19.

- **Syntax.** `syntax.ListForm` holds a form's name, element, iterable,
  and body; `syntax.ListForms` is the set of names. `SortedExpr` now holds
  `Keys []SortKey` and `HeaderDescending`, which marks the old spelling.
  The parser takes `then` and `descending` only on the line their key ends
  on, so a statement starting `descending = ...` on the next line stays a
  statement.
- **Checker** (`compiler/checker/listform.go`). The old `inSortKey` flag is
  now `inForm`, the form's name, so a gateway called from a `filter` body
  says "a filter body may not call gateway ...". A `sum` body that is a
  constant lands as `int`, and the checker records a sum's type on the
  node, which is the type of the `0` an empty list sums to.
- **Runtime** (`compiler/runtime/listform.go`). `sum` adds through the
  same code as `+`, so overflow faults and decimal exactness are `+`'s.
  `any` and `all` stop at the first element that decides them. `groupBy`
  is linear: it keeps each group's elements in a slice and builds the map
  once.
- **Dogfood.** abs-recommendations' `rankRecommendations`, on a copy,
  became 12 lines with no helpers or parallel maps, and its 8 tests pass.
  `for itemId, group in groupBy(r in recommended): r.itemId {` parses as
  written.
