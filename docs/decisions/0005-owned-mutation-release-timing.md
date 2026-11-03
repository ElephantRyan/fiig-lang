# ADR 0005: Temporary Owned Mutation Ships By the First Release

Status: Accepted
Date: 2026-09-22

## Context

Decision record #4 (`fiig-lang.md` §8): whether temporary local mutation of
exclusively-owned values — the "later ownership feature" flagged in §3.5 as a
possible exception to immutable-by-default models — belongs in the first
release.

## Decision

Yes. Implementation cannot be indefinitely deferred past the first release,
even though design work may still land earlier than implementation.

## Rationale

Immutable-by-default (§3.3, §3.5) is a core correctness commitment of the
language, not a nice-to-have — but real workflows will hit cases where
copy-on-every-mutation is either awkward or costly, and an escape hatch that
only exists "eventually" leaves the door open for that pressure to instead
erode the immutable-by-default rule itself (e.g. via workarounds or an
`Any`-like escape). Shipping v1 without owned mutation risks the guarantee
getting compromised elsewhere instead.

## Consequences

- Milestone 2's deliverable, "Initial ownership design document;
  implementation may remain deferred" (`fiig-lang.md` line 294), is still
  accurate as written — design can land at Milestone 2 without the
  implementation. This decision fixes the outer bound: deferred past
  Milestone 2 is fine, deferred past the first release is not.
- Some later milestone must carry ownership implementation as an exit
  criterion — most plausibly Milestone 4 (executable runtime), since owned
  mutation is a runtime memory-semantics feature, though Milestone 2's type
  checker may need to enforce the ownership rules that make it sound. Which
  milestone owns this isn't decided here — revisit once Milestone 2/4 scope
  firms up.
- No current example exercises owned mutation. One should be added once the
  design document exists, consistent with how every other construct here is
  grounded in a real example rather than asserted in prose alone.
