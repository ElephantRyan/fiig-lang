# ADR 0133: `fiig review`: What a Program's Design Looks Like, as Facts

Status: Proposed
Date: 2026-11-21
Related: ADR 0051 (no severity level, ever), ADR 0107 (workflow calls
activities, not gateways), ADR 0006 (errors are reinterpreted explicitly),
ADR 0095 (`mapErr`), `docs/layers.md`, ADR 0106 (`fiig calls`).

## Context

A design audit of Hearsay (formerly abs-recommendations, the first real
application, 2026-11-21) found it passes `fiig check` and still misses what
the layers are for. Every call was legal. The meaning was wrong:

- `common.ApiError` (`Unauthenticated`, `Refused`, `NotFound`, ...) is shaped
  like HTTP, and gateways and activities return it. By the time a result
  reaches a workflow there is nothing left to interpret, so only 2 of its 28
  workflows (`deleteMyRating`, `listMyRatings`) decide anything about an
  error. `docs/layers.md` says that is the workflow's job.
- The same five-arm `AbsError` match is written four times
  (`apiErrorOfAbs`, `signInErrorOfAbs`, `serviceFailure`, `failedText`).
- 37 of its 74 workflows and activities are five lines or fewer, signature
  included.
- Authentication is a first line each of 18 workflows has to remember
  (`currentUser(session)?`). One of them (`picksOf`) calls it and throws the
  identity away.

These are one application's numbers. They show what a report would say; they
are not a threshold (ADR 0051's tests, below, say why).

### Why not more checker rules

ADR 0051 §2.2 admits a checker diagnostic only if the property is
**decidable**, **wrong in every reading**, and **semantically consequential**.
Each candidate guardrail, run through it:

| Candidate | Decidable | Wrong in every reading | Verdict |
| --- | --- | --- | --- |
| A gateway or activity returns an error type an adapter maps to `HttpResponse` | yes | **no**: a domain error such as `RatingError` is also mapped by its adapter; HTTP-shaped vs domain-shaped is a matter of naming, not structure | not a checker rule |
| An activity `match`es an error and returns a different error type | yes | **no**: an activity calling two gateways with different error types must unify them somehow | not a checker rule |
| A workflow that only forwards (`?` or `return other(...)`) | yes | **no**: B12 forces it where the feature has nothing to add; ADR 0107 chose that ("the seam is free when it's always there") | not a checker rule |
| A workflow takes a session cookie and never authenticates | partly | **no**: public routes exist | not a checker rule (see ADR 0137 for the language-side fix) |
| A stringly-typed value compared with `==` to a literal | yes | **no** | not a checker rule |

Every one fails (2), so ADR 0051 §2.3 applies: the response is to fix the
rule, not to lower the severity, and none of these can be made right by
rewording. They are still worth seeing. ADR 0051 says there is no lint tier
*inside the checker*. This ADR puts the observation outside it.

## Decision

### 2.1 A separate command that prints facts

`fiig review [-json] [<root>]` reads the same program the checker does and
prints **measurements and listings**. It does not say a program is wrong.

- It has **no severity, no pass/fail, and no exit-code gate**. Exit 0 whenever
  the program checks; a program that does not check gets the checker's
  errors, as `fiig calls` does.
- It never appears in `fiig check`'s output and is never a numbered bug
  class.
- Each line is a count or a list of names, each of which a reader can verify
  by opening the file.

### 2.2 What it reports

1. **Forwarding.** Workflows and activities whose body only forwards, as a
   count, a ratio and the names. A workflow's forward is shown with the step
   it forwards to.
2. **Where each error type is built.** For each enum used as an `Err` payload:
   the declaration kinds that construct it (gateway, activity, workflow,
   conversion) and the kinds of conversion that map it to `HttpResponse`.
   An enum constructed in gateways and mapped to `HttpResponse` is listed as
   one fact, with both locations.
3. **Repeated matches.** Groups of `match` expressions over the same enum
   whose arm shapes are alike, by structure and not text. Hearsay's four
   `AbsError` matches would be one group of four.
4. **Strings used as enums.** A `string` field or parameter compared with
   `==` against two or more literals in the same declaration.
5. **Size per feature.** For each workflow, the declarations reachable from
   it (the `fiig calls` closure), the files they are in, and their `use`
   lines. This is the measurement the charter's first success criterion
   ("fewer source files to implement a new workflow") needs and nobody has
   taken.
6. **Authentication.** For each adapter route, whether its parameters bind an
   principal (ADR 0137), shown as a column. Until ADR 0137 exists the column is
   omitted.
7. **Undocumented workflows**, if ADR 0136 is accepted.
8. **Checks after I/O.** A workflow that calls an activity before a rule over
   the same input, or an activity that calls a rule over a parameter it
   received unchanged from its workflow, listed with both calls (charter
   principle 11, `docs/layers.md`'s order of checks). Hearsay's `putRating`
   (an ABS round trip, then `createRating` inside `storeRating`) is the
   attested case. It is a fact and not a check: some validation needs the
   outside world first.

### 2.3 Output

Plain text by default, grouped under the numbers above, with the names a
reader needs to act. `-json` prints the same data for an agent or a CI step
that wants to compare two runs. CI may diff it; the command itself never
fails on it.

### 2.4 Where it lives

`tooling/review-cli`, alongside `catalog-cli` and `check-cli`. It uses the
registry and the call graph (`fiig calls`, ADR 0106), and adds nothing to
`compiler/checker`.

## Rationale

- **Observation belongs somewhere.** The audit produced these findings by
  hand with a script and a read. A command makes them repeatable, so a
  second application (the corpus has several) can calibrate what is normal.
- **ADR 0051 is kept intact.** Nothing here is a warning. A reader who wants
  a threshold sets it in their own CI over `-json`.
- **It is the measurement for the charter's success criteria.** Criterion 1
  is currently unmeasured.

## Alternatives considered

- **Checker rules.** Rejected by the table in Context, row by row.
- **Doc only (`layers.md`).** Already exists and was not enough here: `layers.md` lists "a
  workflow that is one line" as a sign, and Hearsay has many of them. ADR 0107 §Alternatives records the same finding
  for the call rules.
- **Wait for more applications before building it.** Reasonable, and it is
  the main objection. The report is cheap, and a second application is what
  makes its numbers mean anything, so the command should exist first.

## Consequences

- A new command and a new tool directory; no change to the checker.
- `docs/layers.md` and ADR 0107 disagree, and the report will show the
  disagreement in numbers. `layers.md` says a one-line workflow means its
  decisions moved into the activity; B12 makes a one-line workflow
  unavoidable when a feature has no decision of its own. This ADR does not
  resolve it.

## Open questions

1. **Resolve `layers.md` vs B12?** Options: (a) reword `layers.md`: a
   forwarding workflow is correct when the feature adds nothing, and the
   smell is a forwarding workflow whose activity contains a decision;
   (b) let an adapter call an activity directly (reverses part of ADR 0107);
   (c) leave it. This is the owner's call.
2. **Authentication without ADR 0137?** Fiig has no notion of a session and
   should not gain one for this; without a binding the column stays omitted.
3. **Should `fiig review` count test files?** Tests repeat fakes and `use`
   lines, and test files are a large share of Hearsay.
