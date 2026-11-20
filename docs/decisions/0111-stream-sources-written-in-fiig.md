# ADR 0111: Stream Sources Written in fiig, and the Stream Adapter

Status: Proposed, implemented (2026-11-20)
Date: 2026-11-20
Accepts, as amended here: ADR 0047 (streams are a third adapter kind).
Related: ADR 0014 (`idempotent`), ADR 0021 (route binding), ADR 0042 (the
socket floor), ADR 0043 (`provides`), ADR 0044 (workflow as the adapter
boundary), ADR 0077 (a raw JSON body), ADR 0079 §6a (per-package
bindings), ADR 0084 (drivers written in fiig: the model this follows),
ADR 0088 (`retry(attempts: N)`), ADR 0109 (connections kept between
calls), ADR 0110 (`fiig.redis`).

## Context

ADR 0047 decided the shape of a stream route: the runtime owns the loop
and calls a workflow once per element; the route declares the element's
`key`; and the route must say what happens to every outcome (`ack`,
`retry`, `halt`, `deadLetter`). It was written before ADR 0042's floor was
enforced, and says "the engine owns the loop" without saying where the
elements come from.

Under ADR 0042 the answer can't be Go. A Kafka, Redis, or NATS consumer is
a wire protocol, and wire protocols are written in fiig. So the runtime
needs what ADR 0084 gave `Database`: a small contract that a package
written in fiig provides, and that the runtime calls.

### How consumers split elsewhere

| System | Who loops | Who acks | Retry | Poison element |
| --- | --- | --- | --- | --- |
| Kafka Streams | the library | commit offset | handler decides | `deserialization.exception.handler`; default: stop |
| Sidekiq / Oban | the library | job row state | `max_attempts`, backoff | discarded after the last attempt |
| SQS + Lambda | the platform | delete message | visibility timeout | redrive to a DLQ after `maxReceiveCount` |
| Redis Streams | the client | `XACK` | leave pending, reclaim with `XCLAIM` | the client's choice |
| Broadway (Elixir) | the library | the producer's `ack/3` | `Message.failed` | the producer decides |

Broadway is the closest fit, for the same reason Postgrex was for ADR 0084.
Its pipeline (the loop, batching, acking) is generic, and each broker is a
*producer* module with a small contract: produce messages, then `ack(ref,
successful, failed)`. The broker's protocol lives in the producer, and
the pipeline never sees it.

### Why Redis Streams first

A consumer group's state (which entries each consumer holds, how often
each was delivered) lives on the Redis server, not on the connection. A
source can therefore poll with `XREADGROUP ... BLOCK` over connections
kept by ADR 0109, and nothing has to own a connection across calls, which
ADR 0109 deferred (its open question 2). Kafka's group membership is held
by heartbeats on a connection, so Kafka needs that question answered
first.

## Decision

### 1. `fiig.stream`: the contract a source provides

A built-in package, beside `fiig.sql`:

```fiig
interface StreamSource {
    receive(subscription: Subscription, batchSize: int) -> Result<List<Delivery>, SourceError>
    settle(subscription: Subscription, delivery: Delivery, outcome: Outcome) -> Result<None, SourceError>
}

model Subscription {
    source: string      // the route's `source` string
    group: string       // the adapter: every process running it shares the work
    consumer: string    // this process, unique among the group's consumers
    waitMillis: int     // how long receive may wait for an element
}

model Delivery {
    id: string          // the source's name for the element
    payload: bytes
    attempt: int        // 1 the first time the group receives it
}

enum Outcome {
    Ack
    Retry
    DeadLetter(target: string)
}

enum SourceError {
    Unavailable(reason: string)   // the source can't be reached now
    Protocol(reason: string)      // the source answered something the package can't read
}
```

A source package names its capability group in its manifest, as a driver
does: `"provides": {"fiig.stream.StreamSource": "RedisStreams"}`.

- **`receive`** returns up to `batchSize` elements, waiting at most
  `waitMillis` when none is ready, and an empty list when none came. It
  also returns elements whose earlier delivery was retried or never
  settled. Finding those is the source's job, since only the source knows
  how its broker records them.
- **`settle`** reports one element's outcome. `Retry` makes the element
  come back from a later `receive`, after a delay the source chooses, with
  `attempt` one higher. `DeadLetter` moves it to `target`.

### 2. The capabilities file names the source

```json
{"StreamSource": {"type": "source", "source": "fiig.redis"}}
```

