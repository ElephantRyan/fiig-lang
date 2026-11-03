# ADR 0058: `bytes` and Binary Patterns

Status: **Accepted** (2026-10-20), with `<< >>` delimiters and sub-byte
fields included (Open questions 1 and Alternative A settled as recommended).
**Implemented** 2026-10-20 (see Implementation notes).
Date: 2026-10-20
Amends: `fiig-lang.md` Milestone 7 (`bytes` primitive type; binary pattern
matching), `grammar.md` (integer literals, patterns, expressions).
Related: ADR 0042 (the native floor is the socket; names "binary pattern
matching in the style of Erlang's `<<Len:16, Body:Len/binary>>`" as a
blocking prerequisite), ADR 0049 §2.6 (methods are a closed table keyed on
the receiver's type; `bytes` indexing yields `int`; Proposed), ADR 0048 (no
silent numeric conversion), ADR 0057 (claim-the-slot growth; `==` compares
contents), ADR 0040 and bug class A9 (exhaustive `match`).

## Context

ADR 0042 moved fiig's native boundary down to five primitive capabilities:
`Socket`, `TlsSocket`, `Clock`, `FileSystem`, `Random`. Everything above
them, including the Postgres wire protocol that replaces `libraries/sql` and
the JSON codec that replaces `compiler/runtime/json.go`, is to be written in
fiig. Four of the five capabilities need a type for raw bytes, and fiig has
none. ADR 0042 lists what is missing: a `bytes` type, iteration (done, ADR
0053), recursion (done, ADR 0054), and binary pattern matching.

ADR 0042 also settled where parsing lives: a protocol parser is a
`conversion` (`bytes -> Result<Message, ParseError>`), pure and free to call
other conversions, while one gateway owns the socket and does the reading.

Nothing in `examples/` uses bytes yet, since nothing could. The attestation
for this ADR is the first protocol the floor needs, Postgres's message
framing, and two others that pressure-test it: HTTP/2 frame headers and
WebSocket frame headers.

### What a protocol parser has to do

Postgres frames every backend message the same way:

```
Byte1   message type ('R', 'Z', 'E', ...)
Int32   length of the message, counting itself but not the type byte
Byte*n  the rest of the message: length - 4 bytes
```

A reader receives some bytes and must answer one of three things: here is a
complete message and the bytes after it; not enough bytes yet, read more; or
the bytes are malformed. Then it dispatches on the type byte and decodes the
body, which has its own fixed layout (`AuthenticationOk` is `'R'`, length 8,
then an `Int32` 0).

Without binary patterns that is index arithmetic: read byte 0, assemble
bytes 1 to 4 into an integer in the right byte order, check the buffer is
long enough, slice. Go writes it as
`binary.BigEndian.Uint32(buf[1:5])` after a length check, and a missed check
panics. This is the code that binary patterns replace.

### Precedent

| Language | Form | Notes |
|---|---|---|
| Erlang, Elixir | `<<Type:8, Len:32/big, Body:(Len-4)/binary, Rest/binary>>` | The reference design. Same syntax builds and matches. Sizes may use names bound earlier in the pattern. A failed match falls through to the next clause. Construction silently truncates: `<<300:8>>` is `<<44>>`. |
| OCaml (`bitstring`) | `{| typ : 8; len : 32 : bigendian; body : (len-4)*8 : bitstring |}` | Erlang's model with named, typed fields. |
| Rust | `[b'R', a, b, c, d, rest @ ..]` | Slice patterns, byte granularity only; multi-byte integers need `u32::from_be_bytes`. Or parser combinators (`nom`). |
| Go | `binary.BigEndian.Uint32(b[1:5])` | Manual slicing; out-of-range panics. |
| Python | `struct.unpack(">BI", b[:5])` | A format string; returns an untyped tuple. |

Erlang is the design to start from. Its two mistakes, from fiig's point of
view, are the silent truncation and a segment syntax (`Len:32/big-unsigned`)
that reads nothing like the rest of fiig.

## Decision

### 1. `bytes` is a primitive type

`bytes` is an immutable sequence of bytes, beside `string`, `int`, `bool`,
and `float`. It is not `List<int>`: a list of 64-bit integers is eight times
the memory, and nothing would stop an element from being 300.

- **Equality** compares contents (ADR 0057 §6). `bytes` is comparable.
- **Conversion with strings:** `s.utf8()` on a `string` gives its UTF-8
  bytes and cannot fail. `b.utf8()` on `bytes` gives `string | None`, `None`
  when the bytes are not valid UTF-8. There is no implicit conversion either
  way.
