# ADR 0118: Stream Routes Bind Keys and Headers

Status: Accepted (2026-11-21; the owner chose route binding over a
metadata parameter). Implemented 2026-11-21.
Date: 2026-11-21
Amends: ADR 0111 §1 (a delivery carries its key and headers) and §3 (a
route binds them).
Related: ADR 0021 (route binding, and its elimination rule), ADR 0044 (a
workflow is the adapter boundary), ADR 0062 (play nice with foreign
data), ADR 0069 §1-§2 (`from header` on HTTP routes), ADR 0077 (`bytes`
binds what arrived), ADR 0110 (`fiig.redis`), ADR 0113 (the Kafka
producer, which already writes keys and headers), ADR 0117 (the Kafka
consumer, whose open question 1 this answers).

## Context

A stream route binds one thing: the element, decoded from the payload
(ADR 0111 §3). A Kafka record also has a key and headers, and real
producers put things there that a consumer needs: a trace id, a tenant,
a schema version, or the entity id that picked the partition. ADR 0113's
producer writes both, so a fiig program can send what another fiig
program can't read. ADR 0117 left this as its open question 1.

| System | How a handler gets the key | How it gets a header |
| --- | --- | --- |
| Spring Kafka `@KafkaListener` | `@Header(KafkaHeaders.RECEIVED_KEY) String key` | `@Header("trace-id") String traceId` on a parameter |
| Kafka Streams | `Record<K, V>.key()`, typed by a serde | `Record.headers().lastHeader("trace-id")` |
| franz-go, librdkafka | `record.Key` | loop over `record.Headers` |
| fiig HTTP routes (ADR 0069 §1) | (no key) | `requestId: string \| None from header "X-Request-Id"` |

The libraries split two ways. Spring binds named values onto ordinary
parameters, so the handler is a plain method. Kafka Streams and the
clients hand over the whole record, so the handler knows it reads Kafka.
fiig already chose Spring's way for HTTP: a workflow sees strings and
`None`, not a request, so the same workflow can sit behind a CLI route
or a test (ADR 0044).

## Decision

### 1. Syntax (amends ADR 0111 §3)

A stream route's argument may say where it comes from: `from header
"name"` or `from key`. The rest of the route is unchanged.

```fiig
adapter stream OrderIntake {
    source "orders" group "intake" from earliest key orderId
        -> placeOrder(order: OrderPlaced, traceId: string | None from header "trace-id", orderKey: string from key) {
        Ok(_) -> ack
        Err(_) -> deadLetter "orders.failed"
    }
}
```

```ebnf
stream-route ::= 'source' string-literal 'group' string-literal 'from' ( 'earliest' | 'latest' )
                 [ 'key' identifier ] '->' identifier '(' stream-args ')' stream-mapping
stream-args  ::= stream-arg { ',' stream-arg } [ ',' ]
stream-arg   ::= identifier ':' type [ 'from' ( 'header' string-literal | 'key' ) ]
```

- The route's `key orderId` and an argument's `from key` are different
  things. The first names a field of the element, for logs and a halt's
  message (ADR 0047 §4). The second is the broker's record key.
- `header` and `key` are contextual here, as `from`, `header` and
  `cookie` already are in an HTTP route's arguments. No word becomes
  reserved.
- `from key` is for stream routes only. An HTTP or CLI route that writes
  it is an error. A stream route that writes `from cookie` is one too: a
  stream has no cookies.

### 2. Types, and what is missing

An argument bound from a header or the key is `string`, `bytes`, or
either `| None`.

- **`bytes`** is the value as the broker carried it. Kafka keys and
  header values are bytes, with no declared encoding.
- **`string`** is that value read as UTF-8. Text is what nearly every
  header carries (trace ids, tenants, content types).
- **No `int`, `decimal`, or enums**, unlike HTTP headers (ADR 0069 §2).
  An HTTP header is text by its RFC; a Kafka header is bytes, and the
  same `"42"` may arrive as text or as a big-endian int, depending on the
  producer. The workflow knows which; it reads the string or the bytes
  and parses. This can widen later if programs keep writing the same
  parse.
- **Absent, and not optional:** the element halts, as a payload that
  doesn't decode does (ADR 0111 §4). The route said every element has
  this header; one that doesn't is a producer the program doesn't
  expect, and halting says so loudly. A program that takes elements
  without it writes `T | None`, which receives `None`.
- **Not UTF-8, bound to `string`** (optional or not): the element halts
  too. The value is there but isn't text; `None` would hide that. A
  program that takes any bytes binds `bytes`.
- **The key** follows the same rules. A Kafka record with a null key has
  no key; one with an empty key has empty bytes (Kafka keeps the two
  apart, and so does the binding).

### 3. A header sent more than once

Kafka allows a name to repeat in one record. **The last one is read**,
as Kafka's own `Headers.lastHeader` reads it and as Spring's default
header mapper keeps it. HTTP joins repeats with `", "` (ADR 0069 §2), because
RFC 9110 says a repeated field's value is that list. Kafka says nothing
of the kind, and joining bytes isn't defined.

**Names match exactly.** Kafka and Redis names are strings with no rule
for case, unlike HTTP's (RFC 9110 §5.1). A name is not empty.

### 4. The element is the one argument without `from`

ADR 0021's elimination rule, as HTTP's body uses it: every argument
with a `from` is claimed by it, and exactly one is left, the element.
The route binds every parameter of its workflow. I3 now reads that
way: one element, and nothing else unbound.

### 5. The contract (amends ADR 0111 §1)

`fiig.stream.Delivery` gains two fields, and the package a new model:

```fiig
model Delivery {
    id: string
    payload: bytes
    attempt: int
    order: string
    key: bytes | None
    headers: List<StreamHeader>
}

