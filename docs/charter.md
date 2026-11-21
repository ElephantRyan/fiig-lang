# Language Charter

Status: Accepted, Milestone 0 complete
Working name: Fiig (see `decisions/0003-project-name.md`)
Bootstrap target: Go (see `decisions/0002-bootstrap-target.md`)

## Mission

Make strongly typed, concurrent application workflows easy for humans and LLMs to discover, compose, verify, and operate. Application architecture — models, business rules, external interactions, and their composition — is a language concern, not a framework convention layered on top of a general-purpose language.

## Goals

- A developer or LLM can implement a new use case by searching a generated registry for existing models, rules, conversions, activities, and gateways, and composing them — without reading most implementation source.
- The compiler makes entire classes of bugs unrepresentable: wrong-type IDs, unhandled errors, unbounded concurrency, invalid transaction nesting, missing effect declarations.
- Concurrency, transactions, and error handling are runtime-owned language features with safe defaults, not SDK-specific patterns the developer must get right by convention.
- Every public declaration carries a compact, machine-readable contract (types, errors, effects, capabilities, guarantees) sufficient for search and reuse without reading source.
- New primitives (rules, gateways, workflows) are added only when the required behavior is genuinely new — reuse is the default path, not the exception.

## Non-goals

- Not a general-purpose systems language. No goal to replace Go, Rust, or C for OS/embedded/driver work.
- Not targeting interactive desktop UI state or durable multi-day workflows in initial versions — different lifecycle and persistence semantics, deferred.
- Not building a self-hosting compiler, a native machine-code backend, a custom garbage collector, or a public package marketplace before earlier milestones prove demand (full list: language.md §9).
- No general foreign-function interface. Application source cannot import arbitrary host-language code.
- No `null`, no unchecked casts, no general-purpose `Any`, no implicit conversions, no unchecked exceptions.
- Not competing on raw syntactic novelty — a modern surface syntax is welcome, but every construct must justify itself against the goals above, not aesthetics alone.

## Initial scope

In scope: HTTP services, CLI applications, queue/event consumers, scheduled jobs, batch processing.

Deferred: interactive desktop UI, durable/long-running workflows that survive process restarts.

## Design principles (summary)

Full rules are in language.md §3; this is the index a reader should recognize before reading examples.

1. **Semantic declarations** — Model, Rule, Conversion, Activity, Gateway, Workflow, Adapter, each with a fixed responsibility and fixed calling rules.
2. **Strong static typing** — nominal models, no implicit conversions, no unchecked `Any`.
3. **No null** — absence is `T | None`, always explicit.
4. **Immutable models; validation in rules** — a model is the shape of data and declares no checks; a rule makes the valid model from the input model, and validation that needs I/O lives in gateways (ADR 0083).
5. **Typed errors, untyped faults** — expected failures are `Result<T, E>`; defects cancel and roll back, they don't propagate as unchecked exceptions.
6. **Structured concurrency by default** — bounded, cancellable, budgeted, with per-item failures preserved rather than aborting a whole batch.
7. **Explicit transactions** — only `transaction` blocks create a transaction; no implicit reads, no nested transactions, no network calls inside one.
8. **One declaration per file, explicit imports** — no wildcard imports; aliases are local spellings over canonical registry identities.
9. **Registry-first reuse** — every export carries a compact contract; search happens before new code is written.
10. **Strict about what the program says and reads, lenient about what it receives and ignores** — a program declares exactly what it reads and produces, and the checker holds it to that; extra input from the outside world (an unread header, a `3.0` for an int) is accepted rather than refused. Being opinionated inside the program is what lets a reader, human or LLM, have all the context it needs (ADR 0062 §2, ADR 0070 §2).
11. **Fail at the earliest point that has the information** — a program refuses what it can before it does any work, and later layers don't re-check what an earlier one settled. Config is checked at startup (ADR 0071, 0122, 0124); a request is limited before the program runs (ADR 0076) and its principal (who is calling) is to be settled before its body is read (ADR 0137, proposed); input is decoded into its type at the boundary (ADR 0073); a workflow runs its pure checks before any step does I/O (`docs/layers.md`); a defect is a fault that cancels, not an error that travels (principle 5).

## Not object-oriented

Fiig has no classes, and each part of object orientation was replaced by something else in its own ADR. A `model` is data only; behavior lives in separate declarations (rules, conversions, activities, gateways, workflows), and only builtins have method-call syntax. There is no inheritance or subtyping: a model reuses another by embedding it (ADR 0045). Substitution goes through an `interface` at a capability selection point (ADR 0043), and a closed set of cases through an `enum` and an exhaustive `match` (ADR 0046). Encapsulation is per file, in `private { }` blocks (ADR 0054), not per object. Models are values, changed through `copy(...)` (ADR 0023), not objects with identity changed in place. This is the split Go, Rust, and Elixir make, composition for reuse and interfaces for substitution, taken further than Go, which keeps methods on types.

## Success criteria

The project has earned its next milestone when a first vertical slice (language.md §12) is measurably clearer and safer than the same architecture built as a Go or Java framework, judged by:

- Fewer source files needed to implement a new workflow.
- Bug classes (from the Milestone 0 catalog) that are compile errors here and runtime incidents in the comparison language.
- An LLM can compose a second workflow correctly using only generated registry contracts, without reading implementation source.

If contracts and the checker don't provide leverage beyond naming conventions, the project should be recast as a framework or code generator rather than a language (language.md §11 go/no-go).
