# ADR 0151: A Formula May Say Why It Failed

Status: Accepted (2026-11-25, by the owner). Implemented the same day (see
Implementation notes).
Date: 2026-11-25
Amends: ADR 0049 §2.2 (restriction 3 and its Rationale), ADR 0139 §2 (B7's
hint).
Related: ADR 0029 (rules don't combine decisions), ADR 0048 §2.7 (no
`Result` in numeric crossings), ADR 0074 §6 (`http.formEncode`), ADR 0079
(an obvious path over enforcement), ADR 0133 (`fiig review`), Hearsay GAPS
G18, bug classes B7, K3.

## Context

A `formula` is fiig's tier for general computation: pure, callable from
every kind (rules included), calling only other formulas, and always in a
`formulas` group (ADR 0049). It is where shared, domain-free logic lives,
the kind of thing a standard library's `String` or `java.time` methods do,
so that several libraries can hold it and any domain can call it.

ADR 0049 §2.2 fences the tier with three restrictions:

1. no capabilities (no `requires`);
2. calls only formulas;
3. may not return a `Result`, or a union holding one (K3).

The third was called the load-bearing one: business judgment almost always
needs a typed failure, so "can't fail with a reason" was the checkable
stand-in for "holds no judgment". Its Rationale said plainly that it is a
proxy, not a proof: a judgment returning `bool` is still a legal formula.

### What the proxy catches

It catches the wrong things. A parser, a decoder or a validator of a format
has a reason to give and no judgment in it:

```fiig
formulas date {
    formula parse(text: string) -> Result<Date, DateError> { ... }   // K3 today
}
```

`date.parse("2026-13-40")` can only return `Date | None` today, which throws
away "month 13 is out of range", the thing a caller shows a user. Every
standard library fiig is compared with gives a reason from a pure function:
Rust's `str::parse` returns `Result<T, ParseIntError>`, Go's `strconv.Atoi`
returns `(int, error)`, Java's `LocalDate.parse` throws
`DateTimeParseException`, and Kotlin's `toIntOrNull` sits beside a `toInt`
that says why.

So fiig's own standard library writes this code as conversions. Of its 211
conversions, about 46 return a `Result` (protocol decoders that fail on a
malformed message, for the most part), and with them go the helpers they
call. They are domain-free computation by ADR 0049's own test, filed in a
kind meant for reshaping a domain's data, only because the formula tier
can't fail with a reason.

That misfiling has a cost. A rule may call a formula but not a conversion,
so domain-free helpers spelled as conversions are out of a rule's reach.
Hearsay's G18: its `tokenRequest` was written as a rule, which was refused
because `http.formEncode` is a conversion; the workaround was to make
`tokenRequest`, `queryOf` and `authorizeUrl` conversions too.

### What the proxy misses

It misses the thing it exists for. A policy written as `bool`,
`formula canAdminister(user: User) -> bool`, is a legal formula today. ADR
0049 accepted that, and the line between a format check and a policy check
has always been a convention ("format checks are formulas; policy checks
are rules", §2.2).

The line the owner draws is about what a declaration knows: a rule holds a
domain's decision, a formula holds general computation any domain could
use. That is not checkable, and the `Result` ban was never a check of it.

### What stays checkable

Restrictions 1 and 2 are the fences that keep the tier safe, and neither
depends on restriction 3: a formula has no effects, and calls nothing but
formulas, so it can't reach a rule, a gateway or an activity whatever it
returns.

## Decision

### 1. A formula may return a `Result`

ADR 0049 §2.2's restriction 3 is lifted. A formula may return `Result<T, E>`
or a union holding one, and K3 is retired: its number stays reserved, and
the bug class's row says it was withdrawn by this ADR.

```fiig
formulas date {
    formula parse(text: string) -> Result<Date, DateError> {
        ...
        if (month < 1 || month > 12) {
            return Err(DateError.MonthOutOfRange(month: month));
        }
        ...
    }
}

rule dueDate(text: string) -> Result<Date, InvoiceError> {
    return mapErr(e in date.parse(text)): InvoiceError.BadDate(text: text);
}
```

Restrictions 1 and 2 are unchanged. A formula still has no `requires`
(K4), calls only formulas (B11), and is always in a group.

F1 applies as anywhere else: a formula's `Result` is handled by its caller,
with `?`, `match` or `mapErr`. A rule that calls a formula which can fail
translates the error into its own, as `dueDate` does.

`math` and the other prelude groups don't change: none of their members
returns a `Result`, and ADR 0048 §2.7's reason for that (a nested numeric
expression would be an unwrap at every step) still holds for them.

### 2. The split between rule and formula is a convention, made visible

With restriction 3 gone, the checker tells a rule from a formula only by
where it may be called from and what it may call:

| | rule | formula |
| --- | --- | --- |
| calls | formulas, and its own private helpers | formulas, itself included |
| is called by | conversions, activities, gateways, workflows, tests | every kind, rules included |
| recursion | no (ADR 0054) | yes |
| declared | one per file | in a `formulas` group, called `group.member(...)` |

