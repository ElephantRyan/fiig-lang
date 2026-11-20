# ADR 0047: Streams Are a Third Adapter Kind; a Stream Route Declares Key, Disposition, and Replay

Status: Accepted, as amended by ADR 0111 (2026-11-20), which says where elements come from and changes §4-§6.
Date: 2026-10-12
Related: ADR 0009/0021 (adapter route binding — this reuses both), ADR 0044
(workflow as the uncallable adapter boundary), ADR 0042 (socket floor, and
*protocol*-level streaming, which this record deliberately does not touch),
ADR 0014 (`idempotent`), ADR 0010/0031 (one capability per gateway), ADR
0046 (the "single statically enumerable artifact" criterion). Closes
Milestone 4's deferred "Streams and basic backpressure" deliverable
(`fiig-lang.md:382`) and its unmet exit criterion, "Demonstrate bounded
memory with a streamed input" (line 401).

## Context

Milestone 4 listed streams and backpressure as a deliverable and shipped
without them, deferred as unattested. The question that reopened it was
whether ADR 0042 set fiig's native floor too high for Flink-style stream
work — if wire protocols are written in fiig over `Socket`, can fiig do
streaming at all?

**The floor is not the problem, and Flink is the evidence.** Flink's
operator code never touches bytes. Byte handling is confined to a
pluggable layer at the edges — `DeserializationSchema` at sources,
`TypeSerializer` for state — and the operators themselves (`map`,
`filter`, `keyBy`, `window`, `process`) receive typed records. That is
exactly fiig's gateway/rule split, drawn in the same place for the same
reason. On the bytes axis Flink agrees with fiig's design.

Two different things share the word "stream", and conflating them is what
made this look like a floor question:

- **Protocol-level streaming** — a continuous byte stream off a socket
  with incremental framing. Already decided in ADR 0042: *"a loop inside
  that single gateway body rather than gateway recursion ... so iteration,
  not a matrix change, is what unblocks streaming protocols."* Nothing
  here changes it.
- **Application-level stream computation** — a long-lived flow of domain
  events. Undecided until now, and the subject of this record.

What fiig actually lacked was an execution shape. Every declaration is
params in, `Result` out, terminate. The live question was never "can fiig
loop" — it was whether a non-terminating shape escapes the gateway into
application code.

## Decision

### 1. A stream is a third adapter kind, not a new execution shape

The engine owns the loop and invokes fiig once per element. `adapter` is
already fiig's "the outside world drives" boundary: uncallable, no body of
statements, routes that name a workflow. A stream source is that same
boundary with a different trigger.

```ebnf
adapter-kind  ::= 'http' | 'cli' | 'stream'
route         ::= http-route | cli-route | stream-route
stream-route  ::= 'source' string-literal 'key' identifier '->' call response-mapping
```

`source` rather than `topic`: fiig should no more hardcode Kafka's
vocabulary than `adapter-kind` hardcodes REST's. `AdapterKind` was always
going to grow past two — nothing in the grammar or
`compiler/syntax/ast.go:130` treats `http`/`cli` as exhaustive.

Per-key state is a capability, so a stateful operator is a `gateway`, not
a `rule`. That consequence is correct rather than merely tolerable: state
*is* an effect, and fiig forcing it through a declared capability is the
whole point of the kind system.

### 2. The three alternatives, and why they lose

ADR 0042 set fiig's own admission test: a feature gets in only if it
*cannot be expressed in fiig at all*, not merely that it would be
inconvenient. Continuous flow **is** expressible with an engine driving
per-element invocations, so anything that adds an execution shape fails
that test before its merits are even weighed.

**A `stream` declaration that declares the topology** (a DAG of source,
operators, sink, in the Beam/Flink SQL/dbt/ksqlDB mould) is the better
long-term answer and is **deferred, not rejected.** It serves the
navigability thesis better than this record does, because the whole path
an event takes becomes one readable artifact. It is blocked on things fiig
does not have: `List<T>` iteration and list literals land in Milestone 7,
and windowing needs an event-time model that `Clock` does not provide.
Deciding it now would mean guessing at event time with no attestation —
the same thing list-literal syntax was deliberately not decided as a side
effect of ADR 0046. The sequencing is safe: this record's routes become
that record's sources and sinks, so nothing built here is thrown away.