model StreamHeader {
    name: string
    value: bytes
}
```

Headers are a list, not a `Map`: the source hands over what the broker
sent, in order and with repeats, and the binding decides (§3). A later
rule can read them all without the contract changing again.

### 6. Each source

- **Kafka** (`fiig.kafka`): a fetched record's key, `None` for null, and
  its headers in order. A header whose value is null reads as empty
  bytes (the producer's `KafkaHeader` has no null value either). A
  header whose name isn't UTF-8 is left out: no route can name it, since
  route names are fiig strings.
- **Redis Streams** (`fiig.redis`): an entry is a flat list of fields.
  `payload` is the element (ADR 0111 §7); **every other field is a
  header**, in the entry's order. A producer writes `XADD orders *
  payload <json> trace-id abc`. A Redis entry has no key, so `key` is
  `None`.

**`from key` is not refused at check time** for a source without keys.
The checker doesn't know the source; the capabilities file names it, per
deployment (ADR 0111 §2). Requiring `from key` to be optional everywhere
was the other choice. It was rejected because keyless records are a
Kafka fact too, not only Redis's, so `| None` is already the honest type
for a program that may meet them; and a program whose producers always
key their records would otherwise carry a `None` arm it can never take.
A required key under Redis halts on the first element, naming the
source and saying the element has no key, which is as early as anything
can find it.

## Alternatives considered

- **A metadata parameter**: the workflow takes `meta: StreamMeta` (key,
  headers, partition, offset) beside the element. One rule instead of
  two, and every value reachable. The owner rejected it: the workflow
  then knows it reads a stream, and can't sit behind an HTTP route or a
  test without a fake `StreamMeta`. That is the coupling ADR 0044 and
  ADR 0069 §1 keep out of workflows.
- **The key and headers as fields of the element model**, filled from
  the record instead of the payload. It hides where a field comes from
  in the model, which is domain code, and the same model decoded from an
  HTTP body would fill them from JSON instead.
- **Join repeated headers**, as HTTP does. There is no defined join for
  bytes, and Kafka's own API reads the last.
- **Lenient on bad UTF-8 or a missing required header** (dead-letter or
  `None`). ADR 0111 §4 halts on a payload that doesn't decode for the
  same reason: skipping loses an element silently, and the program can
  already say it wants leniency, with `| None` or `bytes`.

## Consequences

- Stream route arguments take `from header` and `from key`; `fiig fmt`
  prints them. New bug class **I11** (a header or key argument's type, or
  an empty header name); **I3** reworded (§4).
- `fiig.stream` gains `StreamHeader` and two `Delivery` fields. Every
  source builds them: `fiig.redis` reads entry fields, `fiig.kafka`
  parses record keys and headers it used to skip.
- The runtime binds the values when it calls the workflow; `fiig run
  -replay` binds the same way.

## Open questions

1. **Typed header values** (`int`, `decimal`, enums), once programs show
   which encoding they read (§2).
2. **Reading every value of a repeated header**, as `List<string>`.
3. **A source saying it has no keys**, so `fiig serve` could refuse a
   required `from key` at startup rather than at the first element.