Formulas compose and rules don't. Which one a piece of code is, a domain's
decision or general computation, is the author's call. It is kept by the
obvious path and by visibility, as ADR 0079 keeps architecture boundaries:

- **The obvious path.** A decision is a rule, named for what it decides and
  placed in its domain's folder. General computation is a formula, in a
  group named for its subject (`date`, `http`, `vector`), called in the
  tone of a package (`date.parse(...)`). `docs/layers.md` says so in one
  paragraph.
- **The checker never offers the escape** (§3).
- **The catalog shows a formula's error** (§4).

### 3. B7 stops suggesting a formula

ADR 0139 §2's message for a rule calling a rule ends with two alternatives:
"If `caller` only reshapes data, it can be a conversion; if `callee` is a
format check, it can be a formula (ADR 0049)." While a formula couldn't
fail with a reason, the second was a narrow way out. Now that it can, it is
the easy way around rule isolation for any pair of rules: make both
formulas and the error goes away. A reader, and an LLM above all, follows
the fix a message offers.

The formula clause is removed. B7 offers the shape ADR 0029 and ADR 0139
give: take the other rule's answer as a parameter, and combine the two in
the activity that calls them. The conversion clause, for a caller that
only reshapes data, stays as ADR 0139 wrote it.

```
[B7] rule may not call rule "client.canAdminister"; a rule doesn't
combine decisions, so take its answer as a parameter (`admin: bool`)
and call client.canAdminister from the activity that calls syncTargets
(ADR 0029, ADR 0139). If syncTargets only reshapes data, it can be a
conversion.
```

A format check is still a formula. The checker just doesn't recommend
turning a rule into one.

### 4. A formula's card shows its error

A formula's catalog card gets the `error` field a rule's card has: the `E`
of a `Result<T, E>` it returns. `fiig catalog find --error E` finds
formulas as it finds rules, and a formula in an application that fails with
`OrderError` is as easy to see as a rule that does.

### 5. Revisit when Hearsay has used it

After Hearsay moves its domain-free helpers to formulas (§Consequences),
its formula groups are read for domain errors. If an application's formulas
return its domain's errors (`OrderError`, `AdminError`) rather than a
format's (`DateError`, `PgError`), the convention isn't holding, and this
ADR is reopened for a checkable fence (open question 1).

### 6. A formulas group's private helpers are built

*Added 2026-11-25, when the standard library's move began.* ADR 0054 §1
already accepted this on 2026-10-17 and left it unbuilt until the formula
tier existed; it was never picked up. A formulas file may hold a
`private { }` block, as a rule's file may. Its
helpers are called by the group's members, and nothing outside the file
can call one: `pad.zeroPad(...)` from another file is "formulas group pad
has no formula zeroPad". A helper takes its file's kind, as every helper
does, so it calls only formulas (B11).

Without this, a moved helper's pieces (`http.formEncode`'s byte escaping,
`fiig.time`'s days-in-month) would become public members, in the catalog
and callable by anyone, only because the code moved kinds. One part of ADR 0054
§1 changes with this ADR: it said a group's helpers carry the formula
restrictions, "no `Result`" among them, and §1 of this ADR lifts that one
for helpers as for members.

## Alternatives considered

**A. Keep the ban.** Parsers and decoders stay conversions, out of a rule's
reach, and `date.parse` returns `Date | None`. Rejected: it keeps a proxy
that catches parsers and misses `bool` policies, and the misfiling in
fiig's own standard library is the evidence against it.

**B. Allow `Result` only in a library's formulas** (`"kind": "library"` in
the manifest). Checkable. Rejected: applications hold general computation
too (ADR 0049's `vector` example is the kind an application writes for
itself), and an application author would be pushed to split a package out
only to parse a date with a reason.

**C. Allow only a fixed prelude error type,** such as a built-in
`ParseError { at: int, expected: string }`. Checkable. Rejected: a decoder
couldn't say what failed in its own terms (`PgError.ShortMessage`,
`DateError.MonthOutOfRange`), which is what a reason is for.

**D. Allow a formula's error only from the formula's own package.**
Checkable, and it stops a formula from returning another domain's error.
Rejected for now: an application's own formulas could still return its own
`OrderError`, which is the case that matters, so it fences little. It is a
candidate for open question 1 if the convention doesn't hold.

**E. Merge `conversion` into `formula` here.** The overlap between the two
is real, and this ADR removes the main thing that kept them apart. Not
decided here: conversions also call rules, are one per file, and are the
only kind a principal's refusal may name (U2). A separate ADR, if the
migration in §Consequences leaves the line between them unclear.

## Consequences

- `compiler/checker/formula.go`: K3 is no longer reported.
  `compiler/checker/checker.go`: B7's rule-calls-rule message loses its
  formula clause, and the `formula_error.fiig` invalid program loses its K3
  case.
- `compiler/registry`: a formula's card carries `error` when it returns a
  `Result`.
- `docs/specification/bug-classes.md`: K3's row says it was withdrawn by
  this ADR. `docs/layers.md`: one paragraph on choosing between a rule and
  a formula.
