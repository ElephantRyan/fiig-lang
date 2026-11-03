# ADR 0007: No Additional Mandatory Export Metadata

Status: Accepted
Date: 2026-09-22

## Context

Decision record #11 (`fiig-lang.md` §8): what metadata is mandatory before a
rule or step may be exported.

## Decision

Nothing beyond what the type checker already requires to produce a valid
contract card. Every contract-card field that's mechanically derivable from
the checked declaration itself — canonical identity, kind, input/output
types, typed errors, required capabilities/effects, purity, source location
(§3.11) — is filled in automatically and is sufficient on its own for export.
Authored fields (intent, examples) are populated when provided but are not a
gate.

## Rationale

A mandatory-test requirement was considered and rejected as excessive: it
would block legitimate small, obviously-correct declarations from ever being
exported, and test coverage is already a CI/quality concern, not something
the registry needs to independently enforce.

## Consequences

- `lang catalog find "retrieve customer by id"` (an intent-text search,
  `fiig-lang.md` line 334) may return nothing for an undocumented
  declaration even though it exists and is fully usable — type- and
  capability-based search (`lang catalog find --input CustomerId --output
  Customer`) is the reliable fallback, not intent text.
- No test-coverage gate is enforced by the compiler or registry for export
  eligibility.
- This may need revisiting once real registry usage shows undocumented
  exports actively hurting discoverability — consistent with §9's guardrail
  against building enforcement mechanisms before demand is proven.
