# ADR 0119: Savepoints and Read-Only Transactions

Status: Accepted (2026-11-21; the owner chose a separate `savepoint` word
over nesting `transaction` blocks, and `readOnly`)
Date: 2026-11-21
Answers: ADR 0088 open questions 1 (savepoints) and 2 (read-only
transactions).
Related: fiig-lang.md §3.8 (transactions), ADR 0015 (implicit transaction
binding), ADR 0082 (`Database`'s three operations), ADR 0087 §3 (the
savepoint a handled conflict already runs in), ADR 0088 (isolation and
retry), bug classes C2 and C5.

## Context

**Undoing part of a transaction.** A batch import commits the rows that
are good and reports the ones that aren't. Each row is a few statements:

```fiig
gateway importOrder(row: OrderRow) -> Result<string, ImportError>
    requires Database
{
    Database.execute("INSERT INTO orders (id, customer) VALUES (?, ?)", row.id, row.customer)
    for line in row.lines {
        if line.quantity < 1 {
            return Err(ImportError.BadQuantity(row.id))
        }
        Database.execute("INSERT INTO order_lines (order_id, sku, quantity) VALUES (?, ?, ?)", row.id, line.sku, line.quantity)
    }
    return Ok(row.id)
}
```

Called in a loop inside `transaction Database { }`, a row that fails half
way leaves its `orders` row and its first lines behind, and they commit
with the good rows. Leaving the block with the `Err` rolls back every row.
ADR 0087 §3 already runs one opted-in statement inside a savepoint, but a
program has nothing for several statements, and nothing for a domain
`Err` rather than a constraint conflict.

**Reading under one snapshot.** A report that reads several tables wants
one consistent view and a promise that it writes nothing. Postgres offers
`READ ONLY`: a write in the transaction fails, a `SERIALIZABLE READ ONLY`
transaction takes less predicate locking, and a hot standby accepts only
read-only transactions. fiig can say `serializable` (ADR 0088) but not
`readOnly`.

### How others do it

Savepoints:

| | How a program asks | What a failure inside does |
|---|---|---|
| Postgres, SQLite | `SAVEPOINT s` ... `RELEASE s` or `ROLLBACK TO s` | the program decides which to send |
| Django | nested `atomic()` is a savepoint | an exception leaving it rolls back to it; catching it outside continues the outer one |
| Rails | `transaction(requires_new: true)` | as Django; without `requires_new` a nested block joins the outer one |
| Spring | `Propagation.NESTED` | an exception rolls back to the savepoint, and continues outward unless caught |
| SQLAlchemy | `session.begin_nested()`, a word of its own | as Django |
| Diesel, sqlx | a nested `transaction`/`begin` is a savepoint | an `Err` returned from the closure rolls back to it, and is the call's value |
| Ecto | none: a nested `transaction` joins the outer one | an inner rollback dooms the whole transaction |
| JDBC | `conn.setSavepoint()`, `conn.rollback(sp)` | manual |

Read-only:

| | How a program asks | What a write inside does |
|---|---|---|
| Postgres | `BEGIN READ ONLY` | `25006 read_only_sql_transaction` |
| MySQL | `START TRANSACTION READ ONLY` | error 1792 |
| SQLite | no read-only transaction; `PRAGMA query_only` on the connection | `SQLITE_READONLY` |
| Go `database/sql` | `TxOptions{ReadOnly: true}` | the driver's |
| Spring | `@Transactional(readOnly = true)`, a hint the driver may ignore | depends on the driver |

Every row that nests the word for "transaction" (Django, Rails, Diesel,
sqlx) makes the inner one something other than a transaction: it never
commits by itself. C2 forbids nested `transaction` blocks for that reason:
in fiig, `transaction` always means a real `BEGIN` and a real `COMMIT`.

## Decision

### 1. Syntax

```fiig
transaction Database serializable readOnly retry(attempts: 3) {
    totals = readTotals(day)
    details = readDetails(day)
}

count: int = 0
rejected: List<ImportError> = []
transaction Database {
    for row in rows {
        imported = savepoint {
            importOrder(row)
        }
        match imported {
            Ok(_) -> {
                count = count + 1
            }
            Err(e) -> {
                rejected = append(rejected, e)
            }
        }
    }
}
```

