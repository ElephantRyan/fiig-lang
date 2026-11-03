# LLM composition trial

Status: one cheap single-task trial, run 2026-10-06. Satisfies the spirit
of Milestone 3's exit criterion ("a benchmark LLM can select existing
rules, activities, and gateways using only model definitions and compact
registry contracts") at low cost. The fuller version — multiple tasks, a scoring
methodology, "real feature requests" — is explicitly a Milestone 7
production-gate activity (`fiig-lang.md` §5), not attempted here, and
deliberately not pre-built as infrastructure ahead of that need (§9's
guardrail against building before demand is proven).

## Setup

Claude Sonnet 5, same session already doing the surrounding compiler work
— not a fully independent/blind trial, but the composition step itself
used only `catalog-cli generate`/`explain` JSON output. No `.fiig` source
file was read to decide what to call or how to shape the new workflow.

## Task

Chosen by first surveying the whole registry (`catalog-cli generate
examples/`, condensed to name/kind/params/returns/requires) to find a
real gap rather than inventing a synthetic one. That survey immediately
paid for itself: an initial idea ("create a customer and send them a
welcome email") turned out to already exist (`onboardCustomer`,
`sendWelcomeEmail`) — composing it would have just rebuilt something
already there, exactly the failure mode registry search exists to
prevent. The real gap found instead: nothing notifies a customer after
their order ships. Task: compose a workflow combining the existing
`placeAndShipOrder` workflow with a best-effort customer notification.

## What the registry gave, and didn't

`catalog-cli explain` for `Order`, `Customer`, `sendEmail`,
`placeAndShipOrder`, and `getCustomer` supplied every type/effect fact
needed: field shapes, param/return types, `requires`, `error`,
`idempotent`, and (new this session) `dependencies`/`callers`. Composing
the call graph itself was straightforward from this alone.

What it didn't supply: *style*. `place_and_ship_order.fiig` already has
an established idiom for "call something whose failure shouldn't undo an
already-successful operation" — bind the call to an otherwise-unused local
variable, with a comment explaining why. A contract card has no field for
this; it's not part of any declaration's type/effect contract, so a
composition built from contracts alone has no way to learn it.

## What happened

A first draft wrote the notification as a bare statement inside a
non-producing `match` arm (the natural way to express "do this on success,
do nothing on failure" without yet knowing the bind-to-unused-variable
idiom). It passed the checker — on the first try, no changes needed.

That result was suspicious enough to check directly: the same expression,
written as a bare top-level statement instead of inside a match arm, *was*
correctly rejected by bug class F1 (unhandled `Result`). The two shapes
had different outcomes for no principled reason — `checkMatchStmt`'s
non-producing `ExprBody` branch called `inferExpr` directly, bypassing F1
entirely, while every other non-producing statement shape routed through
it. Confirmed empirically (`compiler/checker/stmt.go`), then fixed:
`checkUnhandledResultExpr` is now shared by both call sites. See
`bug-classes.md`'s F1 entry and `TestUnhandledResultInNonProducingMatchArmIsF1`.

So the trial's real finding isn't "the registry worked" — it's that a
plausible, registry-only composition passed by accident, because of a
latent checker gap, not because it was correct by deliberate design. A
less careful run would have shipped it without ever noticing.

## Outcome

The corrected workflow — `customer = getCustomer(customerId)`, then
`match` on it, binding the notification call to an unused variable in the
`Ok` arm, same idiom as `place_and_ship_order.fiig` — checks cleanly and
is kept as a real example:
`examples/order-processing/workflows/place_ship_and_notify_customer.fiig`.

## Takeaways for the fuller Milestone 7 benchmark

- Registry search (type/effect/error/guarantee) reliably prevents
  reinventing something that already exists — confirmed directly, not
  just asserted.
- Compact contract cards are sufficient for *correctness* (types, effects,
  capabilities) but not for *style/idiom* — a gap worth deciding whether
  to live with (style is cheap to fix in review) or address later (e.g.
  example-based guidance in generated documentation, Milestone 7's
  "package documentation generation").
- A single trial is enough to find a real checker gap; it is not enough
  to say anything statistical about selection accuracy, hallucination
  rate, or context-token cost (`fiig-lang.md` §10's LLM-effectiveness
  metrics) — those need the multi-task, real-feature-request version
  Milestone 7 already owns.
