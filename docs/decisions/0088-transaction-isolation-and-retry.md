# ADR 0088: Transaction Isolation and Retry

Status: Accepted and implemented (2026-11-17; see Implementation notes)
Date: 2026-11-17
Related: fiig-lang.md §3.8 (transactions), ADR 0014 (retry is explicit,
at the call site), ADR 0015 (implicit transaction binding), ADR 0084 §5
and open question 3 (a pinned connection; isolation levels), ADR 0087
(`DatabaseConflict.Retryable`; its open question 1), bug classes C1, C4,
C5.

## Context

A database aborts one of two transactions that collide, and expects the
client to run it again from the start:

- **`40001` serialization failure**, Postgres's normal way of keeping
  `SERIALIZABLE` transactions serializable, raised by any statement or by
  `COMMIT`;
- **`40P01` deadlock**, possible at any isolation level;
- **SQLite's `BUSY`**, when another connection holds the write lock.

ADR 0087 made these `DatabaseConflict.Retryable`, which a single statement
may handle, but a transaction can't be repaired from inside: Postgres
refuses every statement after the failure, and a failure at `COMMIT` comes
after the block has finished. Today the program faults. `retry(attempts:
N):` can't help: it wraps one idempotent gateway call (ADR 0014), and a
`transaction` block is a statement.

fiig also can't ask for an isolation level (ADR 0084 open question 3), so
a program has no way to get the `SERIALIZABLE` guarantee that makes
retrying worth having.

### How others handle it

| | Reruns the whole transaction | Default | Retries on |
|---|---|---|---|
| Gel (EdgeDB) `client.transaction(fn)` | yes | on, 3 attempts | serialization failures, deadlocks |
| Google Spanner `runTransaction(fn)` | yes | on, until the deadline | aborted transactions |
| FoundationDB `db.run(fn)` | yes | on | conflicts |
| CockroachDB `crdb.ExecuteTx` | yes | on | `40001` |
| JDBI `SerializableTransactionRunner` | yes | opt-in | `40001` |
| Spring `@Retryable` around `@Transactional` | yes | opt-in | configured exceptions |
| Ecto, Django, Rails, Go `database/sql` | no | | the program's own loop |

Every library that retries reruns the whole block, and only on retryable
failures, never on the program's own errors; each can because its block
is written to be rerun.

## Decision

### 1. `retry` and an isolation level on the block

```fiig
transaction Database serializable retry(attempts: 5) {
    for id in ids {
        saveNote(id)
    }
}
```

```ebnf
transaction-block ::= 'transaction' identifier [ isolation ] [ 'retry' '(' 'attempts' ':' integer-literal ')' ] block
isolation         ::= 'serializable' | 'repeatableRead'
```

Both are optional; with neither, the block is today's. `serializable` and
`repeatableRead` are contextual words, keywords only in this position, as
`attempts` and `limit` are (grammar.md §1). `retry` is the existing
keyword, with the same `attempts` form as `retry(attempts: N):`.

### 2. Retry is explicit, as ADR 0014 decided

A block reruns only when it says `retry`. Gel, Spanner, FoundationDB, and
CockroachDB retry by default, but ADR 0014 settled that whether something
runs again is a decision the code shows where it happens, not a default
or a provider setting, and a rerun transaction costs latency and load
that a reader should see.

### 3. Only a failure the program didn't handle reruns the block

A block with `retry` reruns when it ends with:

- a statement's `DatabaseConflict.Retryable` that no opted-in call handled
  (ADR 0087 §1), which today faults, or
- `COMMIT` failing with a retryable code (`40001`, `40P01`, SQLite's
  `BUSY` or `LOCKED`).

It does not rerun when the block ends with a domain `Err` from `?` or
`return`, which is the program's decision; when a statement's conflict
was handled; or on any other fault. Each of those ends the block as it
does today.

### 4. Each attempt starts from the beginning

