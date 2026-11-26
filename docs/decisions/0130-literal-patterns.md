# ADR 0130: Literal Patterns for `int`, `string` and `bool`

Status: Accepted (2026-11-25, by the owner). Implemented 2026-11-21 (see
Implementation notes).
Date: 2026-11-21
Answers: ADR 0091 open question 6; `tour.md`'s "Known gaps" (literal patterns).
Related: ADR 0048 (float), ADR 0052 (comparability), ADR 0064 (decimal), ADR
0067 §8 (strings aren't normalized), ADR 0058 (literals in binary patterns),
abs-recommendations GAPS G9.

## Context

A pattern bound names and never held a value to compare against. A literal in
one was an error that told the programmer to bind a name and compare it
(abs-recommendations GAPS G9), and a `match` on a status code was an `if`
chain:

```fiig
if status == 404 { ... } else if status == 500 { ... } else { ... }
```

ADR 0091 listed what a literal pattern touches, and left it for its own ADR:
exhaustiveness with literal arms, float patterns, decimal scale, and
unnormalized strings. A sweep (`docs/improvement-ideas.md`) found one more
thing, which makes the decision urgent:

**`true` and `false` in a pattern were silently names.** `match on { true -> 1
false -> 2 }` passed `fiig check`, and `flag(false)` returned 1, because the
first arm bound `on` to the value and always matched. A construct that
type-checks and answers wrongly.

### How others handle it

| Language | Literals in patterns | Exhaustiveness |
| --- | --- | --- |
| Rust | integers, strings, booleans, ranges; floats are refused (a lint, and an error in newer editions) | a catch-all is required for an integer |
| Scala, Kotlin | all of them, including floats | a catch-all (`else`) for a non-sealed subject |
| Elixir, Erlang | all of them, floats compared by `===` | none; no match is a runtime error |
| OCaml, Haskell | integers, characters, strings, floats allowed | a catch-all for an infinite type |
| Swift | integers, strings, ranges | a `default` |
| Go | `switch` takes values and a `default` is optional | none |

Rust and Swift draw the line where this ADR does: the types that compare
exactly are matchable, floats aren't, and an integer or a string needs a
catch-all.

## Decision

### 1. Three literals

```fiig
match status {
    200 -> "ok"
    404 -> "missing"
    -1 -> "unset"
    0x10 -> "sixteen"
    _ -> "other"
}

match name {
    "ACME" -> 1
    _ -> 0
}

match enabled {
    true -> "on"
    false -> "off"
}
```

```ebnf
literal-pattern ::= [ '-' ] integer-literal | string-literal | 'true' | 'false'
```

- **An `int`** is decimal or hex, with an optional minus.
- **A `string`** is a literal with no interpolation. It matches the text as
  written, code point for code point, as `==` does (ADR 0067 §8: strings aren't
  normalized, so `é` as one code point is not `e` plus an accent).
- **`true` and `false`** are the values, and **no longer bind a name**.
- A literal may be anywhere a pattern is: an arm, `expect x is ...`, and
  inside a constructor: `Err(Failure.Rejected(404)) -> ...`.
- It matches a value **equal to it**, by the rules of `==`.

### 2. No floats, no decimals

A float or decimal literal in a pattern is an error that says to bind a name
and compare: `math.closeTo` for a float. Two floats that should agree can
differ in the last bit (ADR 0048), and a decimal's scale is part of what it
prints as, so `1.5` and `1.50` are equal and different (ADR 0064). An `int`
literal against a float or decimal subject is the same error.

### 3. A catch-all is required, except for a `bool`

A match with a literal arm lists values, and an `int` or a `string` has more
of them than any match writes. So it needs an arm that **catches the rest**, a
`_` or a bare name, or it is **A9**: "missing case(s): every other value (add
a `_` arm)". A `bool` is covered by `true` and `false`. A `T | None` subject
still needs its `None` arm, as everywhere else. The runtime keeps its fault for
a value no arm matched, which a checked program doesn't reach.

### 4. A negative literal starts an arm

A line that begins with `-` after an arm whose body is an expression reads as
a subtraction from that body: `"missing"\n-1 -> ...` is `"missing" - 1`. The
expression parser stops at a minus followed by an int and an arrow: no
expression is followed by `->`, so it can only be the next arm.

### 5. Types

A literal's type must fit the subject's, A1: an `int` pattern on a `string`
says so. A subject that is a union with the literal's type as a member (`string
| None`) fits.

## Alternatives considered

- **Ranges** (`200..299`, Rust's). Useful for status classes, and a second
  syntax and a second exhaustiveness argument. A guard form or `if` covers it
  for now.
- **Float patterns with `closeTo`.** A tolerance in a pattern hides a choice
  the program should make visibly.
- **Leave `true` and `false` as they were.** It is the bug above.
- **A `switch`-like form beside `match`.** Two ways to branch on a value.

## Consequences

- A program that wrote `true` or `false` as a pattern changes meaning: the
  first arm no longer always matches. That was never what it meant, and the
  corpus and the stdlib have none (the tests pass).
- `expect r is Err(RatingError.Invalid("score", _))` is one line (ADR 0091 open
  question 6).
- ADR 0058's binary patterns already took literal segments; this is the same
  idea outside them, with no overlap: a segment's literal is inside `<< >>`.

## Open questions

1. **Ranges** and **alternatives** (`200 | 201 -> ...`), if an `if` chain over
   them turns up often.
2. **Duplicate arms**, and an arm that an earlier literal makes unreachable,
   aren't reported.

## Implementation notes (2026-11-21)

- `syntax.LiteralPattern`; the parser (`parseIntPattern`, the string and
  `true`/`false` cases, the float refusal, the minus lookahead in
  `parseAdditive`); `compiler/checker/literal_pattern.go` (A1, A9);
  `compiler/runtime/match.go`; the formatter.
- Tests: the checker (what passes, A9 and A1 and the float refusal, a literal in
  an `is`), the runtime (equality for each kind, `true`/`false` as values, a
  literal in a constructor), the formatter (stable, string escapes), and the
  parser's one-error cases.
- Dogfooded: the `flag(false)` probe from the sweep now answers 2.
