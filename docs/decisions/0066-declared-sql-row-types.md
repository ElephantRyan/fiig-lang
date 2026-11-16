# ADR 0066: Declared Row Types for SQL Results

Status: Superseded by ADR 0082 (2026-11-16), which answers every question
below. Proposed 2026-11-03, split out of ADR 0064 step 3 at the project
owner's direction, and never accepted.
Date: 2026-11-03
Related: ADR 0025 (`libraries/sql`), ADR 0027 (declarative capabilities),
ADR 0043 (capabilities written in fiig), ADR 0048 §2.3 (non-finite floats at
the boundary), ADR 0057 (`[]` typed by context), ADR 0062 (JSON numbers),
ADR 0064 §8 (decimal as TEXT).

## Context

`libraries/sql` reads a row into an anonymous record (`scan.go`). Each
column becomes whatever the driver handed back: SQLite's storage class
decides the fiig type, and the checker sees capability results as Unknown.
Two consequences:

- A decimal is written as TEXT (ADR 0064 §8) but reads back as a `string`,
  since nothing says which text is a decimal. A program converts it with
  `toDecimalOrNone()`, and a NULL column and unparseable text both come out
  as `None`.
- Nothing checks `row.weight`'s type, for any column type. A wrong
  assumption fails wherever the value is first used, not at the query.

SQLite makes this worse in two ways. A column declared `DECIMAL(10,2)` has
NUMERIC affinity, so `'19.99'` is stored as a REAL before any reader sees
it. And storage class is per value, not per column, so one column can hold
TEXT in one row and REAL in the next.

## How other languages place the type

1. **The database reports it.** psycopg and Postgrex map `NUMERIC` to a
   decimal by column type. It works only where the database has the type;
   SQLite doesn't.
2. **The read site names it.** JDBC's `getBigDecimal`, Go's `Scan` into a
   `Scanner` destination, rusqlite's `get::<_, Decimal>`.
   `toDecimalOrNone()` is this, written by hand.
3. **A declared row type the library fills.** Ecto schemas, sqlx's
   `FromRow` (its macros also check the query against a live schema at
   compile time), Dapper, Python `sqlite3`'s `PARSE_DECLTYPES` converters.

## Proposal (draft)

A query may name a model, and the runtime reads each column into that
field's type, faulting at the query with the column's name when a value
doesn't fit. Anonymous rows stay, so the change is additive.

## Questions to decide

1. **How the call site names the type.** Capability calls are Unknown at
   check time and fiig has no generics. The likely form is the expected type
   from context, `row: Shipment | None = Database.queryOne(...)`, as `[]` is
   typed by context (ADR 0057, A12), but a call's result depending on context
   is a larger step than a literal's.
2. **Shape mismatches.** Extra columns, missing columns, and whether names
   must match field names exactly.
3. **NULL.** Allowed only into a `T | None` field?
4. **REAL into a decimal field.** Rejecting keeps every decimal from SQL
   exact text and exposes the affinity trap at the first read, and can be
   loosened later without breaking anything; the escape is a `float` field
   and `math.toDecimal`. Accepting (as rust_decimal's SQLite reader does)
   works with existing databases but carries float error into a type that
   promises exactness, loses scale (`19.90` reads as `19.9`), and can't be
   taken back. Leaning: reject.
5. **Mixed storage classes** in one column, which question 4's escape
   doesn't cover unless a `float` field also accepts numeric TEXT.
6. **Check time.** Whether the checker can know a query's columns at all
   (sqlx needs a live database at compile time), or the check stays at run
   time.

## Consequences

- One model per result shape, including every join and projection.
- `libraries/sql`'s scan takes a target type; ADR 0064's `toDecimalOrNone()`
  reads become unnecessary where a model is declared.
- The fiig-written Postgres provider (Milestone 7) inherits the same rule.