Before another attempt the runtime rolls the transaction back, waits as
`retry` does (exponential backoff with jitter, through `Clock`, so a test
with a manual clock doesn't wait), checks the call's deadline, and begins
again on a fresh pooled connection. Locals the block assigned are restored
to their values before the first attempt, so a count kept across the block
(`added = added + 1`) counts one successful attempt, not every attempt.

After the last attempt fails, the block faults with the last retryable
failure and the number of attempts, as an unretried block faults today.

### 5. No `idempotent` is needed

`retry(attempts: N):` around a gateway requires the gateway to be
`idempotent` (E1), since running it twice may do twice what it does
outside the program. A transaction block can't do anything outside the
database: C1, C4, and C5 already confine it to `Database` gateways,
rules, and conversions, and the rollback undoes the database's side. The
one effect left, a gateway's `Random` or `Clock` read, gives a fresh value
on the rerun, which is what a fresh attempt should see.

### 6. Isolation levels

`serializable` and `repeatableRead` begin the transaction at that level:
`BEGIN ISOLATION LEVEL SERIALIZABLE` or `REPEATABLE READ` through a driver
written in fiig, `database/sql`'s `TxOptions` through `libraries/sql`.
With no word, the database's default applies (Postgres's is `READ
COMMITTED`). SQLite's transactions are already serializable, so both
words are accepted there and change nothing. `readUncommitted` isn't
offered: Postgres treats it as `READ COMMITTED`, and a program asking for
dirty reads is better told no.

## Consequences

- Parser and AST: `TransactionStmt` gains `Isolation` and `Attempts`;
  `fiig fmt` writes them in §1's order.
- The checker reports an `attempts` below 1, as for `retry`.
- `TransactionalProvider.Begin` takes the isolation level; the driver
  pool sends it with `BEGIN`; `libraries/sql` passes it to `BeginTx`,
  except on SQLite.
- The runtime's transaction loop reruns the block by §3 and §4, restoring
  the block's assigned locals between attempts.
- ADR 0084 open question 3 (isolation levels) and ADR 0087 open question 1
  close; savepoints stay out.

## Alternatives considered

- **Retry by default** (Gel, Spanner). Convenient, but hides a decision
  ADR 0014 keeps visible, and would change what today's blocks do.
- **`retry(attempts: N):` around a `transaction` statement.** `retry` is
  an expression that retries on any `Err`; a block retried on any domain
  `Err` would rerun a decision the program already made.
- **Retrying from a provider setting** in the capabilities file. ADR 0014
  rejected provider-configured retry.
- **Isolation in a separate ADR.** The two are used together: retrying
  matters most under `SERIALIZABLE`, the level that raises `40001`.

## Open questions

1. **Savepoints** as a language feature (`ROLLBACK TO` inside a block),
   beyond the ones ADR 0087 §3 uses for handled conflicts.
2. **Read-only transactions** (`BEGIN READ ONLY`), which let Postgres skip
   some serializable bookkeeping and a replica serve them.

## Implementation notes (2026-11-17)

- **Syntax:** `TransactionStmt` gained `Isolation` and `Attempts`; the
  parser reads the contextual words and the `retry(attempts: N)` clause,
  `fiig fmt` writes them back in §1's order, and the checker reports an
  `attempts` below 1 (untagged, like other malformed literals).
- **Runtime** (`compiler/runtime/transaction.go`): `evalTransaction` loops
  over `runTransactionOnce`, rerunning only on a fault whose conflict is
  `Retryable` (ADR 0087's `Fault.Conflict`), after restoring the
  reassignable locals visible from the block (`Env.snapshot`/`restore`)
  and backing off as `retry` does; the final fault says how many attempts
  were made. `TransactionalProvider.Begin` takes the isolation level, and
  compile-time assertions now catch a provider whose `Begin` no longer
  matches, which a test fake's had silently stopped doing.
- **Providers:** the driver pool sends `BEGIN ISOLATION LEVEL ...`;
  `libraries/sql` passes `TxOptions` except on SQLite, and its `COMMIT`
  failures now carry their conflict; the relay test server turns `BEGIN
  ISOLATION LEVEL` into SQLite's `BEGIN`.
- **Tests:** a provider failing `COMMIT` with `40001` (reruns from the
  starting locals, gives up after N, never retries a domain `Err`, runs
  once without `retry`, passes the isolation level); the checker and
  formatter forms.
- **Dogfooded on Postgres 17** with `fiig serve`: eight concurrent
  `serializable` transactions each reading `MAX(number)` and inserting the
  next. Without `retry`, seven failed with `40001 could not serialize
  access due to read/write dependencies among transactions`; with
  `retry(attempts: 10)` all eight committed, numbered 1 to 8.
