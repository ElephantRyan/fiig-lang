# ADR 0044: An Activity Coordinates Effects; a Workflow Is the Adapter Boundary

Status: Accepted. Implemented 2026-10-14.
Date: 2026-10-10
Supersedes: ADR 0030's composition matrix (its `Activity` declaration stands)

## Context

ADR 0030 gave `Activity` one defining restriction: it "may not call
another `Activity`, a `Gateway`, or a `Workflow` — coordinating multiple
of those is a `Workflow`'s job," and it was therefore marked `Pure` in
the registry because it "never reaches a capability."

That left no home for a *reusable effectful unit* — a named, discoverable
thing that coordinates several gateways and is called from more than one
place. Everything of that shape had to be a `Workflow`, which also
carries boundary meaning: a public error contract, and whatever an
adapter exposes.

The corpus is the evidence, and it was already there when ADR 0030 was
written. Five workflows are called by other fiig code rather than being
entry points: `placeOrder` (from `checkout` and `placeAndShipOrder`),
`placeAndShipOrder` (from `placeShipAndNotifyCustomer`),
`sendWelcomeEmail` and `createCustomer` (from `onboardCustomer`), and
`summarizeCustomer` (from `summarizeCustomers`). None of them is a root.
`createCustomer` is both called *and* adapter-exposed.

The symptom was tracked but unpaid: fiig-lang.md §8 item 7, "how
workflows that call workflows propagate and contextualize errors," has
been open since the roadmap was written. A workflow's error contract and
its notion of success are properties of *its* boundary; reusing one from
inside another forces the caller to adopt or reinterpret both, which is
why that question never got a clean answer.

ADR 0030 split `Activity` from `Workflow` on **purity**. The useful axis
is **position**: is this a reusable interior unit, or the boundary?

## Decision

```go
kindRule:       {},                                                        // unchanged, ADR 0029
kindConversion: {kindRule: true, kindConversion: true},                    // unchanged
kindActivity:   {kindRule: true, kindConversion: true,
                 kindGateway: true, kindActivity: true},                   // was {rule, conversion}
kindGateway:    {kindRule: true, kindConversion: true},                    // unchanged, ADR 0031
kindWorkflow:   {kindRule: true, kindConversion: true,
                 kindActivity: true, kindGateway: true},                   // kindWorkflow dropped
```

- **`Activity` is reusable coordination.** It may call rules,
  conversions, gateways, and other activities; it may contain
  `transaction` blocks. It may **not** be exposed by an adapter.
- **`Workflow` is the boundary.** Nothing in fiig may call a workflow,
  and a workflow is the only declaration an adapter may attach to. It is
  the line between adapters and interior logic, in both directions.

Those two properties are what make a workflow's error contract final:
no fiig caller can reinterpret it, and the only thing on its other side
is an adapter mapping it outward.

An activity still has no `requires` clause. `Gateway` remains the only
declaration that names a capability it consumes, exactly one each — ADR
0010 is untouched. A `transaction` block names a capability for *scoping*,
not for method calls, which is why an activity may contain one without
gaining a `requires`.

**Purity becomes derived rather than stipulated.** ADR 0030 declared
every activity `Pure`. Instead the registry computes it by walking
callees, which `compiler/registry/graph.go` already does: an activity
calling only rules and conversions reports `Pure`, one reaching a gateway
does not. ADR 0030's motivating example (`validateCustomer` composing
`validateEmail` and `validatePhoneNumber`) keeps reporting exactly what it
reported before, without the restriction that produced it. The tradeoff
is that an activity's purity is no longer readable from its declaration
alone — it is read from the registry card, which is where every other
derived fact about a declaration already lives.

## Rationale

ADR 0030 generalized from a single example. `validateCustomer` was pure,
so "activities are pure" looked like a free structural guarantee rather
than a constraint — and the cost only shows up once something effectful
needs reuse, which the corpus already contained and nobody checked.

Deriving purity is strictly better than declaring it. A declared property
can be wrong about itself; a computed one cannot.

Making `Workflow` the sole adapter boundary *and* uncallable closes §8
item 7 by removing the question rather than answering it. There is no
cross-workflow error propagation to specify if no workflow is reachable
from fiig code. The error-contract boundary stops being something a
caller negotiates and becomes a property of position, enforced.