**Generators / `yield` / suspendable declarations** are rejected on four
facts, not on taste. Fiig has no first-class functions (recorded as a
closed, unplanned axis), so stream combinators are unwritable. It has no
user-declarable generics, so `Stream<T>` would need prelude
special-casing by name and compose with nothing. `compiler/runtime` is a
tree-walking interpreter, and suspending/resuming a stack is the largest
implementation-complexity jump available in the component §9 guards
hardest. And it would give fiig two control-flow models, which is the
LLM-navigability cost rejected everywhere else.

**Letting the loop escape the gateway** looks cheapest and is not. It
collides with four checked invariants at once: a `concurrent` block must
declare a bound (D1) and an unbounded loop inside one has nothing to
check against; `transaction` is per-invocation, so a forever-loop inside
one holds a database transaction open indefinitely with no vocabulary in
C2/C4/C5 to describe it; F1 requires every `Result` to be handled, and a
per-element loop has no answer for what it returns; and a non-terminating
workflow cannot be an adapter route target, which is what B6 exists to
enforce.

### 3. The design test this record is built to satisfy

An HTTP route is not debuggable because it is HTTP. It is debuggable
because it manufactures four properties. A stream route is as debuggable
as an HTTP route **iff** its declaration supplies the same four:

| Property | HTTP route | Stream route |
|---|---|---|
| Bounded unit of work | one request | one element (§1) |
| Named instance | path params, request id | declared `key` (§4) |
| Typed outcome, disposed of | `Result<T,E>` → status | disposition mapping (§5) |
| Reproducible in isolation | `curl` | `run-cli` replay (§6) |

Three are grammar the checker enforces; the fourth is a tooling mode. The
point of stating the test is that "streams are debuggable" becomes a
checkable claim rather than an aspiration.

### 4. A stream route declares its element key

```fiig
adapter stream DeductionIngest {
    source "deductions.inbound" key clientId -> recordDeduction(payload: RawDeduction) {
        Ok(_)                                -> ack
        Err(DeductionError.UnknownSource(_)) -> deadLetter "deductions.dlq"
        Err(DeductionError.SourceTimeout(_)) -> retry
    }
}
```

This is not a new idea in fiig; it is a binding precedent.
`BatchResult<Key, Success, Failure>` exists precisely so a bulk operation
preserves input identity, and Milestone 4's own exit criterion is
*"Preserve every input identity in successes and failures"*
(`fiig-lang.md:400`). A stream is the same requirement over an unbounded
input.

`key` is the load-bearing field, because every other debugging affordance
depends on being able to say *which* element. Without it, "the deduction
pipeline is failing" is the most precise statement anyone — human or LLM
— can make.

**Binding reuses ADR 0021 unchanged.** A stream route has no path or
query placeholders, so its single call argument binds from the decoded
element, exactly as a body-carrying HTTP route's single unclaimed
argument does. A7 therefore applies as-is: the element must bind to a
`model`, since a `value` type has no fields to structurally decode
against. `key` must name a field of that model, which is checkable with
the same field lookup A7 already performs.

**Deliberately deferred: element metadata.** Offset, partition, and
ingestion timestamp are real needs and this record gives them no syntax,
because a stream route takes exactly one argument. Event time in
particular should be settled with the topology record in §2, not invented
here — it is a per-record property plus a watermark propagation protocol,
and nothing in the corpus attests either.

### 5. Failure disposition reuses `response-mapping`, and is mandatory

`response-arm ::= pattern '->' response-value` already exists and is
pattern-matched over the target's error enum. A stream's response values
are dispositions rather than status codes:

```ebnf
response-value ::= integer-literal [ expression ]        -- HTTP: status [ body ]
                 | 'exit' integer-literal [ expression ] -- CLI: exit code [ body ]
                 | disposition                            -- stream
                 | expression
disposition    ::= 'ack' | 'retry' | 'halt' | 'deadLetter' string-literal
```

**The mapping is required on a stream route, unlike HTTP and CLI where it
is optional.** That asymmetry is principled, not an oversight: HTTP and
CLI have a sensible default because there is a caller to return to. A
stream has no caller. Without a declared disposition an `Err` has nowhere
to go, and fiig's typed-error discipline would silently stop paying off at
exactly the boundary where it matters most — F1 works today because
*somebody* handles the `Result`.

