# ADR 0063: Integer Overflow Is a Fault, and `math.addOrNone` Reports It as a Value

Status: Accepted (2026-10-28, one-decision-at-a-time review). §1, §3, and
§4 implemented 2026-10-31 (see Implementation notes); §3's stopgap replaced
by ADR 0048's constant evaluator 2026-11-02, and §2 implemented the same
day with ADR 0049's `math` group.
Date: 2026-10-27
Related: ADR 0019 (arithmetic operators; division by zero is a fault), ADR
0048 (float, Proposed; §2.3 makes float overflow a fault), ADR 0049 (the
`math` formula group, Proposed), ADR 0058 (binary patterns), ADR 0062 (JSON
numbers, Proposed and paused).

## Context

`int` is int64 (`runtime.Int`), and arithmetic on it wraps silently. The
runtime evaluates `+ - * /` with Go's operators (`compiler/runtime/expr.go`),
and Go wraps:

| Expression | Result today |
| --- | --- |
| `9223372036854775807 + 1` | `-9223372036854775808` |
| `-(-9223372036854775808)` | `-9223372036854775808` |
| `-9223372036854775808 / -1` | `-9223372036854775808` |

Nothing in the spec, `grammar.md`, or any ADR decided this. It is what Go
does. ADR 0048 names it as an open problem, because 0048 makes float overflow
a fault, so `addMoney` on two large balances would wrap while
`1e308 * 10.0` faults. ADR 0062 §3 works around it: it makes
`string.toInt()` native partly because a digit loop written in fiig can't
rely on overflow being noticed.

Two more gaps turned up while drafting this:

- **An oversized integer literal passes the checker.** `x =
  99999999999999999999` type-checks, and the runtime faults on it with
  `invalid integer literal` (`compiler/runtime/expr.go:33`). The problem is
  visible in the source text, so it should never reach a run.
- **Binary pattern sizes are arithmetic on untrusted input.** In `<<len:
  i32be, body: bytes(len * 8)>>`, `evalSize` (`compiler/runtime/bytes.go`)
  computes `len * 8` with the same wrapping operators. A hostile `len` can
  wrap round to a small positive size and match bytes the pattern was never
  meant to accept.

| Language | On overflow | Way to detect it as a value |
| --- | --- | --- |
| C | undefined behaviour | compiler builtins |
| Java, Go | wraps | Java: `Math.addExact` throws |
| C# | wraps; `checked { }` throws | — |
| Rust | panics in debug builds, wraps in release | `checked_add` → `Option` |
| Swift | always traps | `addingReportingOverflow` |
| Python, Erlang | no overflow; integers grow without limit | — |

## Decision

### 1. Overflowing `int` arithmetic is a fault

`+`, `-`, `*`, unary `-`, and `/` fault when the true result doesn't fit in
int64. For `/` the only such case is `MinInt64 / -1`. `%` can't overflow,
because `MinInt64 % -1` is `0`, which is the true result. The fault carries
the operator and the source position, like division by zero (ADR 0019).

The int form of `math.abs` (ADR 0049) faults the same way on its one
overflowing input, `MinInt64`, which is unary `-`'s case. It gets no
`absOrNone`, for the reason §2 leaves out `negOrNone`. The other int members
of `math` (`min`, `max`, `floorMod`) can't overflow; `floorMod(MinInt64, -1)`
is `0`. *(Added 2026-10-28.)*

A fault cancels the workflow and rolls back the active transaction
(`fiig-lang.md` §3.6), so a wrapped value is never committed.

### 2. `math.addOrNone`, `math.subOrNone`, `math.mulOrNone` (and float `divOrNone`) report overflow as `None`

```
match math.addOrNone(balance, deposit) {
    None -> return Err(BalanceOverflow)
    total -> ...
}
```

Each takes two `int`s and returns `int | None`: the exact result, or `None`
when it doesn't fit. They are bodiless members of ADR 0049's prelude `math`
group. Code that expects overflow, such as a parser of untrusted digits or a
running total of user-supplied quantities, uses them. Everything else uses
the operators and faults.

The `OrNone` suffix states the return type in the name. That is Kotlin's
convention (`toIntOrNull`, `firstOrNull`). It lets these members sit in
`math` beside members that fault (`math.sqrt`, `math.floorToInt`) without a
reader having to guess which kind a call is.

`negOrNone` and `divOrNone` are left out until a program needs them. Each
has exactly one overflowing input (`MinInt64`, and `MinInt64 / -1`), which a
caller can test for directly.

