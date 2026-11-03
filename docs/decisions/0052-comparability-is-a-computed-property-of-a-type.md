# ADR 0052: Comparability Is a Computed Property of a Type

Status: Accepted 2026-10-27. M2, M3, and M1's handle and `Result` halves
implemented 2026-10-27 (`compiler/checker/compare.go`); M1's float half and
`float` in M3 implemented 2026-11-02 with ADR 0048 (`checker.floatPath`,
which also walks enum payloads).
Date: 2026-10-13

> **Note (2026-10-17):** ADR 0055 removed `value` declarations. References to `value` types below are historical.
Settles: ADR 0051's open question on the structural-equality hole
Depends on: ADR 0048 (`float`), ADR 0051 (no severity level)

## Context

ADR 0051 made `==`/`!=` over two `float` operands a type error, and left a hole
it could not close in passing: equality is **structural**, so `==` on a `model`
holding a `float` field still compares it exactly, through a spelling an
operand-type check cannot see. `runtime.Equal` (`compiler/runtime/value.go:178-220`)
recurses through `Record`, `EnumValue`, and `Result`, so a float leaf is reached
no matter how deeply it is buried.

Reading the operators to settle that turned up three more facts that change the
shape of the answer:

- **Comparison constrains no operand types at all today.** `inferBinary`'s
  default branch infers both sides for their own errors and returns `bool`
  (`compiler/checker/expr.go:233-241`). So `1 == "hello"` type-checks and
  evaluates to `false`. Arithmetic is explicitly int-only two cases above it
  (`:222-226`); comparison is unconstrained.
- **Ordering is already type-restricted, but at runtime.** `runtime.Less`
  (`value.go:225`) is defined for `Int` and `Str` only and returns
  `ok = false` otherwise, which `expr.go:181` turns into a Fault:
  `"%s and %s are not ordered"`. The checker's own comment says this out loud —
  *"operand well-definedness (ordering) is checked at runtime (Less/Equal)"*.
  So `customer < otherCustomer` is a statically knowable defect that fiig
  currently discovers by faulting.
- **`runtime.Equal` has no `List` case**, so it falls through to
  `return false` (`value.go:219`). Once `List<T>` lands in Milestone 7,
  `a == b` on two identical lists silently answers `false`.

The hole is therefore not a float question. It is that fiig has four operators
whose operand requirements were never decided.

## Decision

### 2.1 `Comparable` is a recursive predicate over resolved types

A new predicate in `compiler/types`:

| Type | Comparable |
|---|---|
| `int`, `string`, `bool` | yes |
| `float` | **no** |
| `None` | yes |
| `Unknown` | **yes** — permissively, see below |
| `Named` → `model` | all field types |
| `Named` → `enum` | all variant payload field types |
| `Union` | all members |
| `Record` (anonymous) | all field types |
| `Result<T, E>` | **no**, at any depth (2026-10-27) |
| `List<T>` | `Comparable(T)` |
| `BatchResult<K, S, F>` | all three |
| `bytes` | yes; **not ordered** (2026-10-27) |
| `Connection` | **no** — a handle (ADR 0060) |
| interface-typed provider, capability group | **no** — a handle (2026-10-27) |

*Rows added 2026-10-27.* `bytes` compares by content (ADR 0058) but is not
ordered. Python and Rust order bytes lexicographically, but Go does not, and
nothing in the corpus orders binary data. A capability value is not comparable,
for the same reason a `Connection` isn't: it is a handle to behaviour, not
data. ADR 0043 puts the choice of provider at explicit selection points, and
`provider == Stripe` would be a second, hidden way to branch on that choice,
special-casing one implementation behind the interface. Go permits interface
comparison and it surprises people; Rust trait objects can't be compared. The
`value` row this table once had is gone with ADR 0055.

*`Result` changed 2026-10-27.* A `Result` is an outcome to handle, not data to
inspect: the way to compare one is to `match` it and compare what is inside.
`resultA == resultB` would be a way of looking at a `Result` without handling
it. That is not a hole unique to comparison, since F1 fires only on a bare
statement and `let r = charge(x)` with `r` never matched already escapes it,
but comparison need not be one more route around it. Nothing in the corpus
compares `Result`s. Like a `Connection`, a `Result` is found at any depth
(`types.HoldsResult`, the same walk), so a `List<Result<…>>` or a model with
a `Result` field is not comparable either. `BatchResult<K, S, F>` stays
comparable by its arguments: its contents are already-sorted successes and
failures, not unhandled `Result`s.

It carries a visited set: a model can reach itself through `T | None`, and the
predicate must terminate on a cyclic type rather than recurse forever.

