# ADR 0087: Database Conflicts a Program Handles

Status: Accepted and implemented (2026-11-17; see Implementation notes)
Date: 2026-11-17
Related: ADR 0015 (implicit transaction binding), ADR 0025 (`libraries/sql`),
ADR 0057 and ADR 0073 §2 (a type taken from context), ADR 0082 (typed
`Database` calls; this is its open question 2), ADR 0084 §1 and §6
(`DatabaseError`, faults in programs), ADR 0085 §9 (`fiig.postgres`'s
errors), fiig-lang.md §3.6 (expected failures are typed).

## Context

Every `Database` failure is a fault today (ADR 0084 §6). Some of them are
expected outcomes a program has to decide on, and a fault can't be
decided on:

```fiig
activity registerCustomer(input: NewCustomer) -> Result<Customer, CustomerError> {
    validated = validateNewCustomer(input)?
    checkEmailUnique(validated.email)?     // SELECT: is it taken?
    return insertCustomer(validated)       // INSERT
}
```

Two requests with the same email both pass the SELECT and both insert;
the database's unique constraint is the only real guard, and when it
fires the program gets a 500, not `CustomerError.EmailAlreadyInUse`. The
same holds for a foreign key naming a row someone just deleted, and for a
serialization failure or deadlock, which the database expects the client
to retry.

fiig-lang.md §3.6 says expected failures are typed. Making every
`Database` call return a `Result` would type them, but would also change
all 31 call sites in the corpus, and most of them have no conflict to
decide on: a `SELECT` by key, a `CREATE TABLE IF NOT EXISTS`.

### How others separate them

| | Shape | A program detects a duplicate by |
|---|---|---|
| Go `database/sql`, pgx | every call returns `error` | `pgErr.Code == "23505"` |
| Rust sqlx | every call returns `Result` | `e.kind() == ErrorKind::UniqueViolation` |
| Diesel | every call returns `Result` | `DatabaseError(UniqueViolation, info)` |
| Spring | unchecked exceptions | `catch (DuplicateKeyException e)` |
| Prisma | thrown errors | `e.code == "P2002"` |
| Ecto | raises by default; a changeset declaring `unique_constraint(:email)` turns that violation into `{:error, changeset}` | opting in per constraint |

Every one of them sorts failures into the same three groups: integrity
conflicts a program decides on (unique, foreign key, not-null, check);
transient failures to retry (serialization, deadlock); and defects (bad
SQL, a lost connection, a missing permission).

## Decision

### 1. A call opts in by the type its context expects

A `Database` call whose context expects a `Result` with
`DatabaseConflict` as its error returns one; every other call is
unchanged and still faults:

```fiig
gateway insertCustomer(input: NewCustomer) -> Result<Customer, CustomerError>
    requires Database
{
    id = Random.uuid()
    inserted: Result<int, DatabaseConflict> = Database.execute(
        "INSERT INTO customers (id, name, email) VALUES (?, ?, ?)",
        id,
        input.name,
        input.email,
    )
    match inserted {
        Ok(_) -> Ok(Customer(id: id, name: input.name, email: input.email))
        Err(DatabaseConflict.Unique(constraint)) -> Err(CustomerError.EmailAlreadyInUse(input.email))
        Err(other) -> Err(CustomerError.Invalid("customer", "${other}"))
    }
}
```

The `match` must cover every variant (A9), so a program that cares about
one conflict still says what the others mean to it.

The expected types are `Result<int, DatabaseConflict>` for `execute`,
`Result<T | None, DatabaseConflict>` for `queryOne`, and
`Result<List<T>, DatabaseConflict>` for `queryMany` (an `INSERT ...
RETURNING` reads a row and can conflict), where `T` is the row type as
ADR 0082 §5 takes it. Context is the same as for a row type: a typed
binding, a declared return, an argument, or a field. A call with no such
context keeps today's result type, so no existing call site changes.

This is Ecto's choice, made per call rather than per constraint: the
program says where a conflict is a decision, and everywhere else a
conflict is still the defect it would be.

### 2. `DatabaseConflict`, in the prelude

```fiig
enum DatabaseConflict {
    Unique(constraint: string)
    ForeignKey(constraint: string)
    NotNull(column: string)
    Check(constraint: string)
    Retryable(reason: string)
}
```

`Retryable` is a serialization failure or a deadlock: the statement, or
the transaction it was in, may succeed if run again. (`Retry` would read
as the `retry` keyword.) Every other failure, bad SQL, a lost
connection, a refused permission, is still a fault, opted in or not: a
program has nothing to decide about a typo in its own SQL.

The names are the constraint's or column's own, as the database reports
them, so a program can tell `customers_email_key` from `customers_pkey`.

| | Postgres SQLSTATE | SQLite extended result code |
|---|---|---|
| `Unique` | `23505` | 2067 (UNIQUE), 1555 (PRIMARYKEY) |
| `ForeignKey` | `23503` | 787 |
| `NotNull` | `23502` | 1299 |
| `Check` | `23514` | 275 |
| `Retryable` | `40001`, `40P01` | 5 (BUSY), 6 (LOCKED) |

