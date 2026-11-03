# ADR 0034: Catalog Import and Merge

Status: Accepted
Date: 2026-10-09

## Context

ADR 0033 left "catalog import/merge across packages into one combined
registry" as the next open Milestone 6 item: `compiler/registry` and
`catalog-cli` only ever generated a catalog from one package root's own
files, with no notion of a card's package identity at all — fine while
nothing cross-package existed to combine, but no longer sufficient now
that `compiler/loader` can resolve a real dependency closure.

`docs/terminology.md`'s "Canonical identity" entry already specifies
what's needed: "a declaration's true package/version/symbol identity ...
as opposed to any local alias a consuming file uses to refer to it" —
and Milestone 6's own exit criteria require "Local aliases never change
canonical registry identity," which isn't actually checkable yet without
a card knowing which package it came from.

## Decision

1. **`Card` gains `Package` and `PackageVersion` fields**, both `""` by
   default — `Generate` on its own (single package root, no workspace)
   leaves them empty, byte-for-byte unchanged from before this ADR.
   They're only ever set by the new `Merge`.

2. **`registry.Merge(sources []MergeSource) ([]*Card, []FileError,
   error)`** generates each source's own cards via the existing
   `Generate`, tags every card with that source's `Path`/`Version`, and
   combines the results into one catalog sorted by (Package, Name,
   Source) — giving every card the canonical identity the terminology
   doc calls for, independent of whatever alias a consuming file
   happens to use.

3. **`Dependencies` stays scoped to one package**, unchanged. A
   cross-package call (`businessTime.addBusinessDays(...)`) is a
   `FieldAccess` callee, not a bare `Ident`, so `bodyCallNames` already
   never recorded it as a dependency candidate at all — `Generate`'s own
   `filterKnown` doc comment already flagged this ("or, Milestone 6,
   lives in an imported package this registry doesn't see") as a known,
   deliberate gap before this ADR, not something newly introduced by it.
   Resolving a qualified call into a real cross-package dependency edge
   (so `Callers`/the dependency graph span package boundaries too) is a
   further step, not assumed here — it would need the same per-file
   alias-resolution machinery `compiler/checker`'s `resolveFileImports`
   already has, which this AST-only package deliberately doesn't carry
   (see `registry`'s own package doc).

4. **`catalog-cli` gains a `-workspace <dir>` flag** on `generate`,
   `find`, and `explain` (not `diff`, which already compares two full
   package trees directly). Given, it calls `compiler/loader.Load`,
   turns the result's own manifest plus its `Lockfile.Dependencies` into
   `[]registry.MergeSource`, and merges; omitted, behavior for all three
   is exactly what it was before this ADR.

5. **`ByName` stays a first-match lookup**, documented as such rather
   than extended to take a package qualifier — ADR 0032 already requires
   every cross-package reference to go through an alias, so a real name
   collision across two merged packages is a signal the caller wants
   `Card.Package` from the full result set, not something this lookup
   needs to resolve on the caller's behalf.

## Consequences

- `compiler/registry/merge.go` (`MergeSource`, `Merge`) and the two new
  `Card` fields.
- `tooling/catalog-cli`: `-workspace` on `generate`/`find`/`explain`;
  `loadRegistry` split into a bare single-package path and a new
  `loadCatalog` that dispatches to `Merge` when `-workspace` is given.
- Verified by hand against the real `examples/time-library` /
  `company-time` / `standard-time` corpus: `generate --workspace
  examples examples/time-library` returns all three packages' cards in
  one array, each correctly tagged `company.time`/`2.4.0`,
  `examples.timeLibrary`/`1.0.0`, `standard.time`/`1.0.0`; `generate`
  with no `-workspace` is unchanged (no `package`/`packageVersion` keys
  at all).
- Not yet built: cross-package dependency/caller-graph edges (point 3,
  above), symbol-level aliases beyond `use ... as`, deprecation
  metadata, organization package source support, and the capability
  security review — still open Milestone 6 items.
