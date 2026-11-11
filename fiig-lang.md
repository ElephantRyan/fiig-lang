# Development Plan: A Registry-First Concurrent Application Language

Status: Initial planning draft
Working name: Fiig (from Scottish Gaelic "figh," to weave; see docs/decisions/0003-project-name.md)
Primary goal: Make strongly typed, concurrent application workflows easy for humans and LLMs to discover, compose, verify, and operate.

## 1. Product thesis

The language should make application architecture part of the language rather than a convention imposed through frameworks.

Its central development loop is:

1. Search the application registry for existing models, rules, activities, gateways, conversions, and workflows.
2. Compose existing operations into a workflow.
3. Add a new primitive only when the required business behavior is genuinely new.
4. Let the compiler verify types, effects, transaction boundaries, concurrency, and error handling.

The language succeeds if a developer or LLM can implement a new use case using compact operation contracts without reading most implementation source or inventing unnecessary abstractions.

## 2. Initial scope

The first versions should target:

- HTTP services
- CLI applications
- Queue and event consumers
- Scheduled jobs
- Batch processing

Interactive desktop UI state and durable workflows that survive process restarts are deferred. They require different lifecycle and persistence semantics.

The language should initially be an application-focused compiled language, not a completely general-purpose systems language.

Numerical computing is outside the initial scope, but it is an intended future one: see "Numerics library" in §5.

## 3. Agreed design principles

### 3.1 Semantic declarations

The primary declarations are:

| Declaration | Responsibility |
|---|---|
| Model | Strongly typed application data |
| Rule | Pure, deterministic business logic |
| Conversion | A specialized rule relating two types |
| Activity | Reusable coordination of rules, conversions, gateways, and other activities; may hold `transaction` blocks; never adapter-exposed (ADR 0044) |
| Gateway | One reusable semantic interaction with the outside world (ADR 0031, renamed from Step) |
| Workflow | The adapter boundary: composes rules, conversions, activities, and gateways; nothing may call it (ADR 0044) |
| Adapter | Translation between an external protocol and a workflow |

Libraries normally publish models, enums, capability contracts, rules, conversions, activities, and gateways. Applications additionally define workflows and adapters.

### 3.2 Composition rules

- Every rule here governs calls to other declarations, including a declaration calling itself. Calls to a file's own private members are allowed for every kind (ADR 0054).
- Rules call nothing but formulas and their own private helpers (ADR 0029, narrowed by ADRs 0049 and 0054); a rule never calls itself.
- Formulas are pure and may call only other formulas, including themselves (ADR 0049). Every kind may call a formula.
- Conversions are pure and may call rules and other conversions (ADR 0029) — recursive structural mapping stays pure and judgment-free.
- Activities may call rules, conversions, gateways, and other activities, never a capability (ADR 0030, widened by ADR 0044). A cycle between activities in different files is an error (B9); an activity may call itself (ADR 0054).
- Gateways may call rules and conversions.
- Gateways may not coordinate other application gateways.
- Workflows may call rules, conversions, activities, and gateways. Nothing calls a workflow, not even itself (ADR 0044).
- Adapters call exported workflows.
- Transaction blocks initially contain only rules, conversions, and transaction-compatible database gateways.

### 3.3 Strong static typing

- Named models are nominally typed. IDs and amounts are plain primitives;
  there is no newtype declaration (ADR 0055), and named-argument calls are
  the defense against swapped arguments.
- Anonymous local records may be structurally typed.
- Public declarations require explicit input, output, error, and capability contracts.
- Local variables may use type inference.
- There are no implicit conversions.
- There is no general-purpose `Any` or unchecked cast.
- External untyped data must be decoded before entering domain code.

### 3.4 Absence

The language has no `null`.

Absence is represented as a union with `None`:

```text
Customer | None
```

`None` is distinct from an empty string, list, map, or domain collection.

### 3.5 Models and invariants

- Model fields are required unless their type includes `None`.
- Models are immutable by default.
- The compiler should perform copy elision and structural sharing.
- A `copy` operation produces a modified model without reconstructing every
  field by hand; no in-place mutation is exposed at the language level
  (`docs/specification/ownership.md`, `docs/decisions/0023-ownership-resolved-as-copy-sugar.md`).
- Stable, pure, context-independent invariants may be declared on models.
- Contextual business validation belongs in rules.
- Validation requiring a database, clock, network, randomness, or configuration belongs in gateways and workflows.
- Separate lifecycle models are preferred over temporarily invalid objects, such as `NewCustomer` and `Customer` rather than `Customer` with an absent ID.

### 3.6 Errors and faults

Expected failures are typed:

```text
Result<Customer, CustomerError>
```

Unexpected runtime defects are faults. Faults cancel the workflow, roll back active transaction blocks, and produce structured diagnostics. The language should not have unchecked Java-style application exceptions.

