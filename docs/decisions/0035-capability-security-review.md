# ADR 0035: Security Review of Imported Gateway Capabilities

Status: Accepted
Date: 2026-10-09

## Context

Milestone 6's exit criteria include "Imported gateways cannot gain
undeclared capabilities," and its deliverable list separately calls out
"Security review of imported gateway capabilities." Until now, a
gateway's `requires` clause (grammar.md §4) was checked only for shape —
whether a `transaction`/`retry` block around it matched (Category C/E,
`compiler/checker`) — never against any notion of what an *application*
actually agreed to depend on. A library pulled in transitively could
require any capability name at all, and nothing would ever say so.

`compiler/loader` already resolves and builds the full transitive
dependency closure (ADR 0033), and `compiler/manifest` already is the
one place a package states facts about itself to the rest of a build
(ADR 0032) — both existed before this ADR but neither carried anything
about capabilities yet.

## Decision

1. **`manifest.Manifest` gains an optional `Capabilities []string`
   field** — the explicit allowlist of capability names this package's
   own build is permitted to touch, validated against the same bare-
   identifier shape a `requires` clause already requires (no dots —
   unlike a package path, a capability name was never dotted,
   grammar.md §4). Empty/omitted means "this package's own build must
   never need any capability at all" — the strict default, not a silent
   opt-out; a package with no gateways anywhere in its closure (every
   current example package) satisfies this trivially.

2. **`lockfile.Entry` gains `Capabilities []string`** — every capability
   that specific resolved dependency's own gateways require, recorded
   per-entry rather than only as one build-wide aggregate, so an
   auditor reading the lockfile can see exactly which dependency is
   responsible for a given capability showing up in a build at all —
   the actual "security review" artifact, not just a pass/fail check.

3. **`compiler/loader.Load` computes and enforces the check itself**,
   as the last step before returning successfully: it parses root's own
   files (new — `Load` didn't need to before this ADR) to find root's
   own gateways' capabilities, combines that with every entry's own
   `Capabilities`, and reports an error — named by exactly which
   capability and which package introduced it — for anything not present
   in the root manifest's own declared allowlist. A caller gets either a
   fully resolved `*Loaded` (with every capability anywhere in the
   closure already known-safe) or a list of errors; there's no partial
   "resolved but unchecked" state to forget to check later.

4. **No new mechanism for root's own gateways versus a dependency's** —
   both go through the exact same `declared` comparison. A capability
   introduced by the application's own code is exactly as much a fact
   that belongs in its manifest as one inherited from a library three
   levels down; singling out "imported" gateways for stricter treatment
   than the application's own would be a distinction without a
   difference once everything funnels through one manifest-level
   allowlist anyway.

## Rationale

This is deliberately a disk-level build check (`compiler/loader`), not a
`compiler/checker` diagnostic — `checker.CheckFile`/`CheckDir` already
work without any package context at all (`packages` can be `nil`), and
a single file's own type-checking has no way to know what an
*application's* own manifest declares; the capability allowlist is a
build-wide fact, exactly the layer `compiler/loader` already owns.

## Consequences

- `compiler/manifest`: `Capabilities` field, `capabilityNamePattern`
  validation.
- `compiler/lockfile`: `Entry.Capabilities`.
- `compiler/loader`: `gatewayCapabilities`, `checkCapabilities`,
  `Loaded.Capabilities` (the full build-wide deduplicated set, already
  checked by the time it's returned); `Load` now also parses root's own
  `.fiig` files, which it didn't need to before.
- Verified with four new loader tests against synthetic workspaces: an
  application that never declares a dependency's required capability
  fails; the same closure succeeds once the manifest declares it (and
  the per-entry/per-build `Capabilities` lists are correct); an
  application's *own* undeclared gateway capability fails the same way.
- Not yet built: deprecation metadata, organization package source
  support — the two remaining open Milestone 6 deliverables. (Package
  and symbol aliasing, the other previously-open bullet, turns out to
  already be fully covered by the existing `use ... as` mechanism plus
  canonical-identity tracking already shipped across ADR 0032's
  checker work and ADR 0034's `Card.Package`/`PackageVersion` — fiig-
  lang.md §3.10 never described a second, per-symbol import form, so
  there was never a distinct feature left to build here.)
