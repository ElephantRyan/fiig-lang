# ADR 0117: A Kafka Consumer in fiig, over Sessions, in Order

Status: Accepted (2026-11-21, by the owner). Implemented 2026-11-21.
The owner decided §1 (the KIP-848 group protocol only) and §5 (Retry
seeks back, so order holds) before it was written.
Date: 2026-11-21
Builds: ADR 0114 (sessions), amending §2 and §3 and adding to §1 (§2 here).
Amends: ADR 0111 §1 (a delivery names its order, §6 here).
Related: ADR 0042 (the socket floor), ADR 0047 (streams), ADR 0109
(connections kept between calls), ADR 0113 (the Kafka producer), ADR
0116 (compression).

## Context

ADR 0111 made stream sources fiig packages, and Redis Streams the first.
Kafka is the source most stream adapters will want, and ADR 0111 left it
for last because a Kafka consumer keeps state between polls that nothing
in fiig could hold: its member id and epoch in the group, its assigned
partitions, and where it is in each. The broker also expects a heartbeat
every few seconds whether or not the program is between polls, or it
gives the partitions to someone else. ADR 0114 designed sessions for
exactly this and was accepted unbuilt; this ADR builds them and the
consumer on them.

Kafka has two group protocols. The classic one (JoinGroup, SyncGroup,
Heartbeat) works with every broker and has the client lead elect itself
and compute assignments. KIP-848's ConsumerGroupHeartbeat (GA in Kafka
4.0) has the broker assign partitions and tell each member its share in
the heartbeat reply, so a client is one periodic request and a
reconciliation loop.