### 3.7 Concurrency

Concurrency is a language and runtime feature, not an SDK-specific abstraction.

The runtime owns:

- Task scheduling
- Suspension and resumption
- Structured task ownership
- Cancellation
- Timeouts
- Error propagation
- Backpressure
- Concurrency budgets
- Integration with network and database I/O

The initial collection syntax to validate is:

```text
workflow retrieveCustomers(customerIds: List<string>) {
    concurrent(customerId in customerIds):
        getCustomer(customerId)
}
```

Safe defaults:

- Concurrency is always bounded.
- Completion order is the default for speed.
- `preserveOrder` is an explicit modifier.
- Per-item failures are preserved in a `BatchResult` by default.
- Cancellation propagates to every child operation.
- Nested concurrency shares hierarchical budgets and cannot multiply without bound.
- Large inputs may use streams and backpressure.

Effective concurrency is limited by the lowest applicable value among:

- Application default
- Application maximum
- Capability maximum
- Locally requested limit
- Parent workflow budget

### 3.8 Transactions

- Only an explicit `transaction` block creates a language-visible transaction.
- Reads outside `transaction` do not implicitly create an application transaction.
- Reads inside `transaction` participate in the current transaction and can observe preceding writes.
- Writes initially require an active transaction block.
- One transaction block controls exactly one transactional capability.
- Transaction blocks may not nest or overlap.
- Network calls and general workflow calls are initially prohibited inside transaction blocks.
- A workflow may contain multiple sequential transaction blocks.
- Failure after an earlier commit requires explicit compensation; it is not described as rollback.

### 3.9 Files

Every public model, rule, conversion, activity, gateway, workflow, and adapter has its own small file. The compiler enforces one primary semantic declaration per file.

A file's one public declaration may be followed by a `private { ... }` block of helper functions and models visible only inside that file (ADR 0054). Private members are not primary declarations, never appear in the registry, and may not appear in the public declaration's signature. A helper takes its file's kind: a rule's helpers are pure, an activity's may call what an activity may.

### 3.10 Imports

Package and symbol aliasing are normal language features:

```text
use company.time as businessTime
use standard.time as systemTime
```

Aliases are local spellings. Registry entries retain canonical package, version, and symbol identities. Unqualified wildcard imports should be prohibited. A package's version is declared in that package's manifest (ADR 0032), never written at the `use` site itself.

### 3.11 Registries

Registries are generated compiler artifacts rather than hand-maintained runtime service locators.

Each exported declaration provides a compact contract containing:

- Canonical identity and version
- Kind of declaration
- Intent
- Input and output types
- Typed errors
- Required capabilities and effects
- Concurrency and retry guarantees
- Purity, idempotency, and repeatability properties
- Examples
- Source location

The application catalog merges local declarations with the exact locked versions of imported package catalogs. Search should progressively reveal names, compact contracts, examples, and finally source.

## 4. Recommended implementation strategy

Build a real language front end while initially targeting an established runtime.

This provides control over syntax, types, effects, and concurrency semantics without first building a garbage collector, machine-code backend, network stack, package host, and debugger.

Recommended first implementation:

- Implement the compiler and tooling in Go.
- Initially emit Go or a small executable intermediate representation.
- Hide generated target code from normal application development.
- Own concurrency through the language runtime rather than exposing raw goroutines.
- Allow privileged runtime capability providers to use host libraries.
- Do not allow application source to import arbitrary Go, Java, JavaScript, C, or other foreign code.

Before committing to Go, complete a short implementation spike comparing a Go target with a JVM target. The decision should consider compiler complexity, cancellation behavior, database and network integration, cross-platform distribution, debugging, and the team's implementation experience.

## 5. Milestone roadmap

### Milestone 0: Charter and executable examples

Objective: Prove the language model on paper before building a compiler.

Deliverables:

- A concise language charter with goals and non-goals
- Ten representative example programs
- A terminology document
- Initial syntax grammar sketches
- Decision records for unresolved semantic choices
- A catalog of bug classes the compiler must reject

Required examples:

1. Read one customer.
2. Concurrently retrieve many customers with partial failures.
3. Convert scalar models and concurrently convert a collection.
4. Create a customer using pure validation and an external uniqueness check.
5. Update and reread data inside one transaction block.
6. Perform two sequential transaction blocks around a network gateway.
7. Compose two child activities into a parent workflow.
8. Call one workflow from both an HTTP adapter and a CLI adapter.
9. Import two aliased time libraries.
10. Compile a deliberately invalid program demonstrating type, effect, retry, concurrency, and transaction errors.

Exit criteria:

- The ten programs are readable without prose explaining each line.
- The same concepts behave consistently across examples.
- At least five important bug classes cannot be represented as valid programs.
- The examples demonstrate meaningful advantages over implementing the same architecture as a Go or Java framework.

### Milestone 1: Parser, formatter, and source model

Objective: Establish canonical source syntax and excellent diagnostics.

Deliverables:

- Lexer and parser
- Source-span-preserving abstract syntax tree
- Canonical non-configurable formatter
- One-declaration-per-file validation
- Imports and aliases
- Parser diagnostics with structured machine-readable output
- Snapshot tests for every syntax form

Initial syntax only needs:

- Models
- Enums and tagged unions (ADR 0040 — typed errors are enums, not a separate kind)
- Rules and conversions
- Gateways and capability requirements
- Workflows
- Calls, bindings, conditionals, and exhaustive matching
- `Result`, `None`, lists, records, and generics
- Basic `concurrent` and `transaction` syntax

Exit criteria:

- Every example from Milestone 0 parses and formats deterministically.
- Reformatting already formatted source makes no changes.
- Diagnostics identify the exact declaration and source span.

### Milestone 2: Type and effect checker

Objective: Make the key architectural guarantees compile-time properties.

Deliverables:

- Nominal model typing
- Generic collection, `Result`, union, and `None` types
- Exhaustive pattern matching
- Explicit conversion resolution
- Public-signature validation
- Rule-purity checking
- Capability/effect propagation
- Workflow error-set propagation
- Transaction-scope checking
- Concurrency result typing
- Ownership design document (`docs/specification/ownership.md`) — concludes
  no language-level ownership feature is needed; `copy(...)` sugar
  (§3.5) replaces it, so there is no implementation left to defer

Transparent type aliases were considered for this milestone but dropped
outright, not deferred — see
`docs/decisions/0022-transparent-aliases-dropped.md`.

Programs that must fail:

- Passing an `int` to `getCustomer`
- Calling a gateway from a rule
- Calling a network gateway inside `transaction Database`
- Nesting transaction blocks
- Ignoring an unhandled typed error
- Returning `None` where `Customer` is required
- Using an ambiguous conversion
- Omitting a required model field
- Creating an unbounded concurrency block

Exit criteria:

- The checker rejects every invalid example with one focused diagnostic.
- Valid examples require no dynamic casts or unchecked suppression.
- Exported contracts can be generated solely from checked declarations.

### Milestone 3: Registry and LLM-facing tools

Objective: Make reuse and discovery a first-class workflow.

Deliverables:

- Package catalog generation
- Merged application catalog, split into a default catalog (rules,
  conversions, activities, gateways) and a separate composition catalog (workflows), per
  `docs/decisions/0008-separate-composition-catalog.md`
- Versioned, content-hashed registry format
- Command-line search by intent, type, effect, error, and guarantee, scoped
  to the default catalog unless the composition catalog is explicitly
  requested
- Compact contract-card output
- Dependency and caller graph
- Same-signature sibling hints: surfacing other exported declarations with an
  identical input/output type signature as a plain fact, not a similarity
  judgment or a gate (`docs/decisions/0012-duplicate-operation-hints.md`)
- Machine-readable JSON output for agent tooling

Example commands:

```text
lang catalog find "retrieve customer by id"
lang catalog find --input string --output Customer
lang catalog find --pure --output string
lang catalog find --requires Database --idempotent
lang catalog find --error OrderError
lang catalog find --constant code=ACME
lang catalog find --pure --leaf
lang catalog explain customers.getCustomer --compact
```

`catalog explain` already reports a declaration's dependencies (who it
calls — part of its own contract card) and callers (who calls it — derived
on demand from every other card's dependencies, computed, not stored, the
same treatment same-signature sibling hints already get).

Exit criteria:

- A developer can implement the customer retrieval workflow without searching source files.
- A benchmark LLM can select existing rules, activities, and gateways using only model definitions and compact registry contracts.
  A first, single-task trial is done (`docs/specification/llm-composition-trial.md`); the
  fuller multi-task version against real feature requests is Milestone 7's job, not this one's.
- Registry output remains stable across identical builds.

### Milestone 4: Executable runtime and structured concurrency

Objective: Run the first useful programs with safe concurrency.

Deliverables:

- Executable compiler target or interpreter
- Structured task tree
- Bounded concurrent collection processing
- Application, capability, local, and hierarchical concurrency budgets
- Completion-order and preserved-order modes
- `BatchResult<Key, Success, Failure>`
- Cancellation and deadlines
- Retry with backoff and jitter for operations whose contracts allow it
- Streams and basic backpressure
- Deterministic test scheduler and controllable clock
- Fault reporting with task and operation context

Primary vertical slice:

```text
workflow retrieveCustomers(customerIds: List<string>) {
    concurrent(customerId in customerIds):
        getCustomer(customerId)
}
```

