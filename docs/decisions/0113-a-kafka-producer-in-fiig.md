# ADR 0113: A Kafka Producer in fiig, At Least Once

Status: Accepted (2026-11-20; the owner approved it in review). Implemented 2026-11-20.
Date: 2026-11-20
Related: ADR 0042 (the socket floor), ADR 0058 (binary patterns), ADR
0060 (Socket), ADR 0063 (int overflow faults), ADR 0071 (config), ADR
0075 (the crypto group), ADR 0109 (connections kept between calls, and
its open question 2), ADR 0110 (`fiig.redis`, the client this follows).

## Context

Kafka is the next protocol on ADR 0042's list, and the one most asked for
in service work. A consumer needs group membership held on a connection
across calls, which waits on ADR 0109's open question 2. A producer
doesn't: each produce is a request and a response, which ADR 0109's kept
connections already serve.

What a producer needs that fiig lacks:

- **CRC-32C.** A record batch carries a CRC-32C (Castagnoli) of
  everything after the field. Computing it in fiig is possible but a
  table-driven checksum over every byte sent, in a tree-walking
  interpreter, is the hot path of the whole producer.
- **Producer state between calls.** Every client since Kafka 3.0 enables
  idempotence by default: the broker hands the producer an id, and the
  producer numbers each partition's batches so the broker drops a
  duplicate. That id and those sequence numbers have to outlive a call,
  and fiig has nowhere to keep them (ADR 0109 open question 2). So does a
  cache of which broker leads each partition.

| Client | Language | Idempotent by default | Partitioner for a keyed record |
| --- | --- | --- | --- |
| Java client | Java | yes (3.0+) | murmur2 of the key |
| librdkafka | C | yes (2.x) | `consistent_random` (CRC32), murmur2 configurable |
| franz-go | Go | yes | murmur2, matching Java |
| kafka-python | Python | no | murmur2 |
| brod | Erlang | no | its own hash unless set |

## Decision

### 1. `fiig.kafka`: one gateway, at least once

```fiig
capability Kafka {
    gateway send(topic: string, records: List<KafkaRecord>) -> Result<List<Sent>, KafkaError>
        requires Socket
}

model KafkaRecord {
    key: bytes | None
    value: bytes
    headers: List<KafkaHeader> | None
}
```

`send` writes the records to `topic` and returns each one's partition and
offset, in the order given, once every partition leader has acknowledged
them with `acks=all`.

**It is at least once, and the ADR says so plainly.** There is no
idempotence and no transactions. If a reply is lost after the broker
wrote the batch, `send` returns `Err` and a caller that sends again writes
the records twice. A consumer that must not act twice keys its work by
something in the record. Idempotence needs a producer id and per-partition
sequence numbers that outlive one call, which is ADR 0109's open question
2; this producer should gain it when that is decided, not invent a store
for it here.

`send` doesn't retry. Every error comes back, with `retriable` set as
Kafka's protocol marks the code, and the caller decides, as with
`fiig.http` (ADR 0074 §9).

### 2. CRC-32C is a primitive: `crypto.crc32c(data) -> int`

It joins the crypto group beside `sha256`, as an unsigned 32-bit value in
an `int`. It is a checksum, not a cryptographic hash, and its comment says
so. ADR 0042 admits a primitive only when fiig can't express it; this one
fiig can express, and it is admitted for speed, the first primitive to
be. The test is the one ADR 0042 would apply to any later case: the
function is fixed by a standard, has no options, and runs over every
byte a protocol sends.

### 3. Partitions: Java's, so keys land where other producers put them

- **A keyed record** goes to `murmur2(key) & 0x7fffffff` modulo the
  partition count, as the Java client, franz-go, and kafka-python do. A
  Java service and a fiig service writing the same key reach the same
  partition, so the key's ordering holds across them. murmur2 is written
  in fiig over 32-bit arithmetic that can't overflow (ADR 0063): XOR via
  `bytes.xor`, shifts via division, and multiplication split in halves.
- **Records without a key**, in one call, all go to one partition, chosen
  from the clock: one batch, as the Java client's sticky partitioner does.

### 4. Each call reads the metadata

`send` asks a bootstrap broker for the topic's partitions and their
leaders (Metadata v4), then sends each leader its partitions' batches
(Produce v8). Without state between calls there is no metadata cache, so
every call pays one round trip more than a Java producer, over a kept
connection (ADR 0109). A new connection is first checked with ApiVersions,
so an old broker is `Err(Unsupported)`, not a closed socket. Kafka allows
requests to be pipelined, and the check is read-only, so it goes in the
same write as the request; unlike Redis's `SELECT` (ADR 0110), nothing
is changed if it fails.