**Float forms** (added 2026-10-28). `addOrNone`, `subOrNone`, `mulOrNone`, and
`divOrNone` also take two `float`s and return `float | None`, `None` where
ADR 0048 §2.3 would fault. This follows ADR 0049's same-type int/float forms
(`abs`, `min`, `max`). Float gets `divOrNone` although int doesn't: float
division overflows on a range of inputs (`x / 1e-300` for any `x` above about
1.8e8) and divides by zero, so there is no single input to test for. A member
earns a native `OrNone` form when a fiig-written pre-check can't be exact,
because whether the result overflows depends on rounding near the largest
float. `expOrNone` and `powOrNone` are in that class and are added when a
program needs them. `sqrtOrNone` and `logOrNone` are not: their domain checks
(`x < 0.0`, `x <= 0.0`) are exact, so a library can write them (see the
numerics goal in `fiig-lang.md`).

`math` also gains `minInt`, `maxInt`, and `maxFloat` as untyped constants
(ADR 0049 §2.7), so the boundaries this ADR talks about can be named in
source rather than typed out as literals.

### 3. A constant expression outside int64 is a compile error

*Reworded 2026-10-28.* Integer literals are untyped constants (ADR 0048 §2.4,
accepted): a constant expression is evaluated exactly at compile time, and the
range check applies to its value when it takes the type `int`, not to each
literal inside it. This is Go's rule.

| Source | Result |
| --- | --- |
| `-9223372036854775808` | legal: the exact value fits int64 |
| `9223372036854775807 + 1` | compile error: a constant expression that doesn't fit |
| `99999999999999999999` | compile error |
| `x + 1`, `x` a variable | not a constant; overflow is §1's runtime fault |

Checking each literal on its own, as the first draft said, would reject
`-9223372036854775808`. It parses as `-(9223372036854775808)`, and the inner
literal doesn't fit. Today that expression faults at runtime
(`compiler/runtime/expr.go:33`) for the same reason.

**Stopgap if §3 ships before 0048's constant evaluator.** The checker may
range-check literals alone, provided a unary `-` applied directly to an
integer literal is folded into it first, so `-9223372036854775808` is checked
as one value. Java's lexer does the same thing. Once the constant evaluator
exists, the fold is subsumed by it and should be removed.

### 4. Overflow inside a binary pattern size is a non-match

A segment size that overflows makes the pattern fail to match, the same
outcome `evalSize` already gives a zero divisor. The bytes come from outside
the program, so bad sizes are a property of the input, not a programming
mistake, and ADR 0058 already settled that malformed input is a fall-through
to the next arm.

This is not only for consistency. Today a wrapped size is usually caught by
accident: the matcher rejects a size that is negative or larger than the
remaining input (`compiler/runtime/bytes.go`). But wrapping can also produce a
small, plausible size. In `<<len: i64be, body: bytes(len * 8)>>`, a hostile
`len` of 2^61 + 1 makes `len * 8` wrap to exactly `8`, which passes the bounds
check, and the pattern matches a frame whose length field is nonsense. That is
the integer-overflow-in-a-length-field bug behind a long line of parser CVEs.
It needs a 64-bit length field or a size that multiplies fields; an `i32`
field times a small constant can't overflow int64. *(Example added
2026-10-28.)*

## Rationale

**Fault rather than wrap.** A wrapped result is a wrong number that nothing
reports. A fault stops the workflow, names the operator and line, and rolls
back the transaction. For an LLM working from compiler and test feedback,
that difference decides whether it gets feedback at all: a fault appears on
any test run that reaches it, while a wrapped value is caught only by a test
that asserts the exact number. This also makes integer overflow consistent
with integer division by zero (ADR 0019) and float overflow (ADR 0048 §2.3).

**Why not arithmetic that can't overflow (Python, Erlang).** Every system
fiig exchanges integers with uses a fixed 64 bits: SQL `bigint`, Postgres
`int8` on the wire, and JSON from int64 producers. Integers without a limit
would only move the range check to every boundary, and every value would get
slower.

**Why not `Result` or `int | None` from the operators.** F1 requires every
`Result` to be handled, so `a + b` would need unwrapping at every use. The
operators fault, and §2's named members give the value form only where it is
asked for.

**Faults can't be caught, and that is the cost.** Code that needs to recover
from overflow has to choose §2's members in advance. The operator is the
common case and should be short. The recoverable case is rare and should say
what it does.

**Why `addOrNone` and not `tryAdd` or `checkedMath.add`.** Under §1 the
operators check overflow too, so "checked" no longer separates the two forms.
What separates them is what happens on overflow, and `OrNone` names that. In
Rust and C#, `try` implies an error carrying a reason (`try_from` returns
`Result`), which isn't what these return. Fiig may also want `try` later as
error-propagation syntax, as Swift and Zig use it.

