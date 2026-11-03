# ADR 0008: Workflows Live in a Separate Composition Catalog

Status: Accepted
Date: 2026-09-22

## Context

Decision record #6 (`fiig-lang.md` §8): does every exported workflow appear
in the default catalog search, or in a separate composition catalog?

## Decision

Separate. Default `lang catalog find` searches rules, conversions, and
steps — the building blocks. Workflows live in a distinct composition
catalog that has to be searched deliberately.

## Rationale

A developer or LLM composing a *new* workflow (the central development loop,
`fiig-lang.md` §1) is searching for building blocks to combine, not for
existing whole workflows to call as black boxes — calling another workflow
directly is legal (§3.2) but is the less common search intent. Keeping the
default index scoped to composable primitives matches what's actually being
searched for most of the time.

## Consequences

- Milestone 3 deliverables (`fiig-lang.md` "Package catalog generation,"
  "Merged application catalog," CLI search) need two search scopes rather
  than one flat index — e.g. a `--workflows` flag or a separate `lang
  catalog find-workflow` command. Exact CLI shape isn't designed yet.
- Adapters, which call workflows exclusively (§3.1), search the composition
  catalog by definition — worth confirming this is intuitive once the
  registry CLI syntax exists, rather than assuming it needs no further
  documentation.
