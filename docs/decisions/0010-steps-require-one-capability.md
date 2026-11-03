# ADR 0010: A Step Requires Exactly One Capability

Status: Accepted
Date: 2026-09-22

## Context

A gap surfaced by `docs/specification/grammar.md`: can a step's `requires`
clause name more than one capability?

## Decision

No — exactly one. An operation needing more than one capability is expressed
as a workflow composing multiple single-capability steps, not as one step
with several capabilities.

## Rationale

Confirmed directly: writing to a database and sending an email are two
steps, and a workflow is what coordinates them. This also matches the
existing rules independent of that confirmation — it's already implied by §3.1's
definition of a step ("**one** reusable semantic interaction with the
outside world") and by §3.2's "steps may not coordinate other application
steps" — a step is deliberately scoped to one interaction; composing several
is a workflow's job, not a bigger step's.

## Consequences

- `docs/specification/grammar.md`'s `step-decl` production already only
  allowed one capability identifier after `requires` (`'requires' identifier`,
  singular) — this ADR confirms that was correct rather than an oversight,
  and removes it from "Known gaps."
- An operation needing two capabilities *transactionally together* still
  goes through the existing atomic-block rules unchanged: §3.8 already
  limits one atomic block to exactly one transactional capability, so
  cross-capability atomicity was already out of scope before this decision,
  not a new restriction it introduces.