| | Group protocol | Retry of one record | Order on retry |
| --- | --- | --- | --- |
| Java client + Spring Kafka | classic or KIP-848 (`group.protocol`) | `DefaultErrorHandler` seeks back; `@RetryableTopic` moves it to a retry topic | kept by seek back, lost by retry topics |
| librdkafka (Confluent's C, Go, .NET) | classic or KIP-848 | the application seeks | the application's |
| franz-go | classic, KIP-848 in progress | the application seeks | the application's |
| Kafka Streams | classic | none: a failure stops the thread or is skipped by a handler | kept |

## Decision

### 1. KIP-848 only

*(Owner, 2026-11-21: "if someone is writing an application in fiig they
can use the new protocol; fiig is still brand new, we don't need to
support a ton of older versions.")*

The consumer speaks ConsumerGroupHeartbeat v1 (client-chosen member ids,
Kafka 4.0 and later) and nothing of the classic protocol. A broker
without it answers ApiVersions without API 68, and the source reports
`SourceError.Misconfigured` naming Kafka 4.0, which halts the route
(ADR 0111 §4) rather than retrying forever.

### 2. Sessions as built (amends ADR 0114 §2 and §3)

Building sessions settled four things ADR 0114 left open or got wrong
for its first user.

- **A session runs the protocol of the package that calls it.** A
  package provides `fiig.session.SessionProtocol` in its manifest, as a
  driver provides `fiig.sql.Driver`, and `Session.call` from that
  package's gateways reaches sessions running its own protocol. There is
  no `"session"` entry in the capabilities file: the protocol package
  isn't a deployment's choice the way a database is, it is what the
  calling code was written against. `Session` is a primitive beside
  `Socket` (ADR 0042: a connection the runtime owns past any call can't
  be written in fiig), and a protocol package's manifest needs no more
  than a socket package's does.
- **The call names its session.** `Session.call(settings, message,
  deadline)` takes `settings: Map<string, string>`, and each distinct
  settings map is one session of that package. `"host"`, `"port"`, and
  `"tls"` say where it connects; the protocol reads the rest in
  `opened`. The runtime adds `"sessionId"`, a fresh id that stays the same
  while the process runs, so a protocol that reconnects can say it is the
  same client. A Kafka consumer needs a session to the group's
  coordinator and one to each partition leader, and the settings are
  what tells them apart.

  *(Amended 2026-11-21: the settings may also name `"alternates"`,
  `"host:port,host:port"`, with an IPv6 host in brackets,
  `"[::1]:9092"`. When the runtime can't connect to `"host"` and
  `"port"`, it tries each alternate in order. `opened` is given the host
  and port it reached in place of the settings' own. Only when none can
  be reached are the waiting calls Broken, with a reason naming each
  server tried. The alternates are part of the settings, so part of
  which session it is. A malformed alternate faults the call, a defect
  in the package that wrote it. A `MoveTo` goes where it says, with no
  alternates: the coordinator is one broker. Each reconnect starts again
  at `"host"`. A dial that failed also now fails the call that started
  it with Broken, where before that call waited out its deadline.
  `alternatesOf` and `session.open` in `compiler/runtime/session.go`.)*
- **A session can move.** `SessionClose` is `Done(reason)`,
  `Failed(reason)`, or `MoveTo(host, port)`. A move closes the
  connection, opens one to the new place, calls `opened` again, and asks
  the new connection every message still waiting for a reply, so the
  callers never see it. Kafka needs this: any broker can be asked which
  one coordinates a group, and the session has to be with that one.
- **A fifth operation, `closing(state, now) -> Step`**, when the process
  shuts down: the runtime writes its `write`, waits up to a second for
  the connection to drain, and closes. A Kafka member leaves its group
  this way, so its partitions move at once instead of after the
  45-second session timeout.

A protocol's operations are gateways, as every member of a capability
group is, and they are written `requires Session`: the gateway is part
of the session, and P7 keeps it from reaching anything else. The runtime
runs them one at a time per session, on the session's own loop, so one
that calls `Session.call` faults instead of waiting on itself; an
operation returns what to write. *(Making `requires` optional for these
was the other way, and is a grammar change for one interface.)* A
session has one connection; ADR 0114's open question 4 (more than one)
stays open.

### 3. Two kinds of session

`fiig.kafka`'s protocol plays one of two roles, by the settings' `"role"`:

- **`broker`**: a connection that carries requests and returns their
  responses, matched by correlation id. The consumer's Fetch and
  ListOffsets go to partition leaders this way, and Metadata and Produce
  for dead letters (§5).
- **`group`**: the same, to the group's coordinator, plus the group
  itself. `opened` asks FindCoordinator and moves when the answer is
  another broker; then ConsumerGroupHeartbeat every
  `heartbeatIntervalMs` the broker names, OffsetFetch for each newly
  assigned partition, and OffsetCommit for what was handled.

*(Amended 2026-11-21: the producer runs over `broker` sessions too (ADR
0113 §4, as amended). A broker session now asks ApiVersions as it opens
and holds every ask, returning no `accepted`, until the answer comes;
the consumer's Fetch and ListOffsets wait that one round trip on a new
connection. It also answers two asks of the producer's,
`KafkaAsk.Layout(topic, maxAgeMillis)` and `KafkaAsk.Forget(topic)`,
from the layouts it keeps in its state.)*

*(Amended 2026-11-21: with `KAFKA_SASL_MECHANISM` set, either kind of
session logs in first (ADR 0113 §6, as amended): `opened` writes
ApiVersions and SaslHandshake, the session is `"authenticating"` and
holds every ask until SaslAuthenticate is done, and a group session asks
FindCoordinator only then. A refused login leaves a group session
`"refused"`, and its polls answer with why, so the route halts as
Misconfigured (§1).)*

The state of a group session is the member's epoch, its partitions, and
for each the next offset to fetch, the offset to commit, and a retried
offset's attempts and the time it may be fetched again.

### 4. Receive

`receive` asks the group session to **poll**. The reply is every
assigned partition that may be fetched now, its leader, and its
position. The group session holds the reply until there is one, or until
the subscription's `waitMillis`, so an idle consumer waits in the session
and not in a loop. A partition with no committed offset is positioned by
ListOffsets at its leader, at the start or the end of the log as the
route's `from` says (ADR 0111 §3a). `receive` then Fetches from each
leader, decodes the record batches (decompressing them, ADR 0116 §4),
tells the group session how far it got, and returns the records as
deliveries: `id` is `partition/offset`, `payload` is the record's value
(a null value is empty bytes), and `order` is the partition (§6).

### 5. Settle: ack commits, retry seeks back

*(Owner, 2026-11-21, on Retry: "seek back, we don't want to lose order,
that is an important aspect of Kafka.")*

- **`Ack`** marks the offset handled. The group session commits within
  `KAFKA_COMMIT_INTERVAL_MS` (1000 by default), and at once before it
  gives up a partition the broker moved or the process stops, so at most
  that interval's acks are delivered again after a crash. That is ADR
  0111's at-least-once.
- **`Retry`** seeks the partition back to the record and pauses it for
  `KAFKA_RETRY_AFTER_MS` (1000 by default). Nothing after it in the
  partition is handled first. The group session counts the record's
  attempts, so `retry(attempts: N)` reaches its `else` branch. The count
  lives in the session, so a restart or a move of the partition to
  another process starts it again at 1: Kafka has no delivery count to
  keep it in.
- **`DeadLetter(target)`** produces the record's value to topic
  `target`, its key the source partition and offset, then acks it.

### 6. Deliveries name their order (amends ADR 0111 §1)

A Retry that seeks back means the records after it come again after it,
so the runtime must not handle the rest of the batch it already has
from that partition. `fiig.stream.Delivery` gains `order: string`:
deliveries with the same non-empty order are one sequence, and after one
of them is retried the stream adapter skips the rest of that sequence in
the batch, unsettled. Redis Streams gives `""`, so its batches behave as
before.

### 7. Configuration

The consumer reads `KafkaServer` (ADR 0113 §6) for the bootstrap
servers, TLS, and client id, and a new `KafkaConsumer` config for
`KAFKA_COMMIT_INTERVAL_MS`, `KAFKA_RETRY_AFTER_MS`, and
`KAFKA_FETCH_MAX_BYTES` (1 MiB by default, per fetch). A route's
`source` is the topic and its `group` the consumer group.

*(Amended 2026-11-21: the group session opens to the first bootstrap
server, with the rest of `KAFKA_BOOTSTRAP` as its `"alternates"` (§2),
in the order written. A consumer whose first server is down still
starts, through the next one that answers. Before this, it used the
first server only. `groupSettings` in
`compiler/stdlib/kafka/src/GroupSettings.fiig`. Tested by
`TestStreamAdapterOverAKafkaConsumerGroup` with
`FIIG_KAFKA_BOOTSTRAP=127.0.0.1:1,127.0.0.1:59092`, which fails without
the alternates and passes with them.)*

## Alternatives considered

- **The classic group protocol, or both.** Works with Kafka 3.x. The
  client half is several times the size (join, sync, the leader's
  assignor, the rebalance protocol's cooperative variant), and the owner
  decided fiig doesn't need old brokers (§1).
- **Retry topics.** A failed record moves to `orders.retry` and the
  partition carries on, so one bad record doesn't hold the rest up. Order
  per key is what a Kafka user expects of a consumer, and ADR 0111 §3
  already has `deadLetter` for giving up on a record (§5).
- **A capabilities-file entry per session** (ADR 0114 §2 as written).
  Settings belong to the protocol's caller here: the leaders a consumer
  talks to aren't known until it asks (§2).
- **Fetch over `Socket` from `receive`.** ADR 0010 gives a gateway one
  capability, and `receive` needs the group session anyway, so fetching
  through sessions too keeps it to `Session` and keeps the connections to
  leaders open between polls.

## Consequences

- `fiig.session` (the interface, `Step`, `SessionReply`, `SessionClose`),
  the `Session` primitive and the prelude's `SessionError` in the
  compiler, and the session runtime in `compiler/runtime`.
- `fiig.kafka` provides `fiig.stream.StreamSource` and
  `fiig.session.SessionProtocol`.
- The stream adapter skips the rest of a retried delivery's order.
- An application that depends on `fiig.kafka` lists `Session` among its
  manifest's capabilities (ADR 0035), a producer-only one too, since the
  check is by package.
- Tested against a Kafka 4.0 broker
  (`TestStreamAdapterOverAKafkaConsumerGroup`, run when
  `FIIG_KAFKA_BOOTSTRAP` and `FIIG_KAFKA_CONTAINER` name one): order kept
  through a retried record, dead letters, commits a second process
  resumes from, and a leave that hands it the partition at once.

## Open questions

1. **Record keys and headers.** A route binds the value only. A workflow
   that wants the key needs ADR 0111's element to grow fields, for Redis
   too.
2. **Many partitions per process.** One `receive` fetches from each
   leader in turn. A process consuming hundreds of partitions may want
   fetch sessions (KIP-227) and leaders fetched at once.
3. **Attempts across restarts** (§5): a header written back with the
   count, or a small compacted topic, would keep it.
