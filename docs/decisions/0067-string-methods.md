# ADR 0067: String Methods, with Every Count Named

Status: Proposed (2026-11-03). Under one-decision-at-a-time review: §1 and
§2 decided 2026-11-03 (`length()` counts graphemes, `codePointCount()`
beside it, bytes through the encoding); the rest is not yet reviewed.
Date: 2026-11-03
Related: ADR 0048 (`char` rejected), ADR 0049 §2.6 (closed method tables;
"further string methods are additive"), ADR 0050 (naming), ADR 0057 (no `+`
on strings; interpolation instead), ADR 0058 (`bytes`, `utf8()`), ADR 0062
(JSON numbers), ADR 0063 (the `OrNone` convention), ADR 0064 §8
(`toDecimalOrNone`), ADR 0065 (A13 covers every method).

## Context

A string has four methods today: `isBlank()`, `contains(s)`, `utf8()`, and
`toDecimalOrNone()`. An ordinary web service needs more, and none of it can
be written in fiig: a string can't be measured, cut, searched for a prefix,
trimmed, case-converted, split, or joined. `utf8()` gives the bytes, which
can be counted and sliced, but a byte count isn't what a limit like "a note
of at most 280 characters" means, and a byte slice can cut a character in
half.

What a service written today can't do:

| Task | Needs |
|---|---|
| Reject a note longer than 280 characters | a length |
| Show the first 40 characters of a title | a slice |
| Read `Authorization: Bearer abc123` | `startsWith`, `removePrefix` |
| Accept `" Ada@Example.com "` as an email | `trim`, `lowercase` |
| Compare usernames without regard to case | `equalsIgnoringCase` |
| Read `a,b,c` from a query parameter or a CSV line | `split` |
| Write `a, b, c` | `join` (there is no `+` on strings, ADR 0057) |
| Read `"42"` from a header or an environment variable | `toIntOrNone` |

The method table is closed (ADR 0049 §2.6), so a library can't add these.

## Decision

### 1. Three counts, each named: `length()`, `codePointCount()`, `utf8().length()`

The one question every later decision depends on is what a "character" is.
For `"Zoë 👍🏽"` (a thumbs-up with a skin-tone modifier):

| Unit | Count | Who counts this way |
|---|---|---|
| UTF-8 bytes | 13 | Go `len`, Rust `.len()`, PHP `strlen` |
| UTF-16 code units | 8 | Java and JavaScript `.length`, C# `.Length`, Kotlin, Dart |
| Code points | 6 | Python `len`, Ruby `length`, Postgres `char_length`, MySQL `CHAR_LENGTH`, SQLite `length()` |
| Grapheme clusters (what a reader sees) | 5 | Swift `count`, Elixir `String.length`, Raku `.chars` |

No answer is the majority's, so a bare `length()` means a different number
to a reader from each column. Fiig follows Swift: the plain count is what a
person sees, and every other unit is named where it's used.

```
s.length()            // 5: grapheme clusters, what a reader sees
s.codePointCount()    // 6: code points, what a database column counts
s.utf8().length()     // 13: bytes, in the encoding the call names
```

