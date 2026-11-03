# ADR 0009: Adapter Path Parameters Bind by Name

Status: Accepted
Date: 2026-09-22

## Context

A gap surfaced by `docs/specification/grammar.md`, not a numbered §8 item:
how does an HTTP or CLI adapter route's path segment bind to the target
workflow's parameter, and which placeholder notation is canonical?

## Decision

Binding is by name match: a path placeholder must spell the same identifier
as the workflow parameter it fills, not by position. Placeholder notation is
one unified syntax across every adapter kind: `{id}`. HTTP and CLI routes
both write it the same way — `"/customers/{id}"`,
`"customer get {id}"` — both binding to `readCustomer(id: CustomerId)`.

## Rationale

Name-matching over position is simply the safer default — nothing requires
the added complexity of positional binding. For notation, one syntax
everywhere was preferred over a different style per adapter kind. `{id}` was
chosen over `:id`/`<id>` because it's not arbitrary: it's OpenAPI's own
path-templating syntax, and Milestone 5 already commits to generating
OpenAPI from HTTP endpoint types (`fiig-lang.md` line 397) — using the same
notation there means zero translation between the adapter route and the
generated spec. CLI inherits the same notation for consistency, even though
it has no OpenAPI tie-in of its own.

## Consequences

- `docs/specification/grammar.md`'s adapter productions and
  `examples/customer-service/adapters/*.fiig` need updating from `:id`/`<id>`
  to `{id}`.
- If a route ever needs to fill a parameter whose name doesn't make a good
  URL/CLI segment, there's no fallback (no explicit `{seg} -> paramName`
  remapping) — not needed by any current example, flagged rather than
  designed now.
