# ADR 0057: List Literals and `append`

Status: **Accepted** (2026-10-20), **implemented** 2026-10-20 (see
Implementation notes). Revised 2026-10-20: list equality is
content equality, implemented with this ADR (§6); a list literal in an enum
header is a constant when every element is (§7). No open questions remain.
Date: 2026-10-19
Amends: `grammar.md` (`literal`), ADR 0046 §7 (unblocks variant aliases).
Related: ADR 0019 (operators are `int`-only, no built-in-type special case),
ADR 0023 (values are immutable; `copy(...)` is a prelude builtin), ADR 0048
(no silent `int`/`float` conversion), ADR 0052 (comparability of `List<T>`),
ADR 0053 (`for` loops; typed reassignable locals; open question 1), ADR 0056
(no lambdas).

## Context

`List<T>` exists as a type, but fiig source cannot make one. Every list today
arrives from outside: a parameter, a gateway result, a request body. Three
records are blocked on this:

- **ADR 0053, open question 1.** A `for` loop can sum and validate a list but
  cannot build one. "Collect the IDs of orders over a threshold" is unwritable.
- **ADR 0046 §7.** Per-variant aliases (`aliases: ["ACMEINC", "ACME-INC"]`)
  wait on list literals; the resolve rule carries extra `if` branches
  instead.
- **`fiig-lang.md` Milestone 7.** JSON decoding in fiig source needs to build
  a list of decoded elements.

ADR 0056 decided there will be no `map` or `filter` taking a function. So
building a list is a loop that starts from an empty list and appends. That
needs three things: a literal, a way to type an empty literal, and an append.

### What exists to build on

- The lexer has no `[` or `]` token. Nothing else in the grammar uses them.
- ADR 0053's typed declaration `name: Type = expr` already states a local's
  type up front, which is exactly the context an empty list needs.
- ADR 0019 makes `+` `int`-only and says fiig has "no built-in-type special
  case at all", stricter than Java's `String +`. So `out = out + [x]` would
  amend 0019.
- ADR 0023's `copy(base, field: value)` is a prelude builtin called like a
  function. A second one is not a new kind of thing.
- Builtin methods exist only for strings (`.isBlank()`, `.contains(...)`),
  and the checker does not type them.

## Decision

### 1. Syntax: `[a, b, c]`

```
aliases: ["ACMEINC", "ACME-INC"]
ids = [order.id, refund.orderId]
```

Square brackets, comma-separated, trailing comma allowed. A literal may span
lines; the formatter prints it on one line when it fits and one element per
line with a trailing comma when it doesn't.

### 2. Element type: inferred from the elements; an empty literal needs context

- A non-empty literal's type is `List<T>`, where every element has type `T`.
  Elements of different types are an error, with no implicit union and no
  numeric widening (ADR 0048): `[1, "a"]` and `[1, 2.5]` are both errors.
- When the literal has an expected type, the elements are checked against
  it instead. That is how a list of a union is written:
  `xs: List<Order | None> = [order, None]`.
- `[]` has no element type of its own. It is legal only where an expected
  type exists: a typed declaration (`ids: List<string> = []`), a parameter or
  field of known type, a `return` in a declaration with a return type, or an
  operand of `append` (§3). `names = []` with no other context is an error
  that says to write the type: `names: List<string> = []`.

### 3. Appending: `append(list, element)`, Go's builtin

```
rule largeOrderIds(orders: List<Order>, threshold: int) -> List<string> {
    ids: List<string> = []
    for order in orders {
        if order.total >= threshold {
            ids = append(ids, order.id)
        }
    }
    return ids
}
```

- `append` is a prelude builtin like `copy`. It takes one list and one
  element and returns a **new** list. The argument is unchanged, because
  values are immutable (ADR 0023).
- Discarding the result is an error: `append(ids, order.id)` as a bare
  statement does nothing, and Go makes the same call a compile error for the
  same reason.