- **Methods**, in ADR 0049 §2.6's closed-table shape:
  - `b.length()` -> `int`.
  - `b.at(i)` -> `int` in 0 to 255.
  - `b.slice(from, to)` -> `bytes`, sharing storage with `b`.
  - `at` and `slice` outside the bounds are a **fault**, as division by zero
    is. Code that does not know its input's length should use a binary
    pattern (§3), which never faults on short input.
- **`for x in b`** yields each byte as an `int` (ADR 0053).

### 2. Hexadecimal integer literals

`0x52`, `0x89504E47`. Protocol specifications state their constants in hex,
and translating them to decimal by hand is a source of bugs. Letters may be
either case. This is the only new literal form; there is no `0b` or `0o`
until a case is attested.

### 3. Binary patterns: `<< segment, ... >>`

A binary pattern matches a `bytes` value from left to right, segment by
segment. Each segment is `name: encoding`, which binds `name` to the decoded
value, or `literal: encoding`, which matches only that value, or `_:
encoding`, which skips it.

```fiig
conversion readFrame(buffer: bytes) -> Frame | None {
    match buffer {
        <<kind: u8, length: i32be, body: bytes(length - 4), rest: bytes>> ->
            Frame(kind: kind, body: body, rest: rest)
        _ -> None
    }
}

conversion readAuthentication(body: bytes) -> Authentication | None {
    match body {
        <<0: i32be>> -> Authentication.Ok
        <<5: i32be, salt: bytes(4)>> -> Authentication.Md5(salt: salt)
        _ -> None
    }
}
```

`_ -> None` in `readFrame` is the "read more" answer: a buffer too short for
the length it announces does not match, and no arithmetic or bounds check is
written by hand.

**Encodings:**

| Encoding | Binds | Width |
|---|---|---|
| `u8`, `i8` | `int` | 1 byte |
| `u16be`, `u16le`, `i16be`, `i16le`, and likewise 24, 32, 48 | `int` | that many bits |
| `i64be`, `i64le` | `int` | 8 bytes |
| `u1` to `u63` (other than the byte widths above) | `int` | that many bits, most significant first |
| `bytes(n)` | `bytes` | `n` bytes |
| `bytes` | `bytes` | the rest; last segment only |
| `utf8(n)` | `string` | `n` bytes, matching only if they are valid UTF-8 |

- **Byte order is always written** for a multi-byte width: `u32be` or
  `u32le`, never `u32`. There is no default to forget. Go makes the same
  choice by having no `binary.Uint32`, only `BigEndian` and `LittleEndian`.
- **Sub-byte fields** (`u1`, `u4`, `u31`) are big-endian bit order, which is
  what network headers use. HTTP/2 and WebSocket both need them:

  ```fiig
  // HTTP/2 frame header (RFC 9113 §4.1)
  <<length: u24be, kind: u8, flags: u8, _: u1, stream: u31, payload: bytes(length), rest: bytes>>

  // WebSocket frame header (RFC 6455 §5.2), short-length form
  <<fin: u1, _: u3, opcode: u4, masked: u1, length: u7, rest: bytes>>
  ```

- **Alignment is checked at compile time.** Every integer width is a fixed
  number, and every `bytes` and `utf8` segment is whole bytes, so the
  compiler knows each segment's bit offset. A `bytes` or `utf8` segment, and
  the end of the pattern, must fall on a byte boundary; anything else is a
  compile error.
- **No `u64`.** `int` is 64-bit signed, so a `u64` above 2^63 - 1 has no
  value to bind. `i64be` covers signed fields. WebSocket's 64-bit length,
  whose top bit RFC 6455 requires to be 0, is written `0: u1, length: u63`:
  a literal bit then a 63-bit field, which is the wire format exactly.
- **Sizes** in `bytes(n)` and `utf8(n)` are `int` expressions over literals
  and names bound by earlier segments of the same pattern, with `+ - * / %`.
  No calls. A negative or oversized size makes the segment fail to match,
  never fault.
- **Literal segments** take an integer literal (decimal or hex) or, with
  `utf8(n)`, a string literal: `<<"HTTP/": utf8(5), rest: bytes>>`. A literal
  that cannot fit its width (`300: u8`) is a compile error. This is the only
  place a literal is a pattern; literal patterns in ordinary `match` stay
  undecided (`tour.md` known gaps).
- **Names** bound by a segment are in scope for later segments and for the
  arm's body. Binding the same name twice in one pattern is an error.

**Matching** is first arm wins. A pattern that does not match, for any
reason, including short input, a failed literal, a bad size, or invalid
UTF-8, moves on to the next arm. It never faults.

