# ADR 0012: Duplicate-Operation Hints Are Non-Judgmental and Signature-Based

Status: Accepted
Date: 2026-09-22

## Context

Decision record #12 (`fiig-lang.md` §8): how are duplicate-operation
warnings calculated, and how does a developer override them?

## Decision

The registry never judges two operations as semantically similar, and never
warns or gates on that judgment. Instead, search results and contract cards
surface a plain fact: "N other exported declarations share this exact
input/output type signature." Nothing is blocked, so there's no override
mechanism to design — there's nothing to override.

## Rationale

Real semantic similarity detection is hard and failure-prone: two operations
can share a type signature and mean completely different things, or differ
in signature and be genuinely redundant. Judging "these are duplicates" is
exactly the kind of speculative mechanism §9 warns against building before
demand proves it's needed. Grouping by exact type signature, by contrast, is
nearly free — the registry already knows every declaration's types — and
stays true to the goal you described: making the capability layer visible to
humans and agents, not gatekeeping what they're allowed to create.

## Consequences

- `fiig-lang.md` Milestone 3's "Similar-operation warnings" and "An explicit
  mechanism for documenting why a new operation differs from an existing
  one" (§Milestone 3) assumed a judgment-and-override model that this ADR
  replaces — updating that deliverable list to reflect the signature-hint
  approach.
- Registry search/contract-card output (backlog item 9) needs a
  "same-signature siblings" field — mechanical, no new inference required.
