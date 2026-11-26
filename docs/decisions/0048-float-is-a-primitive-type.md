# ADR 0048: Float Is a Primitive Type, With No Non-Finite Values

Status: Accepted (2026-10-27, after a one-decision-at-a-time review; see the
review note below). Implemented 2026-11-02, with §2.7's crossing rules
arriving the same day in ADR 0049's `math` group; and §2.6
on 2026-11-25, as stream adapters' I1 (not the J1 proposed below: J1 is now
the JSON bug class of ADR 0073). See Implementation notes.
Date: 2026-10-12 (revised 2026-10-13: ADR 0051 withdrew J2 and replaced it with
a typing restriction — `==`/`!=` over two `float` operands is a type error)

## Context

Fiig has three primitive types: `string`, `int`, `bool`
(`compiler/types/types.go:22`). An outside review on 2026-10-12 claimed four
primitives were missing — binary floats, raw bytes, `char`, and a unit type.
Three of those four resolved without a new decision: `None` is already a unit
type, `bytes` is already a Milestone 7 deliverable (`fiig-lang.md:466`), and
`char` was rejected on precedent (Python, SQL, JavaScript, and Elixir all do
text processing without one).

Float was the one left genuinely undecided, and the record was thinner than it
looked. ADR 0019 is cited twice as having settled it — once in ADR 0025
("a floating-point column is a Fault, not a lossy truncation — fiig has no
float type at all (ADR 0019)") and once in `libraries/sql/provider.go:23-25`
to the same effect. ADR 0019 says no such thing. Its addendum opens with
**"Not a decision here, but worth leaving as a steer for whoever makes it,"**
and the steer it leaves is scoped to one case: that *a future fractional type
for money* should be fixed-point, not IEEE. That is a correct claim about
money and not a claim about float.

So the question was open, and it decomposes into three that have different
answers:

1. **Speed for float-heavy workloads** (the review's own argument: 3D
   graphics, game engines, physics, audio, ML). This one fails on its face.
   Fiig is a tree-walking interpreter with no first-class functions, no
   user-declarable generics, and no sequential iteration yet; binary64 moves
   machine learning from infeasible to infeasible. No float decision rides on
   this.
2. **Correct representation for measured and geometric values.** This is a
   question about what the language is *for*, not about how fast it runs, and
   it is the one that matters. Normals, dot products, interpolation, and
   rotation matrices are built from `sqrt`, `sin`, and `cos`, whose results
   are irrational. There is no exact decimal representation of `sqrt(2)`. A
   decimal type must round such a result anyway — and would round it more
   slowly, and with a far less studied error model, than IEEE 754, which has
   forty years of numerical analysis behind it. For these values decimal is
   not merely slower than float; it is the wrong type.
3. **Fidelity for float data fiig did not produce.** Independent of whether
   fiig ever does any math. A SQL `REAL`/`DOUBLE` column holds a binary
   double, and no decimal type can round-trip one bit-exactly. Today
   `libraries/sql/provider.go:23-25` Faults on such a column, so any table
   containing one is unqueryable from fiig at all.

The project owner settled (2) on 2026-10-12: fiig is an all-purpose language
with an opinionated application architecture, geometry is ordinary `rule`
code, and the language should support it. That answer also closes (3) as a
side effect.

## Decision

### 2.1 `float` is a primitive type: IEEE 754 binary64, one width, that name

`types.Primitive` gains a fourth member. There is one floating-point type, not
an `f32`/`f64` pair, and it is spelled `float`.

### 2.2 ADR 0019's arithmetic operators extend to `float`; its no-mixing addendum already governs the rest

`+ - * / %` and unary `-` apply to `float` as well as `int`. This **amends ADR
0019**, whose decision clause reads "They apply only to `int` — the only
numeric primitive the language has"; that clause was a description of what
existed, as its own rationale says ("deliberately *not* a statement that Fiig
will stay int-only").

Nothing else about ADR 0019 changes, and in particular its addendum needs no
amendment: there is no implicit widening between numeric types, so `int *
float` is a type error. Crossing between them goes through an explicit
`conversion` or a named `rule`. That addendum was written in advance of a
second numeric type existing precisely so this decision could not quietly
default to Java-style promotion, and it holds unmodified.

### 2.3 Non-finite values are faults, not values

`NaN`, `+Inf`, and `-Inf` are never values of type `float` in a running fiig
program. Every operation that would produce one is a fault (`fiig-lang.md`
§3.6), not a typed error and not a representable result:

| Operation | IEEE 754 result | Fiig |
|---|---|---|
| `1.0 / 0.0` | `+Inf` | fault |
| `0.0 / 0.0` | `NaN` | fault |
| `sqrt(-1.0)` | `NaN` | fault |
| `1e308 * 10.0` | `+Inf` (overflow) | fault |
| `Inf - Inf` | `NaN` | unreachable — no `Inf` exists |

Additionally, `-0.0` is normalized to `0.0` wherever a `float` is produced, so
two `float` values that compare equal are indistinguishable by any operation
in the language.

**Non-finite values from outside are input errors, not faults** (added
2026-10-27). The faults above are for non-finite values that fiig's own
operations produce, which are programming mistakes. A non-finite value can
also arrive from outside: Postgres `double precision` stores `'NaN'` and
`'Infinity'`, SQLite stores infinities, a binary wire format can carry any
bit pattern, and JSON's `1e400` overflows binary64. The program did nothing
wrong in those cases, so by ADR 0019's test they are domain conditions.
Faulting on them would let one bad row in a shared table cancel correct
workflows.

So a non-finite value at a boundary is reported through **that boundary's
own failure channel**. It is never silently converted: not to `None`, as
SQLite does for NaN, and not to a sentinel value.

| Boundary | Failure channel |
| --- | --- |
| JSON conversion (ADR 0062 §4) | `Err` |
| Binary patterns (ADR 0058) | non-match, falling through to the next arm |
| SQL (`libraries/sql`) | **none today** |

A boundary with no failure channel is a gap in that boundary, not permission
to fault. `Database.queryOne` returns a record or `None` and has no way to
report a decode error, which is why `libraries/sql` faults on any float column
today. Until the Postgres work gives database access an error channel (it needs
one anyway, for connection loss, constraint violations, and type mismatches),
SQL keeps faulting on a non-finite value. That is the one place the rule is not
yet met.

Inside a running program the guarantee is unchanged: every `float` is finite.

**Underflow is not a fault** (added 2026-10-28, from ADR 0064 §6). A result
too small to represent, such as `1e-300 * 1e-300` or `math.exp(-1000.0)`,
rounds to the nearest float, which may be `0.0` (normalized, never `-0.0`).
It is finite and nearly correct, unlike an overflow, which loses the value.
Python's `math` returns `0.0` here although it raises on overflow, and Java
and Go agree. This section had specified overflow and left underflow
unstated.

### 2.4 Numeric literals are untyped constants, Go-style

`3.14` is not a `float`. It is an **untyped constant**: an exact,
arbitrary-precision value that takes its type from the context it appears in,
exactly as in Go. `pricePerKg * 2` type-checks where `pricePerKg` is a
`float`, because the `2` becomes a `float`; the same literal in a decimal
context becomes a decimal. Constant arithmetic is evaluated exactly at
compile time and rounded once, when the constant is finally given a type.

Where context determines nothing — `binding ::= identifier '=' expression`
(grammar.md:272) has no type annotation — an untyped constant falls back to a
default type: `int` for an integer constant, `float` for a fractional one.

Constant expressions that cannot be represented in the type they land in are
**compile errors**, not silent wraps or roundings: `smallCount: int = 1e300`
is rejected at compile time rather than producing a fault at runtime.

### 2.5 `float` does not replace or defer decimal

A fixed-point/decimal type remains an anticipated need on exactly the terms
`grammar.md`'s known-gaps entry and ADR 0019's addendum already state. Money
stays whole-cent `int` today and becomes decimal later; it never becomes
`float`. Fiig will have three non-interchangeable numeric types, and that is
the intended end state, not a transitional embarrassment.

### 2.6 `float` is not a key

A `float` may not be an element key in a `stream` adapter route (ADR 0047 §2),
and more generally may not be the discriminator for any identity-based
construct the language adds. Proposed bug class — see §5.

### 2.7 Crossing between numeric types is always a named call, never a silent primitive conversion

There is no `int(x)`/`float(x)` primitive conversion syntax. Fiig has no
spelling for "convert this number to that number" that an author can write
without naming which rounding they mean. Crossing is done by built-in rules,
each of which names its own rounding mode and is callable like any other
rule:

| Rule | Behaviour | `-3.5` ⟶ |
|---|---|---|
| `math.floorToInt(x: float) -> int` | toward -∞ | `-4` |
| `math.ceilToInt(x: float) -> int` | toward +∞ | `-3` |
| `math.truncToInt(x: float) -> int` | toward zero | `-3` |
| `math.roundToInt(x: float) -> int` | half away from zero | `-4` |
| `math.roundHalfEvenToInt(x: float) -> int` | half to even (IEEE default) | `-4` |
| `math.toFloat(n: int) -> float` | nearest float, ties to even (IEEE); never faults | — |

A crossing that cannot be represented in the target type is a **fault**, per
§2.3 and ADR 0019's division-by-zero reasoning: `floorToInt(1e300)` exceeds
`int`. These are programming mistakes to surface, not
domain conditions, so they fault rather than returning a `Result` — a
`Result` here would make every nested geometric expression an unwrap and would
be unusable.

`toFloat` never faults (revised 2026-10-27). The original draft faulted for
`|n| > 2^53`, where an `int` has no exact binary64. That held one conversion
to a stricter standard than every float operation after it: `0.1 + 0.2`,
`1e17 + 1.0`, and `math.sqrt(2.0)` all round without faulting. Every int64 is
far inside binary64's range, so the worst outcome is a relative error of about
10^-16, the precision every `float` already has. `toFloat` therefore rounds to
the nearest binary64, with ties going to the value whose last bit is even, which
is IEEE 754's default rounding. Java's `(double)`, Go's `float64(n)`, Rust's
`as f64`, Python's `float(n)`, and Erlang's `float/1` all do the same. An
exact integer ID passing through `float` is the real risk, and ADR 0062 handles
it by keeping JSON numbers as text rather than by faulting here. The opposite
direction, float to `int`, still faults when out of range, because a value
like `1e300` has no `int` at all.

### 2.8 `%` applies to `float` and truncates, as it does on `int` (Java's rule)

*Added 2026-10-27; this resolves the open question on `%`.*

`a % b` on two `float` operands is the truncated remainder. Its result takes
the sign of the dividend, as with C's `fmod`, Go's `math.Mod`, and Java's `%`
on `double`. Fiig's `int` `%` already behaves this way, inherited from Go
(`compiler/runtime/expr.go`) and never written down. This section writes it
down for both types, so `%` means one thing on every numeric type:

| Expression | Result |
| --- | --- |
| `-7 % 3` | `-1` |
| `-7.5 % 2.0` | `-1.5` |
| `7.5 % -2.0` | `1.5` |
| `x % 0.0` | fault (Java gives `NaN`; §2.3) |
| `-3.0 % 3.0` | `0.0` (Java gives `-0.0`; §2.3 normalizes it) |

Wrapping a periodic value is a different operation, so it gets its own name
rather than a different meaning for `%`. `math.floorMod(a, b)` is the floored
remainder, whose result takes the sign of the divisor, and it is defined for
both `int` and `float`. Java has `Math.floorMod` for integers only.
`math.floorMod(-30.0, 360.0)` is `330.0`, while `-30.0 % 360.0` is `-30.0`.
That follows §2.7's reasoning: where two conventions are both in wide use, the
less common one gets a name instead of hiding behind an operator. Python and
Ruby make `%` floored. Most C-family languages, including Java, C#, Rust, and
JavaScript, make it truncated, and fiig follows that majority.

### 2.9 Comparing an `int` with a `float` is a type error

*Added 2026-10-27; this resolves the open question on comparisons.*

ADR 0019's addendum forbids mixing numeric types under an arithmetic operator.
Comparison operators come from ADR 0011, which predates a second numeric type,
so `count < threshold` with an `int` and a `float` was unspecified. It is a type
error, for all of `<`, `<=`, `>`, `>=`, `==`, and `!=`. The author converts one
side explicitly: `math.toFloat(count) < threshold`.

- **It matches arithmetic.** If `count * threshold` is an error and
  `count < threshold` is not, "no implicit widening" has an exception
  that nothing in the rule explains.
- **Widening can give wrong answers.** Java converts the `int` to a float
  first, so `9007199254740993 > 9007199254740992.0` is `false`. Python and
  Erlang compare exact values instead, which avoids that, but at the cost of
  a rule only comparisons would follow.
- **It is caught at compile time.** Go, Rust, and Swift make the same choice.

Untyped constants (§2.4) keep the common case free: in `x < 3` or
`count < 2.5`, the constant takes its type from the other operand. Only two
typed operands of different numeric types meet the error. `==` and `!=` were
already errors for two `float` operands (ADR 0051), so this adds only the
mixed case.

## Rationale

**One width, named `float`.** The naming follows the existing primitives:
fiig writes `int`, not `i64`, and `string`, not `str`. Choosing one width
follows the same precedent — `int` is a single type (`runtime.Int` is `int64`)
and nobody has missed `int32`. The case for `f32` is storage density and
memory bandwidth in large homogeneous buffers, which is a real case in
graphics and ML and a non-case here: fiig's `List<T>` is boxed, so a
`List<float>` would not be a packed array of 32-bit floats no matter what the
element type promised. If fiig ever drives a GPU, the vertex buffer will live
behind a capability as `bytes`, not as a fiig list, and the width question
belongs to that capability's contract rather than to the language's scalar
set.

**Why non-finite values are faults — the main argument of this ADR.** IEEE
754's special values are the single most consequential thing about adopting
binary floats, and admitting them would break two properties fiig currently
gets for free:

- **`==` would stop being reflexive.** `NaN == NaN` is `false`, so `x == x`
  could be false for a well-typed, non-null `float`.
- **Ordering would stop being total.** All four of `<`, `<=`, `>`, `>=`
  return `false` when either operand is `NaN`, so `not (a < b)` would no
  longer be equivalent to `a >= b` — an identity that every reader, and
  every LLM's prior, assumes without checking.

Rust takes these seriously enough to encode them in its type system: `f64`
implements `PartialEq` and `PartialOrd` but deliberately not `Eq` or `Ord`,
which is why `Vec<f64>::sort` does not exist and `sort_by` with
`partial_cmp().unwrap()` is the idiom. That split is the honest way to admit
NaN — and fiig cannot express it. There is no user-declarable interface
mechanism today (ADR 0043 is heading there and is not implemented), so fiig
has no place to write down "`float` is comparable but not totally ordered."
The choice is therefore not between Rust's rigor and something looser; it is
between faulting on NaN and silently having a primitive whose equality
operator lies.

*Note (2026-10-28):* both halves of this paragraph have since moved. ADR 0052
made `==` on `float` a type error everywhere, so NaN could no longer make
`x == x` false; the reflexivity argument above no longer carries weight, and
the decision rests on total ordering and the three reasons below. ADR 0052
also introduced computed type properties (`Comparable`, `Ordered`), so "float
is partially ordered" is now expressible the way Rust expresses it. Neither
change reverses this ADR, but both make reversing it cheaper than this section
implies, and a future review should start from that.

The precedent for faulting is Erlang, which is a close match rather than a
convenient one: Erlang has no `NaN` and no `Inf`, and `1.0/0.0` raises
`badarith`. It has run production telecom systems for three decades without
IEEE's special values, in a language whose error philosophy — crash loudly on
a programming mistake rather than propagate a poisoned value — is the same one
`fiig-lang.md` §3.6 states.

Three further reasons this fits fiig specifically:

- **It makes ADR 0019 consistent instead of special-cased.** 0019 already
  decided that integer division and modulo by zero are faults, "a programming
  mistake to surface and crash on, not a domain condition a workflow should
  `match` against." Admitting `+Inf` would mean `7 / 0` faults while
  `7.0 / 0.0` quietly yields infinity — two primitives in one language
  disagreeing about the same mistake, for no reason a reader could derive.
- **Fiig already has better vocabulary for every job NaN does.** NaN as
  "missing or invalid" is what `Customer | None` and `Result<T, E>` exist to
  replace, and `+Inf` as "no upper bound" is what the `unbounded` keyword
  already spells in `limit-value`. Admitting non-finite floats would add a
  second, untyped, silently-propagating channel for absence and failure
  alongside the typed ones the whole language is organized around.
- **Overflow becomes one rule rather than two.** `1e308 * 10.0` faulting is
  the same decision as integer overflow faulting, which is currently
  **unspecified** — `runtime.Int` is `int64` and silently wraps, and
  `grammar.md`, `fiig-lang.md`, and ADR 0019 are all silent on it. This ADR
  does not settle the `int` case, but it does make leaving it open more
  expensive: `addMoney` on two large balances wrapping while the float
  equivalent faults is exactly the kind of unprincipled difference the
  previous bullet objects to.

The cost, stated plainly: numerical code that relies on carrying `NaN` through
a long computation and testing once at the end cannot be written this way, and
a few standard algorithms use `Inf` as a legitimate initial value for a
running minimum. Both have direct rewrites (a `Result`, or the first element).

*Revised 2026-10-28.* This paragraph used to end by calling that cost "not
characteristic of the application-architecture work fiig targets." That
dismissal is withdrawn: fiig intends to be usable for numerical programs, and
`fiig-lang.md` now records a NumPy-class numerics library as a goal. The
decision stands for a different reason. Python, the main language of numerical
computing, is close to this ADR at the scalar level: `math.log(0)` and
`math.sqrt(-1)` raise `ValueError`, `1.0 / 0.0` raises `ZeroDivisionError`,
and `math.exp(1000)` raises `OverflowError`. IEEE semantics, with `NaN` and
`Inf` propagating, come from NumPy, a library with native array kernels and
its own types. So faulting scalar `float`s do not keep fiig out of numerics;
the absence of a NumPy would. That library needs native kernels, which the
closed native set (ADR 0042, ADR 0049 §2.1, K5) does not yet permit, and it
can give its own types IEEE behaviour without changing what `float` means.
Until then, the `OrNone` members (ADR 0063 §2) are the scalar escape hatch.

**Why untyped constants, and why they are what makes §2.2 affordable.** The
question this replaces was "does the bare literal `3.14` belong to `float` or
to decimal," which assumed a literal must belong to a type at all. Go shows
it needn't, and taking Go's answer removes the question rather than deciding
it: an untyped `3.14` serves both types, so adopting `float` now does not
mortgage decimal's literal syntax later.

That is the smaller of two benefits. The larger one is that untyped constants
are the missing half of ADR 0019's no-implicit-widening rule, and without
them that rule is punishing. `x * 2` is the single most common mixed-looking
expression in arithmetic code, and in a strict language with typed literals
it must be written `x * float(2)` — friction at every scale factor, every
halving, every percentage. Go pairs its refusal to widen *variables* with
total flexibility about *constants*, and the pairing is why Go's strictness
reads as discipline rather than as ceremony. Fiig had adopted the first half
in ADR 0019 and not the second; this ADR is where the second arrives, because
`float` is the type that makes the gap hurt.

Two further properties come free with the constant system and are worth
having on the record:

- **Exact constant arithmetic.** `0.1 + 0.2` evaluated as untyped constants
  is exact and rounds once on assignment, where the same expression on
  `float` values rounds three times. Go's spec mandates at least 256 mantissa
  bits for this reason.
- **Out-of-range constants are compile errors.** Go rejects `uint8(300)` at
  compile time because the constant system sees the value before a type is
  chosen. Fiig gets the same for free, which matters more here than in Go:
  §2.3 makes float overflow a *runtime fault*, and the constant system moves
  the subset of those that are literally written in the source from runtime
  to compile time, which is always the better place to catch something.

The cost, and it is the one genuine wart: constant *kind* matters, not just
context. In Go `7 / 2` is `3` even in a float context, because both operands
are untyped **integer** constants and `/` on integer constants is integer
division; `7.0 / 2` is `3.5`. This surprises people, and inheriting untyped
constants means inheriting it. It is still the right trade — the alternative
rules are worse — but it belongs in the language tour rather than being
discovered.

**Why crossing is a named rule and not a conversion operator.** ADR 0019
already steered here, saying the cross-type operation is "more likely" to be
"a named `rule` that owns the actual business rule (rounding, what the result
type is)." §2.7 ratifies that steer, and the reason is stronger than a
general preference for explicitness: **"truncate" is not one operation, it is
five, and the one Go picks silently is wrong for exactly the domain that
motivated this ADR.**

Go's `int(x)` truncates toward zero. For non-negative values that coincides
with `floor` and nobody notices. For negative values it does not: `int(-0.5)`
is `0`, where a grid or raster calculation needs `-1`. Any geometry that
spans an origin — world coordinates, centred viewports, signed offsets — hits
this, and it fails as an off-by-one in one quadrant only, which is among the
harder bug shapes to see in review. A language that admits `float`
specifically so geometry can live in `rule` bodies should not ship the
rounding default that breaks geometry at negative coordinates.

So the choice is not between a terse silent conversion and a verbose explicit
one. It is between five named operations and one unnamed operation that is
silently one of the five. Naming them is not ceremony; it is the only way the
author states which they meant. Python makes the same judgement — it has no
`int`-like truncating cast that handles the cases, so `math.floor`,
`math.ceil`, and `round` are separate named functions, and `round` is
documented as half-to-even precisely because the choice is contested.

**Why faults rather than `Result` for an out-of-range crossing.** Returning
`Result<int, E>` would be more principled in isolation and unusable in
practice: F1 requires every `Result` to be handled, so a single nested
expression like `floorToInt((x - minX) / cellSize)` would become an unwrap,
and geometry is nested expressions. Out-of-range is also the right shape for
a fault by ADR 0019's own test — a programming mistake to surface and crash
on, not a domain condition a workflow should `match` against.

**Why decimal is still coming.** The axis is not precision versus speed, it is
which values the language is for. Decimal is right when a value *is* its
decimal representation — money, tax rates, percentages, quantities a human
typed — and exactness is semantically meaningful. Float is right for measured
and geometrically derived values, where the true result is irrational and
every representation rounds. Those are disjoint sets, both of which fiig's
domain contains, so both types are needed. Adopting float because geometry
belongs in rules is not an argument that money should be float; it is an
argument that fiig has two kinds of number and should say so.

## Consequences

**Implementation, in dependency order:**

- `compiler/lexer/lexer.go:228-239` — `scanNumber` currently consumes `digit+`
  and emits one `Int` token, so `3.14` lexes today as `Int(3) Dot Int(14)`.
  It gains a fractional form and a `Float` token kind. The comment there
  recording that the dotted `version-literal` form was removed (ADR 0032) is
  worth preserving; this reintroduces a dot inside a number token for an
  unrelated reason.
- `compiler/syntax/ast.go` — a `FloatLit` node alongside `IntLit:355`.
- `compiler/types/types.go:22` — the `Primitive` doc comment enumerates the
  three names and needs the fourth.
- `compiler/runtime/value.go` — a `Float` value type, and fault paths for
  every operation in §2.3's table.
- `compiler/checker` — arithmetic and comparison operand checking gains
  `float`, and must reject mixed `int`/`float` operands per ADR 0019's
  addendum rather than widening.
- `compiler/checker` — **an untyped-constant representation and a
  constant-expression evaluator (§2.4). This is the largest single piece of
  work in this ADR** and it is new machinery, not an extension of existing
  machinery: constants carry an exact arbitrary-precision value (Go uses
  `math/big`; `big.Rat` suffices for `+ - * /` and is exact where `big.Float`
  is not), a kind (integer or fractional), and no type until context assigns
  one. Type assignment happens at every position that has an expected type —
  parameters, fields, returns, comparison against a typed operand — with the
  §2.4 defaults at positions that don't. Representability is checked at
  assignment, which is where the compile errors in §2.4 come from.

The ordering matters: a `float` primitive without §2.4 is shippable but
unpleasant to write, and §2.4 without §2.7's rules is a `float` you can
compute with but can't get back to `int`. §2.7's rules share the homeless slot
described in the first open question below.

**Grammar and spec text:**

- `grammar.md:27` — a `float-literal` production next to
  `integer-literal ::= digit+`.
- `grammar.md:355` — `literal ::= string-literal | integer-literal` gains the
  float form.
- `grammar.md:458-465` — the known-gap entry for a fractional type needs
  rewriting. Its "no implicit widening" sentence stays and now has a live
  second numeric type to govern; the rest of the entry still describes
  decimal, which §2.5 keeps open.
- ADR 0019 — mark the "apply only to `int`" clause amended by this ADR,
  without touching the addendum.

**Citations that become false, and one argument that loses its example:**

- `libraries/sql/provider.go:23-25` Faults on a float column and attributes
  that to ADR 0019. Both halves change: the attribution was wrong when
  written (0019 declined to decide), and the behaviour itself should become a
  decode to `float`. This closes the fidelity gap in §1 item 3 — tables with
  `REAL`/`DOUBLE` columns become queryable.
- ADR 0025:38-39 makes the same claim in spec text and needs the same
  correction.
- ADR 0047:239 uses the float-column Fault as its worked precedent for
  "dynamically detectable properties should fault loudly." The precedent
  evaporates when the column decodes. 0047's §7 argument is unaffected in
  substance — and in fact this ADR's §2.3 is a larger instance of the same
  principle, so 0047 should cite the NaN fault instead. 0047 is still
  Proposed, so this is an edit, not a supersession.

**Ergonomic cost, named rather than discovered later:** three numeric types
that never mix implicitly means explicit conversions at every boundary between
them, including `int`-to-`float` for something as ordinary as dividing a count.
That is the direct price of ADR 0019's addendum, which was adopted
deliberately and with Java's promotion rules as the thing being avoided. It
should be paid in named `rule`s that own the rounding decision, per 0019's own
guidance, rather than worked around.

## Proposed bug classes

- **J1 — `float` as a stream element key.** ADR 0047 §2's `key identifier`
  must not resolve to a `float` field. Even with NaN eliminated, binary
  equality on keys derived from arithmetic is a defect: two computations that
  should agree can differ in the last bit and silently split one logical key
  across partitions, which is precisely the per-key-ordering property 0047 §7
  is built to protect.
- ~~**J2 — exact equality on `float` operands.**~~ **Withdrawn 2026-10-13 by
  ADR 0051, and replaced by something stronger.** The original entry proposed a
  lint on `a == b` and `a != b` over two `float` operands — the standard check
  in every language that has floats, for the usual reason (`0.1 + 0.2 == 0.3`
  is `false`) — and flagged it as a mere *candidate* because fiig's checker
  reports errors rather than warnings and the pattern is usually wrong rather
  than always wrong: comparing against a literal `0.0` is legitimate.

  ADR 0051 resolved that by removing the ambiguity instead of grading it.
  **`==` and `!=` over two `float` operands are a type error**, with
  `math.closeTo(a, b, relative:, absolute:)` and `math.isZero(x)` added to ADR 0049 §2.4
  as the replacements. The legitimate case that made J2 unwarrantable as an
  error is precisely the one `math.isZero` now spells unambiguously, so nothing
  is lost and no severity level is needed. This is Go's unused-variable move:
  take what other languages warn about and delete the spelling that made the
  warning necessary.

  Two things about this are worth having on the record here rather than only in
  0051. First, it is a **carve-out, not a non-admission**: `inferBinary`'s
  comparison branch constrains no operand types at all (`expr.go:233-241`,
  against `:222-226` where arithmetic is explicitly int-only), so float `==`
  would arrive for free when `float` lands, and excluding it is the first
  type-specific restriction comparison has carried. Second, it **narrows §2.3's
  argument without weakening its conclusion**: one of the two reasons given
  there for eliminating NaN was that `==` would stop being reflexive, and that
  reason is moot once float `==` does not exist. The ordering half — `<`, `<=`,
  `>`, `>=` ceasing to be total, so `not (a < b)` stops meaning `a >= b` —
  carries §2.3 on its own, and ordering on floats remains legal and necessary.
  0051 recorded a hole this left — `==` on a `model` holding a `float` field
  still compared it exactly, via structural equality the operand check cannot
  see — and **ADR 0052 closed it** by making comparability a recursive
  predicate over the whole type graph. J2's replacement is therefore bug class
  **M1**, which is strictly stronger than the lint J2 proposed: it rejects a
  float leaf at any depth and names the field path to it.

The category letter is `J` because ADR 0047 proposes `I1`–`I3`. Both ADRs are
Proposed and neither letter is claimed in `bug-classes.md` yet; if 0047
settles on a different letter, this one moves with it.

## Open questions

- **Built-in pure rules have no home yet, and this ADR now needs two sets of
  them.** `sqrt`, `sin`, `cos`, `atan2`, and `pow` are the actual content of
  the geometry use case; §2.7's crossing rules are required for `float` to
  interoperate with `int` at all. Neither set is expressible in fiig — there
  are no user generics, no iteration, and no way to declare a primitive
  operation. By ADR 0042's inexpressibility criterion they qualify as native,
  but 0042's closed set is *capabilities* (`Socket`, `TlsSocket`, `Clock`,
  `FileSystem`, `Random`), and these are pure: no capability handle, no
  effects, deterministic, and therefore invisible to the effect tracking that
  0042's set exists to make visible. They are a different kind of native
  thing than anything 0042 admits. **Settled by ADR 0049** (2026-10-12,
  revised 2026-10-13) in two parts: the leaves that fiig cannot implement
  correctly are bodiless members of a prelude formula group, spelled
  `math.sqrt(x)` and `math.floorToInt(x)`; composition above them is a new
  declaration kind, `formula`, which is pure, callable from a `rule`, may call
  only other formulas, and is always declared inside a named group. Adopting
  `float` without
  both would yield a type you can add and compare but neither do geometry with
  nor convert back, so 0049 is a prerequisite for implementing this ADR rather
  than a follow-on to it.