As with `Database` (ADR 0084 §2), which broker carries the elements is a
deployment's decision, and a per-package binding (ADR 0079 §6a) gives
one domain's adapter its own. The source package reads its own settings
from `config` (ADR 0071), so `fiig.redis`'s streams use `REDIS_URL` like
its client. `fiig serve` refuses to start a stream adapter with no
`StreamSource` bound, or whose source package isn't a dependency.

### 3. The route (amends ADR 0047 §4 and §5)

```fiig
adapter stream OrderIntake {
    source "orders" group "order-intake" from earliest key orderId -> placeOrder(order: OrderPlaced) {
        Ok(_) -> ack
        Err(OrderError.OutOfStock(_)) -> retry(attempts: 5) else deadLetter "orders.failed"
        Err(OrderError.Invalid(_)) -> deadLetter "orders.invalid"
    }
}
```

```ebnf
stream-route ::= 'source' string-literal 'group' string-literal 'from' ( 'earliest' | 'latest' )
                 [ 'key' identifier ] '->' call stream-mapping
stream-mapping ::= '{' { pattern '->' disposition } '}'
disposition  ::= 'ack' | 'halt' | 'deadLetter' string-literal
               | 'retry' '(' 'attempts' ':' int-literal ')' [ 'else' ( 'halt' | 'deadLetter' string-literal ) ]
```

- **The element binds the route's one argument.** It decodes from the
  payload as JSON into a model, as an HTTP body does (ADR 0021), or binds
  the payload as it arrived when the argument is `bytes` (ADR 0077). A
  program that binds `bytes` decodes it itself and maps a payload it
  can't read to a disposition like any other error.
- **`key` names a field of the element model**, the element's identity in
  logs and in a halt's message (ADR 0047 §4). A `bytes` element has no
  fields, so it has no `key`, and the delivery's `id` names it.
- **The mapping is required and exhaustive** over the workflow's result:
  `Ok`, and every variant of the `Err` enum (`Err(_)` or a bare name
  covers the rest). ADR 0047 §5's reason stands: a stream has no caller
  to hand an unhandled error to.
- **`retry` states its limit**, as `transaction ... retry(attempts: N)`
  does (ADR 0088). On the delivery whose `attempt` reaches N, the element
  goes to the `else` branch instead, and `else halt` is the default. ADR
  0047 wrote a bare `retry`, which would retry a permanent failure
  forever.
- `stream`, `ack`, `halt`, `deadLetter`, `source`, and `key` are
  contextual words (ADR 0018); `retry`, `attempts`, and `else` are already
  keywords, which is also why the contract says `batchSize`, not
  `limit`.

### 3a. The route names its group and where a new one starts

*(Added 2026-11-20 after review: the group was the adapter's package path
and name, and a new group started at the stream's first element, so
moving or renaming an adapter silently made a new group that replayed the
whole stream.)*

- **`group "name"` is required.** A consumer group is state a deployment
  holds in the broker: which elements have been handled, and by whom. So
  it is written down, like a table name, and moving the adapter between
  packages or renaming it changes nothing. Renaming the group is a
  visible edit, and makes a new group.
- **`from earliest` or `from latest` is required**, and applies only when
  the group doesn't exist yet: earliest processes what the stream already
  holds, latest only what arrives after the group starts (Kafka's
  `auto.offset.reset`, written where it can be read). A group that exists
  carries on where it was, whatever the route says.
- **I8** reports either missing.
- The contract's `Subscription` gains `start: StartAt` (`Earliest`,
  `Latest`); `fiig.redis` creates the group at `0` or `$`.
- `group`, `from`, `earliest`, and `latest` are contextual here.

### 4. Halt, faults, and elements that don't decode

**`halt` stops the adapter and `fiig serve` exits with an error** naming
the adapter, the element's key and id, and why. The element is left
unsettled, so the next process to run the adapter receives it again. In
Kubernetes or systemd that is a restart with backoff and an alert, which
is what "stop" means operationally. Kafka Streams' default for an
element it can't handle is the same. *(The owner confirmed this scope on
review, 2026-11-20, over stopping only the route: a halted route would
otherwise be visible only in logs, and fiig has no health check yet.)*

**A dead-letter moves any payload**, through `Redis.sendBytes` (ADR 0110
§1a). It first failed on a payload that isn't UTF-8, and the element came
back forever; `TestStreamDeadLettersBinaryPayloads` is that case.

**A fault halts**, for the same reason a fault is a 500 and not a
retried request: it is a defect in the program or a dependency that
failed in a way the program didn't handle (a database error that isn't
a conflict, ADR 0087). Retrying a defect hides it; skipping the element
loses it.