**The mapping must be exhaustive over the target's error enum**, reusing
A9's existing machinery (`compiler/checker/stmt.go:265`). This is the
feature's strongest claim. In every streaming platform, failure
disposition is engine configuration — Kafka Streams'
`deserialization.exception.handler`, Flink's side outputs, SQS redrive
policies — which is the two-artifact navigability problem in its purest
form: the code says what happens on success and a YAML file in another
repository says what happens on failure. Declaring it makes an unhandled
stream failure mode a **compile error**, which no streaming platform
offers.

`retry` carries a cost that must be stated where it is written, not
discovered in production: retrying one element while its successors
proceed reorders that key. An engine can only preserve ordering by
blocking the partition head, trading throughput for order. See §7.

### 6. Replay is a `run-cli` mode, not a language feature

The stream analogue of `curl` is "feed one element, by key, through the
pipeline." ADR 0044 made workflow the uncallable adapter boundary, so
fiig source cannot invoke the route target — which settles where this
lives: a `run-cli` mode, alongside the existing executable-program
support, not a new callability rule. Element identity is useless if
nothing can act on it, so this ships with §4 rather than after it.

### 7. Per-key ordering: statically unverifiable, runtime-detectable, blocked-on

If the engine parallelizes by key, two elements for the same key can
interleave across workers, and a read-modify-write gateway loses an
update. Each invocation is individually well-formed — pure rule, one
capability, handled `Result`, exhaustive match — so no declaration is
ill-formed anywhere. The bug exists only *between* invocations.

**It is not statically checkable, and the reason should stop anyone from
trying to add a bug class for it.** Every check fiig has is a property of
a finite syntactic object: A1-A9 inspect one declaration or one pair,
B1-B8 one call edge, C1-C5 a call inside a lexical `transaction` block,
D1-D2 a limit expression, E1 a modifier on the callee, F1 whether a value
is consumed, G1-G3 a file's own declaration and import lists. Even A9
exhaustiveness is finite — enumerate variants, enumerate arms, compare.

Per-key ordering is a different kind of property. Stated precisely: *for
all keys k, all invocations bearing k are totally ordered and
non-overlapping in time.* That quantifies over **executions**, not
syntax. There is no finite object to inspect because the scheduler is not
in the program text, and verifying it with the scheduler's text would be
model-checking a distributed system, not type-checking a program.

**It is runtime-detectable, and that is the deliverable.** Fiig already
makes this move deliberately: ADR 0048 §2.3 *Faults* on any arithmetic
that would produce NaN or Infinity rather than letting a poisoned value
propagate. (This paragraph first cited `libraries/sql`'s fault on a
floating-point column, which ADR 0048 replaced with a decode.) The same
move works here, given a `StateStore` capability whose contract carries a
version:

```
StateStore.get(key)                         -> (value, version)
StateStore.put(key, value, expectedVersion) -> Result<None, StateError>
```

A concurrent processor for the same key loses the compare-and-swap and
receives `StateError.Conflict`. That does not prevent the interleaving,
but it converts silent data corruption into a typed, named error — and it
then composes with the rest of this record for free, because §5's
exhaustiveness *forces* a decision about what happens, and §4's declared
key says which key conflicted.

**Static detection is blocked-on, not abandoned.** Bug class C3 is this
same problem and was dropped outright (`bug-classes.md:118-126`): the
semantic rule stands, but no sound implementation was found, for two
reasons that apply here verbatim — you cannot tell a mutating gateway
call from a non-mutating one, and you cannot tell *related* operations
from unrelated ones. C3's first blocker is described there as *"missing
infrastructure a future capability-contract system could supply"*, and
ADR 0043 is that system. If contracts eventually mark operations as
mutating, C3 and this become checkable together. Record it as a
dependency so it is not rediscovered from scratch.

### 8. Fiig's own stream runtime must not break per-key ordering

This is a requirement on fiig, not a caveat about engines. The guarantee
chain for Kafka is `hash(key) % partitions` → one partition → ordered by
offset → one consumer per partition in a group. It has four breakable
links: a partition count change (which re-maps keys and leaves a key's
backlog split across two partitions until it drains), a consumer group
rebalance (where an uncommitted in-flight invocation can overlap its
successor), **concurrency inside the consumer**, and retry (§5).

Three of those belong to the engine. The third belongs to fiig: a harness
that pulls a batch and processes it in parallel — which a tree-walking
interpreter will be tempted to do for throughput — destroys per-key
ordering by itself, with a perfectly correct partition assignment. The
fiig stream runtime must process elements sharing a key sequentially,
and the deterministic test scheduler Milestone 4 already calls for is
where that gets proven.