- **`length()` counts grapheme clusters** (Unicode's extended grapheme
  clusters, UAX #29). `👍🏽` is one, a family emoji (`👨‍👩‍👧`) is one, and `ë`
  is one whether it's precomposed or `e` plus a combining diaeresis. It's
  the count a person would give, so a limit shown to a user ("at most 40
  characters") and a display truncation use it.
- **`codePointCount()` is the database's count.** Postgres `VARCHAR(n)`,
  SQLite's `length()`, and MySQL's `CHAR_LENGTH` count code points, and the
  two counts disagree: 280 `👍🏽` are 280 graphemes and 560 code points, so a
  check written with `length()` passes a value a `VARCHAR(280)` rejects. A
  limit a column enforces is checked with `codePointCount()`.
- **Bytes go through the encoding: `s.utf8().length()`.** There is no
  `byteCount()`. A byte count only means something for an encoding, and
  `utf8()` names it at the call site; if fiig ever reads or writes another
  (Latin-1, UTF-16), it would be another method beside `utf8()`, and its
  byte count would come the same way, with no question which one a bare
  `byteCount()` meant. This already works today.
- **No UTF-16 count.** It's the one unit with no merit of its own; Java and
  JavaScript count it for historical reasons. A boundary that needs it
  (a browser's `maxlength`) is a reason for an encoding method later, not
  for a count now.

**Graphemes change with the Unicode version.** Segmentation is rules plus
property tables, and a Unicode release can join a sequence that used to be
two clusters into one (new emoji sequences do). So `length()` can differ
between toolchains on the same string. Swift and Elixir accept this; fiig
pins one Unicode version per toolchain release, names it in the release
notes, and the lockfile's `toolchain` field records which toolchain built a
program. `codePointCount()` and `utf8().length()` never change.

### 2. `slice(from, to)` cuts by grapheme; there is no `at`

```
s.slice(0, 40)     // the first 40 grapheme clusters; faults if s is shorter
```

The arguments are positions in `length()`'s unit, so
`s.slice(0, s.length())` is `s`, and a slice never cuts an emoji, a
skin-tone modifier, or a combining mark away from its base. Out of range
faults, as `List.slice` and `bytes.slice` do. There is no code-point slice;
a value cut to fit a column is cut by `slice` and then checked with
`codePointCount()`.

There is no `s.at(i)`: it would return a one-code-point string, which is a
`char` by another name (ADR 0048 rejected `char`), and indexing by position
is rarely what text processing needs. Rust and Swift leave out integer
indexing on strings for the same reason.

**Cost.** `length()`, `slice`, and `codePointCount()` are O(n) in the
string's length: UTF-8 doesn't index by code point, and grapheme
boundaries are only found by walking the segmentation rules. Swift's
`count` is O(n) for the same reason. Fiig keeps UTF-8, which every boundary
speaks. The strings a service handles are short; the cost is stated here
so no one assumes O(1).

### 3. Prefixes, suffixes, and trimming

```
s.startsWith(prefix)    // bool
s.endsWith(suffix)      // bool
s.removePrefix(prefix)  // s without prefix, or s unchanged if it doesn't start with it
s.removeSuffix(suffix)
s.trim()                // without leading and trailing whitespace
s.trimStart()
s.trimEnd()
```

Whitespace is Unicode's `White_Space` property, the definition `isBlank()`
already uses, so `s.isBlank()` is exactly `s.trim() == ""`.

`removePrefix` and `removeSuffix` are Kotlin's names and Python 3.9's
(`removeprefix`). The alternative, a `strip` that takes characters, is a
known trap: Python's `"abcab".lstrip("ab")` removes every leading `a` and
`b`, not the prefix `"ab"`, and was misread often enough that Python added
`removeprefix`. Fiig has no character-set trimming.

### 4. Case: `lowercase()`, `uppercase()`, `equalsIgnoringCase(s)`

```
s.lowercase()
s.uppercase()
s.equalsIgnoringCase(other)
```

**No locale.** The result never depends on where the program runs. Java's
`toLowerCase()` uses the default locale, so on a Turkish system `"TITLE"`
lowercases to `"tıtle"` with a dotless `ı`; Kotlin 1.5 replaced
`toLowerCase()` with `lowercase()` to make the locale-free mapping the
default, and fiig takes Kotlin's names for that reason.

**Per code point.** Each code point maps to one code point (Unicode's
simple case mapping, which Go's `strings.ToLower` uses), so the result has
the same code point count as the input. The visible difference from full mapping is
`"ß".uppercase()`, which stays `"ß"` where Python gives `"SS"`.

**Comparing without case uses `equalsIgnoringCase`, not lowercasing.**
`a.lowercase() == b.lowercase()` is the common idiom and is wrong for a few
scripts: Greek sigma has two lowercase forms, so `"ΟΔΟΣ"` lowercases to
`"οδοσ"` while the word is written `"οδος"`, and the two don't match.
Unicode case folding (Go's `strings.EqualFold`) puts `Σ`, `σ`, and `ς`
together. The method gives the right answer a name.

### 5. `split(separator)` and `join(separator)`

```
"a,b,,c,".split(",")      // ["a", "b", "", "c", ""]
["a", "b"].join(", ")      // "a, b"
```

`split` keeps every piece, including empty ones at either end, as Go,
Python, JavaScript, and Rust do. Java's `String.split` silently drops
trailing empty strings, so `"a,b,,".split(",")` has two elements in Java
and four here; fiig takes the majority. `"".split(",")` is `[""]`. An empty
separator faults: Go splits into code points and Python raises, and the
fault is the clearer of the two.

`join` is a method on `List<string>`: `parts.join(", ")`, list first, as
JavaScript's `join` and Kotlin's `joinToString` take it, not separator first
as Python's `", ".join(parts)` and Java's `String.join(", ", parts)` do.
On any other element type it's A1; interpolate first. Since there is no `+`
on strings (ADR 0057), `join` and interpolation are how text is assembled.

### 6. `replaceAll(old, new)`

Every occurrence, left to right, without overlap. The name says "all"
because JavaScript's `replace` replaces only the first occurrence, while
Python's, Java's, and Go's `ReplaceAll` replace every one; a fiig `replace`
would be read one way by half its readers. There is no first-only form. An
empty `old` faults.

### 7. `toIntOrNone()` and `toFloatOrNone()`

```
"42".toIntOrNone()       // 42
" 42".toIntOrNone()      // None: no surrounding whitespace
"1e3".toIntOrNone()      // None
"9223372036854775808".toIntOrNone()   // None: out of range
"2.5".toFloatOrNone()    // 2.5
```

Beside `toDecimalOrNone()` (ADR 0064 §8). An int is an optional `-` and
decimal digits, nothing else: no `+`, no underscores, no whitespace, and
`None` when out of range. A float uses JSON's number grammar, the one
`toDecimalOrNone` reads, so `NaN`, `Infinity`, and hex are `None`. Values
from headers, query strings, environment variables, and files arrive as
text, and the program decides what a bad one means; there is no faulting
`toInt()`.

### 8. Equality and ordering stay as they are

`==` compares code points, and `<` orders by code point, which is what
comparing UTF-8 bytes gives. There is no normalization: `ë` precomposed and
`e` with a combining diaeresis are different strings, as in every language
here except Swift. There is no locale collation. Both are open questions.

## Summary of the table

| Method | Result |
|---|---|
| `length()` | `int`, grapheme clusters |
| `codePointCount()` | `int`, code points |
| `slice(from, to)` | `string`, by grapheme cluster; faults out of range |
| `startsWith(s)`, `endsWith(s)` | `bool` |
| `removePrefix(s)`, `removeSuffix(s)` | `string` |
| `trim()`, `trimStart()`, `trimEnd()` | `string` |
| `lowercase()`, `uppercase()` | `string` |
| `equalsIgnoringCase(s)` | `bool` |
| `split(sep)` | `List<string>` |
| `replaceAll(old, new)` | `string` |
| `toIntOrNone()`, `toFloatOrNone()` | `int \| None`, `float \| None` |
| `List<string>.join(sep)` | `string` |

Existing: `isBlank()`, `contains(s)`, `utf8()` (whose `length()` is the
byte count), `toDecimalOrNone()`. Every method returns a value, so a bare
call is A13 (ADR 0065 §6).

## Alternatives considered

**A. Code points for `length()` (Python, the databases).** The first
draft's choice: it agrees with `VARCHAR(n)`, never changes across Unicode
versions, and needs no tables. Rejected in review because it isn't the
count a person gives (`👍🏽` is two), and a bare `length()` in code points
reads as a different number to readers from Java, JavaScript, Go, and
Swift alike. Kept under its own name, `codePointCount()`, for the
database case.

**B. Bytes for `length()` (Go, Rust).** Cheap and exact, and wrong for
every limit written in characters. `utf8().length()` already gives it.

**C. A `byteCount()` method.** Considered in review and rejected: bytes
are only defined for an encoding, and `utf8().length()` names it, so a
second encoding later can't make the count ambiguous.

**C2. No `length()` at all; every count named.** Unambiguous, but a
string's count a person sees is the one most code wants, and `length()` is
already the name on every other sized type.

**D. `+` for concatenation.** ADR 0057 rejected it; `join` and
interpolation cover the cases.

**E. Regular expressions.** Not proposed. They are a library-sized feature
with their own syntax and performance traps, and nothing above needs one.

## Consequences

- `compiler/types/methods.go`: the `string` table gains §1-§7; `List`'s
  gains `join`, A1 on a non-string element (as `contains` checks its
  element).
- `compiler/runtime/methods.go`: bodies, with the faults named above.
- Grapheme segmentation (UAX #29) in the core module, which has no
  dependencies and Go's standard library doesn't provide: the property
  tables are generated from the Unicode data files by a `go generate`
  step, checked in, and tested against Unicode's
  `GraphemeBreakTest.txt`. The Unicode version is a constant the release
  notes name.
- `docs/known-limitations.md` drops "strings have few methods".
- Corpus: validating a note's length and normalizing an email address.

## Open questions

1. **Other encodings.** `latin1()` or `utf16()` beside `utf8()`, each
   with its byte count through `length()`, when a boundary needs one.
2. **Truncation.** `s.slice(0, math.min(40, s.length()))` is clumsy for
   "at most 40 characters". A `truncate(n)` that never faults would be
   small; it waits for the corpus to show it.
3. **`indexOfOrNone`.** Nothing above needs a position; `split`,
   `startsWith`, and `removePrefix` cover the cases. Waits for a real case.
4. **Normalization (NFC).** Comparing user-entered text across devices
   sometimes needs it, and it needs Unicode tables.
5. **Locale-aware collation** for sorting names for display.
