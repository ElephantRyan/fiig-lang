# ADR 0064: `decimal` Is a Primitive Type: 34 Digits, Exact or a Fault, Rounded Only by Name

Status: Accepted (2026-10-28, one-decision-at-a-time review). Implemented
2026-11-03 in three steps; see Implementation notes. Reading a decimal back
from SQL as a decimal, and §8's REAL rule, moved to ADR 0066 (declared row
types, Proposed).
Date: 2026-10-28
Related: ADR 0019 (arithmetic operators; no implicit widening), ADR 0048
(float; §2.3 boundary rule, §2.4 untyped constants, §2.5 decimal promised,
§2.7 named crossings), ADR 0049 (`math` group; same-name forms per type),
ADR 0052 (comparability), ADR 0062 (JSON numbers, paused for this ADR), ADR
0063 (integer overflow faults; `OrNone` members).

## Context

ADR 0048 §2.5 promised a decimal type and said why fiig needs both: float is
for measured and geometric values, where every representation rounds, and
decimal is for values that *are* their decimal representation: quantities as
recorded or typed (`3.25` kg, `12.5` percent), rates and ratios, readings that
instruments and standards report in decimal, prices and amounts, and any number
that travels as decimal text through JSON, SQL, and CSV and must come back out
unchanged. Money is one case among these, not the reason for the type. ADR 0019 and
`grammar.md`'s known-gaps entry left its design until "an example forces it."

The corpus has no such example. Every fractional-looking value in it is a
scaled `int`
(`Order.total`, `ChargeRequest.amount`, `ShippingRate.flat`). What forces the
design now is ADR 0062, which needs an exact type for JSON numbers like
`19.99`, and the project owner's direction (2026-10-28). §8 requires this ADR
to ship with a corpus example that needs fractions, so the type is dogfooded
rather than only specified.

Several properties are already fixed by accepted ADRs:

- No implicit conversion to or from `int` or `float` (ADR 0019's addendum,
  0048 §2.7). Crossing is a named call whose name says how it rounds.
- Numeric literals are untyped constants (0048 §2.4). `19.99` is exactly
  nineteen point nine nine in a decimal context. A fractional constant with no
  context defaults to `float`, but that is not a silent bug: `ratio = 0.125`
  followed by `ratio * quantity`, where `quantity` is decimal, is a type error.
- The `math` members gain a decimal form under the same names (ADR 0049 §2.4:
  "Decimal will add a third form to the same names").
- Values that are their decimal representation never become `float` (0048
  §2.5).

## Decision

### 1. `decimal` is a primitive with 34 significant digits

`decimal` is a fifth primitive type. **A decimal is at most 34 digits,
written out in full**: at most 34 significant digits, and at most 34 of them
after the point. `19.99` is coefficient `1999`, scale `2`. Every value
therefore has |x| < 10^34, the smallest positive value is 10^-34, and any
decimal prints in plain notation in at most 36 characters. Thirty-four digits
is IEEE 754 decimal128's precision.

*Amended 2026-10-28 (from the §8 review).* The first draft took decimal128's
exponent range as well, up to 10^6145. That made plain-notation output
unbounded: the JSON number `1e6000` has one significant digit, fit the range,
and printed as 6,001 characters. Values that large or that small are not
recorded decimal quantities; they belong to `float`.

The cap is a semantic rule, not a storage format. The Go implementation may
use `math/big` with the cap enforced.

### 2. Arithmetic is exact or a fault

`+`, `-`, `*`, and unary `-` on two decimals give the exact result. When the
exact result needs more than 34 digits, or falls outside the magnitude range,
the operation **faults**, as int overflow does under ADR 0063. Nothing is ever
rounded silently: `19.99 * 0.0725` is exactly `1.449275`, and rounding it to
two places is the program's decision, made by name (§4).

`%` is exact on decimals and truncates, as on `int` and `float` (0048 §2.8).
`math.floorMod` gains a decimal form.

`math.addOrNone`, `subOrNone`, and `mulOrNone` gain decimal forms returning
`decimal | None`, following ADR 0063 §2's float forms. Whether a product
exceeds 34 digits can't be checked before computing it, which is the condition
0063 sets for a native `OrNone` form.

### 3. `/` is not defined on `decimal`; division is `math.divide`

`a / b` with decimal operands is a **compile error**. Division goes through

```fiig
math.divide(a, b, places: 2, rounding: Rounding.HalfEven)
```

