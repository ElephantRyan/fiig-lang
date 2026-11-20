# ADR 0082: A Checked Database Capability

Status: Accepted and implemented (2026-11-16; see Implementation notes)
Date: 2026-11-16
Supersedes: ADR 0066 (declared SQL row types, Proposed, never accepted);
its questions are answered in §5-§8.
Related: ADR 0010 (one capability per gateway), ADR 0025 (the `Database`
contract and `libraries/sql`), ADR 0042 (the primitive floor and the
`nativeCapabilities` grandfather list), ADR 0043 §5 (P7), ADR 0057 (A12),
ADR 0060 §3 (a primitive's typed operation table), ADR 0064 §8 (decimal
as TEXT), ADR 0068 §4 (B10), ADR 0073 §2 (`json.decode` typed by
context), ADR 0080 (an enum's text form), charter principle 10. Alpha
items 9 and 10.

## Context

`Database` is the most used capability in the corpus (21 gateways) and
the least checked. This passes `fiig check` with no diagnostic:

```fiig
rule leaky(id: string) -> int {
    return Database.execute("DELETE FROM t WHERE id = ?", id)
}

gateway wrongCap(id: string) -> int
    requires Socket
{
    n = Database.execute("DELETE FROM t")
    row = Database.queryOne("SELECT 1 AS x")
    s: string = row.x.nonsense
    Database.exceute("typo")
    return n
}
```

A rule does I/O, a gateway uses a capability it doesn't require, a
misspelled operation and a field that can't exist are all accepted. The
cause is one branch in the checker: a receiver that is not a primitive,
a declaration, an import, or a local is "an opaque capability call"; its
arguments are inferred and its result is Unknown, and none of B10, P7, or
K1 is consulted. `EmailSender`, `PaymentGateway`, and `ShippingProvider`
(customer-service, order-processing) take the same branch.

The results are no better at run time. `libraries/sql` reads a row into
an anonymous record whose column types are whatever the driver handed
back (ADR 0066's context): a decimal written as TEXT comes back a
`string`, a SQLite `DECIMAL(10,2)` column has already turned `'19.99'`
into a REAL, and a wrong assumption about a column fails wherever the
value is first used, not at the query.

### What the corpus does

| Operation | Calls | Arguments | What the result becomes |
|---|---|---|---|
| `execute` | 14 | `string`, `int`, `decimal` | an `int`, or ignored |
| `queryOne` | 5 | `string` | matched on `None`, then built into `Account`, `Customer`, `Rating` field by field; once only tested for `None` |
| `queryMany` | 1 | none | looped over, each row built into `Account` |

Every SQL text is a literal. Every column but one (`key_hash`) has the
name of the model field it fills.

### How other libraries place a row's type

| System | The row type comes from | Checked |
|---|---|---|
| Go `database/sql` | the read site: `Scan(&a, &b)`, by position | run time |
| jmoiron/sqlx | a struct the caller passes; a column with no field is an error ("missing destination name") | run time |
| sqlc | generated from `.sql` queries and schema DDL files | generate time, against the files |
| jOOQ | generated from a live schema; a projection is `Record2<String, Integer>` | compile time |
| Diesel | the `table!` schema; raw `sql_query` needs a `QueryableByName` struct | DSL at compile time, raw SQL at run time |
| Ecto | a declared schema; raw `Repo.query` is untyped until `Repo.load(Schema, ...)` | raw SQL at run time |
| Prisma | `schema.prisma` generates the client; `$queryRaw<T>` names `T` unchecked | client at compile time |
| Rust sqlx | `query_as!` against a live database or an offline cache | compile time |

Two families. Where the compiler knows the schema (sqlc, jOOQ, Diesel,
Prisma, the sqlx macros), it needs a schema file or a live database.
Where it doesn't, the caller declares the row's shape and the library
fills it at run time, and every system in the first family keeps that
form too for raw SQL. Fiig's queries are raw SQL, so this ADR takes the
second family; the first would mean tables declared in fiig (ADR 0079
open question 3) and is a later step (§9).

## Decision

### 1. `Database`'s operations come from a table, like `Socket`'s

`Database` stays a native capability on ADR 0042's grandfather list,
not a primitive, but its operations join `types.PrimitiveOps`' kind of
table (ADR 0060 §3) under a name the checker consults when the receiver
`Database` resolves to no declaration:

| Operation | Parameters | Result |
|---|---|---|
| `execute` | `sql`, then the values to bind (§4) | `int`, rows affected |
| `queryOne` | `sql`, then the values to bind | `T \| None`, `T` from context (§5) |
| `queryMany` | `sql`, then the values to bind | `List<T>`, `T` from context |

An operation not in the table is K1. It is a table in Go and not a
`capability` group written in fiig because a query's result type
depends on the call, and fiig has no generics a group could declare it
with. A package that declares its own group named `Database` gets its
group, as today; the table is only what the name means when nothing
else does.

### 2. Every native capability is held to its gateway's `requires`

A call on a receiver that resolves to nothing, a native capability like
`Database` or `EmailSender`, follows the rule a primitive already does
(ADR 0060, ADR 0043 §5):

- outside a gateway it is **B10**: a rule, conversion, formula,
  activity, or workflow may not touch a capability;
- in a gateway that requires something else it is **P7**.

This needs no table, so it covers `EmailSender`, `PaymentGateway`, and
`ShippingProvider` too; only `Database`'s calls are also typed. Whether
the name is allowed at all stays the loader's job (H2, ADR 0042), since
only the manifest knows the grandfather list. `transaction Database`
blocks are unchanged: a direct call inside one in an activity was
already outside a gateway, and is now B10.

### 3. Q1: the SQL text is a literal, with no interpolation

The `sql` argument must be a string literal with no `${...}`. Values
reach the database only as bound parameters, so a query built from a
value is refused at check time rather than reviewed for injection:

```fiig
Database.execute("DELETE FROM sessions WHERE token_hash = ${hash}")   // Q1
Database.execute("DELETE FROM sessions WHERE token_hash = ?", hash)   // fine
```

Precedent: Rust sqlx's macros take only a literal; Google's
`@CompileTimeConstant` (Java) and `safesql` (Go) admit only query text
built from constants; Prisma's `$queryRaw` turns a template's
interpolations into parameters and keeps string-built SQL behind a
method named `$queryRawUnsafe`. Prisma's form, `${x}` in a SQL string
binding a parameter, was considered and rejected: the same literal would
mean one thing passed to `Database` and another anywhere else.

What the rule costs is SQL whose shape depends on data: a table or sort
column chosen at run time, and an `IN` list of varying length. The last
has a parameter form in both target databases (SQLite `json_each(?)`,
Postgres `= ANY($1)`); the others have none in the alpha (open question
1). Placeholders (`?`, `$1`) are the driver's, so the checker does not
count them against the arguments.

### 4. Q2: what may be bound

After `sql`, every argument is a value to bind, by position; they can't
be named, since SQL placeholders aren't. A bound value's type is one of:

| fiig type | Binds as |
|---|---|
| `int`, `float`, `string`, `bool` | the driver's integer, real, text, boolean |
| `decimal` | TEXT in plain notation at its scale (ADR 0064 §8) |
| `bytes` | BLOB (new; `libraries/sql` refuses it today) |
| a payload-free enum | its text form (ADR 0080) |
| `None`, or `T \| None` of the above | NULL when `None` |

Anything else is Q2: a model, list, map, payload enum, `Connection`,
and `Secret` (S1's reasoning: a gateway that means to store one calls
`.reveal()`, which says so).

### 5. A query's row type comes from context

`queryOne`'s `T` and `queryMany`'s come from what context expects, as
`json.decode`'s does (ADR 0073 §2): a typed local, a declared return
type, a parameter, or a field. With none it is A12, fixed the same way,
with a typed declaration:

```fiig
gateway findRating(item: string) -> Result<Rating, RatingError>
    requires Database
    idempotent
{
    row: Rating | None = Database.queryOne("SELECT item, score, note FROM ratings WHERE item = ?", item)
    match row {
        found -> Ok(found)
        None -> Err(RatingError.NotFound(item))
    }
}
```

`T` is either a model, whose fields the columns fill (§6), or one
bindable type from §4's table, for a query that returns one column
(`SELECT COUNT(*)`, `SELECT id`), as `Scan` into one variable and sqlc's
single-column queries do. A model's fields must themselves be §4's
types, through embedding (ADR 0045); a nested model, list, map, or other
union is Q3, since one column holds one value.

### 6. Columns are matched to fields by exact name, and SQL renames

*Amended by ADR 0094: a column matches a field ignoring case and
underscores, so `user_id` fills `userId` with no `AS`, and Postgres's
folded aliases match. The checks both ways below still apply.*

Each field reads the column with its name, compared exactly. A column
named differently is renamed in the query, with `AS`:

```fiig
row: StoredAccount | None = Database.queryOne(
    "SELECT name, email, key_hash AS keyHash FROM accounts WHERE name = ?",
    credentials.name,
)
```

ADR 0080 gave JSON `from json "past_due"` because a JSON producer is
somebody else and its names aren't the program's to choose. A query is
the program's own text, and SQL already has a way to name a column, so
no `from sql` is added.

The match is strict both ways, because the query is something the
program says, not something it receives (principle 10): a field with no
column, a column with no field, and two columns of one name (a join's
`a.id, b.id`) fault at the query, naming them. jmoiron/sqlx refuses an
unmatched column the same way. One consequence: `SELECT *` breaks when
a column is added, which is what it should do in a program that
promises to read every column it asks for.

For a one-column `T` (§5), the column's name doesn't matter, and a
second column faults.

### 7. Each value is read by its field's type

The runtime reads every value against the type its field declares, so
a column whose storage class changes from row to row (SQLite) is read
row by row by the same rule, and faults at the row that doesn't fit:

| Field type | Accepts | Refuses |
|---|---|---|
| `int` | INTEGER | REAL, TEXT |
| `float` | REAL; INTEGER when exact in binary64 | TEXT; a non-finite REAL (ADR 0048 §2.3) |
| `decimal` | TEXT in the number grammar (ADR 0067 §7), keeping its scale; INTEGER | REAL |
| `string` | TEXT in valid UTF-8 | INTEGER, REAL, BLOB |
| `bytes` | BLOB; TEXT as its UTF-8 bytes | INTEGER, REAL |
| `bool` | a driver boolean; INTEGER 0 or 1 | any other integer, TEXT |
| a payload-free enum | TEXT equal to a variant's text form | anything else; the fault lists the spellings |
| `T \| None` | NULL as `None`, otherwise as `T` | |

NULL into a field that isn't `T | None` faults. REAL into `decimal` is
refused, as ADR 0066 leaned: it keeps every decimal from SQL exact text,
it surfaces SQLite's NUMERIC-affinity trap at the first read of a
`DECIMAL(10,2)` column instead of as a wrong cent later, and accepting it
could be added without breaking anyone, where refusing it later could
not. A program that inherits such a column reads a `float` field and
converts with `math.toDecimal`.

The stored data is the program's own, so nothing here is lenient the
way ADR 0062's JSON numbers are: `3.0` in JSON reads as an `int`, and a
REAL `3.0` in a row does not.

### 8. A mismatch is a fault at the query

Database calls have no error channel, so every refusal in §6 and §7 is
a fault raised by the call, naming the operation, the column, the field
and its type, and the value's storage class: `queryOne: column "score"
holds REAL 4.5, but Rating.score is int`. The scripted test provider
(`fiig run -caps`, `tooling/runprogram`) reads its canned rows through
the same reader, so a test row of the wrong shape fails the same way.

The reader lives in `compiler/runtime`, not `libraries/sql`: a provider
hands back column names and raw values, and the runtime fills `T`. One
reader serves the Go provider, the scripted one, and the Postgres
provider Milestone 7 writes in fiig, as one JSON decoder serves the
adapter and `json.decode` (ADR 0073 §3).

### 9. The check stays at run time

The checker knows `T` and checks that SQL can fill it (Q3); it does not
know what a query returns, since that needs the schema. Rust sqlx gets
it from a live database at compile time, which would make `fiig check`
depend on a running database, and sqlc, jOOQ, Diesel, and Prisma get it
from a schema the tool reads. The second is the shape a later ADR on
declared tables would take (ADR 0079 open question 3); this ADR doesn't
foreclose it, since a model already names every column a query reads.

## Consequences

- **Corpus migration.** The five `queryOne` and one `queryMany` sites
  gain a typed declaration; four simplify to `found -> Ok(found)`.
  `findAccountByKey` gets a `StoredAccount` model and `key_hash AS
  keyHash`; `checkEmailUnique` reads `existing: string | None`.
  `execute` sites are unchanged. New invalid programs for Q1-Q3, A12 on
  a query, B10 and P7 on `Database` and on an untyped native capability.
- **New bug classes:** Q1 (SQL text not a literal), Q2 (unbindable
  argument), Q3 (a row type SQL can't fill). Widened: B10 and P7 (every
  native capability), K1 (`Database`'s operations), A12 (a query with no
  expected type).
- **`libraries/sql`** binds `bytes` and payload-free enums, and returns
  columns and raw values for the runtime's reader instead of building
  records. `scan.go`'s anonymous record goes away; nothing in fiig makes
  a record with no type any more.
- **ADR 0064's `toDecimalOrNone()` reads** of SQL text become a
  `decimal` field. ADR 0025's contract table is amended to these result
  types; ADR 0066 is marked superseded.
- Constraint violations (a duplicate key) still fault; turning expected
  database failures into values needs an error channel (open question 2).

## Open questions

1. **SQL whose shape depends on data.** A dynamic sort column or table
   name has no form under Q1. Candidates: an enum whose text forms are
   the allowed identifiers, or a separately named unsafe operation, as
   Prisma does.
2. **An error channel.** *(Answered by ADR 0087: a call opts in to its
   conflicts by its context's type.)* `Result<T, DatabaseError>` for the failures a
   program expects (a unique violation, a serialization failure to
   retry) instead of a fault. ADR 0048 §2.3's non-finite float is the
   same gap.
3. **Declared tables** (§9; ADR 0079 open question 3), which would let
   the checker compare a query's columns with a schema and say which
   package owns a table.
4. **`Enum | string` fields** (ADR 0081's catch-all) in a row. Left out:
   stored data is the program's own, so a value outside the enum is a
   bug to surface, not a producer's addition to tolerate.

## Implementation notes (2026-11-16)

- **Checker** (`compiler/checker/database.go`): `isNativeCapability` is
  the §2 test (capitalized, and not a local, primitive, declaration,
  group, import, or package alias). A package alias that isn't loaded in
  this run stays unchecked, since it may name a package's workflows (ADR
  0079). `checkExpectedQuery` takes the row type from context, the way
  `checkExpectedJson` does, and records it on the call's `DecodeInto`,
  which now serves both. A `queryOne` read as a type without `None` is
  A1, naming the fix.
- **Runtime** (`compiler/runtime/sqlrow.go`): `readQuery` fills the row
  type after the provider returns. A provider still hands back a record
  with no type, now only as the transport between the provider and the
  reader: a checked program never sees one. An unchecked program, which
  has no recorded row type, gets that record unchanged, as before.
- **`string` reads a BLOB of valid UTF-8**, which §7's table refuses:
  some drivers hand TEXT back as bytes, and the reader can't tell the two
  apart. A BLOB that isn't UTF-8 is still refused.
- **`libraries/sql`** binds `bytes` as a BLOB and a payload-free enum as
  its text form, returns a BLOB as `bytes`, and faults on two columns of
  one name before the reader sees the row.
- **Tests:** `compiler/checker`'s `database_test.go` (every class, both
  context forms, the recorded type), `compiler/runtime`'s
  `sqlrow_test.go` (§7's table, exact matching, one-column rows),
  `libraries/sql`'s `rows_test.go` (a round trip through real SQLite of
  every bindable type, a duplicate column, and a `DECIMAL(10,2)` column's
  REAL refused); `invalid-programs/database_error.fiig`.
- **Corpus:** the six query sites migrated; `StoredAccount` is new. Two
  test fakes changed: `compiler/runtime`'s fake database now returns only
  the columns a `SELECT` names, as a database would, and
  `compiler/adapter`'s tests now check the programs they run, so their
  queries read typed rows.
- **Dogfooded** with a `fiig` binary built from `cmd/fiig`: getting-started
  and accounts served over HTTP against real SQLite, every route; a score
  changed to REAL 4.5 in the database faulted at the query with
  `column "score" holds REAL 4.5, but Rating.score is int`.
