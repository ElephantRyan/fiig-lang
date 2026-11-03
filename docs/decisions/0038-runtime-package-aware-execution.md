# ADR 0038: Runtime Package-Aware Execution

Status: Accepted
Date: 2026-10-09

## Context

Milestone 6's loader/checker work (ADR 0032–0037) made cross-package
programs *type-check* correctly, but nobody had gone back to `compiler/
runtime` or `run-cli` — both still worked exactly as before Milestone 6
existed:

- `run-cli` always called `checker.Check(files, nil)` — no `-workspace`
  flag at all, unlike `check-cli`/`catalog-cli`.
- `Interp` carried a single flat `*types.Universe` with no notion of an
  import alias. `evalQualifiedCall` treated *any* bare-identifier target
  that wasn't a declared name as a capability call — the only other
  shape that grammar position meant before Milestone 6. A real
  cross-package call (`businessTime.addBusinessDays(...)`) would have
  been silently misrouted into a capability dispatch and faulted with a
  confusing "no provider registered" error instead of running.

Net effect: a program could be checked against its real dependencies but
never actually run. Found by systematically re-checking every "waits for
Milestone 6" comment left across the codebase, at the user's request.

## Decision

1. **`compiler/checker.resolveFileImports` exported as
   `ResolveFileImports`** so `compiler/runtime`/`run-cli` can resolve a
   file's own `use ... as alias` aliases the identical way the checker
   does, rather than a parallel hand-rolled copy.

2. **`Interp` gains `Imports map[string]map[string]*types.Package`** —
   root's own import aliases, resolved *per declaration name* (one entry
   per root declaration, pointing at whichever file it was declared in's
   own aliases). Per-file precision matters here specifically because
   two files in the same package may alias the same dependency
   differently — matching exactly the granularity `checker.CheckFile`
   already gives root's own files (one fresh `ResolveFileImports` call
   per file). nil for every pre-Milestone-6 caller, in which case nothing
   changes: every qualified call still falls through to a capability
   call.

3. **A new `scope` (compiler/runtime/scope.go)** — which package's
   Universe and import aliases are active for whatever declaration's
   body is currently executing — threaded through `evalExpr`/`evalStmt`
   via `context.Context`, the same idiom `transaction.go`'s
   `activeTxKey` already uses for other per-call-frame state, rather
   than a new parameter everywhere. An **unqualified call never changes
   it** — fiig has no implicit cross-package reference, so a bare name
   always means "declared in this same package," matching
   `compiler/checker`'s own behavior exactly (an unqualified call's
   callee is never wrapped in its `callInScope` either). A **qualified
   call into an imported package's own declaration swaps to that
   package's own Universe/Imports** for the call's duration, then
   restores the caller's.

4. **Once inside an imported package, its own `Imports` is the
   flattened per-package map** (`types.Package.Imports`, built by
   `checker.BuildPackage`/`compiler/loader`), not per-file. This isn't a
   new limitation — it's the exact same simplification
   `checker.callInScope` already accepts once a *type-check* crosses
   into a dependency; the interpreter matches the checker's own
   precision rather than exceeding or regressing it.

5. **A qualified call into an imported declaration now resolves
   Model/Value construction too**, not just the five callable kinds —
   needed because `company.time`'s own `addBusinessDays` constructs
   `standard.time`'s `Instant` under its own "time" alias
   (`time.Instant(...)`), itself a qualified call. `constructible`/
   `constructDecl` are shared between an unqualified construction
   (`evalIdentCall`) and this qualified one (`callDeclInPackage`).

6. **`run-cli` gains a repeatable `-workspace` flag**, mirroring `check-
   cli`/`catalog-cli`: resolves root's dependencies via `compiler/
   loader`, writes the lockfile, passes `packages` to `checker.Check`
   instead of always `nil`, builds `Interp.Imports`, and — the gap this
   whole ADR exists to close — resolves an entry parameter's own type
   (e.g. `systemTime.Instant`) via `types.ResolveWithImports` instead of
   plain `Resolve`, so `-args` actually decodes against a cross-package
   parameter type instead of failing on `Unknown`.

## Consequences

- `compiler/checker/checker.go`: `resolveFileImports` → exported
  `ResolveFileImports`.
- `compiler/runtime/scope.go` (new), `interp.go` (`Imports` field, `Call`
  split into itself + shared `callDecl`/`callableShape`),
  `callexpr.go` (`evalIdentCall`/`evalDeclCall` scope-aware;
  `evalQualifiedCall`'s new package-alias branch; `callDeclInPackage`;
  `constructible`/`constructDecl` shared construction logic).
- `tooling/run-cli`: repeatable `-workspace`, `buildImports`,
  `universesOf`, lockfile writing, `ResolveWithImports` for entry
  parameter types.
- New tests (`compiler/runtime/package_test.go`) prove real execution
  against the same attested `standard-time`/`company-time`/
  `time-library` corpus `TestCrossPackageResolution` (checker) and
  `TestLoadRealExamples` (loader) already check and resolve — this ADR
  is the third leg, actually *running* it, confirmed by hand with
  `run-cli -workspace examples -args '{"from":10}' examples/time-library
  computeFollowUpDate` → `Instant(13)`.
- Several doc comments that predated Milestone 5/6 and were never
  updated (`compiler/checker/checker.go`'s package doc, `compiler/
  types/types.go`'s `Unknown` doc, `compiler/types/universe.go`'s
  `Packages` doc, `compiler/runtime/value.go`'s package doc,
  `compiler/checker/checker_test.go`'s wildcard-import test comment)
  corrected to reflect what actually exists now.
- `docs/specification/bug-classes.md`'s **H1** ("an imported gateway
  gains an undeclared capability") flipped from Deferred to Attested,
  pointing at ADR 0035's loader tests — the catalog now stands at 25/26,
  only A5 (no cast syntax exists yet to misuse) still deferred.
- Not addressed: a parameter's own *nested* field types (a model field
  whose type itself needs cross-package resolution, deeper than one
  level) still resolve against the entry point's own `*types.Universe`
  inside `DecodeJSON`'s recursion, not a per-field import context —
  unattested by anything in the corpus, so left alone rather than
  guessed at, same as every other "not attested, not built" boundary in
  this project.