`Unknown` is comparable **by design, not by oversight.** `types.Equal` already
documents Unknown as equal to anything *"so the checker doesn't cascade false
positives into territory it hasn't modeled"* (`types.go:103-106`), and a
capability call's result is Unknown. A comparability check that rejected
Unknown would start reporting errors on every capability-derived value, which
is the failure mode that design note exists to prevent.

### 2.2 `==` and `!=` require both operands to be comparable

This is ADR 0051 §2.3's float rule, stated transitively. A float anywhere in
the operand's type graph makes the comparison a type error — **M1**.

The diagnostic must name **the path to the offending leaf**, not just the
operand type:

```
cannot compare Order with ==: Order.total.amount is float
    (use math.closeTo or compare a specific field)
```

This is a hard implementation requirement rather than a nicety. `Order` may be
rejected because of a float five fields down, and an error naming only `Order`
would point at correct-looking code — the exact diagnostic failure mode bug
class G3 was fixed to avoid (see ADR 0050 §2.5).

### 2.3 `==` and `!=` require both operands to have the same type

`1 == "hello"` becomes a type error — **M2**. It passes ADR 0051 §2.2's three
tests cleanly: decidable, wrong in every reading, and consequential (it is
currently a silent `false`). `types.Equal`'s existing exact-nominal rule is the
comparison, with Unknown's permissiveness carried through unchanged.

This is adjacent to the hole rather than part of it, and is included because it
is the same line of code and the same question — *what may these operators take?*

### 2.4 Ordering becomes a static check, and is not structural

`<`, `<=`, `>`, `>=` require both operands to be `int`, `string`, or `float`,
and **the same type** — **M3**, with the mixed-type case reported as M2. (Same
type added 2026-10-27 to match ADR 0048 §2.9: `count < threshold` with an `int`
and a `float` is a type error. The `value` case is gone with ADR 0055.) Nothing structural is ordered: there is no
field-wise ordering of models, and none is proposed.

**`float` is ordered but not comparable**, which is the one place §2.1 and §2.4
disagree and is deliberate. ADR 0048 eliminated NaN precisely so that ordering
on floats would stay total, and 0048's revision records that the ordering half
of its argument is what now carries its §2.3 alone. `a < b` on floats is
correct and necessary; `a == b` is the operation that lies.

`Ordered` needs no recursion. It once had to look through `value` wrappers,
which ADR 0055 removed; every ordered type is now a primitive. `bool` is not
ordered: Go agrees (its booleans are comparable but not ordered), nothing in
the corpus orders bools, and `false < true` encodes no business meaning.

This replaces the runtime Fault at `expr.go:181`. A statically decidable defect
reported at runtime is strictly worse than the same defect reported by the
checker, and `runtime.Less`'s `ok` return becomes an invariant assertion rather
than a reachable path.

### 2.5 `runtime.Equal` gains a `List` case

Element-wise, with length checked first. If `List<T>` lands before this is
implemented, the checker must reject list `==` outright instead — the one
outcome ruled out is the current silent `false`.

ADR 0057 §6 (2026-10-20) implements this case together with list literals,
and adds a same-storage shortcut ahead of the walk, which is safe because
values are immutable.

## Rationale

**The mechanism is Go's and Rust's, and they agree.** A Go struct is comparable
iff all its fields are; a Rust `#[derive(PartialEq)]` propagates to field types.
Both compute comparability from structure rather than declaring it per type,
which is what §2.1 does. The notable thing is that they agree on the mechanism
while differing on floats — `float64` *is* comparable in Go — so Go supplies the
shape and not the stance.

**Rust supplies the stance, and fiig has to take a blunter version of it.**
Rust marks a float-bearing type as `PartialEq` but not `Eq`, which is exactly
"you may compare this, but equality is not an equivalence relation here." ADR
0048 already established that fiig cannot express that split — there is no
user-declarable interface mechanism, and 0048 chose to eliminate NaN rather than
model partiality. The same limit applies here: fiig has no way to say "equatable
but not reflexively," so the choice is between permitting float equality
silently and refusing it. Refusing is the same call 0048 made, for the same
reason.

**Java is the cautionary tale for auto-deriving float equality.** A Java record
generates `equals` using `Double.equals`, which compares bits: `NaN` equals
`NaN`, and `+0.0` does not equal `-0.0`. Both results contradict `==` on the
same primitives. That is what happens when structural equality is derived over
floats without deciding anything — two equality operators that disagree, in a
language that never meant to have two.

**Why transitive is not the overreach ADR 0051 worried it might be.** 0051's
open question called it "a wide restriction for a narrow hazard." Having read
the corpus, that worry does not survive:

- **Migration is zero.** The corpus contains exactly two `==` uses —
  `reserved == 0` (int) and `region == Region.US` (a payload-free enum variant,
  `qualifies_for_free_shipping.fiig:7`) — and `float` is not implemented, so no
  existing type has a float leaf to reject.
