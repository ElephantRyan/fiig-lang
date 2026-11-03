# ADR 0033: Workspace-Scanned Loader, Lockfile, and Contract-Diff Version-Bump Check

Status: Accepted
Date: 2026-10-09

## Context

ADR 0032 defined the manifest format and the MVS/cross-major policy but
left three pieces explicitly "not yet built": the real disk-based
dependency loader (`compiler/checker`'s own tests still hand-assemble
`types.Packages`), the lockfile, and the contract-diff-derived
version-bump check. This ADR covers the design choices needed to actually
build those three, none of which ADR 0032 pinned down.

## Decision

**1. Workspace discovery has no new manifest file of its own.** A
"workspace" is just a directory tree; `compiler/loader.scanWorkspace`
walks it and registers every directory containing a `fiig.pkg.json`,
keyed by that manifest's own canonical `Path` — the same thing `go.work`
exists to avoid (an explicit member list), skipped here because nothing
in the corpus attests a case where directory-scanning is ambiguous.
Discovery never descends into a found package's own subtree (fiig has no
nested-package concept). A path may legitimately be discovered more than
once, at different major versions — that's ADR 0032 point 4's
coexistence, not an error; discovering the *same* major twice **is** an
error (ambiguous install).

**2. MVS resolution is two-pass, not build-order-dependent.** Pass one
(`collectRequirements`) walks the whole transitive manifest graph once,
recording the maximum minimum requested per `(path, major)` — this is
the actual "maximum of every minimum" MVS defines, and it has to finish
*before* anything is built, because a dependency built early under a
too-low minimum would wrongly validate against whatever minimum
triggered it first rather than the true closure-wide maximum. Pass two
(`validateInstalled`) checks the one on-disk candidate per `(path,
major)` actually satisfies that maximum. Only then does construction
happen (`loaderState.build`), recursively and memoized by `(path,
major)` — the memoization is what gives two unrelated dependents that
each need the same major of some leaf package the exact same built
`*types.Package`, i.e. real type identity, not two structurally-similar
copies.

**3. The root's own resolved `types.Packages` map is keyed by canonical
path alone, scoped to one root's build.** This looks like it could
collide with cross-major coexistence, but doesn't: a single manifest's
`dependencies` object can only name one minimum per path anyway (it's a
JSON object), so a root never needs two keys for the same path in its
own direct view. Two majors coexisting deeper in the graph (library A
needs `foo` v1, library B needs `foo` v2) is wired correctly *inside*
A's and B's own already-built `*types.Package.Imports`, set when they
were each constructed — the flat keying at the root is sufficient
precisely because resolution for every other node already happened one
level down, not because this ADR invented a namespacing trick ADR 0032
said fiig wouldn't need.

**4. The lockfile (`compiler/lockfile`) is a plain generated JSON file**,
`fiig.lock.json`, one entry per resolved dependency: canonical path,
exact resolved version, a sha256 content hash over that package's own
source files (sorted by relative path, so hashing is independent of
file-system iteration order — matching go.sum's own choice of
algorithm), and the source directory it was loaded from. It is written
fresh on every `check-cli -workspace` run, same as `go build` silently
updating `go.sum`; there's no separate "verify the lockfile matches"
mode yet, since nothing has asked for reproducible-build enforcement
specifically (that's Milestone 7's "reproducible builds" deliverable, not
this one).

**5. The contract-diff version-bump check reuses `compiler/registry`'s
existing `Card`, rather than introducing a second description of a
package's public surface.** `registry.Diff(old, new []*Card) (Bump,
[]Change)` compares two releases' cards by name: removed name or
removed/retyped/renamed field or parameter is `Major` (fiig supports
named-argument calls, so a parameter rename is exactly as breaking as a
type change); an added name, field, variant, or route is `Minor`; no
difference at all is `Patch` — mirroring Elm's own three-level model
rather than a binary breaking/non-breaking split, since "nothing API-
visible changed" is a real, useful third state (a pure bugfix release).
`Dependencies` is deliberately excluded from the diff — it's who a
declaration happens to call internally, never part of its own contract.
`registry.CheckBump(old, new semver.Version, required Bump) error`
enforces Elm's actual policy (ADR 0032 point 6): the version bump the
*new* manifest declares must be at least `required`; declaring more than
required is fine (an author may major-bump for reasons the structural
diff can't see), declaring less is always an error. Exposed as `catalog-
cli diff <oldRoot> <newRoot>`, reading each root's own `fiig.pkg.json`
for the versions being compared rather than taking them as separate
flags — the two directories are expected to be two checkouts of the same
package (e.g. a previous release and the current working tree), which a
CI job already has sitting on disk.

## Consequences

- New `compiler/loader` (workspace scanning, MVS, recursive memoized
  package construction) and `compiler/lockfile` (`Entry`, `Lockfile`,
  `HashFiles`, `Write`/`Load`).
- `compiler/checker.BuildPackage` promoted from a `checker_test.go`-local
  helper to an exported function — `compiler/loader` and the checker's
  own cross-package test now share one code path instead of two parallel
  hand-rolled ones.
- `compiler/registry/diff.go`: `Bump` (`Patch`/`Minor`/`Major`, with a
  `MarshalJSON` rendering the string form so a `Change` stays
  self-describing JSON, not an opaque int), `Change`, `Diff`, `CheckBump`.
- `tooling/check-cli` gained `-workspace <dir>`: when given, resolves
  root's dependencies via `compiler/loader` and writes the resulting
  lockfile at root before checking; omitted, behavior is unchanged from
  before this ADR (no package context).
- `tooling/catalog-cli` gained a `diff <oldRoot> <newRoot>` subcommand.
- `compiler/loader`'s own tests prove MVS unification, below-minimum
  rejection, and cross-major coexistence against synthetic workspaces,
  plus one end-to-end run against the real `examples/time-library` /
  `company-time` / `standard-time` packages — the same corpus
  `TestCrossPackageResolution` (ADR 0032's follow-up) hand-assembled,
  now loaded for real.
- Not yet built: catalog import/merge across packages into one combined
  registry (a separate M6 deliverable from version resolution itself),
  package/symbol aliases beyond the existing `use ... as`, deprecation
  metadata, organization package source support, and the capability
  security review — all still open M6 items per `fiig-lang.md`.