SQLite names no constraint in its error, only the columns (`UNIQUE
constraint failed: customers.email`), so on SQLite `constraint` is that
column list, `customers.email`. A program matching on the constraint's
name is therefore database-specific; matching on the variant is not.

### 3. Inside a transaction, a handled conflict leaves it usable

Postgres aborts the rest of a transaction after any error, so a conflict
a program handled would make the block's next statement fail. Inside
`transaction Database { }`, the runtime runs an opted-in statement
between `SAVEPOINT` and `RELEASE SAVEPOINT`, and on a conflict rolls back
to the savepoint before returning `Err`. The transaction continues as if
the statement hadn't run. Statements that don't opt in pay nothing.
SQLite has the same savepoints, so the behavior is the same on both.

### 4. Providers report what the database said

Classifying is the runtime's, from what a provider reports:

- `fiig.sql`'s `DatabaseError.Server` gains `constraint`, `table`, and
  `column`, each `string | None`, which `fiig.postgres` fills from
  ErrorResponse's `n`, `t`, and `c` fields; drivers written in fiig report
  the server's own code and the runtime maps it by §2's table.
- `libraries/sql` reports SQLite's extended result code and message, from
  which the runtime takes §2's variant and the column list.

### 5. Commit-time conflicts stay faults

A serialization failure can arrive at `COMMIT`, and a deferred constraint
is checked there. A `transaction` block has no result of its own a
program could match on, so a conflict at `COMMIT` is a fault, as today.
Retrying a whole transaction is open question 1.

## Consequences

- The checker recognizes §1's expected types on `Database` calls and
  records the opt-in on the call, as it records the row type; the prelude
  gains `DatabaseConflict`.
- The runtime classifies a provider's error into `DatabaseConflict` for
  an opted-in call, and adds savepoints inside transactions.
- `fiig.sql` and `fiig.postgres` report the constraint, table, and
  column; `libraries/sql` reports SQLite's extended code.
- Corpus: `customer-service`'s `registerCustomer` drops the racy
  `checkEmailUnique` SELECT and handles `Unique` from the insert;
  `checkEmailUnique` goes.
- `known-limitations.md` drops "`Database` calls have no error channel".

## Alternatives considered

- **Every call returns `Result<T, DatabaseError>`** (Go, sqlx, Diesel).
  Honest about every failure, but most calls have nothing to decide, and
  `?` would have to carry a database error through gateway signatures
  that today promise only domain errors.
- **Exceptions caught by type** (Spring). fiig has no exceptions (§3.6).
- **Declaring constraints on a model** (Ecto's changeset). ADR 0083 keeps
  models free of business logic, and a constraint is the database's, not
  the model's.
- **A separate operation** (`Database.tryExecute`). Doubles the operation
  table for one difference that the result type already says.

## Open questions

1. *(Answered by ADR 0088: `retry(attempts: N)` on the block.)*
   **Retrying a whole transaction** after `Retryable`, or after a
   serialization failure at `COMMIT`: fiig's `retry` applies to an
   idempotent gateway (ADR 0014), not to a `transaction` block.
2. **Exclusion constraints** (`23P01`) and other integrity classes, which
   fall to a fault until a program needs them.

## Implementation notes (2026-11-17)

- **Checker** (`compiler/checker/database.go`): `checkExpectedQuery` sees
  `Result<X, DatabaseConflict>` for any of the three operations, checks X
  as the call's plain result, and sets the new `CallExpr.HandlesConflicts`.
- **Runtime:** `Fault.Conflict` carries a classified conflict
  (`compiler/runtime/conflict.go`: `SQLStateConflict`, `SQLiteConflict`).
  `evalHandlingConflicts` runs an opted-in call, reads its rows, and turns
  a fault with a conflict into `Err`; inside a transaction it brackets the
  call with `SAVEPOINT fiig_conflict_<n>` and `RELEASE`, with `ROLLBACK
  TO` on a conflict. The driver's pool classifies a `Server` error's
  SQLSTATE; `libraries/sql` classifies SQLite's extended code.
- **The relay test server** now reports SQLite conflicts with Postgres
  SQLSTATEs, as drivers report the server's code (§4).
- **Corpus:** `insertCustomer` handles `Unique`; `checkEmailUnique` and its
  SELECT are gone; `compiler/runtime`'s fake database reports a taken
  email as the conflict.
- **Tests:** the checker's opt-in forms and the recorded flag; §2's code
  table for both databases; `fiig.postgres` reading `n` from an
  ErrorResponse; against real SQLite, each conflict kind, `INSERT ...
  RETURNING`, a call that didn't opt in still faulting, and a transaction
  committing the rows around a handled conflict.
- **Dogfooded on Postgres 17:** `Unique(people_pk)`, `NotNull(name)`,
  `Check(age_ok)`, `ForeignKey(people_team_fk)`, each by its own name, and
  a transaction inserting `a, a, p1, b` that handled two conflicts and
  committed the other two, which without the savepoint Postgres would have
  aborted.