- ~~**ADR 0019's addendum says nothing about comparisons.**~~ **Resolved
  2026-10-27 by §2.9**: comparing an `int` with a `float` is a type error.
- ~~**Whether `%` applies to `float`.**~~ **Resolved 2026-10-27 by §2.8**: it
  does, truncated, as in Java, with `math.floorMod` for wrapping.
- ~~**Whether `int` overflow should fault.**~~ **Moved to ADR 0063**
  (Proposed 2026-10-27): it faults, with `math.addOrNone` and its siblings
  for code that wants overflow back as a value.
- ~~**The default type for a context-free fractional constant.**~~ **Resolved
  by ADR 0053**, which added the optional annotation this entry proposed
  (`name: Type = expr`). `float` stays the default when there is no annotation,
  and an author who wants another type annotates the binding.

## Implementation notes (2026-11-02)

Built as step 3 of the ADR 0049/0048 plan, after the `formulas` declaration
and before the `math` group.

- **Lexing and syntax.** `scanNumber` emits a `Float` token for a fraction
  (a digit on both sides of the dot, so `3.abs` is still a field access) or
  an exponent (a digit after its optional sign, so `2e` is `2` then `e`).
  `syntax.FloatLit` keeps the text as written, and the formatter prints it
  unchanged.
- **Constants (§2.4).** `compiler/constant` evaluates a constant expression
  exactly with `big.Rat`: literals, `-`, parentheses, and `+ - * / %`. A
  constant is an integer kind or a float kind, and `/` and `%` on two
  integer kinds truncate, as in Go. A literal exponent beyond ±10000 is an
  error, so `1e1000000000` can't build a billion-digit number. The checker
  lands a constant where it is used (`checker/constant.go`): against the
  expected type in `checkExpected`, against the other operand in
  `operands`, against the other elements in a list literal, and otherwise
  at its default type. It then records a `syntax.Const` on the expression's
  root, which the runtime returns without evaluating the parts. A value
  that does not fit is reported there: `2.5` as an `int` is A1, and
  overflow and constant division by zero are untagged, like the old
  literal check. This replaced ADR 0063's stopgap fold.
