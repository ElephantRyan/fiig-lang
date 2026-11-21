# ADR 0125: A Schedule Lock in the Program's SQL Database

Status: Proposed
Date: 2026-11-21
Related: ADR 0112 §4 (`ScheduleLock`), ADR 0124 §6 (`once`, `complete`,
`completed`), ADR 0085 (`fiig.postgres`), ADR 0082 (`Database`).

## Context

`fiig.redis` is the only lock a program can bind to `ScheduleLock`. A
program with a SQL database and no Redis has to add a service to run a
schedule on more than one replica, or to run a startup route `once`.

## Decision

A new package, `fiig.sqllock`, provides `SqlScheduleLock`, bound as

```json
{"ScheduleLock": {"type": "lock", "lock": "fiig.sqllock"}}
```

It keeps claims in the program's own `Database`, in one table:

```sql
fiig_locks (lock_key TEXT PRIMARY KEY, holder TEXT NOT NULL, expires_at BIGINT NOT NULL)
```

- **`claim`** is one statement: `INSERT ... ON CONFLICT (lock_key) DO UPDATE
  ... WHERE expires_at <= now`. It inserts the row, or takes over a row that
  has expired, and a row count of 1 means this caller holds the claim. That is
  `SET NX PX` in SQL, atomic in one statement, and it runs unchanged on
  Postgres and SQLite.
- **`complete`** upserts `<claim key>:done`, expiring after `retainMillis`.
- **`completed`** asks for an unexpired `:done` row.
- **The table is made on first use** (`CREATE TABLE IF NOT EXISTS`) by each
  gateway, so a program has nothing to migrate, and `fiig serve -check`, which
  applies no migration, can still ask.
- **Expiry uses each replica's clock**, as a schedule's firing times already
  do (ADR 0112 §1). A replica whose clock runs late sees a claim as held a
  little longer, and one that runs early takes it over a little sooner.

### Not an advisory lock

Postgres advisory locks belong to a connection and vanish when it closes. A
claim has to last a set time and outlive its winner as a `:done` record, and
the runtime pools connections, so a lock held by one of them can be released
or handed to another call. A row has no such coupling.

## Consequences

- `fiig.sqllock` depends on `fiig.schedule` and holds `SqlScheduleLock` and
  `LockRow`. Its gateways `require Database`, so it declares `Database` in
  its manifest's `capabilities` and `nativeCapabilities`, and a program that
  depends on it must too, as a program that uses `Database` already does.
- **It is a package of its own, not part of `fiig.sql`.** I first put it in
  `fiig.sql`; every package that depends on that (`fiig.postgres`, the
  drivers) then inherited the `Database` requirement and failed the
  capability check (ADR 0042), since a driver runs *under* `Database`.
- A fault from the database (not reachable, a permission refused) is a fault
  of the lock: a startup route refuses to start, as for a lock that is
  `Unavailable`. The database has no error channel yet (ADR 0048 §2.3), so it
  can't be reported as `LockError.Unavailable`.
- Rows aren't deleted. A row is a few dozen bytes for each firing or deploy,
  and a lapsed one is overwritten only if its key comes round again. A
  schedule that fires every minute adds 1,440 rows a day; pruning is open
  question 1.
- **The first deploy races:** on Postgres, two replicas that run
  `CREATE TABLE IF NOT EXISTS` for the first time at the same moment can both
  try to create it, and one gets a duplicate-key error. That replica refuses
  to start, and restarts into a table that exists. Only the first use ever
  does this.

## Open questions

1. **Pruning** expired rows, perhaps `DELETE ... WHERE expires_at < now` on
   each claim.
2. **MySQL** has no `ON CONFLICT`; it needs `ON DUPLICATE KEY UPDATE`, and
   no fiig driver for it exists yet.

## Implementation notes (2026-11-21)

- `compiler/stdlib/sqllock/src/SqlScheduleLock.fiig`.
- Tests: `libraries/sql/sqllock_test.go`, on SQLite. Two replicas of one deploy
  run a `once` route once and a new deploy runs it again; a held claim loses
  and a lapsed one is taken over. Reversing the expiry comparison in `claim`
  failed the second.
- **Dogfooded on Postgres 17** through the `fiig.postgres` driver: one replica
  ran a `once` seed and wrote the two rows, and a second replica of the same
  code logged "another replica has run it for this deploy" and started
  without running it.