```ebnf
transaction-block ::= 'transaction' identifier [ isolation ] [ 'readOnly' ] [ 'retry' '(' 'attempts' ':' integer-literal ')' ] block
isolation         ::= 'serializable' | 'repeatableRead'
savepoint-expr    ::= 'savepoint' block
```

The clauses are in that order, each optional: what the transaction is
(isolation, then read-only), then how it's run (retry). `fiig fmt` writes
them so, and the parser reports another order. `readOnly` is a contextual
word, a keyword only there, like the isolation words.

`savepoint` is a reserved keyword. As a contextual word it would be
ambiguous: `match savepoint {` could be a match on a local named
`savepoint`. No corpus file uses the name.

### 2. Where a savepoint may be

- **Lexically inside a `transaction` block** of the same declaration (C6).
  A savepoint names no capability: it belongs to the enclosing block's
  transaction. A gateway can't hold one, since a gateway may be called
  outside any transaction; C5 already keeps activities and workflows out
  of a transaction block, so lexical is the only place one can be.
- **Not in a `concurrent` body** (C7). A connection's savepoints are a
  stack: two workers taking turns on the transaction's one connection
  would release or roll back each other's.
- **As the whole value of a statement:** `x = savepoint { }`,
  `x: Result<T, E> = savepoint { }`, `return savepoint { }`, or alone.
  A `match` on it binds it first. Alone, its `Result` is dropped, which is
  F1 as for any other.
- **Inside another savepoint**, which Postgres and SQLite both nest.
- `break` and `continue` don't cross a savepoint's body, as they don't
  cross a transaction's (L1, L2).

### 3. What a savepoint does

A savepoint is a value: its body ends with an expression of type
`Result<T, E>`, and that is the savepoint's value.

| How the body ends | The database | The value |
|---|---|---|
| its last expression is `Ok(...)` | `RELEASE SAVEPOINT` | that `Ok` |
| its last expression is `Err(...)` | `ROLLBACK TO SAVEPOINT`, then `RELEASE` | that `Err` |
| a `?` in the body hits an `Err` | `ROLLBACK TO SAVEPOINT`, then `RELEASE` | that `Err` |
| `return` with an `Err` | `ROLLBACK TO SAVEPOINT`, then `RELEASE` | the declaration returns it, and the transaction then rolls back by its own rule |
| `return` with anything else | `RELEASE SAVEPOINT` | the declaration returns it, and the transaction commits |
| a fault | nothing | the fault; the transaction rolls back whole |

**`?` stops at the savepoint.** Inside a savepoint's body, a `?` that hits
an `Err` ends the savepoint, not the declaration, so a unit of several
calls reads as one:

```fiig
moved = savepoint {
    debit(from, amount)?
    credit(to, amount)?
    Ok(amount)
}
```

This is the one change to what `?` means, and it is what makes a
savepoint useful. If an `Err` from inside left the transaction as well,
the transaction would roll back with it and the savepoint would have
saved nothing; skipping a bad row needs the failure to stop at the
savepoint. fiig has no `try`/`catch` to stop it with (fiig-lang.md §3.6),
so the savepoint is the boundary, as Rust's `try` blocks are for `?` and
Diesel's nested transaction is for its closure's `Err`. The rule is the
transaction block's own (ADR 0088, `compiler/runtime/transaction.go`):
`Err` undoes the work, anything else keeps it; the savepoint then gives
the `Err` back as a value instead of passing it on. `?` inside a
`concurrent` body in the savepoint keeps its meaning there.

`return` still means the declaration. A savepoint is rolled back when the
`return` carries an `Err`, as the transaction around it will be.

**Locals are restored with the database.** A savepoint rolled back puts
back the reassignable locals its body changed, as a retried transaction
does between attempts (ADR 0088 §4), so a count kept inside one counts
only what was kept.

The body's last statement must be an expression: a savepoint has no other
way to produce its value. Its type is that expression's, or the type the
binding names when the expression is a bare `Ok(...)` or `Err(...)`.

### 4. Read-only transactions

`transaction Database readOnly { }` begins a transaction that may not
write. It combines with the isolation words and with `retry`.

**The checker** reports **C8** where a read-only block calls a gateway
that uses its capability's `execute`, in its own body or in a private
helper it calls. `execute` is the write operation of ADR 0082's three: it
returns only the rows affected, so a `SELECT` sent through it reads
nothing back the program could use, and every `execute` in the corpus is
an `INSERT`, `UPDATE`, or `DELETE`.

