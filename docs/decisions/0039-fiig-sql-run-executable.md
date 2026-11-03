# ADR 0039: `fiig-sql-run` — Running a Real Database Without Go Code

Status: Accepted
Date: 2026-10-09

## Context

ADR 0025's own consequences flagged: "`libraries/sql` can't be `use`d
from `.fiig` source yet ... it's wired up at the Go level
(`caps.Register("Database", sql.New(db))`) by whoever assembles the
`Interp`." Revisiting it after Milestone 6 (per the user's correction):
that note wasn't actually about `use`/package imports at all — a
capability's `requires` clause was never going to go through the import
system (ADR 0032 treats them as orthogonal axes on purpose). It was
about a different, still-real gap: the only way to actually exercise
`libraries/sql` against a real database was still a hand-written Go
`main()`, which directly contradicts the original motivation ADR 0025
was written to serve — nothing about this project should require
writing Go to use a database from a fiig program, the same way nothing
about it should require writing Go to call `use` or declare a gateway.

ADR 0027 already built the declarative half (`LoadCapabilities`,
`FactoryRegistry`) and `libraries/sql` already had its own `Factory`
(`factory.go`) implementing it — the missing piece was purely an
executable that actually wires the two together with a real driver.

## Decision

1. **`tooling/run-cli`'s reusable pipeline is extracted into `tooling/
   runprogram`** (`LoadFiles`, `BuildImports`, `UniversesOf`,
   `DeclParams`, and the scripted-capability machinery — `-caps`'s
   legacy flat form plus `ScriptedFactory`/`LoadCapabilitiesFile`) —
   the parts of running a checked program that have nothing to do with
   which capability providers a given host registers. `run-cli` itself
   becomes a thin CLI wrapper around it; behavior is byte-for-byte
   unchanged (verified by hand against both a cross-package run and a
   scripted-capability run).

2. **A new executable, `libraries/sql/cmd/fiig-sql-run`**, living inside
   `libraries/sql`'s own module (not the root `fiig-lang` module, which
   stays dependency-free) — it's `run-cli` plus a real driver (`modernc.
   org/sqlite`, blank-imported, no cgo) and `libraries/sql.Factory`
   registered under `"sql"`. A capability config file naming
   `{"type": "sql", "driver": "sqlite", "dsn": "..."}` is now all it
   takes to point a fiig program at a real database — no per-application
   Go code, matching exactly how `-caps`'s scripted form already worked
   for fake responses.

3. **No change to the module boundary ADR 0025 drew.** `fiig-sql-run`
   deliberately duplicates `run-cli`'s small amount of CLI-only
   formatting code (`printTrace`/`printJSON`, flag parsing) rather than
   sharing it across the module boundary — display formatting isn't
   where a correctness bug could hide, so the usual DRY argument for
   `runprogram` doesn't apply to it the same way.

## Rationale

This closes the loop ADR 0025 opened: a generic, driver-agnostic
database contract (`libraries/sql`), a declarative way to configure any
capability provider (ADR 0027), and now a ready-to-run binary that
combines them — the same "swap the deployed database without touching
the program" property JDBC/DB-API/ADO.NET all have, now actually
reachable without writing Go.

## Consequences

- `tooling/runprogram` (new package): `LoadFiles`, `BuildImports`,
  `UniversesOf`, `DeclParams`, `ScriptedCapability`, `ScriptedFactory`,
  `LoadCapabilitiesFile`.
- `tooling/run-cli/main.go` slimmed to a thin wrapper around
  `runprogram`; `caps.go` deleted (moved into `runprogram/scripted.go`).
- `libraries/sql/cmd/fiig-sql-run/main.go` (new): same flags as
  `run-cli`, plus the `"sql"` factory and a real SQLite driver baked in.
- Verified end to end against a real on-disk SQLite file across three
  separate process invocations (schema setup, insert, read), using only
  plain `.fiig` gateway declarations and a JSON capability config — no
  Go code written for the application itself.
- Full build/vet/test clean across both the root module and
  `libraries/sql`'s own module.
