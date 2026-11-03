# ADR 0032: Package Manifests, Minimum Version Selection, and Cross-Major Coexistence

Status: Accepted
Date: 2026-10-09

## Context

Milestone 6 (`fiig-lang.md` §6) needs library/application package types, a
semver policy, a lockfile with exact versions and content hashes, and
catalog import/merge across packages. The grammar already had `use
pkg@version` (`version-literal`, attested only in
`time-library/rules/compute_follow_up_date.fiig`'s `use company.time@2.4`),
but nothing ever specified where a version comes from when it's omitted
(the same file's `use standard.time as systemTime` has none), nor how two
different pins for the same package across one build should be
reconciled — the checker has deliberately treated every dotted/aliased
reference as `Unknown` until now (`compiler/checker/checker.go`'s package
doc).

Surveyed real precedent before deciding:

- **Maven**: nearest-wins / first-declared tie-break over one shared
  classpath with no version namespacing — a heuristic, not a principled
  resolution. A wrong pick surfaces at runtime (`NoSuchMethodError`), not
  build time. This is the actual mechanism behind "dependency hell": one
  flat namespace, one positional winner.
- **npm**: sidesteps collision instead of resolving it — nested
  `node_modules` duplicates freely, at the cost of possible
  duplicate-singleton bugs (two live copies of what should be one thing).
- **Cargo**: unifies within a semver-compatible range, but lets genuinely
  incompatible majors coexist safely, because the type system namespaces
  by version automatically (a type from `foo 1.0` and the "same" type from
  `foo 2.0` are different types — a mismatch is a compile error, not a
  silent mix-up).
- **Go modules**: Minimum Version Selection — deterministic max of every
  required minimum, no solver, no heuristic — for compatible versions.
  Genuinely incompatible majors are handled by semantic import versioning:
  a breaking v2 is a *different module path*, so two majors are two
  distinct, coexisting identities, never one name secretly meaning two
  things.
- **Elm**: the one package manager that doesn't trust the author's own
  semver classification of a release — it diffs the public API itself and
  computes/enforces the required bump.

One direction was considered and rejected mid-design: drop ranges
entirely, pin every `use` to one exact version, resolved from a manifest,
and hard-error on any disagreement anywhere in the transitive closure.
Rejected because, without range tolerance, an ordinary patch-level
difference anywhere in a real transitive graph becomes a full build stop
requiring manual manifest surgery across packages the application author
may not even control — stricter than Maven, but not better: it blocks the
common, harmless case instead of just mis-resolving the rare one. Teams
that try to pin every transitive dependency exactly tend to find this
"pinning hell" worse than nearest-wins in practice, not safer.

## Decision

1. **Drop `@version` from the `use` import grammar.** A version is never
   written at the import site; every package's dependency versions live
   in its manifest instead. This supersedes grammar.md §3.10's `['@'
   version-literal]` and its one attested example — the same kind of
   grammar reversal ADR 0020 and ADR 0022 already made elsewhere once a
   later milestone had better information.

2. **Every package — library or application — gets a manifest**
   (`fiig.pkg.json` at the package root, the same role `go.mod` plays for
   a Go module) declaring:
   - its own canonical dotted package path, `kind` (`library` or
     `application`), and own semver version;
   - a `dependencies` map of package path → **minimum** version — never an
     exact pin, never range syntax.

3. **Resolution is Minimum Version Selection within a major-version
   track**: across the whole transitive closure of manifests pulled into a
   build, the version actually used for a given package is the maximum of
   every minimum anyone in the closure declared for it — deterministic, no
   solver, no nearest-wins heuristic.

4. **Different major versions of the same package may coexist in one
   build.** fiig already requires every cross-package reference to go
   through an alias and bans unqualified wildcard imports (G2) — there is
   no flat global namespace for two majors to collide in, so unlike Go this
   doesn't need a path trick, and unlike Cargo it doesn't need special
   type-system support: safe coexistence falls out for free from aliasing
   already being mandatory. Each file's aliases resolve against its own
   package's own resolved dependency closure.

5. **No new "version conflict" error class.** Once mismatched majors are
   allowed to coexist, the only remaining failure mode is an ordinary
   nominal-type mismatch (a value from one major flowing into a signature
   expecting another) — caught by the checker's existing Category A rules,
   not a package-specific mechanism.

6. **The semver policy is enforced by diffing contract cards, not by
   trusting the manifest author's own classification** (Elm's approach):
   the required bump between two releases of a package is derived from
   `compiler/registry`'s own contract-card diff — M6's already-planned
   "contract-level upgrade diff" deliverable — rather than asserted by
   whoever typed a new number into the manifest's version field.

7. **The lockfile** (not built yet — the step after the manifest format)
   is the generated artifact recording the actual resolved version,
   content hash, and source location per package for a given application
   build. It records the outcome of 3–6; it never decides policy itself.

## Rationale

See Context's survey. Short version: the ecosystems that avoid dependency
hell either resolve deterministically within compatible versions (Go) or
make incompatible coexistence *safe* rather than forbidden (Go's import
versioning, Cargo's type namespacing) — never a positional heuristic over
one shared namespace (Maven). fiig gets the "safe coexistence" half
essentially for free, since mandatory aliasing already rules out the flat
namespace that forces Go and Cargo to build extra machinery for it.

## Consequences

- `use` loses the optional `@version-literal` entirely:
  - `compiler/lexer`: `scanNumber` reverts to integer-only; the `At` (`@`)
    token kind is removed (no remaining producer or consumer).
  - `compiler/syntax`: `Import.Version` field removed.
  - `compiler/parser`: `parseImport`'s `@version` branch removed.
  - `compiler/format`: stops printing a version after a package path.
  - `examples/time-library/rules/compute_follow_up_date.fiig`,
    `compiler/parser/parser_test.go`, `compiler/format/syntax_snapshot_test.go`,
    `compiler/lexer/lexer_test.go` updated to match.
  - `docs/specification/grammar.md` (§1's `version-literal` production,
    §2's import grammar, the "Attested" note), `fiig-lang.md`'s §3.10
    example, and `docs/terminology.md`'s "Canonical identity" example
    updated. Historical ADRs (0022) that quote the old `@2.4` syntax are
    left as-is, per the convention ADR 0031's own consequences section
    already established — this ADR supersedes their terminology, not their
    text.
- New `compiler/semver`: minimal strict `major.minor.patch` parsing and
  comparison (`Parse`, `Compare`, `SameMajor`) — no pre-release/build
  metadata, since nothing attested needs it.
- New `compiler/manifest`: `Manifest` (`Path`, `Kind`, `Version`,
  `Dependencies`), `Parse`/`Load`, and validation that collects every
  problem rather than stopping at the first — the same style
  `checker.CheckFile` already uses for diagnostics.
- `examples/time-library/fiig.pkg.json` — the first real manifest:
  `company.time`, library, `2.4.0`, depending on `standard.time` at a
  declared minimum version.
- Not yet built: actual multi-package loading/resolution (the checker's
  `Universe` is still one flat global map, `compiler/types/universe.go`),
  the lockfile itself, catalog import/merge, and the contract-diff-derived
  version-bump check. Each is a further M6 step on top of this format.
