# ADR 0028: Rules and Conversions May Call Each Other

Status: Accepted
Date: 2026-10-08

## Context

Surfaced while reconsidering `step`'s name and role (still unresolved as
its own question). The original §3.2 composition matrix allowed `Rule ->
Rule` but nothing else for either pure kind: `Conversion -> Rule` only,
and `Rule -> Conversion` wasn't in the allowed-callees table at all — it
fell through to an untagged diagnostic, not even one of the numbered
Category B bug classes.

Two framings were considered and rejected before this one:

1. **Rules/conversions call nothing at all, ever — even each other.**
   Motivated by wanting every rule to be a fully isolated, independently
   testable leaf, with all composition happening at the orchestration
   layer. Failed on a concrete case: extracting a shared validator
   (`isValidEmail`, usable from more than one place) out of a larger
   validation rule (`validateNewCustomer`) is exactly the kind of reuse a
   rule-based system should encourage, and under this framing it's
   impossible — the only way to share that check would be to duplicate
   it inline everywhere it's needed, which defeats the point of naming it
   at all.
2. **A fourth, separate declaration kind** just for pure composition of
   rules/conversions with no capability, keeping `Rule` itself at true
   zero-calls. Rejected as unnecessary complexity once (1)'s actual
   failure mode was understood: the problem was never composition itself,
   it was composition with something *impure*.

## Decision

A rule or conversion may call another rule or conversion (including
itself-shaped recursion, incidentally, though unattested). Neither may
ever call a step, workflow, or capability — that restriction is
unchanged and is the one that actually matters.

```go
var allowedCallees = map[declKind]map[declKind]bool{
	kindRule:       {kindRule: true, kindConversion: true},
	kindConversion: {kindRule: true, kindConversion: true},
	kindStep:       {kindRule: true, kindConversion: true},
	kindWorkflow:   {kindRule: true, kindConversion: true, kindStep: true, kindWorkflow: true},
}
```

## Rationale

Purity composes safely. Calling another rule/conversion doesn't cost
isolation or testability *as long as the callee is also pure* — you're
trusting an independently-tested unit, the same way `validateCustomer`
trusts `isValidEmail`/`isValidPhone` to be correct on their own. That's
qualitatively different from a rule calling a step: there, an effectful,
possibly-nondeterministic dependency would be smuggled into something
that's supposed to be fully deterministic. The line that actually matters
is pure-calls-pure vs. pure-reaches-impure, not "rules call nothing."

A `conversion` is already defined (§3.1) as "a specialized rule relating
two types" — there was never a principled reason for it to be
call-restricted differently than an ordinary rule, just an unexamined
default from the original matrix sketch.

This required no registry changes at all: `compiler/registry/graph.go`'s
dependency walker (Milestone 3) already walks every declaration's body
generically regardless of kind, so a rule's dependencies on other rules
show up in `Card.Dependencies` — and therefore in `Callers` and
`catalog-cli explain` — automatically, the moment the checker allows the
call to exist. Confirmed directly: `validateNewCustomer`'s card now shows
`"dependencies": ["validateEmail"]` with zero changes to the registry
package.

## Consequences

- `compiler/checker/checker.go`: `allowedCallees` updated as above; no
  bug-class renumbering needed (B1–B5 keep their exact existing meaning;
  `bugClassForEffect`'s `default: return ""` path and the `kindConversion:
  return "B5"` case simply fire less often now, for the cases that are
  still actually forbidden).
- New example: `examples/customer-service/rules/validate_email.fiig`
  (`validateEmail`), extracted from `validateNewCustomer` — the first real
  rule-calls-rule example, exercised through the real interpreter
  (`compiler/runtime`), not just the checker.
- `compiler/registry`: new `Query.Leaf` filter (`catalog-cli find --leaf`)
  — cards with no dependencies, i.e. atomic building blocks, as opposed to
  ones composed from other rules/conversions. Purely a derived, optional
  convenience over a fact the registry already tracked; no new metadata an
  author has to declare (consistent with ADR 0007 — export metadata isn't
  mandatory).
- `step`'s name/role is still an open question — this decision doesn't
  settle it, but it does sharpen it: with pure composition fully handled
  by rules/conversions calling each other, whatever `step` becomes is
  unambiguously about the one thing pure declarations still can't do
  (reach a capability), not a dumping ground for "any kind of
  composition."
