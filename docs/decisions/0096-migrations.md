# ADR 0096: Migrations

Status: Accepted (2026-11-20). Implemented 2026-11-19; see Implementation
notes.
Date: 2026-11-19
Related: ADR 0071 (startup refuses with every problem listed), ADR 0079
§6a (a package's own Database binding), ADR 0082 (the Database
capability), ADR 0084 (drivers in fiig), ADR 0088 (transactions), ADR
0091 (each test gets a fresh database), known-limitations.md ("No
migrations"), abs-recommendations GAPS G6.

## Context

A fiig program has no way to change its schema. The getting-started
example and abs-recommendations both create tables from a gateway run on
every start, with `CREATE TABLE IF NOT EXISTS`. That creates a table once
but can never change one: the app's next milestone adds a table, and the
one after it a column to a table that's already deployed (GAPS G6). Every
gateway test also starts by calling `createSchema()`.

### How others do it

| Tool | Migrations are | Applied by | Undo |
|---|---|---|---|
| Flyway | `V3__add_sync_state.sql` | CLI, or at startup (Spring Boot's default) | paid; most teams go forward only |
| golang-migrate, dbmate, Rust sqlx | numbered `.sql` files | CLI, or a library call at startup | optional `down` files |
| Rails, Ecto | code (`add_column :users, :email`) | `rails db:migrate` | `down` / reversible `change` |
| Django, EF Core | generated from the models | manage / CLI command | generated |
| Prisma, Atlas | a declared target schema, diffed into SQL | CLI | none |

All of them record what has run in a table (Flyway's
`flyway_schema_history`), and Flyway refuses to start when a migration
that already ran has been edited.

Generating migrations from models doesn't fit fiig: a model is a JSON
body, a row, and a value, never a table (ADR 0083). Migrations written as
fiig code would need a new declaration kind to say what SQL already says.

## Decision

### 1. A package's `migrations/` directory, in plain SQL

```
backend/
  migrations/
    0001_create_rating.sql
    0002_create_app_user.sql
    0003_add_sync_state.sql
```

A file is named `<number>_<name>.sql`: digits, an underscore, lowercase
letters, digits and underscores. Numbers are unique in a package and
needn't be contiguous. A file may hold several statements separated by
`;`. Any package may have migrations, so a library ships the tables it
needs (a sessions library, say).

A name that doesn't fit, or two files with one number, is an error at
`fiig check` and wherever the program starts.

### 2. Applied at start, under the startup checks

`fiig serve` and `fiig run` apply pending migrations after reading the
configuration and before serving or running, and refuse to start if one
fails. `-migrate=check` refuses to start if any are pending instead of
applying them, for a deployment that runs migrations as its own step.
`fiig migrate` applies them and exits, printing each one:

```
$ fiig migrate -caps caps.json -workspace .. .
applied abs.recommendations 0003_add_sync_state
```

A package's dependencies are migrated before it. A package with its own
Database binding (ADR 0079 §6a) is migrated on that binding; every other
package on the process-wide one.

### 3. A table records what ran

```
fiig_migrations(package, number, name, sha256, applied_at)
```

The key is `(package, number)`. Before applying anything, the recorded
rows are compared with the files, and any of these refuses to start, all
listed at once:

- **An applied file was edited:** its sha256 differs from the recorded
  one. Write a new migration instead.
- **An applied file is missing.**
- **A new file is older than an applied one:** its number is lower than
  the highest applied number in its package. Renumber it.

### 4. Each migration runs in its own transaction

The record row is inserted first, then the statements run, then the
transaction commits. Postgres and SQLite both roll back schema changes,
so a migration that fails leaves nothing behind and is retried on the
next start. Inserting the row first is also the lock: a second instance
starting at the same moment waits on the row's key and then finds the
migration applied.

### 5. Forward only

There are no `down` files. Undoing a migration is another migration. A
`down` file is rarely run until the day it's needed, which is the worst
day to find it broken.

### 6. Tests get the migrations

Each test's fresh in-memory database (ADR 0091) has its package's
migrations, and its dependencies', applied before the test runs. A test
no longer calls a `createSchema()` gateway.

### 7. One set of files for every database

A package's migrations are run as written on whichever database it's
bound to. SQL that differs between SQLite and Postgres is open question 1.

## Rationale

- **SQL is already how a schema is said.** Every migration tool that
  doesn't generate SQL ends up with SQL files or a DSL that maps onto
  them one to one.
- **At start, by default**, because most fiig deployments are one
  process, and a separate step is one more thing to forget. Teams that
  want a separate step get `-migrate=check` and `fiig migrate`.
- **Refuse on an edited migration**, as Flyway does: the database and
  the files must tell one story, and a deployment that quietly diverges
  is found much later.

## Alternatives considered

- **`fiig migrate` only, with startup refusing when migrations are
  pending.** Safer for many instances, but an extra step for the common
  case. Available through `-migrate=check`.
- **Down migrations.** See §5.
- **Migrations generated from models.** See Context.
- **A database lock (Postgres `pg_advisory_lock`).** Ties the runner to
  one database and to one pooled connection; §4's inserted row works on
  both databases.

## Consequences

- abs-recommendations moves its `createSchema` statements into
  `migrations/0001_*.sql` and drops the gateway and its calls (GAPS G6).
- The getting-started example does the same.
- With the schema in files, checking queries against tables becomes
  possible. That's its own ADR.

## Open questions

1. **SQL that differs between databases.** A per-database file
   (`0003_add_index.postgres.sql`) beside the shared one is the likely
   shape; it waits for a program that runs on both.
2. **Long-running migrations** (a backfill over millions of rows). Each
   migration is one transaction; a backfill may want batches. Waits for a
   case.
3. **A migration written in fiig**, for a data change SQL can't express.
   Waits for a case.

## Implementation notes

Built 2026-11-19.

- `compiler/migration` reads `migrations/` (§1's names and numbers) and
  splits a file into statements outside quotes, comments, and Postgres
  dollar quotes. The loader puts a dependency's migrations on its
  `types.Package`, and they count in the lockfile's hash.
- `tooling/migrate` checks and applies them through any Database
  provider's `execute`, `queryMany`, and `Begin`, so the Go SQL library
  and the fiig Postgres driver share one path. `fiig serve` and `fiig run`
  take `-migrate=apply|check`; `fiig migrate` (with `-check`) is the
  separate step. `fiig check` reports a misnamed root migration.
- `fiig test` applies the migrations to each test's fresh database, and
  once to a shared one given with `-caps`.
- `examples/getting-started` moved its `createSchema` gateway and `setup`
  workflow to `migrations/0001_create_ratings.sql`.
- **Dogfood.** On SQLite: first run applies, second is up to date,
  `-migrate=check` refuses a pending one, an edited one is refused, and a
  failing one leaves neither its tables nor its record
  (`cmd/fiig/migrate_test.go`). On Postgres 17 through `fiig.postgres`: a
  function with a dollar-quoted body migrated, and two `fiig migrate`
  processes started together on a 2-second migration applied it once,
  the second reporting "up to date". On a copy of abs-recommendations,
  `createSchema` became `0001_initial_schema.sql` and its 13 tests pass
  without calling it.