`allow_auto_topic_creation` is false: a misspelled topic is
`Err(UnknownTopic)`, not a new topic.

*(Amended 2026-11-21: the producer sends the flexible versions (KIP-482)
now, Metadata v12 and Produce v9, so a broker that drops the old ones
still takes it. v9 is Produce's lowest flexible version; for Metadata,
v12, whose request carries nothing fiig must fill in but the topic's
name. The dogfood broker, Kafka 4.0, lists Metadata 0-13 and Produce
0-12. The requests use header v2 and compact
encodings, the responses header v1, and every struct's tagged fields are
skipped, so a broker that adds one is still read. ApiVersions now
refuses a broker without Metadata v12 and Produce v9, by name.
`metadataV12Request`, `readMetadataV12`, `produceV9Request`, and
`readProduceV9` sit beside the v4 and v8 conversions, which the
consumer's own Metadata and dead-letter Produce still send (ADR 0117
§3); moving those is the consumer's change to make, in
`KafkaSource.fiig`.)*

*(Amended 2026-11-21: **the producer keeps each topic's layout, for
`KAFKA_METADATA_MAX_AGE_MS`** (300000 by default, Java's
`metadata.max.age.ms`; 0 reads it on every send). State that outlives a
call has one home in fiig, a session (ADR 0114, built by ADR 0117), so
`send` moved onto them: it now `requires Session`, not `Socket`. It asks
its **bootstrap session**, a `"broker"` session to the first bootstrap
server with the rest as `"alternates"` (ADR 0117 §2, as amended), for
`KafkaAsk.Layout(topic, maxAgeMillis)`. The session answers from the
layout it keeps when that is younger than the max age, and otherwise
sends Metadata v12, keeps what comes back, and answers with it. Each
produce goes to its leader's broker session as a `KafkaAsk.Request`,
the same sessions the consumer fetches over. A produce answered
UNKNOWN_TOPIC_OR_PARTITION, LEADER_NOT_AVAILABLE, or
NOT_LEADER_OR_FOLLOWER, or a leader that can't be reached, sends
`KafkaAsk.Forget(topic)` to the bootstrap session, so the next send
reads the layout again; the send still returns the error and doesn't
retry (§1).*

*The other way was to keep `Socket` and hold the layout somewhere else:
a process-wide cache in the runtime, which is Go doing a fiig package's
job (ADR 0042), or a new store, which ADR 0109's open question 2 already
answered with sessions. Sessions also gave the producer things it lacked:
the bootstrap list fails over by the runtime's alternates instead of its
own loop, connections stay open by the session instead of
`Socket.reuse`/`keep`, and SASL (open question 2) has one place to live
for the producer and the consumer alike. An application that depends on
`fiig.kafka` already lists `Session` (ADR 0117's Consequences), so
nothing changes for it but `KafkaError.Socket(failure: SocketError)`,
which is now `KafkaError.Session(failure: SessionError)`. With one
bootstrap server, the bootstrap session's settings are that broker's
own, so a one-broker cluster is one connection, as before. A session
that reconnects starts with no layouts, and the cache is one session's,
so two processes each keep their own, as two Java producers do. A broker
session now asks ApiVersions as it opens and holds every ask until the
answer comes (one round trip per connection, as the Java client spends);
one that can't take Metadata v12 and Produce v9 answers each Layout with
`Unsupported`. `bootstrapSettings`, `layoutOf`, `layoutMoved`, and the
Layout and Forget arms of `SessionAsked.fiig` and `SessionReceived.fiig`;
tested by `test/scenarios/Producer.fiig` and
`TestKafkaProducerReadsTheLayoutAgainWhenTheLeadersMove`.)*

### 5. Record batches carry their compression bits

A batch is Kafka's v2 format (magic 2). Its attributes field holds the
codec in bits 0-2 (`none`, `gzip`, `snappy`, `lz4`, `zstd`), and the
encoder takes a `Compression` value, so adding codecs changes one
conversion. This version sends `None` only; snappy and lz4 are the next
ADR, gzip and zstd after.

Update (2026-11-20): ADR 0116 adds all four codecs, chosen by
`KAFKA_COMPRESSION`.

### 6. Configuration

```fiig
config KafkaServer {
    bootstrap: string from env "KAFKA_BOOTSTRAP"            // host:port[,host:port...]
    tls: bool from env "KAFKA_TLS" = false
    clientId: string from env "KAFKA_CLIENT_ID" = "fiig"
    timeoutMillis: int from env "KAFKA_TIMEOUT_MS" = 30000
}
```

`timeoutMillis` bounds a whole `send` and is the produce request's
`timeout_ms`.

### 7. Scope

In: Produce to any number of partitions in one call, keys, headers,
PLAINTEXT and TLS.

Out: idempotence and transactions (§1), SASL (PLAIN, SCRAM, OAUTHBEARER),
compression (§5), a metadata cache, retries, and flexible request
versions (Kafka 2.4's tagged fields), which a future broker that drops the
old versions will need.

## Alternatives considered

- **Idempotence with state in the program**, such as a producer id kept
  in a database row. It would make every send a database write, and the
  sequence numbers are per partition per broker session.
- **A Go producer** (franz-go) behind a capability: fast to build, and
  what ADR 0042 exists to prevent.
- **CRC-32C in fiig.** Expressible, and slow on the producer's hottest
  path. §2 states the speed test that let it in.
- **librdkafka's `consistent_random`** partitioner: matches only other
  librdkafka clients, not the Java ecosystem most Kafka shops run.

## Consequences

- `crypto.crc32c` in `compiler/types/crypto.go` and `compiler/runtime`.
- New built-in package `fiig.kafka`.
- `docs/known-limitations.md` names the at-least-once guarantee.

## Open questions

1. **Idempotence** and a metadata cache, after ADR 0109's open question 2.
   *(2026-11-21: that question is answered: sessions, ADR 0114 as built by
   ADR 0117, can hold a producer id, sequence numbers, and metadata.
   Neither is built yet.)* ~~The metadata cache.~~ Done 2026-11-21 (§4,
   as amended). Idempotence stays open.
2. **SASL**, which most hosted Kafka needs: SCRAM can reuse
   `crypto.pbkdf2Sha256` (ADR 0085), PLAIN is a handshake.
3. ~~**Flexible versions**, before a broker that drops the non-flexible
   ones ships.~~ Done 2026-11-21 for the producer: Metadata v12 and
   Produce v9 (§4, as amended).

## Implementation notes (2026-11-20)

- `crypto.crc32c` (§2) is Go's `hash/crc32` with the Castagnoli table,
  tested against the `123456789` check value and RFC 3720 Appendix B.4.
- `compiler/stdlib/kafka`: the `Kafka` group, its records and errors,
  and one conversion per wire step: `kafkaRequest`, `apiVersionsRequest`
  and `checkApiVersions`, `metadataRequest` and `readMetadata`,
  `producePlan`, `recordBatch`, `produceRequest` and `readProduce`, and
  `murmur2`. The reads go by offset through `i16At`, `i32At`, `i64At`,
  and `stringAt`, each `None` past the end, so a short reply is
  `MalformedReply`, never a fault.
- **Metadata's partitions come in no promised order**, so they are
  collected by index; a test sends them as 2, 0, 1.
- **`send` is one loop over a list of exchanges**: the Metadata
  exchange adds one Produce exchange per leading broker, since a gateway
  can't call a gateway. A bootstrap server that can't be reached gives
  way to the next; a failed produce is returned.
- murmur2 matches Kafka's own `UtilsTest` vectors and a line-by-line Go
  port of `Utils.murmur2`.
- Tests: `test/scenarios/Encoding.fiig` and `Protocol.fiig` (varints,
  murmur2, framing, a batch's CRC and length, ApiVersions, Metadata out
  of order, the partition plan, Produce offsets and errors); and
  `tooling/runprogram`'s `TestKafkaProducerAgainstABrokerInMemory`, a
  broker that checks each batch's CRC with Go's own CRC-32C, the acks and
  transactional id, and sees two sends share one connection.
- **Dogfooded against Kafka 4.0** (single-node KRaft, container
  `fiig-kafka-dogfood`): the broker accepted every batch (it checks the
  CRC); Kafka's console consumer read back the keys, values, a header, and
  a null key at the offsets `send` returned; the Java console producer
  put `order-1`, `order-2`, `order-3`, and `customer-42` on the same
  partitions fiig did; a misspelled topic was `UnknownTopic`; and an
  unreachable first bootstrap server fell over to the second.
