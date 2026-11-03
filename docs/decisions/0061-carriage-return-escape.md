# ADR 0061: The `\r` and `\$` Escapes, and Line Endings Inside String Literals

Status: Accepted (2026-10-26; implemented the same day)
Date: 2026-10-26
Related: ADR 0058 (bytes and binary patterns), ADR 0060 (Socket; found the
gap).
Milestone 7: needed by every CRLF text protocol (HTTP/1.1, SMTP, IMAP,
Redis's RESP).

## Context

A fiig string has four escapes:

```
escape-sequence ::= '\' ('"' | '\' | 'n' | 't')
```

Every other `\x` is a lex error ("unknown escape sequence"), so adding one
cannot change the meaning of an existing program.

HTTP/1.1, SMTP, Redis and IMAP all end lines with CR LF. Writing ADR 0060's
HTTP client found there is no way to spell CR in a string, so
`acme-http`'s `httpRequest` builds every line as bytes:

```
crlf = <<13: u8, 10: u8>>
return <<
    "POST ${request.path} HTTP/1.1": utf8,
    crlf: bytes,
    "Host: ${request.host}": utf8,
    crlf: bytes,
    ...
>>
```

It works, but it splits one line of protocol text into two segments, and a
reader has to know that 13 and 10 are CR and LF.

### A second problem: a raw CR already gets in

Investigating the escape turned up something worse. The lexer accepts a raw
newline inside a string literal, and copies every character it doesn't
recognise straight into the value, CR included. So a file's line endings
change what its strings contain. Tested with the same three-line literal:

```
s = "a
b"
```

| File saved with | `s.utf8().length()` |
| --- | --- |
| LF line endings | 3 |
| CRLF line endings | 4 |

This repo is LF throughout, but the machine it lives on has
`core.autocrlf=true`. Under git, a Windows checkout of this file would
silently compile to a different value. The formatter makes it worse: it
re-escapes `\n` and `\t` when it prints a string, but not CR, so `fmt-cli`
writes the raw CR back out and the difference survives every reformat.

## Decision

### 1. Add `\r`

```
escape-sequence ::= '\' ('"' | '\' | 'n' | 't' | 'r')
```

`\r` is U+000D, CARRIAGE RETURN. The request line becomes:

```
"POST ${request.path} HTTP/1.1\r\n"
```

Every language that has escapes at all spells CR this way: C, Go, Rust,
Java, Kotlin, Swift, Python, JavaScript and JSON. There is no competing
spelling to weigh.

### 2. A line break inside a string literal is always LF

Inside a string literal, a CR LF pair in the source reads as a single LF,
and a CR not followed by LF is a lex error ("use \r"). A string's value no
longer depends on how the file was saved, and the only way to put CR in a
string is to write `\r`.

| Language | Raw CR LF inside a literal |
| --- | --- |
| Go (raw strings) | CR discarded: the spec says so, for this reason |
| Java (text blocks) | normalized to LF |
| Swift (multi-line) | normalized to LF |
| Python | the source reader turns CR LF into LF before lexing |
| Rust | a bare CR is a compile error: "use `\r` instead" |

Rust's rule is the strictest, and on its own it would reject every
multi-line string in a CRLF checkout, which punishes the Windows user for
their git configuration. Normalizing CR LF, and rejecting only the lone CR
that no modern editor produces by accident, gives Go/Java/Swift's
"checkout-independent" property while keeping Rust's "say what you mean"
property for the one case where intent is ambiguous.

### 3. The formatter prints `\r`

`escapeStringText` in `compiler/format` escapes CR as `\r`, alongside its
existing `\n`, `\t`, `\\` and `\"`. A string that holds CR then always
prints back the same way, whatever produced it.

## Alternatives considered

**A. `\xHH` byte escapes (Go, Python, C).** One escape for every control
character, not just CR. Rejected because fiig's strings are UTF-8 text
(ADR 0058 §1 keeps `string` and `bytes` separate), and `\xFF` inside a
string either produces invalid UTF-8 (Go allows it) or needs Rust's rule
restricting `\x` to ASCII. Arbitrary bytes are what binary patterns are
for. CR is the one control character text protocols actually need.

**B. `\u{...}` code-point escapes (Rust, Swift, JavaScript).** Useful for
characters that are hard to type or see, such as a non-breaking space or a
zero-width joiner. Rejected *for now*: no program in the corpus or the
milestone needs it, source files are UTF-8 so any visible character can be
typed directly, and JSON's `\uXXXX` is decoded at runtime by the JSON
library, not by the fiig lexer. Its own ADR when a program needs it.

**C. A `crlf` constant in the prelude.** Avoids touching the lexer, but
`"Host: ${host}${crlf}"` is no clearer than the bytes workaround, and it
adds a prelude name for a single character every other language spells
`\r`.

**D. Leave raw CR alone (status quo for §2).** Rejected: that leaves a
program's meaning depending on its git checkout, which is the kind of
silent, environment-dependent behaviour fiig's checker exists to rule out.

## Decisions taken at review

- **`\$` is in** (was open question 1). There was no way to write a literal
  `${` in a string, because the lexer always starts an interpolation there.
  Kotlin, Dart and Groovy spell it `\$`. `\$` is legal before any character,
  but the formatter prints it only where it matters, before `{`, so `"$5"`
  stays `"$5"`.

## Open questions

None. The one left at acceptance, that a `//` comment's recorded text kept
its trailing CR in a CRLF file and the formatter printed it back, was fixed
the same day (see the implementation notes).

## Consequences

- `grammar.md`: `escape-sequence` gains `'r'` and `'$'`; a sentence on line breaks inside literals.
- `compiler/lexer`: one escape case; CR LF folded to LF inside a string; a
  lone CR inside a string is a lex error.
- `compiler/format`: `escapeStringText` escapes CR.
- Corpus: `acme-http`'s `httpRequest` goes back to one string per line, the
  first attested use.
- Tests: the lexer for `\r`, for CR LF folding and for the lone-CR error;
  the formatter round trip; and the CRLF-saved file from Context, which must
  give the same length as the LF one.
- No new bug class: a lone CR is a lex error, like an unknown escape.

## Implementation notes (2026-10-26)

- `compiler/lexer` `scanStringPart`: `\r` and `\$` cases; a CR followed by
  LF appends LF, any other CR reports "carriage return inside a string
  literal; use \r". CR between tokens is still whitespace.
- `compiler/format` `escapeStringText`: CR prints as `\r`, and `$` prints as
  `\$` only when the next character is `{`. A text part ending in `$` before
  an interpolation needs no escape, since `$${x}` lexes as `$` then `${x}`.
- `acme-http`'s `httpRequest` is now one string with `\r\n` line endings,
  replacing the `crlf` bytes segments. `TestCorpusHTTPRequestBytes`
  (`compiler/runtime/bytes_test.go`) pins its exact wire bytes; nothing had
  executed the conversion before.
- Tests: `compiler/lexer/lexer_test.go` (both escapes, CR LF read as LF, lone
  CR error, CR LF between tokens) and `compiler/format/loop_format_test.go`
  (round trip, and a CRLF copy of the same source formats to the LF one).
- Comments: the lexer drops a trailing CR from a `//` comment's text, so
  every consumer of `Comments()` sees the same text whatever the file's line
  endings, not just the formatter. `TestFormatCRLFCommentsLoseTheirCR` covers
  leading, interior and trailing comments.