which returns the quotient rounded to `places` decimal places in the named
mode. Division by zero faults (ADR 0019). A quotient that doesn't fit the cap
at the requested places faults.

`10.00 / 3` has no exact decimal answer, so every division needs a precision
and a rounding mode, and fiig requires both to be written down. The compile
error tells the author, or an LLM, which call to make before the program ever
runs.

**The quotient is rounded once.** `math.divide` computes the exact quotient
and rounds it to `places` in a single step. It never rounds to 34 digits first
and then to `places`: rounding twice gives wrong answers on ties. For example,
a quotient of `0.12499999…9` rounded first to fewer digits can become `0.125`,
which `HalfUp` then turns into `0.13` instead of the correct `0.12`.
*(Added 2026-10-28.)*

A decimal divisor that is an `int` variable needs `math.toDecimal(count)`;
there is no mixed `(decimal, int)` form, per ADR 0049's no-mixing rule.
Constants need nothing, since `3` takes the decimal type from context.

### 3a. Splitting an amount is `math.allocate`, not division

```fiig
shares = math.allocate(10.00, parts: 3, places: 2)   // [3.34, 3.33, 3.33]
```

`math.allocate(amount: decimal, parts: int, places: int) -> List<decimal>`
splits `amount` into `parts` shares at `places` decimal places that **always
add back to exactly `amount`**. Each share is `amount / parts` rounded toward
zero (`Rounding.Down`), and the remainder, a whole number of units in the last
place, is handed out one unit at a time to the first shares. A negative
amount allocates the same way with the signs reversed.

Dividing `10.00` three ways with `math.divide` gives `3.33` three times, and
the shares add to `9.99`. Any split that must add back to its total needs
this: a budget across departments, a batch across containers, a measured
quantity across samples, a cost across accounts. It is the allocation
operation from Fowler's Money pattern, also provided by Java's Moneta and
Ruby's `money` gem, but nothing about it is specific to currency.

`parts` must be at least 1, and `amount` must be representable at `places`
(`math.allocate(10.005, parts: 2, places: 2)` faults, because the amount
itself has a third decimal place). Both are programming mistakes, so both are
faults. Splitting by weights (a 60/40 split) is left until a program needs it.
*(Added 2026-10-28.)*

### 4. Rounding is one prelude enum and one `math.round`

```fiig
enum Rounding {
    HalfUp      // ties away from zero: 2.345 -> 2.35, -2.345 -> -2.35
    HalfEven    // ties to the even digit: 2.345 -> 2.34, 2.355 -> 2.36
    Up          // away from zero:  2.341 -> 2.35, -2.341 -> -2.35
    Down        // toward zero:     2.349 -> 2.34, -2.349 -> -2.34
    Ceiling     // toward +infinity: 2.341 -> 2.35, -2.349 -> -2.34
    Floor       // toward -infinity: 2.349 -> 2.34, -2.341 -> -2.35
}
```

(examples at `places: 2`)

```fiig
math.round(x, places: 2, rounding: Rounding.HalfUp)
```

`HalfUp` is ordinary rounding as taught in school, what the project owner
called "natural round." `Up` and `Down` are round up and round down by
magnitude, which is how Excel's `ROUNDUP` and `ROUNDDOWN` and Java's
`RoundingMode.UP` and `DOWN` define them, so `-2.341` rounds up to `-2.35`. `Ceiling` and `Floor` are the by-direction forms, for when the sign
matters. `HalfEven` is banker's rounding, IEEE 754's default, used in statistics,
science, and accounting because it doesn't bias sums upward.

`places` is an `int` and may be zero or negative: `places: -2` rounds to the
nearest hundred, as Excel's `ROUND` does. Both `places` and `rounding` are
required and named, with no defaults, for the reason ADR 0051 gave for
`closeTo`'s tolerances: a default hides a choice that standards,
regulations, and domain conventions often make for you.

`Rounding` is a prelude enum, like `SocketError` (ADR 0060). `math.divide`
(§3) takes the same two arguments, so rounding and division share one
vocabulary.

The float `math.round(x)` keeps its one-argument form. `places` is not offered
on `float`: a binary float can't hold most two-place results, so
`round(2.675, 2)` gives `2.67` in Python, which is exactly the surprise this
type exists to avoid.

### 5. Scale is kept for display; equality and ordering use the value

A decimal remembers its scale. `1.50` prints as `1.50` and `1.5` as `1.5`.
Results take the scale of their operation:

| Operation | Result scale |
| --- | --- |
| `+`, `-` | the larger of the two |
| `*` | the sum of the two |
| `math.round`, `math.divide` | `places` (zero if `places` is negative) |
| a literal in decimal context | the digits written after the point |

`==`, `!=`, `<`, `<=`, `>`, and `>=` compare **values**: `1.50 == 1.5` is
true. `decimal` is comparable and ordered in ADR 0052's tables. This is
Python's and C#'s behaviour and not Java's, whose `BigDecimal.equals` says
`1.50` and `1.5` are different, a well-known source of bugs in hash maps and
tests.

**Trailing zeros give way before the cap does** (added 2026-10-28). The
table's scale can carry trailing zeros that count toward the 34 digits:
squaring `2.00` repeatedly gives `4.0000`, `16.00000000`, and so on, so the
coefficient grows two digits a step while the value needs far fewer. When an
exact result exceeds 34 digits only because of trailing zeros, those zeros are
dropped (the scale is lowered, but not below zero) until it fits. The value
is unchanged, so
nothing is rounded. Only a result whose significant digits don't fit faults.
IEEE decimal128 does the same.

**Zero has no sign** (added 2026-10-28). `-0.01 * 0` and
`math.round(-0.001, places: 2, rounding: Rounding.HalfUp)` are `0.00`, never
`-0.00`, as ADR 0048 normalizes `-0.0`. IEEE decimal keeps a negative zero;
fiig doesn't, because a printed `-0.00` is a bug report.

**Equal values can print differently** (stated 2026-10-28). Because scale is
kept, `a == b` does not mean `a` and `b` are interchangeable: `1.5` and
`1.50` are equal and print differently. ADR 0048 normalized `-0.0` so that
equal floats are indistinguishable by any operation; decimal deliberately
does not, because the scale is information people want shown (a reading
recorded as `1.50`, not `1.5`). Two consequences follow. Change detection
such as `if newReading == oldReading { return }` treats `1.5 -> 1.50` as no change, which
is right for the value but skips a display-only change. Any future map or set
keyed on decimal must hash the normalized value.

### 6. Crossings between numeric types are named

| Member | Behaviour |
| --- | --- |
| `math.toDecimal(n: int) -> decimal` | exact, scale 0; never faults (int64 has 19 digits) |
| `math.toDecimal(x: float) -> decimal` | the shortest decimal that converts back to the same float; faults outside §1's range |
| `math.toFloat(d: decimal) -> float` | nearest float, ties to even; never faults (§1's range is inside float's) |
| `math.floorToInt`, `ceilToInt`, `truncToInt`, `roundToInt`, `roundHalfEvenToInt` | decimal forms of 0048 §2.7's crossings; fault when out of `int` range |

`toDecimal(0.1)` is `0.1`, not the 55-digit exact value of the binary float
`0.1` (which would exceed the cap). That is what Go's `strconv`, JavaScript,
and Python's `repr` print for that float, so it is the number the user most
likely meant. Python's `Decimal(0.1)` gives the long exact expansion instead,
which is rarely what anyone wants and would fault here.

**Why shortest round-trip matters for rounding** (added 2026-10-28). The
float `2.675` is really `2.67499999999999982236…` in binary. Converted by
shortest round-trip it is `2.675`, and
`math.round(math.toDecimal(x), places: 2, rounding: Rounding.HalfUp)` gives
`2.68`, the answer a person expects. Converted exactly, as Python's
`Decimal(2.675)` does, it rounds to `2.67`. The conversion is not a hidden
rounding: `math.toFloat(math.toDecimal(x)) == x` holds for every float, so
nothing is lost. It also hides nothing: `0.1 + 0.2` converts to
`0.30000000000000004`, the float's real value.

**`toDecimal(float)` faults outside §1's range** (revised 2026-10-28). A
float like `1e300` or `1e-40` has no decimal of at most 34 digits written in
full, so the crossing faults, as `math.floorToInt(1e300)` does under ADR 0048
§2.7. The opposite direction can't fail: every decimal is inside float's
range, so `toFloat` neither overflows nor underflows.

**Underflow** (added 2026-10-28). This review found that ADR 0048 never said
what float arithmetic does on underflow. It now says: the nearest float,
possibly `0.0`, and never a fault. A result too small to represent is still
finite and nearly correct, unlike an overflow, which loses the value
entirely. Python's `math.exp(-1000)` returns `0.0` although its `math` raises
on overflow; Java and Go agree.

