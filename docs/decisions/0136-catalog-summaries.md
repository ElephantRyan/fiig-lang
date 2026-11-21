# ADR 0136: A Declaration's Leading Comment Is Its Summary in the Catalog

Status: Proposed
Date: 2026-11-21
Related: ADR 0133 (`fiig review`), charter goals 1 and 4 and success
criterion 3, `docs/specification/llm-composition-trial.md`, ADR 0106
(`fiig calls`).

## Context

The charter's third success criterion: an LLM can compose a second workflow
correctly using only generated registry contracts, without reading
implementation source. A `Card` (`compiler/registry/registry.go`) carries
name, kind, params, return type, the `Error` type of a `Result`, effects,
capabilities and guarantees. It has no field for what the declaration is
*for*.

The programs have the text. Hearsay's declarations open with a comment such
as "Removing a rating you never made is 'not found', not a silent success."
The formatter keeps comments (`compiler/format`) so the lexer sees them; the
AST and the registry do not retain them. So the one sentence that says what a
workflow decides is exactly what a catalog reader cannot see.

Without it, the cold-read test (give an agent only the catalog and ask it to
describe each feature) has little to work with. A workflow card says
`putRating(session, itemId, input) -> Result<Rating, ApiError>`, which says
what goes in and out but not why. That was Hearsay's actual failure (ADR
0133): when every layer returns the same error type, the signature carries no
meaning, and the comment is the only place left that does.

## Decision

### 2.1 `Card.Summary`

A card has `summary`: the declaration's **leading comment**, the `//` lines
directly above it with no blank line between, joined with single spaces,
without the `//`. The first sentence is the summary; the rest is
`Card.Notes` (kept, not shown by default).

- Declaration kinds covered: all that have cards today.
- No comment means `summary` is omitted. It is not generated, not inferred
  from the name and not an error.
- `use` lines and a file-level comment before them are not a declaration's
  comment. A comment between `use` lines and the declaration is, if directly
  above it.

### 2.2 Search

`fiig catalog find` searches `summary` as well as names, with names
weighted higher. The compact listing shows the summary after the contract.

### 2.3 `catalog -missing-summary`

`fiig catalog -missing-summary [-kind workflow]` lists declarations without
one. It is a listing, not a diagnostic (ADR 0051); `fiig review` (ADR 0133,
item 7) reports the same list for workflows.

### 2.4 Merge and diff

`registry.Merge` keeps the summary of each card. `registry.Diff` ignores
`summary` when deciding whether a bump is breaking (ADR 0034): a changed
comment is never a contract change.

## Rationale

- **It is already written.** The corpus and Hearsay comment their
  declarations. Surfacing the comment costs the author nothing.
- **It makes the cold-read test possible.** A summary plus the typed
  contract is what an LLM needs and what the charter promises.
- **It follows the file, not a new syntax.** No grammar change, no `@doc`
  form, no stale annotations separate from the code.

## Alternatives considered

- **A `doc "..."` clause.** More syntax, and a second place to keep the
  sentence current.
- **Generate a summary from the name and types.** It would give a card the
  look of meaning with none of it.
- **Require a summary on public declarations.** It would make every
  one-line pass-through carry a sentence, which is not meaning either. A
  required comment is the checker's second severity by another name (ADR
  0051 §2.2 test 2 fails: not wrong in every reading).

## Consequences

- The parser attaches the leading comment to a declaration (the lexer
  already tracks comments for `fmt`). `compiler/registry` gains one field.
- The registry's JSON schema (`docs/schemas`) gains `summary` and `notes`.
  Existing consumers that ignore unknown fields are unaffected.
- A comment that is out of date now appears in search. This is the cost of
  showing it, and the same cost every documented API has.

## Open questions

1. **First sentence or first paragraph?** First sentence is predictable;
   comments in the corpus sometimes put the point in the second.
2. **Interaction with the `private { }` block**: private declarations have no
   cards today, so their comments do not surface.