**Exhaustiveness** (A9): no set of binary patterns can be proved to cover
every `bytes` value, so a `match` with a binary pattern must end with a
catch-all arm (`_` or a bare name). Without one it is A9.

**Matched `bytes` share storage.** `body` and `rest` are views of the input,
not copies, so a recursive parser that peels one token off the front and
recurses on `rest` does constant work per step, as Erlang's sub-binaries do.

### 4. Building `bytes`: the same segments as an expression

A binary expression is the pattern turned around: each segment is
`value: encoding`, and the result is a new `bytes`.

```fiig
conversion startupMessage(user: string, database: string) -> bytes {
    body = <<0x00030000: i32be, "user": utf8, 0: u8, user: utf8, 0: u8,
             "database": utf8, 0: u8, database: utf8, 0: u8, 0: u8>>
    return <<body.length() + 4: i32be, body: bytes>>
}
```

- In an expression, `utf8` and `bytes` take no size; the value's own length
  is used.
- **A value that does not fit its width is a fault**, not truncation. A
  literal that does not fit is a compile error. Erlang's `<<300:8>>` silently
  producing `<<44>>` is the bug class this rules out.
- `<<>>` is the empty `bytes`.
- **Growth is claim-the-slot** (ADR 0057 §5). When the first segment is an
  existing `bytes` value, `out = <<out: bytes, chunk: bytes>>` extends `out`'s
  buffer in place when `out` is the newest value on it, so building output
  in a loop is amortized constant time per byte, the same guarantee
  `append` gives lists.

### 5. Out of scope

`u64` and unsigned values above `int`'s range; `float` segments (IEEE
encodings, wait on ADR 0048); `0b` and `0o` literals; little-endian sub-byte
fields; literal patterns outside binary patterns; `bytes` ordering (`<`);
string methods beyond `utf8()`. Each waits for an attested case.

## Rationale

**Why Erlang's model.** It is the one design where the protocol diagram and
the code are the same shape, left to right, and where building and parsing
are the same syntax. Every protocol in ADR 0042's plan is a header of
fixed-width fields followed by a sized body, which is exactly what it
describes. Rust's slice patterns stop at byte granularity; Go's and Python's
approaches are the hand-written arithmetic this exists to remove.

**Why `<< >>`.** It is the only syntax with a prior for this job, and it
does not collide with anything: fiig has no shift operators, so `<<` is never
an expression on its own. It also keeps binary patterns visibly separate from
list literals (`[ ]`), which a future list pattern may want.

**Why `name: encoding` instead of Erlang's `Name:Size/Spec`.** It is how fiig
already writes "this name has this type" (`orders: List<Order>`), and it
reads as a field list. `length: i32be` is one token of encoding; Erlang's
`Len:32/big-signed-integer` spreads the same facts over a size, a slash, and
up to four hyphenated words, with defaults (unsigned, big-endian, 8 bits) a
reader has to know. The cost: an author or LLM writing Erlang from memory
gets a parse error, loudly, rather than a wrong program.

**Why the byte order is mandatory.** A wrong byte order is not caught by
tests on small values in some protocols and corrupts every value in others.
It is a one-word decision at the point where the author is reading the
protocol specification, which states it.

**Why faults for `at`/`slice` and a fall-through for patterns.** The two
tools answer different questions. A pattern asks "does this input have this
shape?", and "no" is an expected answer, so it moves to the next arm. `at(i)`
asserts that `i` is in range, and when that is false the program has a
defect, which is what faults are for (§3.6). Python raises `IndexError` and
Go panics for the same reason.

**Why no `byte` type.** ADR 0049 §2.6 already decided that indexing yields
`int`, following Python. A distinct `byte` type would need its own
arithmetic and conversions to and from `int`, everywhere, for no safety the
0 to 255 range check at `at` doesn't give.

## Alternatives considered

**A. `bytes[ ... ]` delimiters.** Reads as a typed literal and needs no new
token. Rejected because `[ ]` is the list literal and would invite list
patterns to look identical; recommended against, but a reasonable choice if
`<< >>` reads too foreign.

**B. Erlang's segment syntax verbatim.** Rejected on readability (above),
and because Erlang's defaults (unsigned, big-endian) are exactly the silent
choices §3 makes explicit.

**C. A default byte order of big-endian.** Network order is the common case,
and Erlang defaults to it. Rejected: the cost of writing `be` is two
letters; the cost of a forgotten `le` in a file format is corrupted values
that small test inputs can hide.

**D. Methods only, no patterns** (`b.u32be(1)`). Smaller, but it keeps every
bounds check in the author's hands, which is the source of the bugs this
exists to remove.

**E. Parser combinators.** Need function values, which ADR 0056 rejected.

