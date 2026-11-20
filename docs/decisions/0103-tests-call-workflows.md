# ADR 0103: A Test May Call a Workflow

Status: Accepted (2026-11-20). Not yet implemented; built with ADRs 0104-0107 as one migration.
Date: 2026-11-20
Amends: ADR 0091 §1 ("a test calls what a workflow may"), ADR 0044
("nothing calls a workflow" gains one caller: a test).
Related: ADR 0079 (another package's workflow is reached through a
gateway), ADR 0102 (`test/workflows/` mirrors `src/workflows/`).

## Context

A workflow is a program's entry point: an adapter route calls it, and
nothing else does (ADR 0044; ADR 0079 adds another package's gateway). A
test calls what a workflow may, which leaves the workflow itself out, so
a feature is tested through the activities the workflow calls, not the
way a request reaches it. Under ADR 0102 a workflow's file would have no
testable mirror.

ADR 0044's rule keeps the program's call graph shallow and its entry
points few. A test isn't part of the program (ADR 0091 §2), so calling a
workflow from one doesn't add an edge the program has.

## Decision

A test may call a workflow of its own package, as it calls an activity:

```
// test/workflows/rate.fiig
test ratingTwiceKeepsTheFirstCreatedAt {
    fake Clock { ... }
    expect rate(input: RatingInput(itemId: "b-1", score: 8)) is Ok(first)
    expect rate(input: RatingInput(itemId: "b-1", score: 9)) is Ok(second)
    expect second.createdAt == first.createdAt
}
```

- **Its own package's workflows only.** Another package's workflow is
  reached through the gateway that requires that package (ADR 0079),
  which the test can call or fake as today.
- **Fakes work as before.** A fake replaces a capability or config
  anywhere under the call, the workflow's activities and gateways
  included; T5, T6 and T7 follow the calls through the workflow.
- **No adapter.** The test calls the workflow with values; the route,
  its arguments' decoding, and the response mapping aren't exercised.
  Testing those is the open question below.

## Rationale

- **Test what the program exposes.** A workflow is the unit a request
  runs; testing its activities one by one misses how they're composed.
- **ADR 0044's reason doesn't reach tests.** It protects the program's
  call graph, which a test isn't in.

## Alternatives considered

- **Test through the adapter** (a fake request in, a response out). Tests
  the route too, but needs a request syntax in tests; worth its own ADR.
- **Keep testing activities only.** Leaves `src/workflows/` without a
  mirror under ADR 0102, and the composition untested.

## Consequences

- `test/workflows/` mirrors `src/workflows/`.
- The registry's call graph is unchanged: tests aren't in it.

## Open questions

1. **Testing a route**: a request in, the response out, through the
   adapter's decoding and mapping.