**The `ToInt` names are kept on purpose** (added 2026-10-28). Decimal has the
`Rounding` enum, but its crossings to `int` reuse ADR 0048's named members
rather than taking a `rounding:` argument. 0048 is accepted, and ADR 0049's
rule is the same names with one form per type. Any other mode is a
composition: `math.truncToInt(math.round(d, places: 0, rounding:
Rounding.Up))`.

### 7. Literals and constants

*Rewritten 2026-10-28.* Untyped constants (ADR 0048 §2.4) are evaluated
exactly, as fractions, as Go does. One rule governs their landing in decimal:

**An untyped constant becomes `decimal` only if its exact value fits in 34
digits. Otherwise it is a compile error.**

A fraction has a finite decimal form exactly when its reduced denominator has
no prime factors other than 2 and 5, so the check is cheap and decided at
compile time. This is Go's rule applied to a new type: Go rejects
`var n int = 2.5` with "constant truncated to integer."

```fiig
a: decimal = 10.0 / 4.0    // 2.5, legal
b: decimal = 1.0 / 3.0     // compile error: 1/3 has no exact decimal value
c = 1.0 / 3.0              // untyped: defaults to float and rounds as float does
d = price * (1.0 / 4.0)    // price is decimal; 0.25 lands as decimal, legal
e = price * (1.0 / 3.0)    // compile error
f = total / 4              // total is a decimal variable: §3's error
```

The one rule covers three cases:

- **Constant division.** `/` is allowed inside a constant expression, and the
  result must be exact. That gives a deliberate split: `/` works on decimal
  constants and not on decimal variables (§3), because the compiler can prove
  a constant quotient exact and can't prove a variable one. Go has the same
  split between constant and runtime arithmetic.
- **Oversized literals**, which need more than 34 digits, as ADR 0063 §3 rules
  for `int`. Trailing zeros are trimmed first, per §5, so a 35-digit
  `1.000…0` is legal.
- **Irrational constants.** `math.pi`, `math.tau`, and `math.e` can never be
  exact, so they are float-only by the same rule, with no special case. A
  program that wants a decimal pi writes the digits. This amends ADR 0049 §2.7,
  which said one `pi` would serve decimal as well. `minInt`, `maxInt`, and
  `maxFloat` have no decimal counterpart.

**A fractional constant with no context stays `float`** (ADR 0048 §2.4). Where
decimal values are common, that could look like a trap; Groovy makes `1.1` a
`BigDecimal` for that reason. Here it isn't one, because mixing `float` and
`decimal` is a type error: `rate = 0.0725` followed by `subtotal * rate` fails
loudly, and the fix is `rate: decimal = 0.0725`. For the same reason no literal
suffix is needed, unlike C#'s `19.99m`; context types the literal.

**The scale of a constant** is the number of digits after the point minus the
exponent, floored at zero: `1.5e3` is `1500` (scale 0), and `1e-2` is `0.01`
(scale 2). A computed constant such as `10.0 / 4.0` takes the smallest scale
that represents it exactly (`2.5`, scale 1).

### 8. Boundaries report values that don't fit through their own failure channel

Per ADR 0048 §2.3's rule, a value from outside that isn't a decimal fiig can
hold is an input error, not a fault. That covers values outside §1's range
(more than 34 digits written in full) and the non-finite values some systems
store: Postgres `numeric` accepts `'NaN'`, and since Postgres 14 `'Infinity'`
and `'-Infinity'`.

