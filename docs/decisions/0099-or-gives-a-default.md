# ADR 0099: `or` Gives an Optional Value a Default

Status: Accepted (2026-11-20). Implemented 2026-11-20; see Implementation
notes.
Date: 2026-11-20
Amends: ADR 0041 (`and`/`or` are bool-only).
Related: ADR 0040 (bare-name `match` arms narrow `T | None`), ADR 0063
(the `OrNone` convention), abs-recommendations GAPS G26 and G1.

## Context

`match` is a statement (G1), so taking a value out of a `T | None` with a
fallback costs a block, or a helper rule that exists only for that:

```
rule noteOf(note: string | None) -> string {
    match note {
        None -> ""
        given -> given
    }
}
```

abs-recommendations has several (`noteOf`, `parseOrder`,
`cachedOrFetched`, and the checks in its tests), and the gaps log says the
default is the commonest case.

### How others do it

| Language | Optional with a default |
|---|---|
| Kotlin | `note ?: ""` |
| Swift, C# | `note ?? ""` |
| Rust | `note.unwrap_or("")` |
| Python | `note or ""` (any falsy value) |
| JavaScript | `note ?? ""` (null or undefined); `note \|\| ""` (any falsy) |

## Decision

`x or d`, where `x` is `T | None`, is `x` when it isn't None and `d` when
it is. `d` is a `T`, so the result is a `T`:

```
note: string = input.note or ""
tags: List<string> = input.tags or []
name = input.alias or input.name or "anonymous"
```

- **The left side's type decides.** A `bool` left side is boolean `or`,
  as before. Any `T | None` left side is the default, including `bool |
  None`: `done or true` is `false` when `done` is `false`, which boolean
  `or` would make `true`.
- **A default that may itself be None** keeps the result optional, for a
  next `or` to settle: `alias or name or "anonymous"` is a `string`.
- **The right side runs only when it's needed**, as boolean `or`'s does.
- A default of the wrong type is A1, naming both types.

## Rationale

- **A word, not a symbol.** fiig spells its operators (`and`, `or`,
  `not`); `??` and `?:` would be its first punctuation operators, and `?`
  already means propagate.
- **It reads the way it works.** "The note, or the empty string."
- **Only None is replaced,** unlike Python's and JavaScript's `or`/`||`,
  which also replace `0`, `""`, and `false`. fiig has no truthiness.

## Alternatives considered

- **`??`** (Swift, C#, JavaScript). Familiar, but punctuation, and too
  close to `?`.
- **A method, `note.orElse("")`.** Needs a method on every union with
  None; and a lazy default would need a function value.
- **`if let` / `match` as an expression.** Larger, and G1 decided `if`
  and `match` stay statements.

## Consequences

- G26's commonest case is one expression; helpers like `noteOf` go away.
- A reader seeing `or` checks the left side's type, which the IDE's hover
  shows. Mixing the two in one chain (`ready or input.done or false`) is
  read left to right by type, like every other binary operator.

## Implementation notes

Built 2026-11-20. The checker marks the `BinaryExpr` (`OrDefault`), so
the runtime doesn't have to guess from the value, which is what keeps a
present `false` from being replaced. On a copy of abs-recommendations,
`noteOf` became `input.note or ""` and its 13 tests pass.