- **Cost guarantee.** Appending to the newest version of a list, as in
  `ids = append(ids, x)`, costs amortized constant time per element, so
  building a list of `n` elements is O(n). This is part of the language, not
  an implementation detail: a runtime that makes the building loop
  quadratic is wrong. Appending to an older version that has already been
  extended is allowed and correct, but copies (§5).
- Joining two lists is not in this ADR. It is a `for` loop of `append`s until
  a case is attested.

### 4. Out of scope

Indexing (`xs[0]`), length, `contains`, slicing, and map or dictionary
literals. Each has its own question (what an out-of-range index does, for
one) and none is needed to build a list. Equality is §6.

### 5. Runtime: claim the next slot

A list is a view of the first `n` slots of a buffer that several lists may
share. The buffer also records how many of its slots any list has used.
`append(list, x)`:

1. If the list ends exactly where the buffer's used part ends, and the
   buffer has spare room, claim the next slot with one atomic
   compare-and-swap, write `x` there, and return a view one slot longer.
2. Otherwise another list has already grown from this one, or the buffer is
   full: copy into a new buffer of twice the size, as Go's `append` does.

The old list still sees only its own `n` slots, so the semantics of §3 hold
exactly: nothing a program can observe changes. Go's aliasing bug cannot
happen, because a slot is written only by the append that claimed it:

```
base = [0, 1, 2]          // buffer has room for 4
a = append(base, 100)     // claims slot 3 in place    -> [0, 1, 2, 100]
b = append(base, 200)     // slot 3 taken, so copies    -> [0, 1, 2, 200]
```

In Go, `b` would overwrite `a`'s `100`. The compare-and-swap makes the
check safe when two `concurrent` branches append to the same list: one wins
the slot and the other copies.

**Where it is not fast.** Appending to the same old list repeatedly takes
the fast path once and copies every other time:

```
for x in xs {
    variants = append(base, x)    // copies each time after the first
}
```

That is the cost every append would have without this scheme, never worse.
The building loop always extends the newest list, so it always takes the
fast path. A short list may also keep a larger shared buffer alive, the same
trade Go makes.

**Measured** (a standalone Go prototype of this scheme, building a list of
`n` ints by repeated append):

| `n` | Copy every time | Claim the slot |
|---|---|---|
| 1,000 | 0.45 ms | 0.02 ms |
| 10,000 | 42 ms | 0.03 ms |
| 100,000 | 6.6 s | 3 ms |

The prototype's tests also checked that the old list never changes, that
plain Go slices do show the aliasing bug, and that 64 goroutines appending
to one shared list each get a correct result.

### 6. `==` on lists compares contents

`a == b` on two lists is true when they have the same length and equal
elements in the same order. `[1, 2] == [2, 1]` is false. This implements ADR
0052 §2.5 now rather than later: the runtime's `Equal` has no `List` case
today, so list `==` silently answers `false`, and literals would make that
easy to hit.

This is fiig's rule for every type, not a list special case: `==` compares
contents, all the way down. Comparing two `Order`s compares their fields;
a `List<OrderLine>` field compares its lines; each line compares its fields.
The runtime's `Equal` already walks models and enums this way.

- **There is no Java-style `.equals`.** Java needs two operators because its
  objects have identity: two separate lists holding `[1, 2]` really are
  different, since changing one leaves the other alone. Fiig values are
  immutable and have no identity (ADR 0023), so the only thing `==` can mean
  is "same contents". Any other notion of equality (same set in any order,
  same ID only) is a named rule, as ADR 0019 sends every domain operation to
  a named rule. Kotlin, C# records, Swift, Rust, and Python all made `==`
  compare contents; Java's string `==` bug is the cost of not doing so.
- **The walk always finishes.** An immutable value cannot contain itself:
  the inner value must exist before the outer one, and the outer one can
  never be changed to point back. Every value is a finite tree. A *type* can
  refer to itself (`children: List<Category>`); ADR 0052's comparability
  check carries a visited set for that, and the runtime walk needs none.