**A payload that doesn't decode into the element model halts** with the
decoding error. A program that wants to dead-letter unreadable payloads
binds `bytes` instead (§3), and says so in the mapping.

### 5. The runtime loop

For each stream route, `fiig serve` runs one loop:

1. `receive(subscription, 16)`, with `waitMillis` 2000, as its own
   top-level call (so ADR 0109's kept connections and ADR 0060 §5's
   ownership apply).
2. For each delivery, in order: decode it, call the workflow, and match
   the result against the mapping. Then `settle` the outcome, or halt.
3. A `SourceError` from `receive` is logged, and the loop waits (1 s,
   doubling to 30 s) and asks again. A `SourceError` from `settle` is
   logged and the loop moves on. The element comes back later from
   `receive`, so it is processed at least once, which is what `idempotent`
   (ADR 0014) marks a workflow as safe for.

**Elements are processed one at a time per route.** That keeps every
key's elements in order within this process (ADR 0047 §8) and is the
simplest thing that does. Several processes share a group's elements
through the source. Processing in parallel by key is a later change that
the declared `key` makes possible.

### 5a. A batch fits inside the redelivery delay

*(Added 2026-11-20 after review: a batch of 16 handled one at a time, up to
the 30 s request timeout each, outlived the 30 s after which Redis hands
an unsettled entry to another consumer, so later entries in a batch were
taken by a second consumer while still waiting in the first.)*

- `Subscription` gains `handleMillis`: the longest one element may take,
  its workflow's timeout (`REQUEST_TIMEOUT`) and its settle (10 s).
- A source that reclaims unsettled elements after a delay takes no more
  in one receive than it can see handled within it. `fiig.redis` takes
  `REDIS_STREAM_RETRY_AFTER_MS / handleMillis`, at most the runtime's 16.
- When not even one fits, the source returns the new
  `SourceError.Misconfigured(reason)`, which halts the route at its first
  receive with a message naming the setting.
- `REDIS_STREAM_RETRY_AFTER_MS` defaults to 5 minutes, up from 30 s, which
  with the defaults (40 s an element) is a batch of 7. It is also the
  retry delay, since a retried entry is reclaimed after it.

`group` is the route's `group` (§3a). `consumer` is the host name and
process id. On shutdown the loop finishes the element in hand and stops.

### 6. Bug classes: I. Stream adapter errors

- **I1**: a model element's route has no `key`, its `key` isn't a field of
  the model, the field isn't a type text can carry (W4's list), or a
  `bytes` element's route has a `key`.
- **I2**: a stream route's mapping is missing, or isn't exhaustive over
  the workflow's result; or a `retry` limit isn't at least 1.
- **I3**: a stream route's call doesn't have exactly one argument.
- **I4**: the element is neither a model nor `bytes`.

B6 applies unchanged: the route calls a workflow. A status code or `exit`
in a stream mapping, and a disposition in an HTTP or CLI one, are errors
without a class, as other misplaced route syntax is.

### 7. The first source: Redis Streams in `fiig.redis`

`fiig.redis` provides `StreamSource` as the capability group
`RedisStreams`, whose gateways `require Redis` (ADR 0110), so the
connection handling is the client's.

- **An element is a stream entry with a `payload` field.** A producer
  writes `XADD orders * payload <json>`. Redis entries are flat maps of
  strings, and one field holding the JSON is how the element keeps its
  types.
- **`receive`** first reclaims entries this group left pending longer than
  `REDIS_STREAM_RETRY_AFTER_MS` (default 5 minutes, §5a) with `XPENDING` and
  `XCLAIM`, using the delivery count Redis keeps for `attempt`. With none
  to reclaim, it reads new ones with `XREADGROUP ... BLOCK`. The group is
  created at the start of the stream (`XGROUP CREATE ... 0 MKSTREAM`) the
  first time Redis answers `NOGROUP`, so elements written before the
  first deploy are processed.
- **`settle`**: `Ack` is `XACK`. `Retry` leaves the entry pending, to be
  reclaimed after the delay. `DeadLetter` is `XADD <target> * payload
  <payload> source <stream> id <id>`, then `XACK`. A crash between the two
  leaves a copy in the dead-letter stream and the entry pending, so the
  element is dead-lettered twice rather than lost.

**`retry` reorders a key.** The retried element comes back after its
delay, while the key's later elements are processed now. ADR 0047 §5
said so; this is where it happens.

## What ADR 0047 decided that this changes

- §4: a `bytes` element, which has no `key` (§3 above).
- §5: `retry(attempts: N) [else ...]` instead of a bare `retry`; and
  `halt` is defined (§4 above).
