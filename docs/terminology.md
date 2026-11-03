# Terminology

Status: Accepted, Milestone 0 complete

Full rules are in `fiig-lang.md` §3; this is the glossary a reader should recognize
before reading examples or the charter. Terms are grouped by concern, not
alphabetically, so related terms stay next to each other.

## Declarations

- **Model** — Strongly typed application data. Immutable by default; fields are
  required unless the type includes `None`. `copy(model, field: value, ...)`
  produces a modified value without reconstructing every field by hand; no
  in-place mutation is exposed at the language level
  (`docs/specification/ownership.md`).
- **Enum** (ADR 0040) — A closed sum type: a fixed set of named variants, each
  carrying its own (possibly empty) field list, constructed and matched as
  `TypeName.VariantName` or `TypeName.VariantName(fields...)`. A `match` over
  one must cover every variant or include a catch-all. A typed error
  (`Result<T, E>`'s `E`) is an ordinary enum, not a separate declaration
  kind — every attested error type in the corpus is one.
- **Rule** — Pure, deterministic business logic. Calls nothing at all —
  fully isolated (ADR 0029). Cannot reach the network, a database, the
  clock, or randomness.
- **Conversion** — A specialized rule relating two types. Pure; may call
  other rules and conversions (ADR 0029) — recursive structural mapping
  stays pure and judgment-free.
- **Activity** — Reusable coordination of rules, conversions, gateways,
  and other activities into a named unit; may hold `transaction` blocks
  (ADR 0044, widening ADR 0030). Never adapter-exposed, and has no
  `requires` clause. Purity is derived from what it reaches.
- **Gateway** (ADR 0031, renamed from Step) — One reusable semantic
  interaction with the outside world (network, database, clock,
  randomness). May call rules and conversions. May not coordinate other
  gateways — that coordination belongs to a workflow.
- **Workflow** — Composition of rules, conversions, activities, and
  gateways. The adapter boundary (ADR 0044): the only declaration an
  adapter may attach to, and nothing in fiig may call one.
- **Adapter** — Translation between an external protocol (HTTP, CLI, queue) and
  a workflow. Calls exported workflows; nothing else calls an adapter.

These seven are the only primary declarations. Libraries normally publish
models, enums, capability contracts, rules, conversions, activities, and
gateways; only applications define workflows and adapters.

## Capabilities and packages

- **Capability** — A declared, provider-backed permission a gateway requires
  to do I/O (e.g. a database, an HTTP client). Capabilities are explicit in
  a gateway's contract, not ambient.
- **Capability provider** — The concrete implementation wired to a capability at
  runtime (e.g. one reference database provider). Swappable without changing
  the gateway's contract.
- **Package** — A versioned, distributable unit of declarations. A **library**
  package publishes models/rules/conversions/activities/gateways for reuse; an
  **application** package additionally defines workflows and adapters and is
  not itself importable.

## Type system

- **Nominal type** — A named model whose identity is its name, not its
  shape — two models with identical fields are still different types. Fiig
  has no newtype over a primitive (ADR 0055).
- **Structural type** — A type identified by its shape rather than a name.
  Permitted only for anonymous local records, never for public declarations.
- **`Result<T, E>`** — The typed return shape for an operation with an expected
  failure mode. `E` is a typed error the caller must handle.
- **Typed error** — An expected, declared failure (the `E` in `Result<T, E>`),
  declared as an ordinary **Enum** (ADR 0040), not a separate kind. Part of a
  declaration's public contract.
- **Fault** — An unexpected runtime defect, distinct from a typed error. Cancels
  the workflow, rolls back active transaction blocks, and produces a structured
  diagnostic. The language has no unchecked, Java-style application exceptions.
- **`None`** — The explicit absence of a value, expressed as a union member
  (`Customer | None`), never as an implicit null. Distinct from an empty
  string, list, map, or domain collection.

## Concurrency

- **`concurrent` block** — The language construct for bounded, structured
  concurrent execution over a collection (e.g.
  `concurrent(customerId in customerIds): getCustomer(customerId)`).
- **Concurrency budget** — The bound on simultaneously active operations within
  a `concurrent` block. The *effective* budget is the lowest of the
  application default, application maximum, capability maximum, a locally
  requested limit, and the parent workflow's budget — nested concurrency
  shares its parent's budget rather than multiplying it.
- **`BatchResult<Key, Success, Failure>`** — The default return shape of a
  `concurrent` block: per-item successes and failures keyed by input identity,
  rather than the whole batch failing on one item's error.
- **`preserveOrder`** — An explicit modifier that makes a `concurrent` block
  return results in input order instead of the default completion order.
- **Structured concurrency** — The runtime-owned guarantee that child tasks
  cannot outlive their parent: cancellation, timeouts, and error propagation
  flow through the task tree automatically, not by SDK convention.

## Transactions

- **`transaction` block** — The only construct that opens a language-visible
  database transaction. Controls exactly one transactional capability, cannot
  nest or overlap with another transaction block, and initially forbids network
  calls or workflow calls inside it.
- **Compensation** — The explicit corrective action required when a later gateway
  fails after an earlier `transaction` block already committed. Not called
  "rollback" — the committed block is not undone, it's compensated for.

## Registry and reuse

- **Registry** — The generated (not hand-maintained) index of a package or
  application's exported declarations, built by the compiler from checked
  source.
- **Catalog** — The merged view of a registry: an application's local
  declarations plus the locked-version registries of everything it imports.
  What `lang catalog find` searches.
- **Contract (contract card)** — The compact, machine-readable description the
  registry generates for one exported declaration: canonical identity,
  declaration kind, intent, input/output types, typed errors, required
  capabilities/effects, concurrency and retry guarantees, purity/idempotency/
  repeatability, examples, and source location. Searching and reusing a
  declaration should require only its contract, not its source.
- **Canonical identity** — A declaration's true package/version/symbol identity
  in the registry, as opposed to any local **alias** a consuming file uses to
  refer to it (`use company.time as businessTime`). Aliases are local
  spellings only; they never change what the registry considers the
  declaration to be. A package's version is declared in its own manifest
  (ADR 0032), not at the `use` site.
