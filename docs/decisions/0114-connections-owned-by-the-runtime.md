# ADR 0114: Connections the Runtime Owns, Driven by Protocols Written as State Machines

Status: Proposed (2026-11-20), design only
Date: 2026-11-20
Answers: ADR 0109 open question 2 (a connection owned by something that
outlives every call).
Related: ADR 0042 (the socket floor), ADR 0060 §5 (a connection belongs
to its call), ADR 0084 (the driver pool), ADR 0109 (connections kept
between calls), ADR 0111 (stream sources), ADR 0113 (the Kafka
producer, at least once).

## Context

ADR 0109 lets a call keep an idle connection for the next call. That
covers request-then-reply protocols used one call at a time. Three things
it can't do, and each is now blocking something:

- **Many calls over one connection at once.** HTTP/2, and so gRPC,
  carries many requests on one connection as numbered streams, with one
  reader taking every frame and handing each to the request it belongs
  to. A kept connection belongs to one call at a time.
- **Work no call asked for.** A Kafka consumer must heartbeat its group
  every few seconds or lose its partitions; HTTP/2 must answer PING and
  send WINDOW_UPDATE; a Redis subscription delivers messages whenever
  they come.
- **State between calls.** Kafka's idempotent producer keeps a producer
  id and per-partition sequence numbers (ADR 0113 §1); a consumer keeps
  its group generation and assignment; HTTP/2 keeps its header-compression
  tables (HPACK) and flow-control windows, which are per connection and
  change with every frame.

All three need one owner per connection that lives longer than any call:
it reads everything the server sends, keeps the state, and serves the
calls that use the connection.

### Who owns a multiplexed connection elsewhere

| | Owner of the connection | Where protocol state lives | How a caller gets its reply |
| --- | --- | --- | --- |
| **Go** `net/http2` | a `ClientConn` in the `Transport`'s pool, with one `readLoop` goroutine per connection | fields of `ClientConn`, under a mutex | a channel per stream (`clientStream`) |
| **Rust** hyper / tonic | the `Connection` future `http2::handshake` returns, which the caller must `tokio::spawn`; tonic's `Channel` adds a tower `Buffer` worker task | inside the spawned future (h2's `Connection`) | a `oneshot` per request through the cloneable `SendRequest` |
| **Elixir** Mint / Finch | a process: Finch runs one `gen_statem` per HTTP/2 connection | **Mint's `conn`, a plain immutable value**: `Mint.HTTP.stream(conn, message)` returns the next `conn` and the responses | a message to the calling process |
| **Java** Netty / grpc-java | the channel's `EventLoop` thread; `Http2MultiplexHandler` makes a child channel per stream | handler objects in the channel pipeline | a future per call; grpc-java's `ManagedChannel` pools transports |

All four put one long-lived owner per connection that does all of its
reading and serializes changes to its state. They differ in how the
owner is spelled (a goroutine, a spawned future, a process, an
event-loop thread) and in whether the protocol is written as code that
owns the I/O (Go, h2, Netty) or as a **pure value the owner feeds bytes
to** (Mint).

Mint is the one that fits fiig. fiig has no goroutines, futures, or
processes in the language, and doesn't want them (ADR 0047 §2 rejected
generators and loops escaping a gateway for the same reasons). It is
good at exactly what Mint asks of a protocol: a pure function from
`(state, bytes received)` to `(new state, bytes to send, replies)`. And
fiig already has the other half: the runtime owns a driver's pool (ADR
0084) and a stream route's loop (ADR 0111). Finch is that half.

## Decision (proposed)

### 1. A session protocol is a state machine written in fiig

A package provides `fiig.session.SessionProtocol`, whose operations are
pure: they take the connection's state and an input, and return the next
state and what to do. They never touch a socket.

```fiig
interface SessionProtocol {
    opened(settings: Map<string, string>, now: int) -> Step     // the connection is up
    received(state: bytes, data: bytes, now: int) -> Step       // bytes the server sent
    asked(state: bytes, message: bytes, now: int) -> Step       // a call wants something
    tick(state: bytes, now: int) -> Step                        // nextTickAt came
}

model Step {
    state: bytes                  // the connection's next state (§4)
    write: bytes                  // what to send, perhaps nothing
    accepted: int | None          // asked: the key the reply will carry, or None when full
    replies: List<SessionReply>   // replies to earlier asks
    nextTickAt: int | None        // when tick is wanted next (a heartbeat, a ping)
    close: SessionClose | None    // the connection is done, and why
}

model SessionReply {
    key: int
    message: bytes
    last: bool                    // false for one of several parts, such as a streamed body
}
```

### 2. The runtime owns the connection and runs the machine

The capabilities file binds a session as it binds a driver:

```json
{"Orders": {"type": "session", "protocol": "fiig.http2",
            "settings": {"url": "${ORDERS_URL}"}, "connections": 2}}
```

The runtime opens the socket (with `Socket`, so ADR 0086's certificates
apply), and then, per connection, owns three things: the socket, the
state value, and one reader. It calls the protocol's operations **one at
a time per connection**, so the state is never shared: Mint's discipline,
enforced by the runtime instead of by a process mailbox.