## Open questions

1. **Sub-byte fields now or later.** §3 includes them because HTTP/2 and
   WebSocket need them and alignment is checkable at compile time. If they
   look like too much for a first cut, they can wait, and the first cut
   covers Postgres, which is byte-aligned throughout.
2. **ADR 0049 is Proposed.** §1's methods use its §2.6 shape. If 0049 is not
   accepted first, this ADR adopts §2.6's shape for `bytes` and `string`
   alone.

## Consequences

- Lexer: hex integer literals; `<<` as one token. `>>` closes a binary
  pattern or expression as two `>` tokens, so `List<List<int>>` is
  unaffected.
- Grammar: `binary-pattern` as a `pattern`; `binary-expr` as a `primary`;
  `segment ::= ( identifier | '_' | literal | expression ) ':' encoding`.
- Types: `bytes` joins the primitives (`compiler/types/universe.go`); ADR
  0042's closed primitive set gains it.
- Checker: segment typing; earlier-bound names in sizes; compile-time
  alignment; literal range; duplicate names; A9 requires a catch-all with
  any binary pattern; `bytes` and `string` method tables.
- Runtime: a `Bytes` value over a Go `[]byte` with a claim-the-slot counter;
  matching without copying; range faults on construction.
- Formatter: segments on one line, wrapped one per line past the width
  limit, as calls and list literals wrap.
- Corpus: a Postgres framing example (`readFrame`, `readAuthentication`,
  `startupMessage`) as conversions in a new `examples/` package, run by a
  runtime test with captured wire bytes.
- Unblocks: `Socket`, `TlsSocket`, `FileSystem`, and `Random` providers (ADR
  0042), the Postgres wire library, and JSON in fiig.

## Implementation notes

Implemented 2026-10-20.

- **Bug classes.** Every compile-time segment error is new **A14**; a value
  of the wrong type for its encoding is A1; a binary `match` with no
  catch-all arm is A9.
- **Encodings, exactly.** `u8`/`i8` take no byte order. Any multiple of 8
  from 16 to 56, `u` or `i`, plus `i64`, requires `be` or `le`. Widths that
  are not whole bytes, `u1` to `u63`, are unsigned, big-endian bit order,
  and take no suffix. `u40` and `u56` are allowed for consistency although
  §3's table names only 16, 24, 32 and 48. The rules live in
  `syntax.IntEncodingError`, shared by checker and tests.
- **A string literal segment may omit its size** (`"user": utf8`): the
  literal's own length is the size. A written size must match it.
- **A pattern must consume its whole input.** `<<0: i32be>>` matches exactly
  four bytes; a trailing `rest: bytes` is how a pattern accepts more. This
  is Erlang's rule and §3's examples rely on it; the text above did not say
  it outright.
- **Negative literals cannot be written in a pattern** (`-1: i32be`): a
  pattern segment's literal is an `integer-literal`, which has no sign. Bind
  a name and compare instead. Unattested so far.
- **Methods.** `bytes` has `length`, `at`, `slice`, and `utf8`; `string`
  gained `utf8`, and its existing `isBlank` and `contains` are now typed by
  the checker too, through one method table (ADR 0049 §2.8's shape, without
  0049's acceptance). An unknown method on `bytes` is an error; an unknown
  method on `string` keeps its old unmodeled behavior.
- **Runtime.** `runtime.Bytes` is a view over a shared `[]byte` with an
  unexported claim-the-slot counter, only on values grown by a binary
  expression whose first segment is `bytes`. Matched segments and `slice`
  results are views with their capacity clipped, so they never write into
  the input's buffer. `TestGrowingBytesCopiesLogarithmically` pins §4's cost
  guarantee.
- **Hex literals** are parsed by `syntax.IntValue`, which also keeps a
  leading zero decimal (`010` is ten, not eight).
- **Formatter.** A long binary expression in a statement wraps one segment
  per line. A long binary *pattern* in a match arm does not wrap (no arm
  pattern wraps today); `read_frame.fiig`'s arm is over 100 columns.
- **Not done:** bytes at the HTTP/CLI boundary (JSON has no `bytes` form;
  the adapter and OpenAPI layers do not know the type), and the `Socket`,
  `TlsSocket`, `FileSystem` and `Random` providers this unblocks (ADR 0042).
- **Corpus:** `examples/postgres-wire/` (`readFrame`, `readAuthentication`,
  `startupMessage`), run against captured wire bytes by `compiler/runtime`'s
  `TestCorpusPostgresFraming`. HTTP/2 and WebSocket headers are runtime
  tests (`TestSubByteFields`, `TestWebSocketHeaderAndLittleEndian`).