- ADR 0049 §2.2's restriction 3 and its Rationale paragraph ("Why `Result`
  is the load-bearing restriction") point here.
- **The standard library's misfiled helpers move, as a follow-up, not in
  this ADR:** `fiig.http`'s text helpers and `fiig.time`'s calendar first
  (G18), then each driver's protocol code (`fiig.postgres`, `fiig.kafka`,
  `fiig.redis`, `fiig.s3`, `fiig.smtp`, `fiig.jwt`). The owner's call
  (2026-11-25): a driver's protocol code is general computation in a
  library, not an application reshaping its own data, so it is a formula
  too, grouped by subject within each package. A public conversion that moves is a
  breaking change for its callers: a call into an imported `formulas`
  group is spelled through the group (ADR 0050 §2.6), so
  `http.formEncode(...)` changes at every call site, and its catalog kind
  changes. Recorded in the release notes, with `fiig fmt` or a migration
  note doing the rewrite.
- **Hearsay's, as its own follow-up:** few of its 70 conversions are
  domain-free. The candidates are `keyHash`, `tokenHash` (each a SHA-256 of
  a string) and, once `formEncode` has moved, `authorizeUrl` and
  `tokenRequest`. Its error mappers and `HttpResponse` builders are domain
  reshaping and stay conversions.

## Open questions

1. **A checkable fence, if the convention fails** (§5). Alternative D is
   the likeliest.
2. **Whether `conversion` still earns its own kind** once the
   domain-free helpers have left it (alternative E).
3. **B7's conversion clause.** "If `caller` only reshapes data, it can be a
   conversion" still sends a rule to a kind that may call rules. ADR 0139
   kept it deliberately, for the reshaping case; whether it is used as an
   escape the same way is worth reading for at §5's revisit.

## Implementation notes (2026-11-25)

- **Checker**: `compiler/checker/formula.go` no longer reports K3, and its
  `holdsResult` helper is gone. B7's rule-calls-rule message in
  `checker.go` ends at the conversion clause.
- **Catalog**: a formula's card carries `error` (`errorTypeOf`, as a
  rule's does), so `fiig catalog find --error DateError` finds
  `date.month`.
- **Tests**: `TestAFormulaMaySayWhyItFailed` (checker: a formula returning
  a `Result`, another propagating it with `?`, a rule translating it with
  `mapErr`, no diagnostics), `TestAFormulasErrReachesItsCaller` (runtime:
  `Ok(4)` for month 11, `Err(InvoiceError.BadDate(...))` for month 13),
  `TestAFormulaCardShowsItsError` (registry). `TestFormulaRestrictionsAreK3K4K5`
  became `...K4K5`; `formula_error.fiig` lost its K3 member; the B7
  message's test lost its formula clause.
- **Dogfooded** with `fiig check`, `fiig run` and `fiig catalog find --error DateError` on a small app: a `date.month` formula returning `Result`, a rule mapping it (`Err(InvoiceError.BadDate(13))`, `Ok(7)`), and B7 for a rule calling a rule now ending at the conversion clause.
- **Docs**: K3's row in `bug-classes.md` is withdrawn, with its number
  reserved; `grammar.md`'s formula paragraph; ADR 0049 §2.2 and its
  Rationale, and ADR 0139 §2, point here; `docs/layers.md` gains "A rule or
  a formula".
- **Standard library, phase 1 (2026-11-25)**: `fiig.time`'s nine
  conversions are the `calendar` group (`src/Calendar.fiig`), with
  `zeroPad`, `daysInMonth` and `number` its private helpers, and its tests
  in `test/Calendar.fiig` (a test file mirrors a source file, ADR 0102 §3),
  plus one for a four-digit early year now that `zeroPad` can't be called
  from a test. `fiig.http`'s `formEncode`, `pathSegment`, `isToken` and
  `headerValue` are the `httpText` group (`src/HttpText.fiig`). Their
  callers in `fiig.http`, `fiig.s3` and `fiig.smtp`, the Go test fixtures,
  and `docs/writing-a-client.md` call through the groups.
- **Breaking, for the release notes:**

  | Was | Is |
  | --- | --- |
  | `use fiig.time;` `time.formatIso(m)`, and `formatBasic`, `formatHttp`, `formatRfc5322`, `parseIso`, `toDateTime`, `toMillis`, `weekday` | `use fiig.time.calendar;` `calendar.formatIso(m)`, and the same names |
  | `time.zeroPad(n, w)` | private to `calendar`; no replacement |
  | `use fiig.http;` `http.formEncode(fields)`, and `pathSegment`, `isToken`, `headerValue` | `use fiig.http.httpText;` `httpText.formEncode(fields)`, and the same names |

  The types (`time.DateTime`, `time.TimeError`, `http.Header`) don't move,
  so a file that also names one keeps its `use fiig.time;` or
  `use fiig.http;`.
- **Not done yet**: each driver's protocol code (`fiig.http`'s HTTP/1.1
  internals, `fiig.postgres`, `fiig.kafka`, `fiig.redis`, `fiig.s3`,
  `fiig.smtp`, `fiig.jwt`), grouped by subject; and Hearsay's own
  candidates.