Scale tests:

- Process 10,000 simulated customer IDs without creating 10,000 simultaneously active operations.
- Never exceed the configured capability limit.
- Cancel all active children when the parent is cancelled.
- Preserve every input identity in successes and failures.
- Demonstrate bounded memory with a streamed input.
- Demonstrate that nested concurrency respects the parent budget.

### Milestone 5: Capabilities, database transactions, and adapters

Objective: Build a complete service or CLI without exposing host-language frameworks.

Deliverables:

- Capability provider configuration
- Read and transactional database capability contracts
- Explicit transaction blocks
- Transaction-aware reads
- Commit, rollback, timeout, and retry behavior
- One reference database provider
- HTTP client and server capabilities
- HTTP adapter declarations
- CLI adapter declarations
- Generated OpenAPI from endpoint types and mappings
- Structured tracing across workflows, activities, gateways, and concurrent children

Exit criteria:

- One sample application exposes the same workflow through HTTP and CLI.
- The application contains no host-language imports.
- A read after a write inside one transaction block sees the updated value.
- A network call inside a transaction block is rejected.
- Adapter documentation and implementation are generated from the same checked contracts.

Reopened (2026-10-10): "capability contracts" was implemented as Go
interfaces rather than fiig declarations, so "contains no host-language
imports" held only of `.fiig` files while every provider was Go. ADR 0043
finishes the deliverable as written; ADR 0042 explains why the gap went
unnoticed.

### Milestone 6: Packages and shared libraries

Objective: Support deliberate reuse across services.

Deliverables:

- Library and application package types
- Semantic versioning policy
- Lockfile with exact versions and content hashes
- Package and symbol aliases
- Catalog import and merge
- Contract-level upgrade diff
- Deprecation metadata
- Organization package source support
- Security review of imported gateway capabilities

Exit criteria:

- Two applications can deliberately use different versions of one time library.
- Upgrading produces a readable contract diff.
- Imported gateways cannot gain undeclared capabilities.
- Local aliases never change canonical registry identity.

### Alpha release: downloadable toolchain

Status: the toolchain is done (2026-11-03, `v0.1.0-alpha.1`; both exit
criteria verified against the published Windows archive with no Go on
`PATH`). The alpha also includes the language features in the next
section, owner's decision 2026-11-03.

Objective: Let someone without this repo or a Go toolchain download fiig and
build a program. This is a cross-cutting goal, not a milestone: it does not
wait for Milestone 7, and it carries no stability promise. It distributes the
toolchain only; the public package marketplace remains a guardrail (§9).

Deliverables:

- Release builds of one `fiig` binary (`check`, `run`, `serve`, `fmt`,
  `catalog`, `version`) for Windows, macOS, and Linux, including the
  `libraries/sql` provider (owner's decision 2026-11-03; the separate
  `*-cli` tools stay for the repository's own use). Built 2026-11-03:
  `cmd/fiig`, its own module, over `tooling/cli/*`
- Every tool reports its version, and lockfiles record the toolchain version
  — done 2026-11-03 (`compiler/version`, stamped by ldflags; lockfile
  `toolchain`)
- A container image of `fiig` for hosting an application (`Dockerfile`,
  `docs/deploying.md`), linux/amd64 and linux/arm64
- A license, chosen by the project owner, included in the repo and archives
- README and getting-started guide: install, build the customer example, run it,
  and see a deliberate error reported — done 2026-11-03 (`README.md`,
  `docs/getting-started.md` over a new `examples/getting-started` ratings
  service, since customer-service needs tables the guide would have to
  create first)
- Known-limitations page listing what the alpha does not support (Redis, Kafka,
  decimal money, list literals until ADR 0057 is decided, and so on) — done
  2026-11-03 (`docs/known-limitations.md`; decimal and list literals have
  since shipped)
- Repo under version control, with stray build artifacts removed — `git
  init` and `.gitignore` done 2026-11-03; private repository
  github.com/ElephantRyan/fiig-lang
- Release pipeline that builds and attaches artifacts from a version tag —
  written 2026-11-03 (`.github/workflows/release.yml`, `tooling/release`,
  `docs/releasing.md`); first release `v0.1.0-alpha.1` published
  2026-11-03 with archives and `ghcr.io/elephantryan/fiig`

Exit criteria:

- On a machine with no Go installed, a downloaded archive runs the
  getting-started example end to end.
- A deliberately invalid program fails `check-cli` with the expected bug-class
  code.

### Numerics library: competing with NumPy

Objective: A numerics library robust enough that fiig can compete for
numerical and scientific work, in the role NumPy plays for Python. Like the
alpha release, this is a cross-cutting goal, not a milestone, and it is not
scheduled. It is recorded so that design decisions made before it is built
don't close it off (recorded 2026-10-28, ADR 0048 and 0063 review).

What it requires, as currently understood:

- Native kernels: array storage and vectorised operations implemented below
  fiig. The native set is closed today (ADR 0042; ADR 0049 §2.1, K5), so the
  library needs either a decided extension point for native code or an FFI.
  This is the main blocker, and the FFI ceiling accepted for the solo phase.
- IEEE semantics on its own types. Scalar `float` keeps ADR 0048's faults, as
  Python's `math` does; the library's array types may carry `NaN` and `Inf`,
  as NumPy's do, behind a boundary the type system can see.
- Non-faulting scalar forms where a pre-check can't be exact: `math`'s float
  `OrNone` members (ADR 0063 §2), with `expOrNone` and `powOrNone` next.
  Forms with exact pre-checks (`sqrtOrNone`, `logOrNone`) can be fiig-written.
- Collections and generics strong enough to express n-dimensional arrays.

Candidate exit criteria, to be fixed when this becomes a milestone:

- Standard numerical workloads (least squares, FFT, descriptive statistics)
  written in fiig and checked against NumPy's results.
- Performance on those workloads within a stated factor of NumPy.

### Alpha release: language features

Objective: The language features a small real web service needs, so the
first application (abs-recommendations, an Audiobookshelf ratings sidecar)
can be written entirely in fiig. Owner's decision 2026-11-03: these are part
of the alpha and are finished before work on that application starts, not
in parallel with it. A feature is listed because a service like that one
needs it, and each is built through its ADR as usual.

Done before this list: `fiig serve`, the container image, and HTTP bodies
writing a model as a plain object (2026-11-03).

Designed, build only:

1. ADR 0065 collections: `first`, `last`, `contains`, `sorted` with a key,
   and `Map`. Done 2026-11-03.

Need an ADR first, in build order:

2. String methods: `length` (and what a character is), trimming, case,
   splitting, prefixes. Done 2026-11-03 (ADR 0067).
3. `Random`'s operations and runtime provider (ADR 0042 names the primitive
   but not its operations): random bytes and tokens. Done 2026-11-04 (ADR
   0068), with named arguments on every builtin, and `Clock` and `Random`
   as local effects that activities and gateways call directly.
4. Request headers and cookies in `http` routes, read and set: bearer
   tokens and sessions. Done 2026-11-04 (ADR 0069), with content types
   declared on each route (`produces`, `consumes`), a prelude
   `HttpResponse<B>` and `Cookie`, and `examples/accounts`.
4a. CORS for `http` adapters, so a browser front end on another origin
   can call the service (owner's decision 2026-11-04, deferred out of
   ADR 0069 into its own ADR). Done 2026-11-06 (ADR 0070): `fiig serve
   -allow-origin`, with methods derived from the routes.
5. Configuration values reachable from fiig code, from the environment:
   service URLs and secrets. Designed in ADR 0071 (accepted 2026-11-07:
   `config` declarations, `Secret`, `-env-file`, `fiig config`); it also
   settled ADR 0070 §1's revisit (origins stay deployment input). Done
   2026-11-07, with `fiig config`, manifest renames, and a per-vertical
   example (`checkout-core`, `checkout-clientvertical`).
6. The JSON shape of an enum and a `Result` in an HTTP body (today the
   tagged form `fiig run -json` uses). Done 2026-11-08 (ADR 0072): a
   payload-free enum is a string, a payload enum an object with a
   `variant` member, request bodies decode the same shapes, and
   `examples/shipping-quotes`.
