# ADR 0106: Seeing What a Call Calls: Highlighting and `fiig calls`

Status: Accepted (2026-11-20). Implemented 2026-11-20.
Date: 2026-11-20
Related: ADR 0050 §2.3 (call-site tones), ADR 0090 (the IntelliJ plugin),
Milestone 3 (the registry), ADR 0107 (workflows don't call gateways).

## Context

A bare call, `findBook(id)`, doesn't say whether it calls a rule, a
conversion, an activity, or a gateway. ADR 0050 made a call show where
its callee lives, not what kind it is. The owner wants a file to say more
without tracing the code, for a reader and for an LLM.

Prefixing every call with its kind (`rule.checkRating(x)`) was
considered and found noisy. With ADR 0107 the ambiguity that matters is
in two places: in a workflow (an activity or a pure call?) and in an
activity (a gateway or a pure call?). Marking only effectful calls
(`activity.`, `gateway.`), Swift's idea, was also set aside for now.

## Decision

### 1. The IntelliJ plugin highlights calls by kind

Each kind of callee gets its own colour, configurable in the colour
scheme: rule, conversion, activity, gateway, workflow, private helper,
capability operation, formula. A call to a declaration with effects is
easy to tell from a pure one at a glance.

### 2. `fiig calls <file>`

Prints each declaration a file calls, with its kind and, for one with
effects, the capabilities it reaches:

```
$ fiig calls src/ratings/Rate.fiig
loadUserAndBook   activity   effects: Database, AbsApi
checkRating       rule       pure
saveRating        activity   effects: Database
```

`-json` prints the same as JSON, for tools and agents.

## Rationale

- **People get it without noise**: colour carries the kind.
- **An LLM gets it in one command**, or one file lookup, since a name maps
  to its file (ADRs 0104, 0105).

## Alternatives considered

- **Every call prefixed with its kind.** Noisy, and mostly redundant
  given what each kind may call.
- **Effectful calls prefixed** (`activity.`, `gateway.`). Revisit if LLMs
  are seen misreading pure and effectful calls.

## Consequences

- No source changes.

## Implementation notes

Built 2026-11-20.

- `fiig calls` (`tooling/cli/callscmd`) reads the registry. The program
  root is the nearest directory with a `fiig.pkg.json`, else the current
  directory, or `-root`. Building it found a registry bug: an activity
  calling a name outside the tree the registry was generated from was
  marked pure; it no longer is.
- `fiig lsp` answers `textDocument/semanticTokens/full` with a token per
  call, typed by the callee's kind (`tooling/lsp/semantic.go`).
- The IntelliJ plugin maps each type to a colour under **Color Scheme |
  fiig | Calls**. Pure calls start from the function-call colour,
  activities from the instance-method colour, gateways and capability
  operations from the static-method colour. Verified Compatible with
  IntelliJ IDEA 2026.2 and 2026.3.