- **Cost.** Unequal values usually differ early, and the walk stops at the
  first difference, after cheap checks (type, variant, length) first. Equal
  values cost time proportional to their size, since every piece must be
  checked.
- **Same storage is equal.** If both sides are the same value in memory,
  `Equal` answers true without walking. This is safe only because values
  are immutable, and it is common: `copy(...)` (ADR 0023) shares every field
  it does not change, and claim-the-slot lists (§5) share buffers.
- **Floats are the exception, through ADR 0052.** A `float` anywhere in a
  type makes `==` on it a compile error (ADR 0052 §2.2), because NaN is not
  equal to itself. A list or model containing a float is compared by a
  named rule.
- **Maps, when they come.** Equality computed from the type means the
  compiler can derive a hash that agrees with it, so any comparable type can
  be a map key with no `equals`/`hashCode` contract to break. A value may
  compute its hash once and keep it, since it never changes.

### 7. Lists are constants when their elements are

ADR 0046 §4a restricts enum header values to constants. A list literal is a
constant when every element is one, so `aliases: ["ACMEINC", "ACME-INC"]` is
allowed in a header and `aliases: [normalize(raw)]` is not. With a `key` field
of list type, ADR 0046 §5's lookup matches any element, and duplicate checking
covers every element of every variant: no alias may belong to two variants.

## Rationale

**Why `[ ]`.** It is the shape of JSON, Python, JavaScript, Swift, Elixir,
Haskell, and DMN's FEEL, the nearest relative of fiig's rule tier. Go
(`[]string{"a"}`), Kotlin (`listOf("a")`), Java (`List.of("a")`), and Rust
(`vec!["a"]`) all spell it differently, but no reader misreads `["a"]`. Fiig's
constructor-call shape, `List(a, b)`, was considered: it would be the only
positional constructor in a language whose constructors take named
arguments.

**Why infer, and why empty needs context.** Go writes the element type on
every literal; that is redundant on `["a", "b"]`, where every element already
states it. Swift and Kotlin infer from the elements and require context only
for the empty case, which is the one literal that carries no information.
TypeScript's answer, inferring `never[]` or `any[]`, is the failure this
avoids. ADR 0053's typed declaration supplies the context with syntax that
already exists, so a building loop starts with a line the reader already
knows.

**Why `append` and not `+`.** Three candidates:

| Form | Problem |
|---|---|
| `out = out + [x]` | Amends ADR 0019's "no built-in-type special case at all". Strings would then ask for `+` too. |
| `out.append(x)` | Every mainstream prior (Python, Java, Kotlin, JavaScript `push`) says a method named `append` changes the list in place and returns nothing. Code written from that prior, `out.append(x)` as a statement, silently does nothing. |
| `out = append(out, x)` | Go's form, letter for letter. Its prior is already "returns the result, assign it", and Go's compile error for an unused `append` is the same rule fiig adopts. |

`append` is the only one that keeps ADR 0019 intact and matches its prior.

**Why claim-the-slot.** Copying on every append is the obvious way to keep
lists immutable, and it makes the building loop O(n²): 6.6 seconds for
100,000 elements in the prototype. Four other ways to avoid that:

| Approach | Used by | Why not, or not yet |
|---|---|---|
| Unchecked shared capacity | Go | The aliasing bug. Claim-the-slot is this plus the one check Go leaves out. |
| Persistent vector (32-way tree) | Clojure, Scala, Immutable.js | Fast for every append pattern, including repeated appends to one old list, but more code and slower indexing. Worth it only if that pattern turns out to be common. |
| Reference counting, mutate when unshared | Swift, Lean 4, Koka, Roc | Fiig's runtime uses Go's garbage collector, which tracks no counts; every value would need one. |
| Prove the old list is dead, mutate in place | Optimizing compilers | Needs alias analysis. The runtime check gets the same result without it. |

