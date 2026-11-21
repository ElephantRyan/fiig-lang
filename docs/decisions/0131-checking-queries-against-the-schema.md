# ADR 0131: Checking Queries Against the Schema, `fiig check -schema`

Status: Proposed
Date: 2026-11-21
Answers: ADR 0082 open question 3 (in part); ADR 0096's "checking queries
against tables becomes possible. That's its own ADR."
Related: ADR 0082 (checked `Database`, Q1-Q3), ADR 0096 (migrations), ADR 0091
§5 (a test's in-memory database).

## Context

A query's SQL is a literal (Q1), and its rows are read into the row type the
context gives (ADR 0082), but whether the SQL names a table and columns that
exist was found at the query, as a fault, in front of a user: `fiig check`
passed a program with `SELECT usernme FROM session`. ADR 0082 §9 kept the check
at run time because it needed the schema, and ruled out reading a live
database at check time.

ADR 0096 changed what is known: the schema is in files. A program's
migrations, plain SQL in `migrations/`, build it, and `fiig test` already
applies them to a fresh in-memory SQLite database for each test.

### How others handle it

| Tool | Where the schema comes from | When the query is checked |
| --- | --- | --- |
| sqlc | the schema's DDL files | at generate time |
| Rust sqlx macros | a live database, or an offline cache of it | at compile time |
| jOOQ | a live database, to generate classes | at compile time |
| Diesel, Prisma | a declared schema | at compile time |
| Go `database/sql` | none | at the first run |

fiig's migrations are the sqlc shape: the DDL is in files, and nothing needs a
running server.

## Decision

### `fiig check -schema`

```
fiig check -schema .
```

After the ordinary check passes, `fiig check -schema`:

1. applies the program's migrations, and its dependencies', to a new
   in-memory SQLite database, as `fiig test` does;
2. prepares every `Database.queryOne`, `queryMany` and `execute` whose SQL is a
   literal, in a gateway, a capability group's gateway, or a private helper;
3. reports **Q4** for each one the schema doesn't fit:

```
src/sessions/FindSession.fiig:10:9: [Q4] Database.queryOne's query doesn't fit the schema the migrations make: SQL logic error: no such column: usernme (1) (ADR 0131)
```

It is a flag and not the default, since it needs a database engine, and the
ordinary check stays instant and has none.

### What it reports, and what it doesn't

- **Reported:** a table that doesn't exist, a column that doesn't, a
  column named by two tables (`no such table`, `no such column`, `has no column
  named`, `ambiguous column name`).
- **Counted, not reported:** any other refusal. SQL that SQLite can't prepare
  (Postgres's `ANY(...)`, `FOR UPDATE SKIP LOCKED`, `RETURNING` in places) is
  indistinguishable from a typo by its error, so the check says "N of M queries
  use SQL that SQLite can't prepare and were not checked" and exits 0.
- **Not checked, with a note:** a program with **no migrations** (it makes its
  tables in a gateway, so every query would look wrong), and a program whose
  **migrations SQLite can't run** (Postgres-only DDL).
- **Not compared:** a query's result columns with the row type. A column's
  name is matched to a field ignoring case and underscores (ADR 0094), and a
  mismatch is still a fault at the query. Prepared statements don't give a
  column list through `database/sql`.
- Only the program's own queries; a dependency's tables are its own business,
  as the package that made them (`fiig.sqllock` makes its table on first use).

### The Postgres question

A program that runs on Postgres and uses its dialect throughout gets a note and
little else. That is the honest answer for a tool with no Postgres to ask: a
check against a real server would be a second, heavier mode, `-caps` pointing
at a throwaway database, and is left open.

## Alternatives considered

- **Parse the SQL ourselves.** A SQL parser per dialect is large, and each
  engine's own `PREPARE` is the oracle for what it accepts.
- **A live database in `fiig check`.** What sqlx does. It makes check depend on
  a server, which ADR 0082 §9 refused.
- **Declared tables in fiig** (ADR 0082 open question 3). The schema would be
  written twice, in fiig and in a migration.
- **Always on.** It would slow the check and add a database engine to every
  check, including the editor's.

## Consequences

- `tooling/schemacheck` holds the query extraction and the check;
  `libraries/sql`'s `Provider` gains `Validate` (a prepare) and `Close`;
  `checkcmd.Main` takes the capability factories and `fiig check` passes the
  `sql` one.
- A check that fails to find a query's schema problem for want of an engine
  says so in a note on stderr, so a clean run isn't mistaken for a checked one.

## Open questions

1. **A real Postgres**, through `-caps`, for a program in its dialect.
2. **Comparing result columns with the row type**, which needs the statement's
   column list from the driver.
3. **The editor:** the language server could run it on save for a program with
   migrations. It is slower than the checker and is left to the user's command.

## Implementation notes (2026-11-21)

- Tests: `tooling/schemacheck` (which calls are queries, which prepare errors
  count) and `libraries/sql`'s `schemacheck_test.go`, against real SQLite: a
  misspelt column and table are found and the good queries aren't; Postgres-only
  SQL is counted; no migrations and unrunnable migrations are notes.
- It found a bug in its own first version: every check in a process shared one
  in-memory database name, so a second check saw the first's tables. Each now
  has a database of its own, closed after.
- **Dogfooded on abs-recommendations' backend** (four migrations, 19
  queries): clean, as it should be. With a column and a table misspelt in a
  copy, `fiig check` still passed and `fiig check -schema` reported both,
  by file and line.