7. ~~JSON decoding in fiig code (ADR 0062's library).~~ Done 2026-11-09,
   ADR 0073: a prelude `json` group, `json.decode` typed by context,
   `json.encode` to bytes in declared order, keywords as field names,
   `from json "name"`, J1 and J2, and the adapter on the same reader,
   decoder, and encoder. `examples/event-inbox`.
8. ~~An HTTP client package written in fiig over `Socket`.~~ Done
   2026-11-10, ADR 0074: `fiig.http`, built into the toolchain, with one
   `send`, statuses as data, no redirects followed, a whole-exchange
   timeout (`Socket` deadlines and `SocketError.TimedOut`), and
   `examples/inbox-sender`.
8a. Hashing and HMAC: SHA-256 and `hmacSha256` taking a `Secret` key
   (ADR 0071 §4), for webhook signatures, signed cookies, and stored
   random keys; plus a deliberately slow password hash (Argon2id or
   scrypt) if accounts store passwords (owner's decision 2026-11-07).
8b. A JSON request body read raw: a route that consumes
   `application/json` binding `bytes`, so a webhook's signature can be
   checked over the bytes before `json.decode` (with 8a; known
   limitations, found by ADR 0073's dogfooding).
8c. The JSON form of `bytes` (base64, as Go sends `[]byte`, or an array of
   numbers), so a `bytes` field stops being J1 (ADR 0073).
8d. An enum as a path segment or query value (ADR 0072 open question 2).
8e. A union of enums that share a variant name, in a body (`OrderError |
   CustomerError`, both with `NotFound`; ADR 0072 open question 1).
8f. `fiig openapi`: print the document `GenerateOpenAPI` builds. Likely a
   command, not an ADR.
9. A checked `Database` capability: its calls typed and its use held to a
   gateway's `requires`, as P7 does for capability groups written in fiig.
10. ADR 0066 declared SQL row types.
11. Model invariants (§3.5), or a recorded decision that rules cover them.

Exit criteria:

- Each feature is implemented, tested, and documented, with a corpus
  example.
- `docs/known-limitations.md` no longer lists anything above.

### Milestone 7: Self-hosted protocol and provider layer

Objective: Move the native boundary down to the socket so that every
transport, protocol, and domain capability is written in fiig (ADR 0042).

Deliverables:

- `bytes` primitive type — done (ADR 0058, 2026-10-20)
- Binary pattern matching — done (ADR 0058, 2026-10-20): Erlang's model,
  `<<kind: u8, length: i32be, body: bytes(length - 4), rest: bytes>>`, with
  mandatory byte order, sub-byte fields, and a fault, not truncation, when
  a built value does not fit
- List literals and `append` — done (ADR 0057, 2026-10-20): `[a, b]`,
  element type inferred, `[]` typed by context, `xs = append(xs, x)` in
  amortized constant time, `==` compares contents
- Sequential iteration over `List<T>`
- `while` loops — done (ADR 0059, 2026-10-25): `while cond { }`, legal in
  every kind, stopped by the call's deadline rather than an iteration cap
- Kind-internal declarations (ADR 0054, implemented); lambdas decided against, for now (ADR 0056)
- Closed primitive capability set (`Socket`, `Clock`, `FileSystem`, `Random`), enforced; ADR 0060 folded `TlsSocket` into `Socket`
- `Socket` operations and the `Connection` handle — done (ADR 0060, 2026-10-26):
  `connect`/`connectTls`/`startTls`/`read`/`write`/`close`, connections owned
  by the top-level call, a scripted fake (`socket-script` in `run-cli -caps`)
- `\r` and `\$` string escapes — done (ADR 0061, 2026-10-26); CR LF inside
  a literal reads as LF, so a string doesn't depend on the checkout
- `interface` declarations and manifest-declared providers (ADR 0043) — done
  (2026-10-23): `interface`, `capability` groups written in fiig, local or
  imported by path tail (`use acme.http.Client`)
- Provider values, with structural verification against `provides` — done
  (ADR 0043, 2026-10-23): `return Stripe`, `Provider.charge(...)`, P1/P2
- Activity as reusable effectful coordinator; workflow as the uncallable adapter boundary (ADR 0044)
- Model embedding, Go-style: explicit, flattening, no subtyping
- One reference wire protocol written in fiig
- JSON encoding and decoding in fiig
- Postgres wire protocol in fiig, replacing `libraries/sql`
- Interpreter performance work for byte-stream workloads

Exit criteria:

- A wire protocol is implemented in fiig over `Socket` with no Go.
- Two providers fulfilling one interface are chosen at runtime by an ordinary rule that returns the provider (ADR 0043 §3, not a string name). Adding a third is a new package, a manifest dependency, and one edit to that rule; the interface, the existing providers, and every `Provider.op(...)` call site stay unchanged.
- A non-primitive capability with no fiig fulfiller is a checker error.
- No domain capability is backed by Go.

### Milestone 8: Developer experience and production hardening

Objective: Make the language pleasant enough for sustained real development.

Deliverables:

- Incremental compilation
- Debugger or structured workflow trace viewer
- Test framework with fake capability providers
- Package documentation generation
- Reproducible builds
- Security and resource-limit audit
- Profiling and runtime metrics
- Migration and compatibility policy

Production gate:

- Build and operate one non-critical internal service.
- Compare implementation size, defect rate, latency, memory, and change effort with an equivalent service in an established language.
- Run the LLM composition benchmark against real feature requests.

### Milestone 9: IDE support

Objective: Make fiig fully usable in IntelliJ IDEA with the same feedback the compiler gives on the command line.

Deliverables:

- Stable machine-readable diagnostics from `check-cli` (JSON: file, line, column, end position, message, code)
- `.fiig` file type with registered extension, decided per the prototype backlog
- TextMate grammar generated from or checked against the lexer's token definitions, for syntax highlighting
- Language server wrapping the compiler: live diagnostics, hover, and go-to-definition
- Completion driven by registry contracts and values in scope
- Caller views and find-usages on registry identities
- Rename across references, including across packages from Milestone 6
- Quick fixes for compiler diagnostics where the fix is mechanical, such as adding a missing import or handling an unhandled `Result`
- Comment toggling, with the line comment prefix declared in the file type
- Format on save through `fmt-cli`
- IntelliJ plugin connecting to the language server through the platform's LSP API, or a direct integration if the LSP API is unavailable in the target IDE edition
- Plugin packaging and a README covering installation from disk

Exit criteria:

- A new `.fiig` project opens in IntelliJ with highlighting, and a deliberately invalid program shows its errors at the correct lines.
- Completion offers registry-declared contracts and in-scope values.
- Go-to-definition and find-usages work across packages from Milestone 6.
- Format on save leaves a file unchanged when the formatter has already run.

## 6. First prototype backlog

Create these issues in roughly this order:

1. Write the language charter and explicit non-goals.
2. Choose a temporary project name and file extensions.
3. Write the ten canonical example programs.
4. Define the lexical grammar and identifier conventions.
5. Define the grammar for models, enums, rules, conversions, activities, gateways, workflows, and adapters.
6. Define `Result`, unions, `None`, and pattern matching.
7. Define concurrency collection syntax and all defaults.
8. Define transaction-block syntax and prohibited operations.
9. Specify registry contract cards and canonical identities.
10. Implement the lexer, parser, and formatter.
11. Implement nominal types and required model fields.
12. Implement rule purity and declaration call restrictions.
13. Implement enums and exhaustive matching.
14. Implement effect and capability propagation.
15. Generate the application registry.
16. Implement a simulated gateway provider and deterministic scheduler.
17. Execute the customer retrieval vertical slice.
18. Add bounded concurrency, cancellation, and `BatchResult`.
19. Build registry search and compact output.
20. Run the first LLM composition evaluation.

## 7. Proposed repository structure

```text
language/
    docs/
        charter.md
        terminology.md
        decisions/
        specification/
    examples/
        customer-service/
        order-processing/
        time-library/
        invalid-programs/
    compiler/
        lexer/
        parser/
        syntax/
        types/
        effects/
        registry/
        diagnostics/
        backend/
    runtime/
        tasks/
        cancellation/
        streams/
        capabilities/
        database/
        network/
    tooling/
        formatter/
        language-server/
        catalog-cli/
    tests/
        syntax/
        compile-pass/
        compile-fail/
        runtime/
        scale/
        llm-evaluations/
```

This is a target organization, not a requirement to create every directory immediately.

## 8. Decision records still needed

The following choices should be written as short architecture decision records before their implementations become expensive to change:

1. Go target versus JVM target for the bootstrap implementation.
2. Exact syntax for declarations and blocks.
3. Whether a two-way conversion is one declaration with two directed contracts.
4. Whether temporary owned mutation belongs in the first release.
5. Whether public workflow error sets are explicit, inferred, or inferred and then checked against an explicit contract.
6. Whether every exported workflow appears in the default catalog search or in a separate composition catalog.
7. ~~How workflows that call workflows propagate and contextualize
   errors.~~ Moot as of ADR 0044: a workflow is a root and nothing may
   call one, so there is no cross-workflow propagation to specify.
   Reusable effectful coordination is an `Activity`.
8. Whether pure CPU rules use the same `concurrent` syntax or an explicitly named `parallel` mode.
9. Whether retries are only explicit or may be supplied by capability configuration.
10. How transaction-compatible read gateways bind to an active transaction.
11. What metadata is mandatory before a rule, activity, or gateway may be exported.
12. How duplicate-operation warnings are calculated and overridden.
13. Whether library authors get an open extension point for business
    logic (traits/interfaces, first-class function values, or neither).
    **Mostly resolved, along the ownership axis rather than the
    pure/effectful one originally assumed.** ADR 0043 settles effectful
    extension by a *third party*: ship a package that fulfills a
    capability contract, selected by name. ADR 0046 settles extension —
    pure or effectful — over an implementation set the authoring code
    *owns*: the cases are enum variants, dispatch is an exhaustive
    match, and completeness is compiler-enforced rather than declared.
    Not first-class functions, and not tied to iteration as originally
    guessed. **Still open:** pure dispatch where a *third party* supplies
    the implementation — a discount policy shipped by someone who cannot
    edit the enum. That is interface-side work and belongs with the
    `capability`→`interface` rename; bounded generics will force the
    question. An earlier edit struck this item as fully resolved, which
    overstated what 0043 decided.
14. How far a kind's body may declare things, and whether lambdas belong
    in the language. **Private members decided and implemented in ADR 0054
    (Accepted, implemented 2026-10-17). Lambdas decided against, for now, in
    ADR 0056 (Accepted 2026-10-19). Only private enums are still open.** The proposal
    from review: each top-level kind (`rule`, `conversion`, `activity`,
    `gateway`, `workflow`) is a unit like a Java class, with one public
    entry function and private helpers declared inside it. Candidates for
    interior declaration, none of which may escape the kind:
    - private helper functions, called from the entry function, for readability
    - lambdas
    - `for` loops (ADR 0053, already a statement)
    - models used only inside the kind

    Open questions before an ADR:
    - **Calls out of a kind.** §3.2 and ADRs 0029/0030 decide which *kinds*
      may call which, and the proposal leaves that alone. Interior helpers
      call only within their own kind. Confirm.
    - **Rule isolation.** ADR 0029 restored "rules call nothing." Private
      helpers are calls within the rule. This loosens that sentence, so the
      ADR must say so explicitly.
    - **Escape (decided in ADR 0054).** A private member escapes if it is
      referenced from outside its file. Private members are never in the
      registry and never in the public signature. Each kind's file is its
      class: one public entry, and everything else in the file is private.
    - **Lambdas need function types, and function types need generics** for
      anything useful such as `map` or `filter`. Generics are deferred (see
      `docs/specification/tour.md`). Decide whether lambdas wait for generics
      or ship as monomorphic, non-generic function values first.
    - **Capture.** Java requires captured locals to be effectively final.
      Fiig's immutable values make the same rule natural. Decide whether a
      lambda may capture a reassignable local (ADR 0053), and whether it may
      capture at all.
    - **Relation to item 13.** Lambdas here are the first-class function
      values that item 13 names as one of its options.

## 9. Guardrails against over-engineering

Do not build the following until the earlier milestones demonstrate demand:

- A self-hosting compiler
- A native machine-code backend
- A custom garbage collector
- Distributed transactions
- Durable multi-day workflows
- A macro system
- User-defined syntax
- Runtime reflection
- A public package marketplace
- A general foreign-function interface
- Multiple async runtimes or schedulers
- Desktop UI frameworks

Every major feature should answer at least one of these questions:

- Does it make invalid application behavior unrepresentable?
- Does it reduce the context required to understand or modify a workflow?
- Does it improve safe reuse through the registry?
- Does it make concurrency, failure, or effects more predictable?

## 10. Validation metrics

### Language safety

- Percentage of expected failures represented in types
- Number of effect violations caught at compile time
- Number of unsafe transaction compositions rejected
- Number of concurrency-limit violations in runtime tests

### Developer experience

- Time to implement a workflow using existing declarations
- Compiler feedback cycles required
- Number of source files that must be opened
- Size and clarity of diagnostics

### LLM effectiveness

- Existing-operation selection accuracy
- Unnecessary new-rule or new-gateway creation rate
- Hallucinated-symbol rate
- Context tokens required per workflow
- Compilation attempts required
- Percentage of tasks completed using registry contracts without implementation source

### Runtime behavior

- Peak active tasks versus configured limits
- Cancellation latency
- Peak memory for large streamed inputs
- Throughput and tail latency
- Retry amplification during downstream failure

## 11. Recommended immediate starting point

Start with a two-week design-and-prototype cycle rather than a broad compiler build.

### Week 1

- Finalize the charter and non-goals.
- Choose a temporary name and file extension.
- Write the ten canonical programs by hand.
- Write compile-error examples alongside them.
- Resolve syntax only where the examples require it.
- Draft the compact registry representation for each declaration.

### Week 2

- Implement a minimal lexer and parser for models, rules, activities, gateways, workflows, and `concurrent`.
- Implement a canonical formatter.
- Generate registry JSON without yet executing programs.
- Build `catalog find` over the generated registry.
- Test whether a developer or LLM can construct `retrieveCustomers` using only compact catalog entries.

At the end of the cycle, make a deliberate go/no-go decision:

- Continue as a language if the contracts and checker provide real leverage unavailable from a framework.
- Recast it as a framework or code generator if the proposed semantics remain mostly naming conventions.
- Revise the constructs if the examples repeatedly require escape hatches.

## 12. First demonstrable release

The first public-quality demonstration should be intentionally narrow:

1. Define `Customer` and `CustomerError`.
2. Declare a `getCustomer` gateway backed by a simulated capability.
3. Declare `retrieveCustomers` using bounded concurrency.
4. Return keyed successes and failures in completion order.
5. Support `preserveOrder`.
6. Enforce application and capability concurrency limits.
7. Cancel child calls when the workflow is cancelled.
8. Generate and query the application registry.
9. Produce clear compile errors for incorrect IDs, missing error handling, and unbounded concurrency.
10. Ask an LLM to build a second workflow using only the generated registry.

If this vertical slice feels substantially clearer and safer than its host-language equivalent, the project has earned its next milestone.
