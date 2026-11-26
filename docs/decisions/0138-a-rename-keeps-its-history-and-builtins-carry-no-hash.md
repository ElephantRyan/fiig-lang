# ADR 0138: A Renamed Package Keeps Its Migration History, and a Builtin Carries No Lock Hash

Status: Accepted (2026-11-25, by the owner; the options chosen in review on
2026-11-21). Implemented 2026-11-21 (see Implementation notes).
Date: 2026-11-21
Related: ADR 0096 (migrations), ADR 0033 (the lockfile), ADR 0032 (package
versions), Hearsay GAPS G55 and G62.

## Context

Two problems from Hearsay come from fiig keying a record by something that
can change without the program changing.

**G55.** `fiig_migrations` records each applied migration by package path.
Hearsay renamed its package from `abs.recommendations` to `hearsay`, and the
next `fiig migrate` found no history under `hearsay` and ran
`0001_initial_schema` again:

```
error: package hearsay: migration 0001_initial_schema failed and was rolled back:
table rating already exists
```

The fix was a hand-run `UPDATE fiig_migrations SET package = 'hearsay' WHERE
package = 'abs.recommendations'`. That repairs one database. Every other
deployment needs the same step, and a forgotten one fails startup the same
way.

**G62.** `fiig.lock.json` records a hash of each dependency's source.
`fiig.http` ships inside the toolchain (`"source": "builtin:http"`), so a
toolchain build that changed its source changed the hash. `check`, `test`,
`run` and `migrate` write the lockfile on every run, so Hearsay got a
one-line diff with no change of its own, on every commit. Nothing reads the
hash; it is only written.

### How others handle it

| | Migration history keyed by | A rename |
| --- | --- | --- |
| Rails | the migration's version (timestamp) | never matters |
| Flyway | the version, per schema | never matters |
| Liquibase | the changelog file path | breaks it; fixed with a `logicalFilePath` attribute declared in the changelog |

Liquibase's fix is declared in the code, which is what §1 does. For G62, Go
keeps the standard library out of `go.sum`: the Go version pins it.

## Decision

### 1. `formerly` in the manifest

```json
{
    "path": "hearsay",
    "formerly": ["abs.recommendations"],
    "kind": "application",
    "version": "0.1.0"
}
```

`formerly` lists the paths the package had before, newest last. Each is a
valid dotted path, none appears twice, and none is the package's own path.
When the program migrates (`fiig migrate`, or `fiig serve` and `fiig run`
starting up):

- If the package's own path has no recorded history and exactly one former
  path does, that history is moved to the package's path in one transaction,
  and the run reports it:

  ```
  applied hearsay (moved 5 recorded under abs.recommendations, its former path)
  ```

  Pending migrations are then applied as usual. A second process doing the
  same at once moves nothing.
- With `-migrate=check`, the move is a write, so it is reported as pending
  and nothing is changed.
- History recorded under both the new path and a former one, or under two
  former paths, is refused with both named. Fiig doesn't guess which one
  belongs to this database, so a person settles it.

The entry can stay in the manifest for good. Once every deployment has
migrated, removing it changes nothing.

### 2. A builtin's lock entry has no hash

A package the toolchain ships (`"source": "builtin:..."`) is written without
`hash`. The lockfile's `toolchain` field pins its bytes. A package from a
workspace or an organization's source keeps its hash.

```json
{
  "toolchain": "0.1.0-alpha.2",
  "dependencies": [
    { "path": "fiig.http", "version": "1.0.0", "source": "builtin:http", "capabilities": ["Socket"] }
  ]
}
```

A `dev` toolchain pins nothing precisely, which is acceptable for a
development build.

## Rationale

- **The code carries the fix.** Every database is repaired by the next
  start, not by an operator remembering a step (§1). That follows charter
  principle 11: the program settles it at startup, where it has the
  information.
- **The lockfile changes only when the dependencies do** (§2). That is what a
  reviewer expects a lockfile diff to mean.

## Alternatives considered

- **`fiig migrate -rename-from <old>`**, a one-time flag. Smaller, but each
  deployment must remember to run it, which is the failure G55 already has.
- **Document the `UPDATE`.** The same problem, written down.
- **Write the lockfile only when dependencies change**, and keep the builtin
  hash. The recorded hash then silently goes stale and records something
  untrue.

## Consequences

- `docs/schemas/fiig.pkg.schema.json` gains `formerly`.
- The committed lockfiles of `examples/inbox-sender`, `relay-notes`,
  `shop-orders-split` and `sql-relay` lose their `fiig.http` hash line the
  next time any of them is checked. It's a one-time diff.
- Only the root package's `formerly` is read. A renamed library's
  dependents would need it read from each dependency's manifest, through
  `compiler/loader`, and nothing attests that yet.

## Implementation notes (2026-11-21)

`compiler/manifest` (`Formerly`, validated), `tooling/migrate`
(`formerHistory`, `moveHistory`, `Package.Formerly`), and
`compiler/lockfile.Write`. Tests:
`cmd/fiig`'s `TestARenamedPackageKeepsItsMigrationHistory` on SQLite,
`compiler/manifest`'s `TestValidateFormerly`, and `compiler/lockfile`'s
`TestWriteOmitsTheHashOfABuiltin`.
