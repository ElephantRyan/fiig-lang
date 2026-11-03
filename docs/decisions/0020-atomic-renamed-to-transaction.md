# ADR 0020: The `atomic` Block Keyword Is Renamed to `transaction`

Status: Accepted
Date: 2026-10-03

## Context

Working out bug class C3 (`docs/specification/bug-classes.md`) forced a
clarification of §3.8's rule that "writes initially require an active
`atomic` block." A single write is already atomic on its own — an `INSERT`
either commits fully or fails — so requiring an `atomic` block around one
write didn't add anything a lone write didn't already have, and read as
redundant. The real requirement only bites when *multiple* operations (e.g.
a write followed by a read) need to stay consistent as a unit, so that no
other process can interleave between them. That's exactly what a database
transaction is for, and the keyword `atomic` never named that — it named
single-statement indivisibility, which was already true without the block.

## Decision

Rename the keyword from `atomic` to `transaction` everywhere: lexer
(`KwAtomic` → `KwTransaction`), parser/AST (`AtomicStmt` → `TransactionStmt`),
checker (`activeAtomic` → `activeTransaction`, `checkAtomicStmt` →
`checkTransactionStmt`, all C1–C5 diagnostic text), formatter, grammar
production (`atomic-block` → `transaction-block`), and every spec/doc/example
reference. `transaction Database { ... }` replaces `atomic Database { ... }`.

This is a pure rename. Every rule §3.8 already stated is unchanged: one
block controls exactly one transactional capability, blocks may not nest or
overlap, and network calls or workflow calls remain prohibited inside one.
C3 itself (requiring the block for multi-operation sequences) is still not
implemented in the checker — this ADR doesn't change that, only the name of
the construct C3 will eventually require.

## Rationale

`transaction` names what the block actually contributes (consistency across
multiple operations) instead of a property (indivisibility) individual
writes already have without it. It also maps directly to terminology
developers already know from every mainstream database, so `transaction
Database { ... }` reads as "open a transaction," not as a claim about
single-statement atomicity that was never in question.

## Consequences

- `compiler/lexer`: keyword table entry and `Kind` constant renamed.
- `compiler/parser`, `compiler/syntax`: `AtomicStmt` → `TransactionStmt`;
  `parseAtomicStmt` → `parseTransactionStmt`.
- `compiler/checker`: `activeAtomic` → `activeTransaction`,
  `checkAtomicStmt` → `checkTransactionStmt`; C1–C5 diagnostic message text
  updated.
- `compiler/format`: emits `transaction %s {` instead of `atomic %s {`.
- `examples/invalid-programs/transaction_error.fiig` and four other examples
  (`change_customer_email.fiig`, `release_inventory.fiig`,
  `place_and_ship_order.fiig`, `place_order.fiig`) use `transaction` instead
  of `atomic`. Two workflow names that spelled out "atomic" were renamed for
  the same reason: `updateAndRereadWithoutAtomic` →
  `updateAndRereadWithoutTransaction`, `createAndAtomicallyEmail` →
  `createAndEmailInTransaction`.
- `docs/specification/grammar.md`, `docs/specification/bug-classes.md`,
  `docs/terminology.md`, `docs/charter.md`, `fiig-lang.md`,
  `examples/README.md`: keyword and prose updated.
- ADRs 0010, 0015, and 0018 predate this decision and still say `atomic` when
  describing the keyword — left as-is as historical record, per this
  project's convention of not rewriting prior decisions. This ADR supersedes
  their terminology; their reasoning about block semantics (nesting,
  one-capability-per-block, implicit transaction binding) still holds
  unchanged under the new name.