**The database** catches the rest: a `queryOne` of
`INSERT ... RETURNING`, or a write in a gateway the checker doesn't
follow. On both databases a write inside a read-only transaction faults,
which fits: the program said it wouldn't write.

### 5. How each database does it

| | Postgres (`fiig.postgres`, and `libraries/sql` on Postgres) | SQLite (`libraries/sql`) |
|---|---|---|
| `readOnly` | `BEGIN READ ONLY`, with the level: `BEGIN ISOLATION LEVEL SERIALIZABLE READ ONLY` | `BEGIN`, then `PRAGMA query_only = ON` on the transaction's connection; `PRAGMA query_only = OFF` before the connection goes back to the pool |
| a write inside | fault, `25006` | fault, `SQLITE_READONLY` |
| `savepoint` | `SAVEPOINT fiig_savepoint_N`, `RELEASE SAVEPOINT`, `ROLLBACK TO SAVEPOINT` | the same statements |

The savepoint statements go through the transaction's own `execute`, as
ADR 0087 §3's already do, so a driver written in fiig needs nothing new:
both `SqlDriver` and `SqlConnectionDriver` carry them as ordinary
statements. `query_only` is a connection setting SQLite doesn't roll back,
which is why it is turned off before the connection is reused, and a
connection that can't be turned back is discarded.

### 6. No `DEFERRABLE`

`SERIALIZABLE READ ONLY DEFERRABLE` waits at `BEGIN` for a snapshot that
can't conflict, then runs with no risk of `40001`. It is for long reports,
and the wait is unbounded under write load: a block that takes seconds to
start for a reason the source doesn't show. `serializable readOnly` is
`SERIALIZABLE READ ONLY`, and `retry` covers the rare `40001` it can still
raise. A `deferrable` word is open question 3.

## Consequences

- Lexer: `savepoint` is reserved. Parser and AST: `TransactionStmt` gains
  `ReadOnly`; a new `SavepointExpr`; `TryExpr` gains `Savepoint`, set on
  every `?` in a savepoint's body outside a `concurrent` body.
- `fiig fmt` writes `readOnly` in §1's order and a savepoint as a block.
- Checker: C6, C7, C8; the savepoint's body must end with a `Result`.
- `TransactionalProvider.Begin` takes `TxOptions{Isolation, ReadOnly}`.
- Runtime: a savepoint runs through the active transaction's `execute`.

## Alternatives considered

- **Nested `transaction` blocks as savepoints** (Django, Rails, Diesel,
  sqlx). Rejected: `transaction` would then not always mean a real commit,
  and a reader couldn't tell which a block is without looking outward.
  C2's reason stands.
- **A savepoint whose `Err` continues outward** (Django and Spring without
  a catch). Without a `catch`, the `Err` leaves the transaction too and
  rolls everything back, so the savepoint saves nothing.
- **A savepoint handle** (`sp = Database.savepoint()`, then
  `Database.rollbackTo(sp)`), as JDBC does. Manual, and the release is
  forgotten on the path nobody tested.
- **Ok-wrapping the body's last value**, as Rust's `try` blocks do. Then
  `savepoint { importOrder(row) }` would be a `Result` inside a `Result`;
  fiig writes `Ok(...)`.
- **`readOnly` as a capability setting** (Spring's hint). It is what the
  block promises, so it belongs on the block.
- **Inferring `readOnly`** from a block with no `execute`. A program that
  says it reads only gets told when it doesn't; one that never said so
  shouldn't change behaviour when a write is added.

## Open questions

1. A `match` as a savepoint body's last statement, each arm producing the
   `Result`. The checker's producing rule ties an arm's value to the
   declaration's return type today.
2. The `Err` type of a savepoint ending in a bare `Ok(...)` with no type
   from context: the checker could take it from the body's `?` operands.
3. `deferrable`, for long serializable reports (§6).
4. Sending `readOnly` transactions to a standby (ADR 0084's pool).
5. ADR 0087 §3's savepoint for a handled conflict has the same problem in
   a `concurrent` body that C7 rules out for `savepoint`.
6. C8 follows gateways called by name. A capability group's gateway, or
   another package's, is caught by the database at run time.
