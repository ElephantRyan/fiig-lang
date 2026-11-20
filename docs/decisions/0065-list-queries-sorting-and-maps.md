# ADR 0065: List Queries, Sorting, and `Map`

Status: Accepted (2026-10-29, one-decision-at-a-time review). Amended by
ADR 0093: §3 takes more keys joined by `then`, `descending` moves onto each
key, open question 1 is closed, and §6's `Map` gains `atOr`. Implemented
2026-11-03 in four steps; see Implementation notes. Two pieces wait: a map
literal as a constant (§5's last sentence), and a JSON object's member
order when decoded into a `Map`, which is ADR 0062's.
Date: 2026-10-28
Amends: `grammar.md` (map literals, `sorted` form, two-name `for`), ADR 0057
(A12 and A13 widen).
Related: ADR 0023 (values are immutable), ADR 0049 §2.6 (closed method tables;
`List`'s four methods), ADR 0052 (comparability and ordering), ADR 0053 (`for`),
ADR 0056 (no lambdas; sort by key is its named revisit trigger), ADR 0057
(list literals, `append`, claim-the-slot), ADR 0062 (JSON numbers, paused; a
JSON object needs a map), ADR 0063 (the `OrNone` convention), ADR 0064 (decimal;
open question on decimal map keys).

## Context

ADR 0049 §2.6 gave `List<T>` four methods, `length()`, `at(i)`, `atOrNone(i)`,
and `slice(a, b)`, and sent the rest to this ADR: `first`, `last`, `contains`,
sorting, and `Map`. The method table is closed (0049 §2.6), so nothing here can
be added by a library until fiig has generics.

What a program has to write today:

| Task | Today |
|---|---|
| first element, if any | `xs.atOrNone(0)` (once 0049 §2.6 is built) |
| last element, if any | `xs.atOrNone(xs.length() - 1)` |
| is `x` in `xs` | a `for` loop with an early `return` |
| sort | not possible: a loop can't place an element without a sort written by hand |
| look a value up by key | a `List` of records scanned with a `for` loop |

**Map is the larger gap.** fiig has no keyed collection. ADR 0062 needs one:
a JSON object is a set of named members, and decoding one into anything but a
declared model means a map. HTTP headers, counts per category, an index built
from a list before a lookup loop, and a translation table loaded at run time are
the ordinary cases. ADR 0046's enums cover lookups whose keys are known when the
program is written; a map covers keys that arrive with the data.

**Corpus.** No `.fiig` file needs these yet. The three `for` loops in
`examples/` sum, validate, and filter. That says little: loops arrived with ADR
0053, lists with ADR 0057, and the corpus was written by authors who could not
yet do any of this. The case rests on 0049 §2.6's deferral and 0062's need.

**ADR 0056's constraint.** There are no lambdas, so `sortBy { it.total }`,
`filter`, and `map` taking functions are not available. 0056 names sort by key
as the most likely reason to revisit it, and says the first answer should be a
syntax form whose body is an expression over a named element, as `concurrent`
already is.

## Decision

### 1. `first`, `last`, and `contains` on `List<T>`

```
xs.first()          // T; faults if xs is empty
xs.firstOrNone()    // T | None
xs.last()           // T; faults if xs is empty
xs.lastOrNone()     // T | None
xs.contains(x)      // bool; requires Comparable(T)
```

The pairs follow `at`/`atOrNone` (0049 §2.6) and Kotlin's `first`/`firstOrNull`.
`contains` uses `==`, so it needs the same thing `==` does: a `List<float>` or a
list holding a `Result` or a handle is M1, with a diagnostic that points to a
`for` loop with `math.closeTo` for floats. A `List<decimal>` compares by value
(ADR 0064 §5), so `[1.50].contains(1.5)` is true.

Every mainstream language allows `contains` on a float list (Python, Java,
Kotlin, JavaScript, Go, Rust, Swift), and they disagree on NaN and `-0.0`: Java's
`Double.equals` finds NaN where its `==` doesn't, and JavaScript's `includes` and
`indexOf` differ the same way. Fiig has removed both cases (NaN faults and
`-0.0` is normalized, ADR 0048), but `[0.1 + 0.2].contains(0.3)` is still false,
and that rounding is why ADR 0051 refused float `==`. Allowing `contains` would
bring float equality back through a method name, the hole ADR 0052 closed for
structural equality.

**Float counterparts.** Every other primitive gets the whole of this ADR, so
floats get a counterpart wherever `==` blocks them. These go in the prelude
`math` group (ADR 0049 §2.4), beside `closeTo` and `isZero`, and use
`closeTo`'s rule with both tolerances required (ADR 0051):

| Blocked for floats | Counterpart |
|---|---|
| `xs.contains(x)` on `List<float>` | `math.containsClose(xs, x, relative:, absolute:)` → `bool` |
| `xs == ys` on `List<float>` (ADR 0057) | `math.allClose(xs, ys, relative:, absolute:)` → `bool`: same length, each pair close |
| `m1 == m2` on `Map<K, float>` (§8) | `math.allClose(m1, m2, relative:, absolute:)` → `bool`: same keys, each value close |
| `Map<float, V>` (§4) | none; see §4 |

The names are NumPy's (`allclose`; `isclose` is already `closeTo`). `allClose`
accepts a list or a map of any key type, so the checker types it the way it
types `append`; neither can be written in fiig until fiig has generics. The M1
diagnostic for `contains` and `==` names the counterpart.

These cover lists and maps whose elements are floats. A list of models with a
float field is still M1 for `contains` (ADR 0052's transitive rule), and no
float formula reaches inside a model; see open question 3.

### 2. `xs.sorted()` and `xs.sortedDescending()` return a new, sorted list

`T` must be ordered (ADR 0052 §2.4: `int`, `string`, `float`, `decimal`), or the
call is M3. The sort is **stable**: elements that compare equal keep their
order. For most ordered types that can't be observed, but for `decimal` it can,
since `1.5` and `1.50` are equal and print differently (0064 §5).
`sortedDescending()` is stable too; it is not `sorted()` reversed, which would
reverse ties.

The name is `sorted`, not `sort`, for the reason ADR 0057 chose `append` over
`.append`: every mainstream `xs.sort()` (Python, Java, Kotlin, Swift, Go,
JavaScript) changes the list in place, so `xs.sort()` as a statement would read
as working while doing nothing. Python's `sorted()`, Kotlin's and Swift's
`sorted()`, and JavaScript's `toSorted()` (added in ES2023 for this reason)
already mean "returns a sorted copy".

### 3. Sorting by key is a syntax form: `sorted(x in xs): key`

```
byTotal = sorted(order in orders): order.total
newestFirst = sorted(reading in readings, descending): reading.takenAt
```

The shape is `concurrent`'s (`grammar.md`: `concurrent '(' identifier 'in'
expression { ',' modifier } ')' ':' expression`), with one modifier,
`descending`. It answers ADR 0056's revisit trigger the way 0056 said to, with a
syntax form and not a function value. 0056 stays accepted, since nothing is
passed as a value.

- The key expression's type must be ordered (M3). A key of type `T | None` is
  not ordered; the author decides where `None` goes, for example with a `match`
  in a private helper.
- **The key is evaluated exactly once per element**, before any comparison, as
  with Python's `key=`. A key that costs something isn't paid for O(n log n)
  times.
- **The key may call only what a rule body may call**, formulas and
  closed-table methods (ADR 0049), whatever kind of declaration contains it. A key that reaches a gateway would make a sort
  effectful and its order depend on outside state.
- The sort is stable, so ties keep list order.
- `sorted(x in xs): x` is the same as `xs.sorted()`. Both exist because the
  plain case is common and shouldn't need a name for the element.

**One key per form.** Sorting by region and then by total is two sorts, the
second key first: `sorted(o in (sorted(o in orders): o.total)): o.region`.
Stability makes that correct, though it reads poorly; see open question 1.

**`sorted` is contextual, not reserved.** It begins the form only in
expression position when the next token is `(`; everywhere else it is an
ordinary identifier. Reserving it, as `concurrent` is, would break `xs.sorted()`
(§2), since a name after `.` must be an identifier or one of ADR 0018's
contextual keywords (`compiler/parser/parser.go`, `expectName`), and would take
`sorted` away as a local name, as in `sorted = xs.sorted()`. The rule can't be
ambiguous: there are no function values (ADR 0056), so a local named `sorted`
can never be called, and a formula is always called through its group's name.
ADR 0018 made the structural keywords contextual for the same kind of reason.

### 4. `Map<K, V>` is a prelude generic; `K` must be comparable

`Map<K, V>` joins `List`, `Result`, and `BatchResult` as a prelude generic. `K`
must be `Comparable` (ADR 0052 §2.1), so `Map<float, V>` and `Map<Result<…>, V>`
are M1. A key's hash is derived from the same structure `==` walks, so equal
keys always hash equally, and there is no `equals`/`hashCode` pair for an author
to get out of step (ADR 0057 §6 anticipated this).

**`float` keys have no counterpart.** Closeness isn't transitive: `a` close to
`b` and `b` close to `c` doesn't make `a` close to `c`, so no hash can put
close keys in one bucket. No language offers a map keyed by tolerance; NumPy
and pandas round or bin first. Fiig's way is the same: round to a decimal key
with `math.toDecimal` and `math.round` (ADR 0064), and use `Map<decimal, V>`.
The M1 diagnostic on a `float` key says so.

**`decimal` keys are allowed** (answers ADR 0064's open question). They hash by
value: trailing zeros are trimmed first, so `1.5` and `1.50` are the same key.
The map keeps **the key as first inserted**. If `m` has the key `1.50`,
`m.put(1.5, v)` replaces the value, and the key still prints as `1.50`. Python's
`dict` (`d[1.0] = x` after `d[1] = y` keeps the key `1`) and Java's `HashMap.put` do the same.

### 5. Literals: `["a": 1, "b": 2]` and `[:]`

```
limits: Map<string, int> = ["small": 10, "large": 500]
seen: Map<string, bool> = [:]
```

Swift's syntax. It reuses `[` `]` from ADR 0057, so it can't be confused with a
block, as `{` would be after `if x == `. Element typing follows ADR 0057 §2:
keys and values are inferred from the entries, with no implicit union, and the
empty literal `[:]` needs an expected type (A12). `[]` is an empty list and `[:]`
an empty map.

**Duplicate keys in a literal are an error.** When both keys are constants, it
is a compile error. Otherwise it is a fault at run time, as in Swift. A literal
lists distinct entries; a repeated key is a mistake, and keeping the last
(Python, JavaScript) loses one silently. The fault's message names the key and
the literal's source position. Faults can't be caught (`fiig-lang.md` §3.6,
ADR 0063), so code that expects repeated keys builds the map with `put` or `putIfAbsent` (§6)
instead of a literal. A map literal is a constant when every
key and value is one, on ADR 0057 §7's terms.

### 6. Operations return values; a map is never changed in place

```
m.at(k)             // V; faults if k is absent
m.atOrNone(k)       // V | None
m.containsKey(k)    // bool
m.length()          // int
m.keys()            // List<K>, in iteration order
m.values()          // List<V>, in iteration order
m.put(k, v)         // Map<K, V> with k set to v, added or replaced
m.putIfAbsent(k, v) // Map<K, V> with k set to v only if k is absent; else m
m.remove(k)         // Map<K, V> without k; unchanged if k is absent
```

`at` and `atOrNone` are `List`'s names: on a list the argument is a position, on
a map a key. `containsKey` is not `contains`, so that `xs.contains(x)` (an
element) and `m.containsKey(k)` (a key) never look alike.

`at` is kept over the more familiar `get` because in Java, Kotlin, and Python
`get` is the lookup that *doesn't* fail (it returns `null` or `None`), so
`m.get(k)` would read as safe where fiig's faults. C++ uses `at` the same way
for both `vector` and `map`: one name for lookup by position and by key, and it
throws.

**The names are the familiar ones; each returns a new map.** `put`,
`putIfAbsent`, and `remove` are Java's and Kotlin's names, and immutable maps
mostly keep them: Elixir's `Map.put`/`put_new`/`delete`, Vavr's `put`/`remove`,
C#'s `ImmutableDictionary.Remove`, F#'s `Map.Add`/`Remove`. A bare `m.put(k, v)`
statement would read as working while doing nothing, but A13 (below) makes it a
compile error whose message teaches the fix: "`put` returns a new map; assign
it: `m = m.put(k, v)`". Java's `put` returns the previous value and fiig's
returns the map; confusing the two is a type error.

`putIfAbsent` tests whether the key is **present**, as Python and Elixir do,
not whether its value is `None`. Java's `putIfAbsent` treats a key mapped to
`null` as absent; in a `Map<K, V | None>` that would overwrite a stored `None`.
When the key is present, the map is returned unchanged, key spelling (§4) and
position (§7) included. Its value argument is always evaluated; the lazy forms
elsewhere (`computeIfAbsent`, `or_insert_with`, `put_new_lazy`) take a function,
which fiig doesn't have (ADR 0056). "Replace only if present" (Java's `replace`,
Elixir's `Map.replace`) is not in the table: it is
`if m.containsKey(k) { m = m.put(k, v) }`.

**A13 widens to every closed-table method.** Discarding the result of `append`
is A13 today. Every method in 0049 §2.6's tables, on `string`, `bytes`, `List`,
and `Map`, is pure, so a bare `m.put(k, v)` or `xs.sorted()` statement does
nothing and is an error, the same rule as `append`.

**`atOrNone` and `V | None`.** When `V` itself admits `None`, `atOrNone` can't
tell "absent" from "present and `None`". Kotlin's `getOrNull` has the same
limit. Use `containsKey` first. The same is true of `List<T | None>.atOrNone`.

### 7. Iteration is in insertion order; `for key, value in m`

```
for sku, count in stock {
    if count == 0 {
        missing = append(missing, sku)
    }
}
```

`for` over a map binds two names; `_` discards one. `for key in m` is an error
whose message suggests `for key in m.keys()` or `for value in m.values()`.
Languages disagree on what one name over a map means (keys in Python and Go,
entries in JavaScript and Kotlin), so fiig makes the author say which. The
error can later be relaxed to Python's meaning without breaking code that
compiles; the reverse would break it. Iteration is in **insertion order**: a new key goes at
the end, `put` on an existing key keeps its position, and `remove` removes the
key and leaves the others in order. These are the rules of Python's `dict`,
JavaScript's `Map`, and Kotlin's default maps.

Go randomizes iteration order deliberately, so code can't come to depend on it;
Java, Rust, and Swift leave it unspecified. Fiig specifies it because a run that
differs from the last one, with nothing changed, is feedback an author or an LLM
can't act on. A JSON object (ADR 0062) also round-trips with its members in the
order they arrived.

### 8. `==` on maps ignores order

`Map<K, V>` is comparable when `V` is (`K` always is, §4). Two maps are equal
when they have the same keys and equal values for each, in any order, as in
Python, Java, and Kotlin. So two equal maps can iterate in different orders,
just as two equal decimals can print differently (ADR 0064 §5). Maps are not
ordered.

### 9. Cost: every operation on every map is O(log n)

As with ADR 0057 §3, cost is part of the language:

- `at`, `atOrNone`, `containsKey`, `put`, `putIfAbsent`, and `remove` cost
  O(log n) or better **on every map value**, not only the newest one built
  from a given map. A hash trie (Clojure, Scala, and Elixir's large maps) gives
  O(log₃₂ n).
- Building a map of n entries with `m = m.put(k, v)` costs O(n log n) or
  better, never O(n²).
- Iteration, `keys()`, and `values()` cost O(n).
- Sorting costs O(n log n) comparisons, and `sorted(x in xs): key` evaluates the
  key n times.

**Hashes are seeded per process.** Map keys often come from outside, such as
JSON members and headers. Python (PEP 456), Rust, and Go seed their hashes at
random so that an attacker can't choose keys that all collide. Insertion order
lets fiig do the same without making iteration order vary. Fiig exposes no
`hash()` function, so the seed can't be observed and no program's output
depends on it.

**Why every map value, and not 0057's terms.** ADR 0057 §5 promised lists only
that building is fast: appending to an older list copies it. For maps,
branching from an older map is ordinary, as when one base configuration gets
a different value per request:

```
for region in regions {
    config = base.put("region", region.name)    // every put branches from base
}
```

Under 0057's terms each of those `put`s copies `base`, O(n), and the cost
appears only under that usage. The bound here rules it out.

The data structure is chosen when this is built, from a prototype measured as
ADR 0057 §5's was. A structure qualifies only if it meets the bound on every
map value. Scala 2.13's immutable `VectorMap`, a hash trie paired with a
persistent vector of keys in insertion order, shows the bound is reachable
with insertion order kept. 0057's claim-the-slot alone does not qualify,
since a `put` on an older map copies. The guarantee is the decision; the
structure is not.

## Rationale

**Why methods and not functions.** ADR 0049 §2.6 decided that operations on a
prelude type are entries in its closed method table. `append` is a builtin
function because ADR 0057 kept Go's builtin, whose name has an in-place prior
as a method. `put` and `remove` have that prior too and are methods anyway:
A13 now covers every closed-table method (§6), so the misreading 0057 guarded
against is a compile error, and the familiar name is worth more than a new
one. `append` stays as 0057 decided; it is Go's spelling, which is familiar in
its own right.

**Why a syntax form for keyed sort now.** A sort that takes only `List<int>` or
`List<string>` leaves out the common case, sorting records. ADR 0056 already
chose the shape, `concurrent`'s, and the precedent: FEEL needs a function
literal only for `sort`, and SQL's `ORDER BY` is a clause over a row. Writing the
form now is not attested demand; it is finishing the sort 0049 sent here.

**Why `Map` without `Set`.** `Map<K, bool>` or `List.contains` covers
membership until a set is attested. A set would add a literal syntax and a
second keyed type.

## Alternatives considered

**A. Only the `OrNone` forms of `first` and `last` (Swift's `first` is
optional).** Rejected for consistency with `at`/`atOrNone`, which are accepted.
`at(0)` would remain the faulting form anyway.

**B. `xs.sorted(descending: true)`.** Rejected: 0049 §2.6's methods take
positional arguments, and a `bool` argument is the kind of flag named arguments
exist to avoid. Kotlin uses `sortedDescending()`.

**C. Sort by a field path (`orders.sortedBy(.total)`).** Smaller than a syntax
form, but covers only a field. A key computed from two fields, or through a
formula, needs the expression form anyway.

**D. Map literals with braces (`{"a": 1}`, Python and JSON).** Rejected: `{`
opens a block, and `if x == {"a": 1} {` needs lookahead to parse and to read.

**E. Unordered or randomized iteration (Go, Rust, Swift).** Rejected in §7.
Go's reason, stopping code from depending on an order, doesn't apply when the
order is specified.

**F. Sorted maps (Rust's `BTreeMap`, Java's `TreeMap`).** These need an ordered
key, which most keys aren't (`bool`, `bytes`, models). `m.keys().sorted()`
gives sorted iteration when wanted.

Specialized maps, such as a sorted map, a bidirectional map, or a multimap,
would each be their own named type when one is attested (Guava's `BiMap` and
`Multimap`, Java's `TreeMap`), never a mode or variant of `Map`. `Map` always
means §4 to §9: comparable keys, insertion order, and order-insensitive `==`.

**G. Mutable maps with `m[k] = v`.** Rejected: values are immutable (ADR 0023).

**H. `with` and `without` (Rust's `im` crate; C#'s and Java's record
"withers").** The first draft's names, chosen because they don't suggest an
in-place change. Rejected 2026-10-29: `with` is mostly a record operation, not
a map one, and the names are unfamiliar to most developers, while A13 already
catches the misreading they guarded against (§6).

## Consequences

- `compiler/types`: `Map` joins the prelude generics; `Comparable` gets a
  `Map<K, V>` row (`Comparable(V)`) and an M1 check on `K` wherever a `Map` type
  is written.
- Lexer/parser: `:` inside `[` `]` makes a map literal; `[:]`; the `sorted`
  form with the `descending` modifier; `for a, b in m`.
- `grammar.md`: `map-literal`, `sorted-expr`, and the two-name `for`. `sorted`
  is a contextual word (§3): the lexer leaves it an identifier, and the parser
  starts the form on `sorted` followed by `(` in expression position.
- Checker: `List` method table gains §1 and §2; `Map` gets §6. `contains` is M1
  on a non-comparable element. `sorted()`, `sortedDescending()` and the form's
  key are M3 on an unordered type. The key may call only what a rule may.
  `[:]` without context is A12. A13 covers every closed-table method. Duplicate
  constant keys in a map literal get a new class.
- Runtime: a `Map` value with stable sorting, a seeded structural hash, and
  §9's costs; a fault on duplicate keys in a non-constant literal; `Equal` gets
  an order-insensitive `Map` case.
- ADR 0049 §2.4: the `math` table gains `containsClose` and `allClose` (§1).
  M1's messages for `contains`, `==` on a float list or map, and a `float` map
  key name the counterpart or the decimal-key route.
- ADR 0064: its open question on decimal map keys is answered by §4.
- ADR 0062: a JSON object can decode into a `Map<string, …>`. Which type, and
  how a member's order is kept, is 0062's to decide.
- Corpus: a keyed lookup built from a list (a count per category or an index
  by id), and a keyed sort of records.

## Open questions

1. **More than one sort key.** Nested `sorted` forms work but read poorly.
   Candidates are a key list (`sorted(o in orders): o.region, then o.total`) or
   a per-key `descending`. Waits for a real case.
2. **An index in list loops** (`for i, x in xs`). §7's two-name `for` makes the
   syntax available. Not part of this ADR.
3. **A predicate form for lists of float-bearing models.** `contains` on a
   `List<Reading>`, where `Reading` holds a float, is M1, and §1's counterparts
   only take floats. A form shaped like §3's, such as
   `any(r in readings): math.closeTo(r.value, target, relative: 1e-9, absolute: 0.0)`,
   would cover it and every other element type. Not attested; waits for a real
   case, as ADR 0056 asks of `filter`-like operations.
4. **Stacks and queues.** Fiig has neither; `List` covers a stack with
   `append`, `last`, and `slice`. When one is attested, the names start from
   `push`/`pop`/`peek`, which nearly every language uses for stacks and which
   `peek` shares with most queue APIs. Immutability splits `pop` as Clojure
   does: `peek` returns the element and `pop` returns the collection without
   it, since fiig has no tuples to return both. `put` should not be a queue
   name (Python's `queue.Queue` uses it), because §6 gives `put` to maps.

## Implementation notes

**Step 1, 2026-11-03: list queries, sorting, and A13 (§1, §2, §6).**

- `compiler/types/methods.go`: `List` gains `first`, `firstOrNone`, `last`,
  `lastOrNone`, `contains`, `sorted`, and `sortedDescending`. The table
  can't state constraints, so `checker.checkListElement` does: `contains`
  is M1 on a handle, a `Result`, or a float at any depth (the message names
  `math.containsClose` for `List<float>`, and the field path otherwise),
  and the sorts are M3 on an unordered element.
- `math.containsClose` and `math.allClose` (list form) join the prelude
  `math` group with named tolerances; `allClose`'s map form waits for
  `Map`. `==` on `List<float>` now names `math.allClose`.
- A13 covers every closed-table method: the checker marks each call that
  resolved to a table method, and a bare one is reported.
- Runtime: `first()` and `last()` on an empty list fault and name the
  `OrNone` form; `contains` uses `Equal`, so decimals match by value; the
  sorts use a stable sort, descending included, so `1.50` and `1.5` keep
  their order either way.
- Also fixed: M3's message omitted `decimal` from the ordered types.
- Tests: `compiler/checker/list_query_test.go`,
  `compiler/runtime/list_query_test.go`.

**Step 2, 2026-11-03: `sorted(x in xs): key` (§3).**

- `syntax.SortedExpr`, parsed in `parsePrimary`, formatted, and walked by
  `callnames.go` (so B9 sees calls in a key).
- **Where the form starts differs from §3's text.** §3 argues that
  `sorted` followed by `(` can't be ambiguous because no value named
  `sorted` can be called. A declaration named `sorted` can be, by its bare
  name, so `sorted(x)` would have parsed as the form. The parser starts the
  form only at `sorted ( name in`; since `in` is a keyword, no call begins
  that way. `TestSortedFormCheck` calls a rule named `sorted`.
- The key is checked with the caller kind set to rule, so a call a rule
  can't make is reported with the key's own message ("a sorted key may not
  call gateway ..."), and same-file private helpers get no exemption there.
- M3 for a key that isn't ordered, with a `T | None` key told to decide
  where `None` goes. A non-list iterable is A1.
- A bare `sorted(...)` statement is A13, as a bare method call is (§6).
- Runtime: each key is evaluated once into a slice, then indices are
  sorted stably by key, descending as its own stable sort.
- Tests: `compiler/checker/sorted_test.go`,
  `compiler/runtime/sorted_test.go`, `compiler/format/sorted_format_test.go`.

**Step 3, 2026-11-03: `Map` (§4-§9).**

- **Structure (§9).** `compiler/runtime/maptrie.go` is a persistent hash
  array mapped trie over `uint64` keys: 32-way nodes, a child kept as a
  leaf until a second key needs its slot, path copying on every update, so
  every version stays valid and every operation on any version touches at
  most 13 nodes. `Map` (`map.go`) holds two of them: one by key hash, whose
  leaves are collision buckets, and one by insertion sequence, which
  iterates in ascending order and so gives the map its order. This is
  Scala's `VectorMap` idea with a trie in place of the vector, so a remove
  leaves no tombstone. Measured (`BenchmarkMap*`, one desktop): a `put` on
  the newest map is about 1.1 µs at 1,000 entries and 2.6 µs at 100,000;
  a `put` that branches from a 100,000-entry base, §9's example, is about
  1.7 µs, with no copy of the base; a lookup is 40-120 ns.
  `TestMapMatchesReference` checks 4,000 random puts and removes, each on a
  random earlier version, against a plain model.
- **Hash (§4, §9).** Structural and consistent with `Equal`: a decimal
  hashes with trailing zeros trimmed, a record by sorted field names, a map
  by the sum of its entries' hashes, so equal maps in different orders hash
  alike. The seed is `maphash.MakeSeed()`, once per process.
- `Map` joins the prelude generics, so K2 reserves it. Its method table is
  §6's. A13 already covers every method (step 1).
- Keys must be comparable: `checker.checkMapKey` runs on every resolved
  type and on a literal's inferred key type, once per position. A `float`
  key's message gives §4's decimal route.
- Literals: `[k: v, ...]` and `[:]`, typed like list literals (expected
  type first, else inferred with no implicit union; `[:]` alone is A12).
  Equal constant keys are the new class **A18**, compared by value; a
  repeated key known only at run time faults, naming the literal's
  position.
- `for key, value in m` in insertion order, `_` discarding either (which
  also made `_` legal in a one-name `for`). One name over a map, or two
  over anything else, is I1 with §7's suggestion.
- `==` ignores order (§8); `math.allClose` gains its map form, and `==` on
  `Map<K, float>` names it.
- Boundaries: a `Map` with string keys encodes as a JSON object with its
  members in the map's order (`encoding/json` would sort them, so an
  ordered object type writes it); other keys encode as `[key, value]`
  pairs. `Map<string, V>` decodes from a JSON object, taking members in
  name order, since the parser doesn't keep arrival order; that is ADR
  0062's to settle. OpenAPI describes a string-keyed map as an object with
  `additionalProperties`.
- Not done: a map literal is not yet a constant (§5's last sentence), so
  it can't appear in a headed enum's header.
- Tests: `compiler/runtime/map_test.go`, `map_lang_test.go`,
  `compiler/checker/map_test.go`, and `TestFormatMapSyntaxIsStable`.

**Step 4, 2026-11-03: corpus.**

- `order-processing/rules/order_count_by_customer.fiig` counts orders per
  customer into a `Map<string, int>`, with a private helper that says what
  an absent key means; `orders_by_id.fiig` builds an index; and
  `largest_orders_first.fiig` is a keyed sort of records. examples/README
  row 21. Dogfooded with `fiig run` (three orders: `[c2: 2, c1: 1]`, and
  the two equal totals keep their order) and `fiig catalog find --output
  'Map<string, int>'`, which finds the counting rule.