An earlier draft of this ADR let adapters expose an activity, on the
grounds that `createCustomer` is adapter-exposed *and* called, so the
restriction appeared falsified by real code. That reasoning was
backwards, and is worth recording: **code written under the rule being
replaced is weak evidence about the replacement rule.** `createCustomer`
has both roles only because ADR 0030 left nowhere else for reusable
effectful logic to live — it is a symptom of the conflation this ADR
fixes, not a counterexample to the fix. Measured properly, the overlap
is one declaration in the entire corpus, and the cost of forbidding it
is one extra declaration:

```fiig
// activities/register_customer.fiig — the reusable unit
activity registerCustomer(input: NewCustomer) -> Result<Customer, CustomerError> {
    validated = validateNewCustomer(input)?
    checkEmailUnique(validated.email)?
    return insertCustomer(validated)
}

// workflows/create_customer.fiig — the line the adapter attaches to
workflow createCustomer(input: NewCustomer) -> Result<Customer, CustomerError> {
    return registerCustomer(input)
}
```

That wrapper is not ceremony. It reads as empty only because
`CustomerError` is currently both the interior and the published error
type; the workflow is where that published contract is pinned, so when
the activity's error enum later grows a variant that should not appear on
an HTTP API, the workflow is where it has to be noticed. For a language
whose premise is explicit contracts, making the boundary a declaration
rather than an inference is the right trade. The real cost is two names
for one operation, which is unavoidable without making workflows
callable again.

## Implementation notes

Decisions made while implementing that the sections above left open:

- **B8 is redefined, not removed.** It now means "an activity or workflow
  calls a workflow." B2/B4/B5 keep their existing meaning for rule,
  gateway, and conversion callers.
- **B9 is new:** an activity on a call cycle, reported at each member's own
  declaration. Detection walks the program-wide universe, so it works across
  files under the one-declaration-per-file rule.
- **C5 widened to activity calls.** Once a workflow call is illegal
  everywhere (B8), C5 would have had no independent case. An activity may
  *contain* a `transaction` block, so letting one be *called* inside another
  would be a nested transaction by another route (C2). The
  `transaction_error.fiig` fixture therefore calls `summarizeCustomer`
  (an activity), not a workflow.
- **`checkout` and `onboardCustomer` call `registerCustomer`**, not
  `createCustomer`: the latter is now the boundary workflow and uncallable.
- **Registry purity** is computed in `registry.Generate` after dependency
  filtering. An unresolved callee makes an activity impure rather than
  assumed pure.
- Gateway → activity/workflow is still untagged, as before; the catalog has
  no number for it.

## Consequences

- `compiler/checker/checker.go`: `allowedCallees[kindActivity]` gains
  `kindGateway` and `kindActivity`; `allowedCallees[kindWorkflow]` loses
  `kindWorkflow`.
- Bug class B8 ("an activity calls a gateway, another activity, or a
  workflow") is replaced by two: a declaration calls a workflow, and an
  adapter exposes a non-workflow.
- Activity recursion is now possible, so the checker needs cycle
  detection on activity call graphs — the first place fiig needs it,
  since rules call nothing and conversion recursion is structural and
  terminates on the type graph.
- `compiler/registry`: `Pure` for an activity is computed from its
  callees rather than hardcoded `true`.
- Corpus migration. Four workflows become activities with unchanged
  bodies: `placeOrder`, `placeAndShipOrder`, `sendWelcomeEmail`,
  `summarizeCustomer`. `createCustomer` splits as shown above.
  `checkout`, `onboardCustomer`, `placeShipAndNotifyCustomer`,
  `changeCustomerEmail`, `lookupCustomer`, `readCustomer` and the
  remaining demonstration workflows stay workflows — each is either
  adapter-exposed or a genuine root, and `checkout`'s cross-domain
  `CheckoutError` reinterpretation (ADR 0006) is exactly the work a
  boundary exists to do.
- Four invalid-program fixtures call a workflow (`type_error.fiig`,
  `error_handling_error.fiig`, `effect_error.fiig`,
  `transaction_error.fiig`). Each needs its callee switched to the
  migrated activity, or it will trip the new "calls a workflow" class in
  addition to the one violation it exists to demonstrate.
- `examples/README.md`'s "Required example 7: compose two child
  workflows into a parent workflow" no longer describes a legal program
  and needs rewording — composing two child *activities* is the same
  demonstration.
- fiig-lang.md §8 item 7 is struck as moot rather than resolved.
- The question of whether `Workflow` should collapse into `Activity`
  entirely, raised while drafting, is **closed**: a workflow now carries
  two enforced properties — uncallable, and the only adapter-attachable
  declaration — which is a real distinction rather than the thin one an
  earlier draft described.