- Bytes arriving: `received`, then write `write`, deliver `replies`.
- A call's message: `asked`, write, and the call waits for the replies
  with its key, until one is `last`, or its deadline (ADR 0076).
- `nextTickAt`: the runtime calls `tick` then, and only then, so a
  heartbeat costs nothing in between.
- `accepted: None` (no free stream, HTTP/2's `MAX_CONCURRENT_STREAMS`) or
  a connection that is `close`d: the runtime tries another connection or
  opens one, up to `connections`, or the call waits within its deadline.
- A socket error, a fault in an operation, or `close`: every waiting call
  gets `Err(SessionError.Broken(...))`, and the connection is replaced on
  the next call. A fault in a protocol package is a defect, so it is
  also logged as one.

### 3. Programs call it through a gateway of the protocol package

A protocol package's public gateways take fiig values, encode them, and
use the session primitive:

```fiig
capability Http2Client {
    gateway send(request: Request) -> Result<Reply, HttpError>
        requires Session
    {
        message = encodeRequest(request)
        parts = mapErr(e in Session.call(message, deadline: request.deadline)): HttpError.Session(failure: e)?
        return decodeReply(parts)
    }
}
```

`Session.call(message) -> Result<List<bytes>, SessionError>` is the one
new primitive operation, typed in the table beside `Socket`'s (ADR 0060
§3). It reaches the binding of the capability the gateway's caller
requires, as `Database` does through its driver (ADR 0084 §2). The
program never sees a connection, a stream id, or the state.

### 4. State is bytes, for now

The contract has to name the state's type, and fiig has no generics, so
`bytes` is the one type every protocol can use: the package encodes its
own state model with binary patterns (ADR 0058) and reads it back.

This is the weakest part of the design. It costs an encode and a decode
per step, and a protocol that keeps tables (HPACK's dynamic table, up to
4 KiB by default) re-encodes them every frame. Two ways out, both open
question 1:

- a declared state type checked by shape, as the runtime finds a driver's
  gateways by name (ADR 0084 §1), with a new bug class for a package
  whose operations disagree on it;
- generics, if fiig ever gets them.

### 5. How this unblocks the three cases

- **HTTP/2 and gRPC**: `fiig.http2` is a `SessionProtocol` whose state
  holds the HPACK tables, the flow-control windows, and the open streams;
  `asked` allocates the next odd stream id and writes HEADERS and DATA;
  `received` splits frames and answers PING and SETTINGS. gRPC is then a
  library over `fiig.http2` with length-prefixed messages.
- **The Kafka consumer** (ADR 0111's next source): the state holds the
  group generation, member id, assignment, and fetch offsets; `tick`
  sends Heartbeat; the source's `receive` becomes `Session.call` of a
  Fetch, so the stream adapter itself doesn't change.
- **The idempotent Kafka producer**: the producer id and sequence numbers
  live in the state of a session per broker, so ADR 0113 §1 can turn
  idempotence on without changing `Kafka.send`'s signature.

## Alternatives considered

- **HTTP/2 in Go, in the runtime** (Netty's model, with Go's `x/net/http2`).
  Fastest to build and well tested, and exactly what ADR 0042 rejects:
  gRPC would be backed by Go.
- **Owners in the language**: a `process` or `actor` declaration with a
  mailbox, as in Erlang. It is the most general answer, and it brings a
  second control-flow model, unbounded lifetimes, and message ordering to
  a language whose every declaration is called and returns (ADR 0047 §2).
  A session is one specific owner the runtime can run without fiig code
  ever running concurrently with itself.
- **A gateway that never returns**, looping over the socket. It breaks
  deadlines, connection ownership (ADR 0060 §5), and F1, as ADR 0047 §2
  found, and a gateway still can't call the workflow a reply is for.
- **Extending ADR 0109's pool** with "lend this connection to two calls".
  Two calls reading one socket interleave each other's bytes; something
  has to read on both's behalf, which is this ADR's owner.

## Consequences (if accepted)

- A built-in package `fiig.session` (the interface and `Step`), a
  `"session"` capability type and `Session.call` in `compiler/runtime`,
  with a per-connection reader and serialized calls into the protocol.
- `fiig.http2`, then a gRPC library, then a Kafka consumer source, each
  its own ADR.
- ADR 0109 open question 2 and ADR 0113 open question 1 point here.

## Open questions

1. **The state's type** (§4): bytes, a shape-checked declared model, or
   generics.
2. **Backpressure**: a server that streams faster than calls read. HTTP/2
   has flow control for this, and the state machine would grant window
   only as a call takes its parts, which needs a fifth input, "a call
   took part of its reply".
3. **Server push and server-initiated streams** (HTTP/2 PUSH_PROMISE,
   Redis pub/sub messages with no ask): replies with no waiting key. A
   stream source over a session could take them as elements.
4. **How many connections**, and when a session should open another
   rather than queue (§2).
5. **Testing**: the state machine is pure, so `fiig test` can drive it
   with bytes alone; whether the runtime half wants a scripted fake like
   `socket-script`.
