# ADR 0123: Time Is a Library Over Int Milliseconds

Status: Accepted (2026-11-20; the owner chose the library over a primitive in review). Implemented 2026-11-20 (98a4d3a, `fiig.time`); see Implementation notes.
Date: 2026-11-20
Related: ADR 0042 (the closed primitive set: write in fiig what can be
written in fiig), ADR 0055 (no newtypes; named arguments are the defense
against swapped values), ADR 0063 (int overflow faults), ADR 0068 §4-5
(`Clock` is a local effect and is untyped), ADR 0099 (`or` gives a default),
ADR 0112 (schedules are UTC; `firedAt` is Unix milliseconds), ADR 0120
(`rfc5322Date`), ADR 0121 (`amzDate`), known-limitations ("`Clock` is
untyped ... A time type is open").

## Context

A moment in time is an `int` of Unix milliseconds everywhere in fiig today:
`Clock.now()`, a schedule's `firedAt`, a Kafka record's timestamp, a
deadline, and abs-recommendations' `createdAt`, `updatedAt` and
`expiresAt`. That has worked: the app has filed no gap about it.

What `int` can't do is say a time out loud. Two built-in packages needed a
calendar and each wrote one, **the same 25 lines of Howard Hinnant's
`civil_from_days`**, changed only in the output string:

- `fiig.smtp`: `rfc5322Date`, `Fri, 20 Nov 2026 13:59:58 +0000`.
- `fiig.s3`: `amzDate`, `20261120T135958Z`.

`fiig.http` will want a third (`Retry-After` as a date, a cookie's
`Expires`, `Date` headers), a JSON API wants RFC 3339, and the next
protocol wants another. Each copy is a place for a leap-year bug, and each
one clamps a time before 1970 to the epoch because truncating division
(ADR 0048 §2.8) makes the pre-1970 case easy to get wrong.

### How others handle it

| Language | A moment | A calendar date and time of day | A span | Zones |
| --- | --- | --- | --- | --- |
| Go | `time.Time`, a struct in the standard library | the same struct, in a `Location` | `time.Duration`, an `int64` of nanoseconds | tzdata shipped or read from the OS |
| Java | `java.time.Instant` | `LocalDateTime`, `LocalDate`, `ZonedDateTime` | `Duration`, `Period` | `ZoneId`, from a bundled database |
| Rust | `SystemTime` in std; `chrono` and `jiff` for the rest | `NaiveDateTime`, `civil::DateTime` | `Duration` | `chrono-tz`, `jiff`'s bundled tzdb |
| Python | `datetime` (naive or aware) | `datetime`, `date`, `time` | `timedelta` | `zoneinfo` |
| JavaScript | `Date` (a number of milliseconds), `Temporal.Instant` | `Temporal.PlainDateTime` | `Temporal.Duration` | `Temporal.ZonedDateTime` |
| Elixir | `DateTime` in UTC | `NaiveDateTime`, `Date`, `Time` | integers in a chosen unit | the `tzdata` package |
| SQL (Postgres) | `timestamptz`, stored as UTC | `timestamp`, `date`, `time` | `interval` | a bundled database |

Three lessons repeat:

1. **An instant and a calendar reading of it are different things.** Java,
   Rust, Elixir and Temporal split them because "2026-11-20 14:00" means
   nothing without a zone, and `java.util.Date` and Python's naive
   `datetime` are the famous sources of bugs from not splitting them.
2. **Zones are a database, not an algorithm.** Every language that has them
   ships or reads tzdata, and the hour that happens twice is a rule of its
   own. ADR 0112 already decided fiig has none yet.
3. **A moment is stored as one number, and the type is a library.** Go,
   Java, Rust (the crates), Python and Elixir put it in the standard
   library, not the language. Only SQL has it in the language, because SQL
   has no standard library to put it in.

## Decision

### 1. A moment stays an `int` of Unix milliseconds. No new primitive.

`Clock.now()`, `firedAt`, `createdAt: int` are already the type. Nothing
the corpus does changes. This is ADR 0042's rule applied: calendar
arithmetic is writable in fiig (it was written twice), so it is a package,
not part of the closed primitive set. It also keeps the JSON, SQL and
OpenAPI forms of a time as they are, a number, with no mapping to build
(§Alternatives).

### 2. `fiig.time`: pure conversions over that int

A built-in package, embedded like `fiig.smtp`, in fiig, every declaration
pure and none of them reading the clock: the moment is always an argument
(ADR 0068 §4). The calendar functions are **conversions**, since they
return a `Result` (a formula can't, K3), so a rule can't call them (B7);
activities, gateways and other conversions do. The `duration` formulas
are callable anywhere, a rule included:

```fiig
model DateTime {            // a calendar reading in UTC
    year: int
    month: int              // 1-12
    day: int                // 1-31
    hour: int               // 0-23
    minute: int             // 0-59
    second: int             // 0-59
    millisecond: int        // 0-999
}

enum TimeError {
    OutOfRange(reason: string)   // before year 0 or after year 9999
    Invalid(reason: string)      // a field out of range, or text that isn't a time
}

conversion toDateTime(millis: int) -> Result<DateTime, TimeError>
conversion toMillis(dateTime: DateTime) -> Result<int, TimeError>   // checks every field, leap years
conversion weekday(millis: int) -> int                              // 0 is Sunday

conversion formatIso(millis: int) -> Result<string, TimeError>      // 2026-11-20T13:59:58.000Z (RFC 3339)
conversion formatHttp(millis: int) -> Result<string, TimeError>     // Fri, 20 Nov 2026 13:59:58 GMT (RFC 9110)
conversion formatRfc5322(millis: int) -> Result<string, TimeError>  // Fri, 20 Nov 2026 13:59:58 +0000
conversion formatBasic(millis: int) -> Result<string, TimeError>    // 20261120T135958Z (SigV4)
conversion parseIso(text: string) -> Result<int, TimeError>         // RFC 3339; an offset is folded into UTC

formulas duration {         // spans are ints too; these name the unit at the call site
    seconds(n: int) -> int
    minutes(n: int) -> int
    hours(n: int) -> int
    days(n: int) -> int
}
```

- **UTC only.** A reading in a zone needs a database and a rule for the
  repeated hour (ADR 0112 open question 1). `parseIso` accepts an offset
  (`+02:00`) and returns the UTC moment, the way every API in the table
  does; nothing remembers the offset.
- **Years 0000-9999.** RFC 3339 has four-digit years, and a four-digit
  year is what every format above can say. Outside it is
  `Err(OutOfRange)`, not a clamp: fiig's `smtp` and `s3` clamp to the
  epoch today, which silently writes the wrong date.
- **Floor division, so before 1970 is right.** The package works the day
  count out with floor division (`days_from_civil` and `civil_from_days`,
  Hinnant's), not by clamping. The test file checks it against Python's
  `datetime` across the whole range, as ADR 0121 did for SigV4.
- **Durations are ints.** `duration.days(7)` is `604800000`. There is no
  `Duration` type, for ADR 0055's reason: a span is an `int` of
  milliseconds, as `timeoutMillis` and `holdMillis` already are, and the
  name at the call site is the defense.
- **No pattern language.** There are named formats for the five forms that
  protocols use, not `format(millis, "yyyy-MM-dd")`. A pattern string is a
  small language (Go's reference-time layout, Java's `DateTimeFormatter`,
  strftime) that each ecosystem gets wrong in its own way; a program that
  needs a sixth form builds it from `toDateTime`.

### 3. `fiig.smtp` and `fiig.s3` use it

`rfc5322Date` and `amzDate` are deleted, and their tests move. Both
callers already hold a value from `Clock.now()`, so each passes it through
`formatRfc5322` or `formatBasic` and turns an `Err` into its own error that
names the clock (a clock outside 0000-9999 is no case to handle, but fiig
has no way to write a fault, so it is an error).
The 25 duplicated lines become one tested copy.

## Alternatives considered

- **A `time` (or `instant`) primitive**, with `duration`. The type system
  could then say that `expiresAt` isn't a `timeoutMillis`, JSON could
  write RFC 3339 and OpenAPI `format: date-time` by itself, and a SQL
  column could be `timestamptz`. It is also the largest option: a fifth
  primitive against ADR 0042's closed set, the parser, checker,
  formatter, comparison (ADR 0052), JSON in and out, the OpenAPI schema,
  every SQL driver's binding, and `fiig.postgres`'s type mapping. It is
  the right answer if a real program reaches the boundary and finds `int`
  wrong there. abs-recommendations is a real program and hasn't. If one
  does, `fiig.time`'s functions become the primitive's methods without
  changing what they compute.
- **A `model Instant { millis: int }`** (ADR 0068's example uses it). It
  gets the distinct type but is the newtype ADR 0055 removed, and a
  one-field model is a JSON object `{"millis": ...}` at every boundary,
  worse than the number a client sends today.
- **Native Go leaves in the runtime** (`time.Format`). It would be less
  code, and it is the thing ADR 0042 turned down: the calendar is
  written in fiig, tested in fiig, and readable by a program author.
  Zones would come with Go's tzdata, which is an argument for it later
  (open question 1) and not for the first version.
- **Leave it**, as two copies. That is the cost today. It is small per copy
  and grows with each protocol, and both copies clamp.

## Consequences

- `compiler/stdlib/time/` (`fiig.time`), embedded in `stdlib.go`, with
  tests whose expected values come from Python's `datetime` over a spread
  of dates including century leap years (1900, 2000, 2100), year 0 and
  9999, and pre-1970.
- `fiig.smtp` and `fiig.s3` lose their calendar; their manifests depend on
  `fiig.time`, as `fiig.s3` depends on `fiig.http` today.
- Docs: `known-limitations.md` loses "A time type is open", and says what
  `Clock` and `fiig.time` do and don't give: no zones, spans are ints.

## Implementation notes (2026-11-20)

- `compiler/stdlib/time`: `DateTime`, `TimeError`, the `duration` formulas,
  `toDateTime`, `toMillis`, `weekday`, `formatIso`, `formatHttp`,
  `formatRfc5322`, `formatBasic`, `parseIso`, and `zeroPad` (the one helper
  every format shares, public since a private block is per file). 269 fiig
  tests, whose expected values come from Python's `calendar` and
  `datetime`: 24 moments chosen for the places calendars go wrong (the
  epoch and the second before it, 1900 and 2100 not leap, 1600 and 2400
  leap, the Gregorian switch date, 1 January of year 1, the last moment of
  9999, the 2038 rollover), year 0 worked out by hand (Python has none),
  and a sweep that takes `toDateTime` and `toMillis` round trip across the
  whole range every four years and a bit. A deliberate off-by-one in the day
  offset failed dozens of them.
- **`fiig.smtp` and `fiig.s3` use it.** `rfc5322Date`, `amzDate` and their
  tests are gone (6 and 3 tests), and both packages gained a dependency on
  `fiig.time`. The `Err` case isn't a fault, since fiig has no way to write
  one: smtp maps it to `SmtpError.Protocol` and s3 to
  `S3Error.InvalidRequest`, each saying the clock is outside 0000 to 9999. A
  moment before 1970 used to be written as the epoch, and is now correct.
- **Rules can't call these.** An early draft said a rule could; it can't,
  since the functions return `Result`. A rule that needs a date as text is
  not a thing the corpus has, and the shell (an activity or a gateway)
  formats at the edge.
- `fiig fmt -w` also reflowed `fiig.smtp` and `fiig.s3`, which had never been
  through it.

## Open questions

1. **Time zones.** A tzdata-backed `fiig.time.zones`, probably native
   leaves around Go's `time.LoadLocation`, since shipping the database in
   fiig is not sensible. It needs the repeated-hour rule and the
   skipped-hour rule, and ADR 0112's `cron ... in "Europe/Paris"`.
2. **Calendar types without a zone** (`LocalDate`, `LocalTime`), for a
   birthday or an opening hour that is not an instant. `DateTime` is UTC
   today, and a date of birth shouldn't need a time of day.
3. **A typed `Clock.now()`.** The checker doesn't type its call (ADR 0068
   §5), so `Clock.now() + "x"` goes unnoticed. Typing it as `int` is a
   small checker change and is independent of this ADR.
4. **When to promote to a primitive**: the first program that needs JSON
   dates, an OpenAPI `date-time`, or `timestamptz` and finds `int` wrong.
   The trigger is a real one, not a count.
5. **Pattern strings** (`format(millis, "yyyy-MM-dd")`). The owner chose
   named formats for now (2026-11-20) and is open to a pattern language
   later, once a program needs a form the five names don't give. It would
   be a design of its own, since each ecosystem's layout language differs.
6. **Sub-millisecond precision.** Kafka and JSON are milliseconds, Postgres
   microseconds, Go nanoseconds. `int` milliseconds loses the rest, and
   a database that stores microseconds would round on the way in.
