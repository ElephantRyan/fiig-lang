# ADR 0107: A Workflow Calls Activities, Not Gateways

Status: Accepted (2026-11-20). Not yet implemented; built with ADRs
0102-0106 as one migration.
Date: 2026-11-20
Amends: ADR 0044 (what a workflow may call).
Related: ADR 0006 (errors are reinterpreted explicitly), ADR 0095
(`mapErr`), ADR 0103 (tests call workflows).

## Context

abs-recommendations' 19 workflows are one line each, passing to an
activity that does everything. The layers looked redundant because the
app put its decisions in activities (tests couldn't call workflows) and
nothing kept workflows away from gateways. The owner's model:

| Layer | Job |
|---|---|
| adapter | turns the app's errors into the transport's (HTTP status) |
| workflow | decides what a result means for this feature (a `NotFound` here becomes `Unauthorized`) |
| activity | a step: coordinates gateways; often shared by several workflows |
| gateway | talks to the outside world; turns its failures into the app's errors |

A workflow that calls a gateway directly is coupled to it: moving that
call into a shared step later means changing the workflow. With an
activity between them, the workflow depends on the step, not on how it's
done.

## Decision

A workflow may call activities, rules, and conversions. A workflow
calling a gateway, or a capability, is an error:

```
workflow rate may not call gateway findBook; call it from an activity, and the activity from the workflow (ADR 0107)
```

What else may call a gateway is unchanged: an activity, and another
package's gateway for ADR 0079's cross-domain call.

```
workflow rate(session: string | None, itemId: string, input: RatingInput) -> Result<Rating, AppError> {
    match loadUserAndBook(session, itemId) {
        Err(AppError.NotFound) -> return Err(AppError.Unauthorized)
        Err(other) -> return Err(other)
        Ok(found) -> return saveRating(found, input)
    }
}
```

A guide, `docs/layers.md`, describes the four layers and the error flow,
with the app's rate flow as its example.

## Rationale

- **The seam is free when it's always there.** An activity that wraps
  one gateway call is the seam, not overhead.
- **Each layer has a job**, so neither is a pass-through.

## Alternatives considered

- **A guide only.** The coupling it prevents is invisible until the day a
  step needs moving; the owner chose enforcement.

## Consequences

- Corpus workflows that call gateways get an activity between.
- abs-recommendations moves decisions into workflows and steps into
  activities.
