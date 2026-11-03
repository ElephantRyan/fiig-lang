# ADR 0067: String Methods, Counted in Code Points

Status: Proposed (2026-11-03). Drafted for one-decision-at-a-time review;
nothing here is decided.
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

### 1. `length()` counts code points

The one question every later decision depends on is what a "character" is.
For `"Zoë 👍🏽"` (a thumbs-up with a skin-tone modifier):

| Unit | Count | Who counts this way |
|---|---|---|
| UTF-8 bytes | 13 | Go `len`, Rust `.len()`, fiig `s.utf8().length()` |
| UTF-16 code units | 8 | Java and JavaScript `.length`, C# `.Length` |
| Code points | 6 | Python `len`, Go `utf8.RuneCountInString`, Postgres `char_length`, MySQL `CHAR_LENGTH`, SQLite `length()` |
| Grapheme clusters (what a reader sees) | 5 | Swift `count`, JavaScript `Intl.Segmenter` |

**Proposed: code points.** `s.length()` is 6 here.

- **It agrees with the databases.** A Postgres `VARCHAR(280)`, a SQLite
  `length(note) <= 280` check, and MySQL's `CHAR_LENGTH` all count code
  points. A service that checks a limit and then stores the value should
  get the same answer as the column that holds it.
- **It doesn't change with the Unicode version.** Grapheme boundaries come
  from rules (UAX #29) and tables that change with each Unicode release, so
  the same string's length could change when the toolchain upgrades. A
  code point count never does.
- **It needs no tables.** Fiig's core module has no dependencies; grapheme
  segmentation would mean carrying and updating the Unicode tables.
- **UTF-16 is the one choice with no merit of its own.** Java and
  JavaScript count it for historical reasons, and it splits an emoji into
  two "characters".

**What it gives up.** A reader sees `👍🏽` as one character and code points
count two; a family emoji (`👨‍👩‍👧`) counts five. `e` followed by a
combining diaeresis is two code points, where `ë` precomposed is one. No
choice agrees with every boundary: a browser's `.length` counts 8 for the
string above. The frontend and the service have to agree on a unit, and code
points are the one the database also uses.

`length()` is the name `List`, `bytes`, and `Map` already use. The bytes stay
one call away: `s.utf8().length()`.

### 2. `slice(from, to)` cuts by code point; there is no `at`

```
s.slice(0, 40)     // the first 40 code points; faults if s is shorter
```

The arguments are code point positions, as `length()` counts, so a slice
never cuts a character's bytes in half. Out of range faults, as `List.slice`
and `bytes.slice` do.

There is no `s.at(i)`: it would return a one-code-point string, which is a
`char` by another name (ADR 0048 rejected `char`), and indexing by position
is rarely what text processing needs. Rust and Swift leave out integer
indexing on strings for the same reason.

**Cost.** UTF-8 doesn't index by code point, so `length()` and `slice` are
O(n) in the string's length. Python makes them O(1) by storing each string
at a fixed width per code point; fiig keeps UTF-8, which every boundary
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
the same length as the input. The visible difference from full mapping is
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
| `length()` | `int`, code points |
| `slice(from, to)` | `string`, by code point; faults out of range |
| `startsWith(s)`, `endsWith(s)` | `bool` |
| `removePrefix(s)`, `removeSuffix(s)` | `string` |
| `trim()`, `trimStart()`, `trimEnd()` | `string` |
| `lowercase()`, `uppercase()` | `string` |
| `equalsIgnoringCase(s)` | `bool` |
| `split(sep)` | `List<string>` |
| `replaceAll(old, new)` | `string` |
| `toIntOrNone()`, `toFloatOrNone()` | `int \| None`, `float \| None` |
| `List<string>.join(sep)` | `string` |

Existing: `isBlank()`, `contains(s)`, `utf8()`, `toDecimalOrNone()`. Every
method returns a value, so a bare call is A13 (ADR 0065 §6).

## Alternatives considered

**A. Grapheme clusters for `length()` (Swift).** The count a person would
give. Rejected for §1's reasons: it disagrees with the database, changes
across Unicode versions, and needs tables. Kept as an open question under
its own name.

**B. Bytes for `length()` (Go, Rust).** Cheap and exact, and wrong for
every limit written in characters. `utf8().length()` already gives it.

**C. No `length()`; name the unit (`codePointCount()`, Swift's
`unicodeScalars.count`).** Unambiguous, and the safest for readers coming
from Java or JavaScript. Rejected in the draft because `length()` is
already the name on every other sized type, and code points are the count
most languages that read the way fiig does (Python) give. Worth revisiting
in review: it is the conservative choice.

**D. `+` for concatenation.** ADR 0057 rejected it; `join` and
interpolation cover the cases.

**E. Regular expressions.** Not proposed. They are a library-sized feature
with their own syntax and performance traps, and nothing above needs one.

## Consequences

- `compiler/types/methods.go`: the `string` table gains §1-§7; `List`'s
  gains `join`, A1 on a non-string element (as `contains` checks its
  element).
- `compiler/runtime/methods.go`: bodies, with the faults named above.
- `docs/known-limitations.md` drops "strings have few methods".
- Corpus: validating a note's length and normalizing an email address.

## Open questions

1. **Grapheme count.** If a real case needs what a reader sees, it would be
   a separately named method (`graphemeCount()`), never a change to
   `length()`.
2. **Truncation.** `s.slice(0, math.min(40, s.length()))` is clumsy for
   "at most 40 characters". A `truncate(n)` that never faults would be
   small; it waits for the corpus to show it.
3. **`indexOfOrNone`.** Nothing above needs a position; `split`,
   `startsWith`, and `removePrefix` cover the cases. Waits for a real case.
4. **Normalization (NFC).** Comparing user-entered text across devices
   sometimes needs it, and it needs Unicode tables.
5. **Locale-aware collation** for sorting names for display.