| Boundary | Decimal behaviour |
| --- | --- |
| JSON (ADR 0062) | a number converts exactly, keeping its scale; outside §1's range is `Err` (so `1e6000` is `Err`) |
| SQL `NUMERIC`/`DECIMAL` | exact; outside the range, `NaN`, or `Infinity` faults today, because `libraries/sql` has no error channel (0048 §2.3's recorded gap) |
| SQLite | written as TEXT; read exactly from TEXT or INTEGER; a REAL value read into a decimal is a boundary failure (a fault today) |
| `string.toDecimalOrNone()` | JSON's number grammar exactly, exponents included, with scale per §7; `None` on other text or outside the range |
| `toString` | plain notation with the value's scale (`1.50`, `-0.07`); at most 36 characters |

**SQLite has no decimal storage** (added 2026-10-28). A column declared
`NUMERIC` stores `19.99` as REAL, a binary float, so a decimal written as a
number would come back rounded through float. `fiig-sql-run` uses SQLite, so
`libraries/sql` writes decimals as TEXT. A REAL value arriving where a
decimal is expected means something upstream stored the value as a float.
Converting it would hide that, so it is reported instead.

`toDecimalOrNone` uses JSON's grammar so there is one decimal text grammar to
learn, at both of fiig's text boundaries.

Postgres's binary `numeric` format is part of the Postgres wire work, not this
ADR.

## Rationale

**Why a 34-digit cap rather than arbitrary precision.** Java's `BigDecimal`
and Postgres's `NUMERIC` grow without limit. Exact multiplication then grows
without limit too: `1.05^100` has more than 200 digits. Hostile input like the
JSON number `1e1000000` costs real memory. A cap bounds every value, matches
how `int` and `float` behave, and turns runaway growth into a fault an author
sees. Python avoids growth with a global precision context instead, which is
hidden state that changes what `a * b` means depending on code elsewhere.

**Why fault rather than round when the cap is reached (unlike C#).** C#'s
`decimal` rounds silently past 28 digits. That is rare, but it is a silent
rounding in the one type whose purpose is exactness, and ADR 0063 already
chose a fault over a silent wrong number for `int`. Code that multiplies
repeatedly without rounding (applying a growth factor of `1.05` about
sixteen times) will fault. Code that repeats a decimal calculation normally
rounds each step to the precision its domain records, and the fault tells
the author, or an LLM, to say which.

**Why `/` is a compile error rather than exact-or-fault.** Java's
`BigDecimal.divide(other)`, with no rounding argument, throws when the quotient
doesn't terminate, so `10 / 4` works and `10 / 3` fails at runtime. That is a
test-time surprise, where a compile error is immediate and names the fix.
Kotlin's `/` on `BigDecimal` is a cautionary tale in the other direction: it
silently rounds half-even to the left operand's scale, so `BigDecimal("10") /
BigDecimal("3")` is `3`. Making the author write the precision and the mode is
the only form that is neither surprising nor silent.

**Why one enum rather than `roundUp`, `roundDown`, and friends.** Division
needs the same choice of mode as rounding. Named functions would need a
divide per mode as well, twelve functions where two will do. The enum also
makes the mode a value, so a program can store the rounding rule a
jurisdiction requires and pass it in.

**Why `Up` and `Down` mean magnitude.** Everyday "round up" is ambiguous for
negative numbers. Excel, the most widely used tool for this kind of arithmetic, and Java both
define it as away from zero, and `Ceiling`/`Floor` cover the by-direction
meaning, so both readings are available under unambiguous names.

**Why scale-in-the-type is not chosen now.** SQL's `NUMERIC(10, 2)` and Ada's
fixed-point types make "two places" part of the type, and assigning `1.005`
to one would need an explicit round. That is a real benefit for any value with
a fixed recorded precision.
It also brings scale arithmetic into the type checker (`decimal(2) *
decimal(4)` is `decimal(6)`). It can be layered on later without changing this
ADR, in the same way ADR 0063 recorded declared `int` ranges as a possible
next step.

## Alternatives considered

**A. Arbitrary precision (Java `BigDecimal`, Postgres).** Rejected: unbounded
growth and unbounded cost on untrusted input (Rationale). The same argument
rejected decimal128's full exponent range (§1's amendment).

**B. Global precision context (Python `decimal`).** Rejected: hidden state.

**C. Fixed 128-bit with silent rounding (C#).** Rejected: silent rounding in
the exact type.

**D. Scale in the type (SQL `NUMERIC(p, s)`).** Deferred, not rejected
(Rationale).

**E. Named rounding functions (`math.roundUp(x, places:)`, `math.roundDown`,
`math.round`).** Shorter at the call site, and close to how the project owner
first described the need. Rejected in favour of the enum, because division
would need a parallel set of names.

**F. A default rounding mode.** Rejected for the reason in §4.

## Consequences

- **Equal decimals are not interchangeable** (§5). Code that assumes
  `a == b` implies identical output, as ADR 0048 guarantees for `float`, is
  wrong for decimal. Printing and serialization show the scale; comparison,
  hashing, and change detection use the value.
- `compiler/types`: `decimal` joins the primitive set (`types.go:22`) and
  ADR 0042's closed primitive list; `Comparable` and `Ordered` accept it
  (ADR 0052's tables gain a row).
- `compiler/checker`: `/` on decimal is a type error naming `math.divide`;
  mixed decimal/int and decimal/float arithmetic is already rejected by ADR
  0019's rule.
- `compiler/runtime`: a `Decimal` value (coefficient and scale) with exact
  `+ - * %` and cap checks.
- ADR 0049: the `math` table gains decimal forms of `abs`, `min`, `max`,
  `floorMod`, the `OrNone` members, and the `ToInt` crossings, plus `round`
  with `places`/`rounding`, `divide`, `allocate`, `toDecimal`, and the decimal
  form of `toFloat`. `Rounding` joins the prelude.
- These members wait on ADR 0049's group mechanism; the type, its operators,
  literals, and comparison do not.
- ADR 0062 (paused) can resume: `Number` converts to decimal per §8.
- Corpus: rules that need fractions, chosen so the example isn't only about
  currency: converting a recorded quantity between units at a published
  factor and rounding to the precision the source records, and splitting a
  quantity with `math.allocate` (§3a). Changing existing `int` fields such as
  `Order.total` is not part of this ADR.
- `libraries/sql`: decimal as TEXT in SQLite; REAL into decimal, `NaN`, and
  `Infinity` are boundary failures (§8).
- No new bug class: `/` on decimal is an ordinary type error.

## Open questions

- ~~**Whether `HalfUp` should be renamed.**~~ Settled 2026-10-28: keep
  `HalfUp`, the industry name (Java, ICU, Python's `ROUND_HALF_UP`). It means
  "ties away from zero" (`-2.345 -> -2.35`), which the enum's comment states;
  `HalfAwayFromZero` was exact but unfamiliar.
- **Whether float should get the by-magnitude forms.** Float has `trunc` (same
  as `Down`) but no `Up`; nothing needs it yet.
- **Whether `decimal` should be usable as a key** once maps exist (§5 says
  how it must hash if so), which belongs to the collections ADR.

## Implementation notes

The ADR is built in three steps: (1) the type itself; (2) the `math`
members, the `Rounding` enum, and the corpus example; (3) `libraries/sql`.

**Step 1, 2026-11-03: the type, constants, operators, comparison.**

- `compiler/decimal` is the arithmetic, shared by the checker, the runtime,
  and the boundaries: a `big.Int` coefficient and a scale. `New` enforces
  the cap (at most 34 significant digits, at most 34 after the point) and
  drops trailing zeros first (§5). `Parse` is JSON's number grammar with
  §7's scale, bounded by the text's length so `1e1000000000` costs nothing.
  `Round` and `Quo` round once (§3); `Quo` caps its working places at
  34 + digits(divisor) + 1, past which the verdict and the value can't
  change, so a huge `places` never builds 10^places. `FromFloat` is
  shortest round-trip (§6). Round, Quo, FromFloat, and the `Rounding`
  constants are tested here and used from step 2.
- Constants (§7): `checker.landConstant` lands a constant as decimal when
  that is the numeric type it lands in. A literal keeps its written scale
  through `-` and parentheses (`checker.constantScale`); a computed one takes
  the smallest exact scale. No exact value is A1; too many digits is
  untagged, as for int's range. `constant.Value` gained `Irrational`, set on
  `math.pi`, `tau`, and `e` and carried through arithmetic, so `math.pi`
  as a decimal says it is irrational rather than "does not fit".
  `syntax.Const` gained `Dec`, which the runtime reads.
- `/` on two decimals is A1 naming `math.divide` with the operands written
  out (§3). Mixing decimal with int or float is the existing A1, whose
  message now also names `math.toDecimal`. `decimal` is comparable and
  ordered (ADR 0052): `ordered` accepts it, and `floatPath` never flags it.
- Runtime: `runtime.Decimal` wraps `decimal.Decimal`. `+ - * %` and unary
  `-` are exact; over the cap faults with `decimal overflow at L:C: a * b
  does not fit in decimal (34 digits written in full)`. `%` by a zero
  decimal faults. `Equal` and `Less` compare values, so `1.50 == 1.5`.
- Boundaries done in this step because they were silent: `run-cli -json`
  printed a decimal as `null`, so `EncodeJSON` writes plain notation with
  the scale. `DecodeJSON` reads a decimal exactly and makes an
  out-of-range number an error, not a fault (§8). An HTTP path or query
  decimal reads the same grammar, and OpenAPI describes decimal as `type:
  number, format: decimal` (the OpenAPI format registry's name).
  `libraries/sql` still refuses to bind a decimal, explicitly; step 3.
- `math` calls with a decimal argument are A1 (no such form) until step 2.
- Corpus: `invalid-programs/decimal_error.fiig` (1/3 as a decimal, `/`).
  The valid corpus example needs `math.round` and `math.allocate`, so it
  is step 2's.
- Tests: `compiler/decimal/decimal_test.go`,
  `compiler/checker/decimal_test.go`, `compiler/runtime/decimal_test.go`,
  and `TestDecodeStringDecimal`. The runtime test helper `runEach` now
  refuses a program with checker diagnostics; a first draft of these tests
  showed it silently ran one.

**Step 2, 2026-11-03: the `math` members and `Rounding` (§2-§4, §6, §8).**

- `Rounding` is a prelude enum beside `SocketError`, so K2 reserves its
  name. The prelude source became one source per declaration, since a
  second public declaration in one file is G1 (ADR 0054).
- `compiler/types/math.go` gains `toDecimal`, `divide`, `allocate`, and a
  decimal form of `toFloat`, the `*ToInt` crossings, `abs`, `min`, `max`,
  `floorMod`, `addOrNone`, `subOrNone`, `mulOrNone`, and `round`. `places`,
  `rounding`, and `allocate`'s `parts` must be passed by name (`parts` and
  `places` are two ints, which is closeTo's reason). The decimal `*ToInt`
  forms use the matching mode: floor `Floor`, ceil `Ceiling`, trunc
  `Down`, round `HalfUp` (Go's `math.Round` also ties away from zero), and
  roundHalfEven `HalfEven`.
- Decimal's `round` takes arguments float's doesn't, the first member
  whose forms differ in their parameters. `types.BindMathArgs` binds a
  call's arguments to one form, and the checker and runtime both use it.
  The checker keeps the forms the arguments bind to, then picks one by the
  arguments' types as before; a constant alone never picks the decimal
  form, since a fractional constant with no context is a float. A member
  with several forms that matches none lists them all, so
  `math.round(price)` shows the decimal form and its `places:`. The runtime
  picks the form whose parameters bind and whose types the values have, so
  nothing is recorded on the AST.
- Found while testing: a constant argument was landed in its parameter
  without a type check, which was harmless while every parameter was
  numeric; `rounding: 2` now reports A1.
- `math.allocate` faults on fewer than 1 part or an amount with more
  places than `places`. It also faults above 10,000,000 parts, an
  implementation limit the ADR doesn't state, so a mistaken count is a
  fault rather than a list the process can't allocate. Shares keep
  `places` as their scale, trimmed at the cap.
- `string.toDecimalOrNone()` joins the method table (§8).
- A fieldless enum variant now prints as written, `Rounding.HalfUp`, not
  `Rounding.HalfUp()`; it appears in `math` fault messages. Nothing
  depended on the parentheses.
- Corpus: `order-processing/formulas/shipping.fiig` gains
  `poundsToKilograms` (the exact 0.45359237 factor), used by
  `rules/declared_weight.fiig` with `math.round`, and
  `rules/package_weights.fiig` splits a declared weight with
  `math.allocate`. examples/README row 19.
- Tests: `compiler/checker/decimal_test.go` (`TestDecimalMathFormsCheck`,
  `TestDecimalMathMisuse`), `compiler/runtime/decimal_test.go`
  (`TestDecimalMath`), and `TestAllocateAddsBack`.

**Step 3, 2026-11-03: `libraries/sql` (§8), narrowed.**

- A decimal binds as TEXT in plain notation at its scale, so `1.50` is
  stored as the text `1.50` (`TestDecimalBindsAsText`).
- Reading it back gives a `string`: a row is an anonymous record whose
  column types come from SQLite's storage classes, so nothing says which
  text is a decimal. A program converts with `toDecimalOrNone()`. Reading
  into a declared decimal field, and whether a REAL is accepted into one,
  are ADR 0066's, split out at the project owner's direction. Until then
  §8's "REAL into decimal is a boundary failure" has nothing to apply to: a
  REAL column reads as a `float`, and ADR 0048 §2.3 still faults on `NaN`
  and `Infinity`.
- Dogfooded with `fiig-sql-run` against a SQLite file: a gateway inserts
  `1.50` and reads back `1.50`.
