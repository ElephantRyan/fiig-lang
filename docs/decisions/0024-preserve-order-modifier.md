# ADR 0024: `preserveOrder` Concurrency Modifier Syntax

Status: Accepted. Superseded by ADR 0148 (2026-11-23): a batch is always in
input order, and `preserveOrder` is a syntax error `fiig fmt` removes.
Date: 2026-10-06

## Context

`fiig-lang.md` §3.7 and `docs/terminology.md` both document `preserveOrder` as
"an explicit modifier that makes a `concurrent` block return results in input
order instead of the default completion order" — it's in the Milestone 0
charter, and the Milestone 0 backlog's first demonstrable release explicitly
lists "Support `preserveOrder`" as its own step, separate from the bounded
`concurrent` block itself.

Despite that, no grammar production, lexer keyword, parser support, or AST
field for it was ever added during Milestone 1. `ConcurrentExpr` (`compiler/
syntax/ast.go`) only ever had `Var`, `Iterable`, `Limit`, and `Body` — there
was no way to write `preserveOrder` in source at all. This surfaced while
starting Milestone 4 (the runtime), whose own primary vertical slice
(`retrieveCustomers`) is the exact feature `preserveOrder` is documented
against.

## Decision

`concurrent(...)` accepts zero or more comma-separated modifiers after the
`identifier 'in' expression` clause, each at most once, in either order:
`limit: <value>` (already existed) and the new bare `preserveOrder`. Both of
these are legal and equivalent:

```text
concurrent(id in ids, limit: 5, preserveOrder): getCustomer(id)
concurrent(id in ids, preserveOrder, limit: 5): getCustomer(id)
```

`preserveOrder` is a new reserved word, lexed unconditionally like `limit`,
`attempts`, `idempotent`, etc. (ADR 0018 is explicitly scoped to the eight
declaration-start keywords only; `preserveOrder` is the same kind of
single-position expression keyword those other concurrency/retry words
already are, not a new case for that ADR to cover). `ConcurrentExpr` gets a
`PreserveOrder bool` field, defaulting to `false` — the pre-existing
completion-order default is unchanged.

## Rationale

- A boolean flag alongside `Limit` is the smallest change that matches how
  the modifier is already documented everywhere else (a modifier of the
  `concurrent` block, not a separate statement or block form).
- Allowing either order (`limit` before or after `preserveOrder`) avoids
  inventing an arbitrary canonical order with no attested preference; the
  formatter still only ever emits one order (`limit:` then `preserveOrder`)
  for its own canonical output, per the usual "parser is permissive, printer
  is canonical" split this codebase already follows elsewhere.
- No checker change is needed: `preserveOrder` doesn't affect any of the
  Category D (concurrency) static checks, which are entirely about numeric
  limits. It's purely a Milestone 4 runtime concern — the worker pool either
  returns results via a channel in completion order (default) or collects
  them positionally by input index (`preserveOrder`).

## Consequences

- `compiler/lexer`: new `KwPreserveOrder` token / keyword.
- `compiler/syntax`: `ConcurrentExpr.PreserveOrder bool`.
- `compiler/parser`: `parseConcurrentExpr` reads a loop of comma-separated
  modifiers instead of a single optional `limit:` clause.
- `compiler/format`: `printConcurrentExpr` emits `, preserveOrder` when set.
- `docs/specification/grammar.md`: `concurrent-expr`'s production and keyword
  list updated.
- New example: `examples/customer-service/workflows/
  retrieve_customers_in_order.fiig`, the first (and so far only) attested use.
- `compiler/runtime` (Milestone 4) is the first and only consumer of this
  field so far — see its own doc comments for how completion-order vs.
  input-order collection is actually implemented.