- **The checker is now how the runtime learns constant types**, so every
  program has to be checked before it runs. `run-cli` and `fiig-sql-run`
  already did that for the root package, but `compiler/loader` only built
  dependencies, so a dependency's `x: float = 2` would have run as the int
  `2`. The loader now type-checks each dependency with `CheckPackage` and
  refuses to load one that has diagnostics, naming the package in the
  error. That is a behaviour change: before, a dependency with a type error
  loaded and ran. Nothing in the corpus relied on it.
- **Typing.** Arithmetic takes two `int`s or two `float`s; mixing them is
  A1 and names the conversions. Mixing them in a comparison is M2 (§2.9).
  Unary `-` takes either type. M1 rejects `==` and `!=` on a `float` at any
  depth and names the path to it (`Order.total.amount is float`). Its walk
  (`checker.floatPath`) also goes into enum payloads, which `types.Holds`
  skips. M3 orders `float`.
- **Runtime.** `runtime.Float` is a `float64` that is never NaN, never
  infinite, and never `-0.0`. `x / 0.0` and `x % 0.0` fault with the
  integer message (`division by zero`), and overflow faults with
  `float overflow at <pos>`. Underflow rounds toward `0.0`. `%` is
  `math.Mod`, which truncates. A float prints as the shortest decimal that
  reads back exactly, always with a `.` or an exponent (`3.0`, `0.1`,
  `1e+21`), as Python's `repr` does. JSON output uses the same spelling.
