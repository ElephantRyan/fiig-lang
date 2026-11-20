# ADR 0104: A File Is Named After Its Declaration, in TitleCase

Status: Accepted (2026-11-20). Not yet implemented; built with ADRs
0102-0107 as one migration.
Date: 2026-11-20
Amends: fiig-lang.md §3.9 (files).
Related: ADR 0102 (test files mirror source files), ADR 0105 (folders are
packages).

## Context

Nothing decided file names. The corpus used snake_case
(`rank_recommendations.fiig`, 252 of 295 files) by habit, and
abs-recommendations copied it.

Languages use snake_case where the file name is the import name and must
be an identifier (Python, Rust), lowercase to dodge case-insensitive file
systems (Go), or a suffix with meaning (Go's `_test.go`). TitleCase is
used where a file holds one type named after it (Java, Kotlin, C#,
Haskell, Elm). In fiig code never names a file and a file holds one
declaration, so none of snake_case's reasons apply.

## Decision

- A source file is named after its declaration, first letter
  capitalised: `rule rankRecommendations` is `RankRecommendations.fiig`,
  `model Rating` is `Rating.fiig`. A mismatch is an error naming the right
  name.
- A test file is named after the source file it mirrors (ADR 0102); a
  file in `test/scenarios/` names what it checks.
- Two declarations in a package whose names differ only in case are an
  error, so no two files collide on Windows or macOS.
- Folders stay lowercase: they are packages (ADR 0105). Migration files
  (`0001_create_ratings.sql`) aren't declarations and keep their form.

## Rationale

- **A name leads straight to its file**, for a reader, `grep`, or an LLM.
- **Enforced, so it can't drift**, as the corpus's habit did.

## Alternatives considered

- **snake_case**, **kebab-case**: a translation between the name and
  the file.
- **Exactly the declaration's name** (`rankRecommendations.fiig`): two
  casings in one folder.

## Consequences

- Every file is renamed once, by `fiig fmt -layout`.