- **The pattern it blocks is the hazard itself.** The legitimate-looking use is
  change detection: `if newReading == oldReading`. When `Reading` holds a
  `temperature: float`, that comparison is wrong for precisely the reason J2 was
  raised — and it is *more* dangerous than a bare `a == b`, because the float is
  invisible at the call site.
- **The escape hatch is specific and already specified.** ADR 0049 §2.4's
  `math.closeTo(a, b, relative:, absolute:)` and `math.isZero(x)`, or comparing a field
  that is not a float.
- **The cost is a predicate, not a subsystem.** 0051 guessed this would be "a
  larger change"; it is one recursive function over seven type cases with a
  visited set.

**Why the restriction lands on the operator and not on the declaration.** The
alternative was to reject a `model` that *contains* a float, or to require
models to opt into comparability. Both are worse. A float field is perfectly
legitimate — that is what ADR 0048 admitted `float` for — and the defect only
exists at a comparison site. Checking at the operator keeps the rule where the
mistake is, and needs no new declaration syntax.

**Why structural model equality survives at all.** A tempting wider answer was
to forbid `==` on models entirely, on the grounds that "are these two customers
the same?" is an identity question that belongs in a named `rule`. That would
have closed the hole with no transitive analysis. It is rejected because
change detection on a float-free model — `if newAddress == oldAddress { return }` —
is good code that the rule would break for no reason. The hazard is the float
leaf, so the restriction should be the float leaf.

**Why ordering is pulled in.** Settling equality statically while leaving
`customer < otherCustomer` to fault at runtime would leave fiig's four
comparison operators split across two enforcement models for no reason anyone
chose — the runtime check predates the question rather than answering it.
Converting it is also the strictly cheaper direction: it adds a check and
deletes a reachable fault.

## Consequences

**ADR 0051's open question is closed, and M1 is J2 restored at the right level.**
ADR 0048's J2 was withdrawn as a lint because fiig has no warning level; it
returns here as a type rule whose transitive form is strictly stronger than the
lint would have been. That is 0051's "move it down into typing" step carried
through to the end rather than stated and left.

**Implementation:**

- `compiler/types` — `Comparable(Type) bool` and `Ordered(Type) bool`, with the
  visited set. This is the whole of the new machinery.
- `compiler/checker` — `inferBinary`'s default branch splits: equality checks
  §2.2 and §2.3, ordering checks §2.4. M1's diagnostic needs the field path,
  which means the predicate must return a path on failure rather than a bare
  `bool` — `Comparable` is better shaped as `(ok bool, path []string)`.
- `compiler/runtime` — a `List` case in `Equal` (§2.5); `Less`'s `ok = false`
  branch becomes unreachable and its Fault message should say so rather than
  being deleted, since the runtime is also reachable from `run-cli` on
  unchecked input.
- `docs/specification/grammar.md` — the comparison operators' operand rules,
  which are currently unstated.
- `docs/specification/bug-classes.md` — M1–M3 in the I/J/K/L/M merge pass.

**Five Proposed ADRs now hold an unused bug-class letter** (I, J, K, L, M for
0047–0052; 0051 proposes none). The merge pass is overdue and should happen
when the first of them is implemented.

**The three-tests procedure from ADR 0051 did real work here.** M2 and M3 were
found by applying it to the operators rather than to the reported defect, and
both pass it. That is some evidence the procedure is a usable tool and not just
a rationalization of the severity decision.

## Proposed bug classes

- **M1 — `==`/`!=` on an operand whose type transitively contains `float`**
  (and, since 2026-10-27, a handle or a `Result`; see §2.1).
  Includes the direct `a == b` case ADR 0051 §2.3 specified. The diagnostic
  names the field path to the float leaf.
- **M2 — `==`/`!=` on operands of different types.** Currently a silent `false`.
- **M3 — `<`/`<=`/`>`/`>=` on an operand type that is not `int`, `string`,
  or `float`.** Currently a runtime Fault.

The letter is `M` because `bug-classes.md` runs A–H and ADRs 0047–0050 hold
`I`–`L`.

## Open questions

- ~~**Absolute versus relative tolerance for `math.closeTo`.**~~ Settled in
  ADR 0051 (2026-10-27): both, by Python's `math.isclose` rule, with both
  tolerances required at the call site and no defaults. That matters more
  here, because §2.2 makes `closeTo` the only way to compare float-bearing data.
- *Resolved 2026-10-27:* whether `Result<T, E>` is comparable (no, §2.1),
  and whether `bool` is ordered (no, §2.4; an earlier draft wrongly said Go
  permits `false < true`). Whether a `value`'s underlying type should be
  restricted to a primitive was made moot by ADR 0055, which removed `value`.
  F1 not covering a `Result` bound by `let` and never matched is a separate
  finding, not a comparability question.
