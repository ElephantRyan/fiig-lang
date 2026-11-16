# ADR 0025: SQL Capability Contract, and Shipped Libraries as Separate Modules

Status: Accepted. Amended 2026-11-04: `Database.nextId()` is removed.
ADR 0068 made `Random.uuid()` the way to draw an ID, which a seeded
`Random` makes repeatable in tests and `fiig catalog` shows as a use of
`Random`; `nextId` was a UUID made in Go behind `Database`, hiding both.
No corpus program called it after `insertCustomer` moved to
`Random.uuid()`. Amended 2026-11-16 by ADR 0082: `queryOne` and `queryMany`
read their rows into a row type the call's context gives (`T | None`,
`List<T>`) instead of an anonymous record, and the SQL text must be a
string literal.
Date: 2026-10-07

> **Note (2026-10-17):** ADR 0055 removed `value` declarations. References to `value` types below are historical.

## Context

Milestone 5 (`fiig-lang.md` §5) lists "read and transactional database
capability contracts" and "one reference database provider" as its own
deliverables. Before this, `Database.queryOne(...)`/`execute(...)`/
`nextId()` existed only as placeholder text the Milestone 0 example authors
wrote before any capability system existed — never a designed contract,
and `reserve_inventory.fiig`'s `if not reserved` quietly assumed `execute`
returns a boolean, which isn't real SQL semantics.

Two questions needed answers before writing any implementation:

1. What does a program actually call, and what does it get back?
2. Where does the real implementation live, given fiig-lang.md §4 already
   says privileged capability providers may use host libraries, but this
   environment has no C compiler (ruling out cgo-based drivers like
   `mattn/go-sqlite3`), and the project has had zero third-party
   dependencies until now?

## Decision

**The contract** (implemented by `libraries/sql`, wrapping Go's
`database/sql` generically):

| Call | Returns |
|---|---|
| `Database.queryOne(sql, args...)` | an anonymous record, or `None` if no row matched |
| `Database.queryMany(sql, args...)` | a `List` of anonymous records |
| `Database.execute(sql, args...)` | an `int` — rows affected |
| `Database.nextId()` | a `string` — a locally generated UUID |

A result column decodes to the obvious fiig `Value` (SQL integer → `Int`,
text → `Str`, `NULL` → `None`); a floating-point column is a Fault, not a
lossy truncation — fiig has no float type at all (ADR 0019). *(Corrected by
ADR 0048: ADR 0019 never decided against float, and since 2026-11-02 a
floating-point column decodes to `float`. A non-finite one, such as
Postgres's `'NaN'`, still Faults, because these calls have no error channel
yet.)* `nextId`
generates its own UUID rather than asking the database for one (no
auto-increment column, no driver-specific `RETURNING`/`LAST_INSERT_ID()` to
abstract over) because fiig IDs are already modeled as opaque strings
(`value CustomerId: string`), not auto-increment integers.

**The library boundary**: `libraries/sql` is its own Go module (own
`go.mod`, joined to the root via `go.work` and a `replace` directive), not
part of the `fiig-lang` module compiler/parser/checker live in. It depends
on nothing but `database/sql` (standard library); only its own tests need
a real driver to run against (a pure-Go SQLite driver, `modernc.org/sqlite`
— no cgo), and that dependency is isolated to this module's own `go.mod`,
never the core's.

## Rationale

Surveyed how other general-purpose languages handle "SQL support" before
deciding, rather than guessing: Java's JDBC, Python's DB-API (PEP 249),
Go's own `database/sql`, and C#'s ADO.NET all converge on the same shape —
a generic, engine-agnostic interface, with the actual database chosen by
whoever deploys the application. PHP is the cautionary tale in the other
direction: it originally shipped `mysql_query()` and friends hard-wired to
one specific database, and later replaced that with PDO (a generic,
driver-based abstraction) once the mistake became clear. COBOL's `EXEC SQL`
is the one real "truly built into the compiler" precedent, and it's a
legacy pattern nobody repeats, for the same reason. Given that convergence,
embedding a specific engine into fiig's core — or even into `libraries/sql`
itself — would be choosing the one approach the industry already tried and
walked back from.

Separating `libraries/sql` into its own module follows directly from
keeping the core dependency-free: without separation, this library's own
*test* dependency (a real driver, needed to prove it works against
something real) would land in the one shared `go.mod`, and so would every
future library's test dependencies. Module separation means someone
building only the compiler never fetches any of it.

Designing the contract now (rather than keeping the placeholder names)
matters because it's the thing a fiig program actually calls — freezing an
unexamined accident into the language surface would be strictly worse than
deciding it deliberately, especially since Milestone 5 explicitly scopes
this as a deliverable.

## Consequences

- `libraries/sql/` — new Go module (`go.mod`, `provider.go`, `scan.go`,
  `uuid.go`, tests). `go.work` at the repo root ties it to the core module
  for local development without requiring a published version of either.
- `compiler/runtime`: `CapabilityProvider.Call` gained a `context.Context`
  parameter (caught while building this — the old signature gave a real
  I/O-bound provider no way to honor a deadline mid-call, only between
  calls). Repo-wide mechanical fixup across every test fake.
- `examples/order-processing/steps/reserve_inventory.fiig`: `if not
  reserved` → `if reserved == 0`, matching `execute`'s real rows-affected
  contract instead of the old boolean assumption.
- `libraries/sql` can't be `use`d from `.fiig` source yet — no package/
  import resolution exists until Milestone 6. It's wired up at the Go
  level (`caps.Register("Database", sql.New(db))`) by whoever assembles
  the `Interp`, same as any simulated/test provider, just backed by a real
  database this time.
- `compiler/runtime/transaction.go` (`Tx`, `TransactionalProvider`,
  `evalTransaction`) is this ADR's other half: real commit/rollback
  semantics, implemented and tested the same session as a prerequisite for
  `libraries/sql`'s transactions to mean anything.