Flink's ordering story is stronger (`keyBy` shuffles into key groups, each
processed single-threaded) but introduces the other hazard: checkpoint
recovery **replays** elements, and Flink's exactly-once is exactly-once
*state*, not exactly-once *side effect*. Fiig gateways perform side
effects. So streaming brings two hazards — replay and reordering — and
fiig already has vocabulary for one: `idempotent` (ADR 0014) is the
replay marker. There is no reordering marker, and §7's versioned
`StateStore` is its counterpart.

### 9. Registry fields

Adapter cards already carry `AdapterKind`, `Routes []string`, and
`Dependencies: adapterCallNames(d)` (`compiler/registry/registry.go:145`),
so the adapter→workflow edge is already in the dependency graph and
transfers unchanged.

What does not transfer: baking the source and disposition into the flat
`Routes` summary string makes *"what consumes this source"* and *"which
routes silently drop on failure"* answerable only by string matching.
Both want structured card fields, plus a `catalog-cli find --source`
filter — the stream analogue of the caller graph.

## Rationale

The whole-industry precedent for §1 is the FaaS model: Lambda, Azure
Durable Functions, Kafka Streams' `Processor` viewed from outside, Flink
SQL UDFs, Snowflake UDFs. All are request/response functions driven by an
external loop, and all of them work.

The argument that fiig is *well suited* to this, rather than merely
capable of it, is worth recording because it is the reason to do it at
all. Flink's hardest operational failures come from user functions being
arbitrary JVM code — performing I/O inside a `map`, mutating shared
state, being non-deterministic — all of which break replay on recovery.
Flink's documentation pleads for deterministic, side-effect-free
operators and cannot enforce it. Fiig enforces it: a rule is pure and
isolated (ADR 0029), effects are declared one capability per gateway
(ADRs 0010/0031), errors are typed and must be handled (F1). A
fiig-authored operator is *checkably* replay-safe, which is a property no
Flink user currently gets.

The three rejected alternatives in §2 were each rejected against a
written criterion rather than a preference — ADR 0042's inexpressibility
test for the execution-shape options, and four named invariant collisions
for the escaping loop.

## Consequences

- `compiler/syntax`: `AdapterKind` gains `AdapterStream`; `Route` gains a
  `Source` and `Key`; `response-value` gains a disposition form.
  `ast.go:130`'s comment ("`http` or `cli`") needs updating.
- `compiler/parser`: `stream-route`, and the `disposition` production.
  `deadLetter` takes a string literal; `ack`/`retry`/`halt` take nothing.
  These are contextual keywords, per ADR 0018.
- `compiler/checker`: `checkRouteBinding` extended — a stream route has no
  placeholder regions and exactly one call argument, which binds from the
  element and must be a model (A7, unchanged). Three new bug classes,
  proposed as a new category **I. Stream adapter errors**:
  - **I1** — a stream route's `key` does not name a field of the bound
    element model.
  - **I2** — a stream route's disposition mapping is missing, or is not
    exhaustive over the target's error enum.
  - **I3** — a stream route has more than one call argument; there is no
    second source to bind it from.
  B6 applies unchanged: a stream route must call an exported workflow.
- `compiler/registry`: structured `Source` and disposition fields on the
  adapter card (§9); `catalog-cli find --source`.
- `compiler/runtime`: per-key sequential processing (§8), proven against
  Milestone 4's deterministic test scheduler; a `run-cli` replay mode
  keyed by element identity (§6).
- A `StateStore` primitive-or-contract capability with versioned
  get/put (§7). Its placement is a real open question: it is not in ADR
  0042's closed primitive set (`Socket`, `TlsSocket`, `Clock`,
  `FileSystem`, `Random`), and by 0042's inexpressibility criterion it
  probably should not be — a state store is expressible in fiig over a
  database. That makes it an ADR 0043 contract, which means §7's runtime
  detection is **downstream of 0043 being implemented**, not available
  immediately.
- Corpus: no migration. Nothing existing is a stream. The natural first
  attestation is a deduction-ingest example, which also finally exercises
  Milestone 4's "bounded memory with a streamed input" exit criterion.
- Documentation: `fiig-lang.md:382` should distinguish protocol-level from
  application-level streaming by name, or this gets re-litigated every
  time someone reads that line.
- Unresolved beyond this record: the declarative topology (§2), element
  metadata and event time (§4), and whether `I` is the right category
  letter next to numerals.
