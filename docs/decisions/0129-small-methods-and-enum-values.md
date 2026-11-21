# ADR 0129: A Batch of Small Methods, and `Enum.values()`

Status: Proposed
Date: 2026-11-21
Related: ADR 0049 (the closed method table), ADR 0065 (list queries), ADR
0067 (strings), ADR 0093 (list forms), ADR 0056 (no lambdas, no generics).

## Context

A sweep of the language on 2026-11-21 (`docs/improvement-ideas.md`) tried a
dozen things any program does and found six missing: padding a string,
removing duplicates from a list, reversing one, taking or skipping the first
few, finding an element's position, and listing an enum's variants. A library
author can't add them, since there are no generics or lambdas (ADR 0056), so
they come from the closed method table (ADR 0049 §2.6), as ADR 0065 and ADR
0093 added theirs.

## Decision

Every method returns a new value and changes nothing, as the table's others do.

### 1. Strings

```fiig
"7".padLeft(3, "0")      // "007"
"7".padRight(3, "0")     // "700"
"7".padLeft(6, "ab")     // "ababa7"
```

`padLeft(width, fill)` and `padRight(width, fill)`. The width counts grapheme
clusters, as `length()` does (ADR 0067), so `e` plus a combining accent is one
position. The fill repeats and its last copy is cut to fit; an empty fill
faults, as `split("")` does. A string already `width` wide or more is returned
as it is.

### 1a. `repeat(count)` (added 2026-11-21)

`"ab".repeat(3)` is `"ababab"`. A count of 0, or an empty string, gives `""`.
A negative count faults, as `take` and `drop` do. So does a result too long
for a string to hold, where Go's own `strings.Repeat` would panic. Hearsay
wanted it for a boundary test (a 281-character note, GAPS G2) and built the
string from nested interpolations instead.

**No size cap, by decision (the owner, 2026-11-21).** `repeat`,
`padLeft` and `padRight` fault only when the length overflows, as Go's,
Rust's, Python's and Lua's do. They don't fault on a fixed maximum length,
as JavaScript engines and .NET do (about 1 GB), and there's no per-request
memory budget, as PHP's `memory_limit` and Erlang's `max_heap_size` give.
A count that comes from a request is input, so a rule bounds it first
(charter principle 11, `docs/layers.md`'s order of checks). A cap on a few
methods would promise more than it gives, since `append` loops,
interpolation and decoding can use up memory just the same. The cost is
that running out of memory stops the whole process, not just the request
(`docs/known-limitations.md`). Revisit with a per-request memory budget if
a real case appears.

### 2. Lists

| Method | Result |
| --- | --- |
| `reverse()` | the elements in reverse order |
| `take(count)` | the first `count`, or all of them when there are fewer |
| `drop(count)` | all but the first `count`, or none when there are fewer |
| `distinct()` | each element once, the first of its equals, in order |
| `indexOfOrNone(element)` | the position of the first `==` element, or `None` |

- `take` and `drop` share the receiver's storage as `slice` does (ADR 0057's
  claim-the-slot), and **fault on a negative count**, as `slice` faults on a
  bad range.
- `distinct` and `indexOfOrNone` compare with `==` and so hold the element
  type to what `contains` does (M1): no handle, no `Result`, and a float needs
  `math.containsClose`.
- The name `indexOfOrNone` follows `atOrNone` and `firstOrNone`: a result that
  may be absent says so.

### 2a. `concat(other)` (added 2026-11-21)

`xs.concat(ys)` is a new list: `xs`'s elements, then `ys`'s, in order. Both
lists have the same element type. Neither input is changed, and the result
has storage of its own, so appending to it never claims an input's spare
capacity (ADR 0057 §5).

```fiig
everyone = RatedBooks.recommended().concat(LinkedRatings.matched())
ids = recommenders.concat(stateOwners).distinct()
```

ADR 0057 left joining two lists out until a case was attested. Hearsay wrote
the same `for`/`append` helper twice (`links/EveryRecommendation.fiig`,
`links/RatingsOfBook.fiig`). The owner chose a method over the other three
ways in:
- **`append(xs, ys...)`**, Go's: it needs spread syntax, and gives `append`,
  chosen for one element, two meanings.
- **`+` on lists**: ADR 0057 rejected it, along with `+` on strings, so `+`
  stays arithmetic.
- **`[...xs, ...ys]`**: new syntax, and a second way to build a list.

The name is JavaScript's (`a.concat(b)`) and Java's (`Stream.concat`).
`plus`, Kotlin's name, reads as arithmetic.

### 3. `Level.values()`

For an enum **none of whose variants carries data**, `Level.values()` is every
variant in declaration order, a `List<Level>`. On an enum with a payload
variant it is an error that names the variant, since there is no one value to
list. A variant named `values` is that variant, as before.

## Not here

- **`Set`** and **`zip`** need a type (a set, a pair) and a decision on
  tuples. They are their own questions.
- **`flatten`** and **`flatMap`**, which fiig's forms (ADR 0093) may cover
  with a nested `for`.

## Consequences

- Six rows in `compiler/types/methods.go`, their bodies in
  `compiler/runtime/methods.go`, the M1 check widened in
  `compiler/checker/compare.go`, and one case each in the checker's and the
  runtime's enum call.
- A program that defined its own `padLeft` as a rule is unaffected: a method
  and a declaration are different names (`x.padLeft` and `padLeft(x)`).

## Implementation notes (2026-11-21)

- Tests: `compiler/runtime/methods_batch_test.go` (padding with a combining
  accent and a flag in the text and the fill, every list method with its edge
  cases, `values()` in order) and `compiler/checker/methods_batch_test.go`.
