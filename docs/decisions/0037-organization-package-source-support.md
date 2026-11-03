# ADR 0037: Organization Package Source Support

Status: Accepted
Date: 2026-10-09

## Context

`fiig-lang.md`'s Milestone 6 deliverable list has one bullet with no
elaboration anywhere else in the docs: "Organization package source
support" — no terminology.md entry, no exit criterion, no mention in any
other spec section. Rather than guess at a feature with nothing attested
behind it, asked directly: the motivating case is a Java shop's own real
experience — an internal client library built for an internal API,
needing to be a normal, resolvable dependency rather than something
special-cased outside the package system.

## Decision

Scoped to exactly that case, deliberately not further:

1. **`compiler/loader.Load` searches multiple local roots, not one.**
   Signature changes from `Load(root, workspaceRoot string)` to
   `Load(root string, workspaceRoots []string)`. Each root is scanned
   the same way (`scanWorkspace`) and the discoveries are merged into one
   index before resolution proceeds exactly as before — an internal
   library living in its own directory tree (its own repo checkout,
   typically) no longer has to be collocated with the main workspace
   tree to be found. `tooling/check-cli` and `tooling/catalog-cli` both
   expose this as a repeatable `-workspace`/`--workspace` flag (a small
   `flag.Value` implementation, `stringList`, in each — not a shared
   package, matching this codebase's existing preference for a few
   repeated lines over a premature shared abstraction).

2. **No change to resolution semantics.** The same package discovered
   at the same major version under two different roots is still the
   existing "ambiguous dependency" hard error (`validateInstalled`) —
   multiple search roots widen *where* a candidate can be found, never
   *how* a conflict between two candidates gets resolved. This
   specifically avoids reintroducing anything resembling Maven's
   nearest-wins-over-one-flat-classpath behavior (ADR 0032's own
   rejected precedent) — a second root is not a second, lower-priority
   place to silently fall back to.

3. **`manifest.Manifest` gains `Origin string`** — a package's own
   declared canonical source location (typically a git URL, or any
   free-form description), read directly by a human auditing a build,
   never parsed or fetched by anything. Copied through unchanged to
   `lockfile.Entry.Origin` so the lockfile itself shows where each
   resolved dependency's code actually lives, without requiring a reader
   to go re-open every manifest in the closure by hand.

4. **Deliberately not built: any network/fetch mechanism.** `compiler/
   loader`'s own package doc already draws this line ("there's no
   remote registry or fetch step — Milestone 6 doesn't call for one");
   nothing here moves that line. Origin is provenance metadata, not an
   instruction the loader acts on.

## Consequences

- `compiler/loader`: `Load`'s signature change (one caller change in
  each of `check-cli`/`catalog-cli`, plus every existing loader test).
- `compiler/manifest`: `Origin` field, no validation beyond "it's a
  string" (same treatment `Deprecation.Message` already gets — free
  text, read by a human, not machine-checked).
- `compiler/lockfile`: `Entry.Origin`.
- `tooling/check-cli`, `tooling/catalog-cli`: repeatable `-workspace`.
- Verified against a synthetic but realistic scenario matching the
  motivating case exactly: an application depending on
  `acme.billingClient`, which lives in a separate `internal-libs` root.
  Searching only the main workspace fails with a clear "not found"
  error; adding the internal root resolves it, and the written lockfile
  records both the local path it was actually loaded from and its
  declared `origin` URL.
- Every Milestone 6 deliverable is now shipped or explicitly closed as
  already-satisfied. Milestone 6 is feature-complete per its own
  deliverable list; its exit criteria (cross-major coexistence, readable
  contract diff, capability enforcement, canonical identity surviving
  aliases) were each verified individually across ADRs 0032 and 0033–37.
