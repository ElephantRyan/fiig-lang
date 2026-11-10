# ADR 0062: JSON Numbers Keep Their Text

Status: Accepted (2026-10-30, one-decision-at-a-time review). §6
implemented 2026-10-30 (`compiler/runtime/jsonparse.go`); §1-§5 and §7
implemented 2026-11-09 by ADR 0073's build (`JsonValue` in the prelude,
`json.decode` for §4's conversions, and the encoder's §5 fault).
Date: 2026-10-26
Related: ADR 0019 (arithmetic, no implicit widening), ADR 0048 (float),
ADR 0049 (primitive method tables), ADR 0051 (no severity levels), ADR 0052
(comparability), ADR 0063 (overflow faults; the `OrNone` convention), ADR
0064 (decimal), ADR 0065 (`Map`; defers the JSON object's type to this ADR).
Milestone 7: prerequisite for "JSON encoding and decoding in fiig". Typed
decoding into models is a separate ADR. This one decides only what a JSON
number *is* once fiig has read it.

## Context

RFC 8259 defines a JSON number as text (`-? int frac? exp?`) and sets no
limit on its size or precision. Every implementation therefore has to choose
a numeric type for it, and the choice decides which values survive the trip:

```json
{"id": 9007199254740993, "reading": 1.50, "ratio": 1e-7, "big": 1e400}
```

`id` is 2^53 + 1, which a binary64 can't hold. `reading`'s trailing zero is
lost as soon as it becomes a number of any kind. `big` overflows binary64.
RFC 7493 (I-JSON) gives the interoperable subset: integers within ±(2^53 − 1)
and binary64 for anything else. Its advice for larger values is to send them
as strings, which is why Twitter's API sends `id_str` next to `id`.

Fiig has to choose too. What makes its case unusual is that it has only one
number type to choose from today. `int` is int64. `float` is ADR 0048, still
Proposed and unbuilt. Decimal is promised (ADR 0048 §2.5) but has no ADR.
(Updated 2026-10-29: ADR 0048 and ADR 0064, decimal, are both accepted, though
neither is implemented; §4 defines the conversions to them.)

### Fiig already chose, at its Go boundary, and the choice is wrong

`compiler/runtime/json.go` decodes JSON into a fiig value for `run-cli
-args`, for the HTTP adapter's request bodies (`compiler/adapter`), and for
`fiig-sql-run`. It uses `encoding/json`'s default `any` decoding, which turns
every number into a float64, and then converts with `Int(int64(f))`.
Tested against `conversion echo(n: int) -> int { return n }`:

| JSON sent | fiig received |
| --- | --- |
| `9007199254740993` | `9007199254740992` |
| `9223372036854775807` | `-9223372036854775808` |
| `1.5` | `1` |
| `1e3` | `1000` |

None of these is an error. An HTTP route whose body has `amount: int`
accepts `{"amount": 1.5}` and charges 1. That is the bug class the
milestone's JSON library has to avoid, and it has already shipped in Go.

## Decision

### 1. A decoded JSON number is its source text

The JSON library's value tree keeps a number exactly as it appeared:

```
enum JsonValue {
    Null
    Bool(value: bool)
    Number(text: string)
    Text(value: string)
    Array(items: List<JsonValue>)
    Object(members: Map<string, JsonValue>)
}
```

The decoder checks the text against RFC 8259's number grammar when it scans,
so a `Number` that came from a decoder is always a valid JSON number. It
converts the text to nothing. `1.50` stays `"1.50"` and `1e400` stays
`"1e400"`. (I probed this enum: the recursion through `List<JsonValue>`
type-checks and runs today.)

**An object is a `Map` (ADR 0065), and a duplicate name is an `Err`.**
`obj.members.atOrNone("id")` replaces a loop over a member list, and 0065 §7's
insertion order keeps members in the order they arrived, so decoding then
encoding an object reproduces it. RFC 8259 says only that names "SHOULD be
unique" and that behaviour with duplicates is unpredictable; I-JSON (RFC 7493)
says they MUST NOT appear. Parsers disagree (Go, Python, and JavaScript keep
the last; others keep the first), and that disagreement is exploitable: a
validating proxy and a backend that read different values for one name let a
request pass validation with one value and run with the other. The decoder
returns an `Err` naming the duplicate, as I-JSON requires and as 0065 §5 does
for a map literal.

The value tree's job is to be lossless. Choosing a numeric type is the job of
whoever reads the value, because only the reader knows whether `reading` is
a measurement to keep exactly, a value to compute with, or a count.

| Implementation | A JSON number becomes |
| --- | --- |
| Go `encoding/json` with `UseNumber` | `json.Number`, a string |
| Rust `serde_json` (`arbitrary_precision`) | a `Number` holding a `String` |
| Python `json` | exact `int`, `float`, or `Decimal` through `parse_float` |
| Jackson | `BigDecimal` / `BigInteger` when configured to |
| PostgreSQL `jsonb` | `numeric`, exact |
| JavaScript `JSON.parse` | binary64, lossy. A TC39 proposal ("JSON.parse source text access") adds a way to get the text back for exactly this reason |

Every library that cares about exactness keeps the text or an exact decimal.
The one that doesn't, JavaScript, is the one that needed `id_str`.

### 2. A number becomes `int` when its value is a whole number

Converting a `Number` to `int` succeeds when the number's exact value is a
whole number within int64, however the text writes it. Anything else is an
`Err` that names the text:

| Text | As `int` |
| --- | --- |
| `3`, `-7`, `0` | `Ok` |
| `3.0`, `3.00`, `3e0`, `1e3` | `Ok` (3, 3, 3, 1000) |
| `2.5`, `12345e-2` | `Err`: has a fractional part |
| `1e19`, `9223372036854775808` | `Err`: out of range |

This is JSON Schema's rule (draft 6 on, `1.0` is an `integer`), Pydantic v2's
(it rejects `2.5` as "a number with a fractional part" and accepts `3.0`), and
protobuf's JSON mapping's (integer fields accept `1.0` and exponent forms).
Those are the libraries built for consuming JSON whose producer the reader
doesn't control, which is fiig's position with a payment provider's webhook or
another team's Python service. Python's `json.dumps(3.0)` writes `3.0`, so a
producer that keeps a count in a float sends it that way.

The bug this ADR exists to fix is *silent* loss: fiig's Go boundary turns
`1.5` into `1` (Context). This rule fixes it as fully as a strict one would.
`2.5` is still an `Err`, never a truncation.

The value is evaluated exactly, through decimal (ADR 0064): the text converts
exactly, the fractional part must be zero, and the result must fit int64. Any
JSON number too large for decimal's range is far outside int64 anyway, so it
is out of range either way.

`string.toIntOrNone()` (§3) stays strict. It parses protocol text such as
`Content-Length`, where `"3.0"` is malformed. A JSON number is a number; a
protocol field is a format.

### 3. Strings get `toIntOrNone()`

```
"42".toIntOrNone()     // 42
"007".toIntOrNone()    // 7
"+7".toIntOrNone()     // None
"4 2".toIntOrNone()    // None
```

`string.toIntOrNone() -> int | None` joins the string method table
(`compiler/checker/bytes.go`, ADR 0049's shape). The name follows ADR 0063's
`OrNone` convention, Kotlin's `toIntOrNull`, and ADR 0064's
`string.toDecimalOrNone()`. The JSON library's `int` conversion is §2's value
rule, not `toIntOrNone()`, which would reject `3.0`.

It accepts `-? digits` within int64, leading zeros included, and nothing
else: no `+`, no whitespace, no `_`, no `0x`.

**It is a primitive method** rather than JSON-library code because nearly
every protocol in this milestone carries integers as text: HTTP's
`Content-Length`, Postgres's text-format `DataRow` values (`SELECT count(*)`
arrives as the bytes `"3"`), Redis's `:42\r\n`, and CSV fields. A digit loop
written in fiig works, but under ADR 0063 `int` overflow faults, so a loop
over a 20-digit input faults where it should return `None` unless every step
uses `math.mulOrNone` and `math.addOrNone`. One native parser is the place to
get that right.

**`+` is rejected although Go's `Atoi`, Java, Kotlin, and Rust accept it.**
This is a parser for protocol formats, and HTTP, Postgres, and Redis all write
an integer as digits with an optional `-`; no conforming producer sends `+7`.
Leniency here is a security hazard rather than interoperability: HTTP request
smuggling works when a proxy and a backend read one `Content-Length`
differently, as when a proxy rejects `+3` and a backend reads it as 3. A
parser exactly as strict as the format agrees with every conforming peer.
Node's HTTP parser and others have removed this kind of leniency in security
fixes. Code that takes human input strips a `+` first; a strict parser can be
loosened at a call site, and a lenient one can't be tightened.

**Leading zeros are accepted**, because HTTP's `Content-Length` grammar is
`1*DIGIT` and CSV exports keep them. `toDecimalOrNone()` (ADR 0064) uses JSON's
number grammar and so rejects `"007"`. The two differ deliberately:
`toIntOrNone` reads protocol fields, and `toDecimalOrNone` reads JSON-shaped
decimal text.

`int | None` rather than `Result` follows `bytes.utf8()`: the input is an
integer or isn't, and there is only one reason it can fail.

### 4. Fractional numbers convert to `float` and `decimal` from their text

ADR 0048 (`float`) and ADR 0064 (`decimal`) are accepted, so each conversion
is defined here. All three start from the `Number`'s text:

| Target | Rule | `1.50` | `0.1` | `1e400` | `1e-400` |
| --- | --- | --- | --- | --- | --- |
| `int` | §2's value rule | `Err` (fractional) | `Err` | `Err` | `Err` |
| `decimal` | exact, scale kept (0064) | `1.50` | `0.1` | `Err` (outside 34 digits) | `Err` |
| `float` | nearest binary64 | `1.5` | nearest to 0.1 | `Err` (overflow) | `0.0` (underflow) |

- **`float` rounds to the nearest binary64 without an error.** Almost no
  decimal fraction is exact in binary (`0.1` isn't), so rejecting inexact
  values would reject nearly every fractional number. Declaring a field
  `float` accepts that; Python, Go, JavaScript, and Pydantic do the same. A
  field whose digits must survive, such as a recorded reading or a rate, is
  declared `decimal`.
- **Overflow is an `Err`, not `+Inf`.** Non-finite floats never exist (0048
  §2.3), and a bad external value goes through the boundary's failure channel
  (0048 §2.3's boundary rule), which for JSON is `Err`. Go's `ParseFloat`
  reports a range error here too.
- **Underflow is `0.0`.** Zero is the nearest binary64 to `1e-400`, so this is
  the same rounding as `0.1`, at the other end of the range; Python, Go, and
  JavaScript return 0. A very small value is not an invalid one. `-1e-400`
  gives `0.0`, since 0048 §2.3 normalizes `-0.0`.
- **`decimal` is exact** (0064's JSON row): every JSON number is a finite
  decimal, so only the 34-digit range can fail, and it fails as `Err`.

The conversions' names, and how a model field declared `reading: float` or
`ratio: decimal` invokes them, belong to the typed-decoding ADR. This one fixes
what each conversion means.

### 5. Encoding writes the text back exactly

The encoder writes `Number`'s text verbatim, so decode followed by encode
reproduces every number byte for byte. That matters for proxies, for
signature checks over a payload, and for any value that passes through
without being read. An `int` becomes `Number` through its decimal text
(`"${n}"`), which is already canonical.

A `Number` built by hand can hold invalid text (`JsonValue.Number(text:
"abc")`), because fiig has no way to restrict an enum variant's constructor.
The encoder checks each `Number` against the same grammar the decoder uses,
and **invalid text is a fault**; encoding returns `bytes`, not a `Result`
(decided 2026-10-29). A decoded `Number` is always valid, and one built from
an `int`, `float`, or `decimal` is valid by construction, so `Number("abc")`
can only come from the program's own code: a defect, which fiig reports as a
fault, not bad input, which it reports as `Err` (`fiig-lang.md` §3.6). A
`Result` would make every `encode` call in every program handle (F1) an error
that correct code can't produce. The fault's message names the text. A
mechanism that seals a variant's constructor, as serde_json makes an invalid
`Number` unconstructible, would remove the fault; nothing else needs one yet.

The encoder writes an int beyond ±(2^53 − 1) exactly, even though I-JSON
says JavaScript readers will mangle it. Deciding to send such a value as a
string is a choice about the receiver, which only the caller can make. Fiig
has no warnings (ADR 0051), and the encoder silently stringifying it would be
the wrong fix.

### 6. The Go boundary follows the same rules

`runtime.DecodeJSON` switches to `json.Decoder.UseNumber()` and applies §2 to
the `json.Number` text, so `-args`, HTTP request bodies, and `fiig-sql-run`
accept `3.0` and `1e3` for an `int` and reject `2.5` instead of truncating it.
This fixes a shipped bug, and it doesn't wait on the fiig JSON library. The Go
boundary and the fiig library then accept and reject the same numbers, so
moving an adapter from one to the other changes nothing a caller can see.

Three more parts, found by checking every JSON entry point (added 2026-10-30):

- **`DecodeUntypedJSON` stops truncating.** It decodes scripted capability
  responses (`tooling/runprogram/scripted.go`), with no type to decode
  against, and today turns every number into an `int`, so a script's `1.5`
  reaches fiig code as `1`. An integral value (§2's rule) becomes an `int`;
  anything else is an error naming the text until `float` and `decimal` are
  implemented, and then follows §4. A test that silently truncates its inputs
  passes against data that no longer means what it says.
- **Duplicate names are an `Err`, as in §1.** Every call site decodes into
  Go's `map[string]any` (`compiler/adapter/http.go`, and `json.Unmarshal` in
  `run-cli` and `fiig-sql-run`), which keeps the last duplicate silently: the
  parser disagreement §1 rejects. The boundary reads objects with
  `json.Decoder.Token()` and reports a duplicate name; an HTTP request body
  with one gets a 400. Member order is not kept at the boundary and needn't
  be, since objects there decode into models by field name, never into a
  `Map`.
- **Fractional numbers follow §4 as their types land.** `float` and `decimal`
  are accepted but not implemented, so a parameter can't yet be declared
  either; when one can, the boundary converts by §4.

### 7. `==` on numbers compares their text

`Number("1.0") == Number("1")` is `false` (decided 2026-10-30). `==` on
`JsonValue` is structural (ADR 0052), and a `Number`'s payload is its text.
`JsonValue` is a lossless tree whose job is to reproduce its input, so two
values are equal when they would encode identically.

The precedent is split. Libraries that keep the text compare text: Go's
`json.Number`, Jackson's `DecimalNode` (`1.0` and `1.00` differ), and
serde_json with `arbitrary_precision`. Tools that treat JSON as data compare
values: jq, Postgres `jsonb`, JSON Schema's `const` and `enum`, Python, and
JavaScript. Fiig's tree is the first kind.

This sits oddly beside §2: `Number("3.0")` and `Number("3")` convert to the
same `int` but are not `==`. Numeric equality goes through a conversion, as
any computation already must. A value-based `==` would be the first exception
to ADR 0052's structural equality, for one variant, and couldn't compare
`1e400` with `1e401` through decimal anyway. Normalizing the text on decode
would break §5's byte-for-byte round trip.

## Alternatives considered

**A. Decode every number to `float` (JavaScript).** Rejected: it loses
`9007199254740993` before fiig code ever sees it, and it drops every
decimal's scale, so `1.50` comes back as `1.5`.

**B. Decode integers to `int` and other numbers to `float` (Python).** It
looks natural, but the type then depends on how the producer wrote the value:
`1` and `1.0` decode differently, and `1e3` is a float in Python even though
its value is integral. It still loses `reading`'s trailing zero, and a number
beyond int64 has nowhere to go. A tree of typed numbers would also mean
picking the representation before the reader says what it wants, which is
the decision §1 exists to delay.

**C. A dedicated `JsonNumber` model with sign, digits and exponent fields.**
Precise, and it would make validity structural instead of checked. Rejected
for now: every consumer would have to reassemble the parts, and the text
form is what all of §1's precedents expose. It could come back as an
internal representation, since the public conversions wouldn't change.

**D. Convert to `int` strictly by text (Go, serde_json).** The first draft's
rule: only `-? digits` converts, so `3.0` and `1e3` are `Err`. Its argument was
that a producer sending `3.0` stores a float and may later send `2.5`, so
rejecting `3.0` surfaces the mismatch on the first request. Rejected
2026-10-29: a fiig service usually can't change the producer, so strictness
means it can't consume that service at all; and a later `2.5` is a loud `Err`
under §2, not a silent loss. Go and serde_json reject `3.0` largely because
they choose a target type while scanning, not as a stance on interoperation.
The draft's second argument, that a value rule must evaluate exponents
exactly, no longer costs anything now that decimal exists.

## Consequences

- `compiler/checker/bytes.go` and the runtime method dispatch:
  `string.toIntOrNone() -> int | None`.
- `compiler/runtime/json.go`: `UseNumber` and §2's value-rule `int` decoding (§6).
  Tests for the four rows of the Context table: the two large integers now
  arrive exactly, `1.5` is an `Err`, and `1e3` is 1000 by §2's value rule. `-args` and the HTTP adapter's error messages name the
  offending text.
- `run-cli`, `fiig-sql-run`, `compiler/adapter`: decode through a
  `json.Decoder` with `UseNumber` instead of `json.Unmarshal` into `any`,
  reading objects by token so duplicate names are reported (§6).
- `tooling/runprogram/scripted.go` via `DecodeUntypedJSON`: integral numbers
  only until `float`/`decimal` are implemented (§6).
- The JSON library (the next ADR, typed decoding) builds on `JsonValue` from
  §1 and the conversions from §2 and §3. Its number scanner validates
  RFC 8259's grammar.
- No new bug class. Every rejection of input is an `Err` value or `None`;
  the one fault is invalid text in a hand-built `Number` at encode time (§5).
- ADR 0048 and ADR 0064 gain a downstream consumer. Their conversions from
  JSON are defined here (§4): float overflow is `Err`, underflow is `0.0`,
  and decimal is exact within its range.
