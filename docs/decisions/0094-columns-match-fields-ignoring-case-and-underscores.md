# ADR 0094: Columns Match Fields Ignoring Case and Underscores

Status: Accepted (2026-11-19). Implemented 2026-11-19; see Implementation
notes.
Date: 2026-11-19
Amends: ADR 0082 §6 (how a column finds its field); Q3 widens.
Related: ADR 0073 §7b (`from json`), ADR 0082 (typed rows), ADR 0085 (the
Postgres driver), charter principle 10, abs-recommendations GAPS G5.

## Context

ADR 0082 §6 fills a model from a row by exact name, and renames in SQL:

```
"SELECT user_id AS userId, item_id AS itemId, score, note, recommended, created_at AS createdAt, updated_at AS updatedAt FROM rating WHERE user_id = ?"
```

abs-recommendations, the first app built on fiig, pastes that 7-column
alias list into three gateways (GAPS G5). SQL text must be a literal (Q1),
so the list can't be shared, and adding a field means editing every query,
with a miss found only when the query runs.

**The rule is also wrong on Postgres.** Postgres folds an unquoted name to
lower case, aliases included. On the `fiig-pg-dogfood` container (Postgres
17):

```
SELECT 1 AS userId, 2 AS "itemId"
 userid | itemId
```

So every unquoted alias the app writes comes back as `userid`, and the
field `userId` has no column. SQLite keeps the case it's given, which is
why the app hasn't seen it. The only fix under §6 is `AS "userId"` on every
renamed column, in every query that runs on Postgres.

Two habits meet at a row: SQL names columns `snake_case`, and fiig names
fields `camelCase`. Every program that reads a table it didn't name hits
this, which is most programs.

### How others do it

| Library | `user_id` column into a `userId` field |
|---|---|
| Spring `BeanPropertyRowMapper` | matches by default: case ignored, underscores ignored |
| Dapper | case ignored by default; `MatchNamesWithUnderscores` also ignores underscores |
| Go sqlx | lowercases the field name by default; a `db:"user_id"` tag otherwise |
| MyBatis | `mapUnderscoreToCamelCase`, off by default |
| Rust sqlx | exact; `#[sqlx(rename_all = "snake_case")]` or `#[sqlx(rename)]` |
| JPA / Hibernate | `@Column(name = ...)`; Spring Boot's naming strategy maps camelCase to snake_case |

Spring's mapper is the closest: it fills `userId` from `user_id`,
`USER_ID`, or `userid`, which covers both SQL's habit and Postgres's
folding.

## Decision

### 1. A column fills the field whose name it matches, ignoring case and underscores

A field and a column match when their names are equal after both are
lowercased (ASCII letters only) and their underscores removed. `user_id`,
`USER_ID`, `userid`, and `userId` all fill `userId`.

```
model Rating {
    userId: string
    itemId: string
    score: int
    createdAt: int
}

row: Rating | None = Database.queryOne(
    "SELECT user_id, item_id, score, created_at FROM rating WHERE user_id = ?",
    userId,
)
```

No `AS`, and the same query reads the same on SQLite and on Postgres.

### 2. Everything else stays strict

§6's other checks still apply to the matched names:

- A field with no column faults at the query, naming the field.
- A column with no field faults at the query, naming the column.
- Two columns that match the same field fault, naming both. `SELECT
  user_id, userId` is that, as `a.id, b.id` already is.

These are still faults at run time, since the checker doesn't know the
schema (ADR 0082 §6).

### 3. Two fields that would match the same column are Q3

A model with fields `userId` and `user_id`, or `userId` and `userid`, can't
be a row type: a column `user_id` would match both. That's Q3 at the query
that reads the model, naming both fields, since the model can't be filled
from SQL (ADR 0082 §5). The model is fine everywhere else.

### 4. `AS` still renames

A column that doesn't match this way is renamed in SQL, as before: `cust_no
AS customerNumber`. On Postgres the alias folds to `customernumber`, which
now matches too, so `AS` needs no quotes.

### 5. Nothing else changes

A one-column row type (§5) still ignores the column's name. Bound
parameters, `execute`, and the drivers are unchanged. Every query that
works today still works, since an exact match is also a match here.

## Rationale

- **Principle 10: lenient about what it receives.** Column names come from
  the schema, which is often someone else's or written in SQL's habit. The
  program still says exactly what it reads (every field, checked both
  ways); only the spelling of the names is forgiven.
- **It fixes a real bug, not just repetition.** On Postgres, §6 makes the
  natural query fail. A rule that needs quoted aliases on one database and
  not another isn't one rule.
- **One sentence to learn, no syntax.** The alternative needs an
  annotation on every field of every row model.

## Alternatives considered

- **`from column "user_id"` on a field**, like ADR 0073's `from json`.
  Explicit, but it's one annotation per field, and the common case is a
  mechanical spelling change. ADR 0082 §6 turned down `from sql` for the
  same reason. It can still be added for a name that doesn't match this
  way, if a program needs one.
- **A model-level `rename_all`**, like Rust sqlx and serde. One line per
  model, but the model then knows it's read from SQL, and the same model
  is often a JSON body too.
- **Exact match, with `AS "userId"` documented for Postgres.** Fixes
  neither the repetition nor the trap.
- **Case-insensitive only (Dapper's default).** Fixes Postgres's folding,
  but `user_id` still needs an alias.

## Consequences

- abs-recommendations drops the alias list from its three queries (G5).
- A reader who sees `user_id` in SQL and `userId` in the model needs this
  rule; it goes in ADR 0082 §6, `grammar.md`'s Database section, and the
  tour.
- Q3 gains a case (§3).
- The fault messages say which column a field looked for in both
  spellings: `field "userId" has no column (looked for userId or user_id)`.

## Open questions

1. **Non-ASCII column names.** Only ASCII letters are lowercased. A column
   named in another script matches only exactly. Waits for a case.

## Implementation notes

Built 2026-11-19.

- `syntax.ColumnKey` is the comparison (ASCII lowercase, underscores
  removed), shared by the runtime's row reader and the checker.
- `compiler/runtime/sqlrow.go` matches each column to a field, the exact
  name first, and then checks both ways. The missing-field fault names the
  spellings it looked for: `Rating.createdAt has no column (looked for
  createdAt or created_at)`.
- Q3's new case is in `rowRefusal` (`compiler/checker/database.go`).
- **Dogfood, Postgres 17** (`fiig-pg-dogfood`, through `fiig.postgres`): a
  copy of `examples/relay-notes` read `SELECT id AS note_id, text AS
  noteText, 7 AS createdAt` into `StampedNote{noteId, noteText,
  createdAt}`. The second and third aliases came back folded, `notetext`
  and `createdat`, and matched.