**Compile-time ranges are a possible next step.** Overflow depends on runtime
values, so neither wrapping nor faulting can be checked at compile time in
general. Declared ranges (`quantity: int(0..10_000)`, as in Ada and Pascal
subranges) would let the checker compute an expression's range and report
possible overflow as a compile error. That is a separate, larger feature. It
depends on this ADR, because a range can only be enforced if exceeding it is
an error.

## Alternatives considered

**A. Keep wrapping (Java, Go).** Rejected: it is the silent wrong-number
behaviour this ADR exists to remove.

**B. Rust's split, panicking in debug and wrapping in release.** Rejected:
fiig has no build modes, and a program that behaves differently in tests than
in production defeats the test loop described above.

**C. A `checked { }` block (C#).** Rejected: it makes the meaning of `+`
depend on lexical context, and the safe behaviour would need opting into.

**D. Saturating arithmetic (stop at `MaxInt64`).** Rejected as a default:
saturating gives a wrong number the same way wrapping does, just a different
one.

## Consequences

- `compiler/runtime/expr.go`: overflow checks on `+ - *`, unary `-`, and `/`.
  Go's `math/bits` or the standard pre-checks (`r > 0 && l > MaxInt64 - r`)
  are enough.
- `compiler/runtime/bytes.go`: `evalSize` returns a non-match on overflow
  (§4).
- `compiler/checker`: constant expressions outside int64 are reported (§3),
  or, before ADR 0048's constant evaluator exists, literals with a directly
  applied unary `-` folded in. The runtime's `-literal` path must then
  evaluate the folded value too, or `-9223372036854775808` still faults.
- ADR 0049: `math.addOrNone`, `math.subOrNone`, `math.mulOrNone` join the
  prelude `math` group. They wait on 0049's group mechanism; §1, §3, and §4
  do not, and can be implemented first.
- ADR 0048: its open question on `int` overflow is resolved by this ADR.
- ADR 0062 (paused): its §3 should rename `string.toInt()` to
  `string.toIntOrNone()` for consistency with §2. Its argument for a native
  parser still holds: a digit loop could now use `math.mulOrNone`, but the
  native parser stays simpler.
- No new bug class. §3 is an ordinary type error.

## Implementation notes (2026-10-31)

- **§1.** `compiler/runtime/intarith.go` holds `intArith` and `intNeg`,
  which return the result and whether it fit. `evalBinary` and the unary `-`
  path in `expr.go` use them. The fault reads `integer overflow at 2:12:
  9223372036854775807 + 1 does not fit in int`; the position is the
  expression's start, since a `BinaryExpr` keeps no operator position.
  Division by zero is still checked first, with its old message.
- **§3, stopgap form.** `checker.intLitFits` reports a literal outside
  int64, untagged, as an ordinary type error. A `NegExpr` whose operand is an
  `IntLit` is checked as one value through the new `syntax.NegIntValue`,
  which also accepts `-0x8000000000000000`; `-(9223372036854775808)` is still
  an error, as in Java. Binary pattern literal segments and sizes get the
  same check. A literal in a binary *expression* is checked once, by the
  ordinary expression path, before A14's width check. The runtime folds
  `-literal` the same way, so `-9223372036854775808` evaluates.
- **§4.** `evalSize` routes `+ - * /` and unary `-` through the same
  helpers and treats overflow as a non-match. The test uses the ADR's own
  example: `len * 8` with `len` = 2^61 + 1, which used to wrap to exactly 8
  and match.
- Tests: `compiler/runtime/intarith_test.go` (every operator at the int64
  limits, the fault message, the folded minimum, the hostile frame) and
  `compiler/checker/intlit_test.go`. Checked end to end with `run-cli` and
  `check-cli`.
- **§3, stopgap removed (2026-11-02).** ADR 0048 §2.4's constant evaluator
  (`compiler/constant`) replaced `intLitFits` and `syntax.NegIntValue`. A
  whole constant expression is now evaluated exactly and range-checked where
  it lands, so `-(9223372036854775808)` is legal, `9223372036854775807 + 1`
  is a compile error instead of a runtime fault, and the runtime reads the
  checker's value rather than folding `-literal` itself.
- **§2 (2026-11-02, with ADR 0049's `math` group).** `math.addOrNone`,
  `subOrNone`, and `mulOrNone` have an int and a float form, and
  `divOrNone` a float form, as the 2026-10-28 addition says. Each runs
  the operator's own arithmetic (`intArith`, `floatArith`) and returns
  `None` exactly where the operator would fault, including a float
  division by zero. `math.minInt`, `math.maxInt`, and `math.maxFloat` are
  untyped constants, so `math.maxInt + 1` is a compile error, like the
  literal it replaces. Tests: `TestMathOrNone` and `TestMathConstantsRun`
  in `compiler/runtime/math_test.go`.
