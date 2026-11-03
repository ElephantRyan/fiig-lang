# ADR 0036: Deprecation Metadata

Status: Accepted
Date: 2026-10-09

## Context

Last open-with-no-attached-exit-criterion item from Milestone 6's
deliverable list (after ADR 0035 closed the security review and
resolved "package and symbol aliases" as already satisfied):
"Deprecation metadata." Nothing in the lexer preserves comments
(`compiler/registry`'s own package doc already notes this, re: intent/
examples metadata), so there's no annotation syntax to hang this off of
in source — same situation ADR 0032 already resolved once, by putting a
version in the manifest instead of at the `use` site.

## Decision

1. **`manifest.Manifest` gains `Deprecations map[string]Deprecation`**,
   keyed by one of the package's own declared (bare, undotted) names.
   `Deprecation` is `{Since, Message}` — `Since` a semver string
   (validated the same way a dependency's own minimum is), `Message`
   free-form guidance (typically naming a replacement), read directly by
   a human or an LLM, not parsed by anything. Deprecating is advisory
   only — nothing in `compiler/checker` or `compiler/loader` treats a
   call to a deprecated declaration as an error or even a warning;
   `fiig-lang.md` doesn't call for enforcement, only metadata.

2. **`registry.Card` gains `Deprecated *manifest.Deprecation`**, set by
   `Generate` itself — not only by `Merge` — by loading root's own
   manifest (if one exists; most of the `examples/` corpus still has
   none, which `Generate` already treats as "no package context" the
   same way it's treated everywhere else across Milestone 6) and
   matching cards by bare name. This is the first time `compiler/
   registry` imports `compiler/manifest` — a deliberate, narrow
   coupling (reusing `manifest.Deprecation` directly rather than
   duplicating its shape), not a sign the two packages are merging
   into one; `registry` still never requires a manifest to exist.

3. **`registry.Query` gains `Deprecated bool`**, exposed as `catalog-cli
   find --deprecated` — an audit view ("what in this package's own
   surface still needs migrating"), the practical reason to want this
   metadata queryable rather than just present in raw JSON output.

4. **No interaction with `registry.Diff`/`CheckBump` (ADR 0033).**
   Removing a declaration is still a Major bump whether or not it was
   previously marked deprecated — deprecation is a heads-up ahead of a
   future breaking removal, not a discount on the removal itself when it
   actually happens. Real-world semver practice agrees: a deprecation
   warning and the breaking change it warns about are two separate
   events, not one softened into the other.

## Consequences

- `compiler/manifest`: `Deprecation` struct, `Manifest.Deprecations`,
  validation (reusing the bare-identifier pattern a capability name
  already needed, renamed from `capabilityNamePattern` to
  `identifierPattern` since it's no longer capability-specific).
- `compiler/registry`: new dependency on `compiler/manifest`;
  `Card.Deprecated`; `Generate` loads root's manifest (gracefully
  degrading to nothing-deprecated when absent or invalid); `Query.
  Deprecated` / `Find`'s matching filter.
- `tooling/catalog-cli find` gained `--deprecated`.
- Verified against a realistic copy of `examples/company-time` (not the
  corpus itself, which has no actual migration case to attest):
  `generate` correctly tags `addBusinessDays` once its manifest
  deprecates it, and `find --deprecated` correctly isolates it.
- Every Milestone 6 deliverable is now shipped or explicitly closed
  except **organization package source support** — the one remaining
  open item.