Claim-the-slot covers the loop this ADR exists for, changes about ten places
in `compiler/runtime`, and keeps indexing a plain slice access for when
indexing is added.

## Alternatives considered

**A. Explicit element type on every literal (Go).** Rejected as redundant
for the non-empty case; kept for the empty case through the typed
declaration.

**B. `+` for concatenation.** Rejected; see the table above. Revisit only as
part of a decision about `+` on strings.

**C. A list-builder syntax form** (FEEL's `for x in xs return e`). It fits
ADR 0056 §3, but it is a second way to loop beside `for`, and it does not
cover the early-exit and multi-statement bodies that `for` already handles.
Worth revisiting if building loops turn out to be mostly one-line maps.

## Consequences

- Lexer: `[` and `]` tokens.
- Grammar: `list-literal ::= '[' [ expression { ',' expression } [ ',' ] ] ']'`
  as a `primary`.
- Checker: list `==` is legal when the element type is comparable (ADR 0052
  §2.1's `List<T>` row); a list literal is a constant when its elements are
  (§7). Element-type inference; expected-type checking for literals,
  including `[]`; `append` typed as `(List<T>, T) -> List<T>`; an error for a
  discarded `append`.
- Runtime: a `List` case in `Equal` (length first, then elements in order)
  and a same-storage shortcut ahead of the walk (§6).
- Runtime: `List` gains a shared used-slot counter beside `Elems`; a list
  literal allocates a fresh buffer; `append` claims the next slot or copies
  (§5). A test builds a long list in a loop and asserts the number of
  buffer copies grows logarithmically, not linearly, so a regression to
  copy-every-time fails the build (a benchmark alone would not).
- Formatter: one line, or one element per line with a trailing comma.
- Corpus: a building loop in `examples/` (`largeOrderIds` or similar), and
  ADR 0046's aliases rewritten with a literal once the header form is
  decided.

## Implementation notes

Implemented 2026-10-20.

- **Bug classes.** Mixed element types and a wrong element against a
  declared type are A1, the ordinary type mismatch. `[]` with no expected
  type is new **A12**; a discarded `append` is new **A13**.
- **Where `[]` gets its type.** Anywhere the checker already has an expected
  type: a typed declaration, reassignment of a typed local, a parameter, a
  field, a `return`. A union with exactly one `List<T>` member
  (`List<string> | None`) counts. `append([], x)` takes `T` from `x`. An
  unmodeled position (a capability argument) accepts `[]` silently rather
  than blaming the author for a missing annotation.
- **`append` is a name, not a keyword**, checked like `copy`: a call to
  `append` is the builtin. It is in `syntax.PreludeCallForms`, so it never
  shows as a registry dependency.
- **Runtime.** `runtime.List` gained an unexported shared `used` counter;
  `appendList` in `compiler/runtime/list.go` is §5. Lists built any other
  way (a literal, decoded JSON) have no counter, so their first append copies
  into a doubled buffer and later ones claim slots. `TestBuildingALoopCopiesLogarithmically`
  counts copies while building 100,000 elements and fails above 20.
- **Equality.** `runtime.Equal` gained a `List` case and two same-storage
  shortcuts: two views of the same buffer slots, and two records sharing a
  field map. The checker still does not reject `==` on types containing a
  float; that is ADR 0052, which stays Proposed.
- **Formatter.** One line when it fits within 100 columns, otherwise one
  element per line with a trailing comma, like a wrapped call. Comments
  inside a wrapped literal move to the end of its closing line.
- **§7's enum-header constants** shipped with ADR 0046's implementation
  (2026-10-24), including list-typed `key` fields.
- **Corpus:** `order-processing/rules/large_order_ids.fiig`, run by
  `compiler/runtime`'s `TestCorpusLargeOrderIdsBuildsAList`.