- §6, replay with `run-cli`: not built. A test can already call the
  route's workflow with an element (ADR 0103); fetching one element from
  a source by id needs a third contract operation, and it is open question
  1.
- §7's versioned `StateStore` and §9's structured registry fields and
  `find --source`: not built (open questions 2 and 3). The card's route
  summary carries the source and key.

## Alternatives considered

- **The runtime speaks the broker's protocol** (a Go Kafka or Redis
  consumer). It would have been faster to build, and it is what ADR 0042
  exists to prevent.
- **The source package owns the loop**: a gateway that never returns,
  calling the workflow per element. Gateways can't call workflows (ADR
  0044), and a call that never returns breaks deadlines, connection
  ownership, and F1, as ADR 0047 §2 found for loops escaping the gateway.
- **`ack` and `nack` only**, with retries and dead-lettering left to the
  broker's configuration (SQS redrive, RabbitMQ DLX). That is the
  two-artifact problem ADR 0047 §5 exists to solve.
- **Unlimited `retry`.** One element that can never succeed would be
  retried forever and its failure never surface.
- **Skip an element that faults or doesn't decode.** It silently loses the
  element. Halting is louder, and `bytes` and `deadLetter` are there for a
  program that knows what it wants instead.

## Consequences

- New built-in package `fiig.stream`; `"type": "source"` in the
  capabilities file; `compiler/runtime` calls a source's gateways as it
  calls a driver's.
- `adapter stream` in `compiler/syntax`, the parser, the formatter, and
  the checker (I1-I4); `fiig serve` runs stream adapters beside HTTP
  ones, and `fiig config` lists what they read.
- `fiig.redis` gains `RedisStreams`.
- ADR 0047's status becomes "Accepted, as amended by ADR 0111".

## Open questions

1. **Replay by id** (ADR 0047 §6): `fiig run` feeding one element fetched
   from the source, which needs a `fetch(subscription, id)` operation.
2. **`StateStore`** (ADR 0047 §7), still downstream of capability
   contracts saying which operations mutate.
3. **Structured registry fields** for a route's source and dispositions,
   and `catalog-cli find --source` (ADR 0047 §9).
4. **Throughput settings**: the batch size (16), the wait (2 s), and
   processing in parallel by key. They are fixed until a program needs
   them changed.
5. **Kafka and NATS**: both keep group or subscription state on a
   connection, so they wait on ADR 0109's open question 2.

## Implementation notes (2026-11-20)

- **Syntax and checks:** `AdapterStream`, `Route.Key`, and
  `DispositionResponse` in `compiler/syntax`; `parseStreamRoute` and
  `parseDisposition` in the parser, which accepts only dispositions in a
  stream mapping; the formatter prints both; `compiler/checker/stream.go`
  is I1-I4, with its own exhaustiveness check, since A9's covers only a
  match's top-level tags (`Ok`, `Err`) and I2 needs every `Err` variant.
- **`fiig.stream`** is `compiler/stdlib/stream`. `compiler/runtime`'s
  `SourceProvider` is the `"source"` type; it resolves the package's
  provided group as the driver pool does and calls `receive` and `settle`
  each as a top-level call with its own owner, so the client's kept
  connection (ADR 0109) serves every poll.
- **The loop** is `compiler/adapter/stream.go`. `fiig serve` splits the
  adapters it is given by kind, runs one `StreamRunner` per stream route,
  and exits 1 when one halts. **A source's config is read at startup like
  the program's**: no workflow reaches the source's gateways, so `fiig
  serve` adds them to the config walk, and `fiig config <root> <stream
  adapter>` lists every source package's variables.
- **Redis Streams** is `compiler/stdlib/redis/src/RedisStreams.fiig`, which
  `provides stream.StreamSource` and calls `Redis.send`.
- **Tests:** `compiler/checker/stream_test.go` (I1-I4, the parser refusing
  a status code); `tooling/runprogram/stream_test.go` against
  `fakeredis_test.go`, an in-memory Redis with the consumer-group
  commands: ack, dead-letter, retry then dead-letter on the last attempt,
  a halt on a payload that isn't JSON and its redelivery, an element
  written while the consumer waits, clean shutdown, and connections kept.
- **Dogfooded against Redis 7** (container `fiig-redis-dogfood`, port
  56379) with `fiig serve`: the same four outcomes, `fiig serve` exiting 1
  on the halt with the element's id, the retried entry dead-lettered on
  its second delivery after a restart, and an element added during the
  blocking read processed within the wait.