- **Boundaries (§2.3's table).**
  - JSON decode reads a float from any JSON number, `3` included, and
    rejects one beyond float's range, such as `1e400`, as an argument
    error.
  - HTTP path and query values reject `NaN` and `Inf`, which `strconv`
    would otherwise accept. OpenAPI describes `float` as
    `number`/`double`.
  - `libraries/sql` now decodes `REAL` and `DOUBLE` columns to `float` and
    binds `float` parameters. A non-finite column value still faults; that
    is the gap §2.3 already records for this boundary.
  - Binary patterns have no float encoding yet (ADR 0058), so that row of
    the table has nothing to apply to.
- **Corpus.** `order-processing/rules/billable_weight.fiig` uses measured
  values, where `/ 5000` lands as a float with no conversion.
  `invalid-programs/float_error.fiig` attests the A1 mix and the M1
  comparison.
- **Tests.** In `compiler/checker`: `float_test.go`, the updated
  `intlit_test.go`, and the fixture case. In `compiler/runtime`:
  `float_test.go`. Also `TestFloatLiterals` in `compiler/lexer`,
  `TestDecodeStringFloat` in `compiler/adapter`, `TestLoadChecksDependencies`
  in `compiler/loader`, and `TestFloatColumnsRoundTrip` in `libraries/sql`,
  which runs against real SQLite.
- **§2.7, the crossings (2026-11-02, with ADR 0049's `math` group).**
  `math.toFloat` and the five `math.*ToInt` formulas are members of the
  prelude group; see ADR 0049's step 4 notes. `*ToInt` faults when the
  rounded value is outside `[-2^63, 2^63)`, naming the call and its
  position (`math.ceilToInt(2e+20) at 8:12 does not fit in int`), and
  `toFloat` uses Go's conversion, which rounds ties to even. The A1 message
  for mixed arithmetic names them. `order-processing/rules/billed_kilograms.fiig`
  rounds a float weight up to whole kilograms with `math.ceilToInt`.
