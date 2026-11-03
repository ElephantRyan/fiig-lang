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
4. **Immutable models with declared invariants** — validation that needs I/O lives in rules/gateways, not models.
5. **Typed errors, untyped faults** — expected failures are `Result<T, E>`; defects cancel and roll back, they don't propagate as unchecked exceptions.
6. **Structured concurrency by default** — bounded, cancellable, budgeted, with per-item failures preserved rather than aborting a whole batch.
7. **Explicit transactions** — only `transaction` blocks create a transaction; no implicit reads, no nested transactions, no network calls inside one.
8. **One declaration per file, explicit imports** — no wildcard imports; aliases are local spellings over canonical registry identities.
9. **Registry-first reuse** — every export carries a compact contract; search happens before new code is written.

## Success criteria

The project has earned its next milestone when a first vertical slice (language.md §12) is measurably clearer and safer than the same architecture built as a Go or Java framework, judged by:

- Fewer source files needed to implement a new workflow.
- Bug classes (from the Milestone 0 catalog) that are compile errors here and runtime incidents in the comparison language.
- An LLM can compose a second workflow correctly using only generated registry contracts, without reading implementation source.

If contracts and the checker don't provide leverage beyond naming conventions, the project should be recast as a framework or code generator rather than a language (language.md §11 go/no-go).
