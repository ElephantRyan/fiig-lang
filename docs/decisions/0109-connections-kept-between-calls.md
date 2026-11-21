# ADR 0109: Connections Kept Between Calls

Status: Accepted and implemented (2026-11-20; Implementation notes)
Date: 2026-11-20
Amends: ADR 0060 §5 (a connection belongs to the call that opened it),
ADR 0074 §8 (one connection per request).
Related: ADR 0035 (capability review), ADR 0042 (the socket floor), ADR
0047 (streams), ADR 0076 (request deadlines), ADR 0079 §6a (per-package
bindings), ADR 0084 (the driver pool), ADR 0086 (`Socket`'s entry in the
capabilities file).

## Context

ADR 0060 §5 makes the top-level call the owner of every connection it
opens, and closes them when the call ends. It said a connection that
outlives a request "needs an owner longer-lived than a call ... and its own
ADR." ADR 0084 then made one exception: connections a `fiig.sql.SqlDriver`
opens belong to the runtime's pool for that `Database`. It said `fiig.http`
"could later use a pool of the same kind; this ADR doesn't generalize one."

That leaves every other protocol written in fiig paying a new connection
per call:

- **`fiig.http`** connects, sends `Connection: close`, and closes (ADR 0074
  §8). Every call to an `https` API pays a TCP handshake and a TLS
  handshake, one to three round trips, before the first request byte.
- **Redis, and every protocol after it** on the list in ADR 0042 (Kafka's
  producer, MongoDB, SMTP), would do the same. Redis is used as a cache
  because it answers in well under a millisecond; a new TLS connection per
  command makes it slower than the database it fronts.

ADR 0084's pool can't simply be reused for these. It sits behind a
capability the runtime mediates (`Database`), and it exists in that form
because `queryOne`'s result type comes from the call site and fiig has no
generics (ADR 0084, "Alternatives considered"). `HttpClient.send` and a
Redis command have no such problem: their whole protocol is fiig code that
holds a `Connection`. They need to keep the connection, not to hand the
protocol to the runtime.

### Two different things called "a long-lived connection"

1. **An idle connection kept for reuse.** Nothing about the connection
   matters to the next call except where it goes and how it was set up
   (host, TLS, and who it logged in as). HTTP keep-alive, a Redis command
   connection, a Kafka producer's connection to a broker.
2. **A connection that carries a session across many calls.** A Redis
   `SUBSCRIBE`, Postgres `LISTEN`, a Kafka consumer's group membership, a
   `MULTI` left open between calls. The server remembers what has happened
   on the connection, and that state is the point.

This ADR is about the first only. The second needs something that runs
for longer than a call and gets told when the server sends something. That
is an adapter, the subject of ADR 0047, not a pool.

### How pools are keyed elsewhere

| Library | Who owns the pool | Key |
| --- | --- | --- |
| Go `net/http.Transport` | the transport value | proxy, scheme, address (`connectMethodKey`); 2 idle per host and 100 in all by default, idle 90 s |
| urllib3 `PoolManager` | the manager | scheme, host, port, and the TLS settings (CA, client certificate, verification) |
| hyper-util | the client | scheme and authority |
| Elixir Finch over Mint | a pool per origin (NimblePool) | scheme, host, port; Mint's connection is a plain value a process checks out and checks back in |
| redis-py `ConnectionPool` | one per client object | the client's own host, port, database, and credentials |

Two lessons. Every pool keys by where the connection goes *and* how it was
secured, because a connection that trusts a different CA isn't
interchangeable. And where the server remembers who the connection is
(redis-py's database and credentials, a Postgres user), that identity is
part of the key. Usually it gets there by giving each configured client
its own pool.

Finch and Mint are the closest fit. Mint, like a fiig protocol package, is
protocol code that takes a connection value and returns it. Finch keeps the
idle ones, and the code using a connection decides whether it's clean
enough to check back in.

Fiig has no client object to hang a pool on: `HttpClient` is a capability
group, not a value a program constructs. So the identity has to travel
with the connection explicitly.

## Decision

### 1. Two new `Socket` operations

```
Socket.reuse(host: string, port: int, tls: bool, session: string) -> Connection | None
Socket.keep(connection: Connection, session: string)               -> None
```

- **`keep`** hands the connection to `Socket`'s pool for later calls. From
  then on it belongs to the pool, not the call. Using the handle again is
  a fault, as using a connection after its owner ended is (ADR 0060 §4).
- **`reuse`** takes an idle connection from the pool that matches §2's
  key. Ownership moves to the calling call, and ADR 0060 §5 applies again:
  if the call ends without `keep` or `close`, the runtime closes it. With
  no match it returns `None` at once. It never waits and never opens a
  connection.

A protocol package follows the same pattern every time:

```fiig
connection: Connection | None = Socket.reuse(host, port, tls: true, session: "")
// None -> Socket.connectTls(...) as today, then the handshake
// ... the exchange ...
// clean -> Socket.keep(connection, session: "")
// anything else -> Socket.close(connection)
```

**The safe default is today's behaviour.** A package that never calls
`keep`, or forgets to on some path, gets ADR 0060 §5: the connection is
closed when the call ends. Forgetting `keep` costs a handshake, not a
leak.

Both operations go in the compiler-known table of typed primitive
signatures (ADR 0060 §3), so they need `requires Socket`, and B10 keeps
them out of pure code.

### 2. The key

A kept connection is reused only by a call that matches all of these:

- **the package whose code called `keep`.** The gateway running `reuse`
  must be in the same package. A connection that has logged in is a
  credential. Handing one to another package would bypass that package's
  own configuration and the manifest review of ADR 0035;
- **the `Socket` binding.** A per-package binding (ADR 0079 §6a) with its
  own `trust` or client certificates (ADR 0086) gets its own pool;
- **host and port**, exactly as given to `connect` or `connectTls`;
- **TLS, and the server name it was verified for.** `reuse(..., tls: true)`
  matches only a connection whose TLS server name is its `host`.
  `connectTls` connections always qualify, and a `startTls` to a different
  name never matches;
- **`session`**, a string the package writes.

**`session` names everything the server remembers about the connection**
that would change the next exchange: the user it authenticated as, the
Redis database it `SELECT`ed, a protocol version it negotiated. HTTP/1.1
has none and passes `""`. The argument is required and has no default, so
every package that keeps connections writes down, at the call, what it
believes the connection carries.

The checker can't verify the session string. Whether it captures the
server's state is a property of the protocol, not of the program text. Of
ADR 0047's three buckets this one is "document", and the price of getting
it wrong is bounded by the first bullet above: a package can only confuse
itself.

### 3. What the runtime refuses to keep

`keep` is a request. The runtime closes the connection instead, silently,
when:

- any operation on it returned a `SocketError` (`Closed` included);
- bytes the peer sent are waiting unread. The protocol didn't consume
  everything, so the next user would read the end of someone else's reply;
- the pool for that key already holds its limit of idle connections (§5).

These are network outcomes the program can't act on, so they aren't
errors. The cases that are misuse fault, as other misuse of a connection
does (ADR 0060 §4):

- `keep` on a connection this call doesn't own: a closed one, one already
  kept, or one a driver's `connect` opened for ADR 0084's pool;
- `keep` on a handle that was passed to `startTls` (the new handle is the
  one to keep).

The runtime can only see bytes it has buffered or that are on the socket.
Bytes the protocol read into a fiig `bytes` value and didn't use are
invisible to it. Checking its own buffer is empty before `keep` is the
protocol's job (§6).

### 4. What `reuse` checks

Before handing over an idle connection, the runtime checks it without
blocking. A connection the peer has closed, or one with unexpected bytes
waiting, is discarded, and the next idle one is tried. Servers close idle
connections on their own schedule (nginx after 75 s, Node after 5 s by
default), so this check catches most stale connections, not all: a server
can still close in the moment between the check and the first write.

`reuse` returns plain `Connection`, so the package knows the connection was
reused and can treat a failure right at the start differently. Whether to
retry is the protocol's decision (§6 for HTTP). The runtime never retries.

### 5. Limits, in `Socket`'s entry

```json
{"Socket": {"type": "socket",
  "idle": {"perKey": 10, "total": 100, "timeout": "90s"}}}
```

- **`perKey`** (default 10): idle connections kept per key. Go's default
  of 2 is the setting most often raised in practice; 10 matches ADR 0084's
  `maxConnections` and the `requests` library's `pool_maxsize`.
- **`total`** (default 100, Go's): idle connections across all keys, so a
  program calling many hosts can't hold thousands of sockets. Past the
  limit, the connection idle the longest is closed.
- **`timeout`** (default 90 s, Go's `IdleConnTimeout`): an idle connection
  older than this is closed by the runtime.

There is no limit on *open* connections per key. As with Go's default,
concurrent calls bound them, and capping them would add a wait that ADR
0084's pool needed for transactions but these protocols don't. The pool
closes when the runtime stops (`CapabilityRegistry.Close`), as ADR 0084's
does.

### 5a. A maximum lifetime

*(Amended 2026-11-21: closes open question 1.)* `idle` gains
**`maxLifetime`** (default `"10m"`; `"0s"` is no limit): a connection
older than this, counted from when `connect` opened it, not from when it
was last kept, is not handed out by `Socket.reuse`, not taken by
`Socket.keep`, and is closed while idle when its lifetime ends, or at the
idle timeout if that comes first. Reuse doesn't reset its age, and
neither does `startTls`.

```json
{"Socket": {"type": "socket",
  "idle": {"perKey": 10, "total": 100, "timeout": "90s", "maxLifetime": "10m"}}}
```

- **Why it's needed after all.** Open question 1 left it out because the
  idle timeout covers DNS moves for HTTP. It covers them only for a
  connection that goes quiet: a service calling one API several times a
  minute never leaves its connection idle for 90 s, so it would talk to
  the address it first resolved until that host goes away. Cloud load
  balancers change their addresses as they scale, with DNS TTLs of about
  60 s, and expect clients to follow within minutes.
- **Ten minutes**, not ADR 0084's 30, because these are connections to
  load-balanced services rather than to one database server, whose
  address changes only on failover. Ending a connection costs one TCP and
  TLS handshake, a few milliseconds every ten minutes per connection,
  which is nothing beside following a move. Go's `Transport` has no such
  limit; HikariCP's is 30 minutes and pgx's an hour, both for databases.
- **Measured from opening, not from keeping**, so a connection reused
  without a pause still ends, which is the case the idle timeout misses.
- **Checked on `reuse` as well as by a timer**, so a connection is never
  handed out late however the timer is scheduled.

### 6. `fiig.http` keeps connections (amends ADR 0074 §8)

- **No more `Connection: close`.** `send` calls `reuse(host, port, tls:
  secure, session: "")` first and connects only on `None`.
- **Kept only when the reply's end was known.** After `ReplyRead.Done`,
  the connection is kept only if: the body was delimited by
  `Content-Length` or chunked encoding, not by the server closing; the
  reply is HTTP/1.1 without `Connection: close`; the status isn't `101`;
  and nothing is left in the read buffer. Otherwise it's closed as today.
  `ReplyRead.Done` gains `keep`, which `readReply` works out from all
  four.
- **One retry for a stale connection.** If the connection came from
  `reuse` and the write fails, or the first read ends `Closed` or `Reset`
  before any reply byte, `send` retries once on a new connection, within
  the same deadline. It does so only for the idempotent methods (RFC 9110
  §9.2.2: `GET`, `HEAD`, `OPTIONS`, `TRACE`, `PUT`, `DELETE`), as RFC 9112
  §9.3.1 permits and Go does. A `POST` that fails this way is returned as
  `HttpError.Socket`, as today. ADR 0074 §9's "retries belong to the
  caller" still holds: this retry is for a connection that went stale
  while idle, not for a request the server failed.
- ADR 0074's open question 1 is closed by this section.

### 7. What this ADR doesn't do

- **Sessions across calls** (the second meaning in Context): `SUBSCRIBE`,
  `LISTEN`, consumer-group membership. A connection doing any of these must
  not be kept, and a protocol package that offers them needs the runtime
  to run something for longer than a call. That's ADR 0047's adapter.
- **One connection shared by concurrent calls.** `reuse` hands out a
  connection to one call at a time. HTTP/2 and therefore gRPC multiplex
  many requests over one connection, and need a connection read by
  something that outlives each request. That is the same thing sessions
  need, so it waits on the same design (open question 2).
- **ADR 0084's pool is unchanged.** It answers a different question:
  pinning one connection across a `transaction Database` block, and
  deciding keep-or-discard from a typed `DatabaseError` the runtime reads.
  A driver's connections stay in that pool and can't be passed to `keep`
  (§3).

### 8. Bug classes

None new. Using a handle after `keep` is a fault, as using one after
`close` is. Both could in principle be found statically, as a moved
value is in Rust, but fiig checks neither today, and checking only one
would be inconsistent.

## Alternatives considered

- **Generalize ADR 0084's driver pool.** Each protocol would declare a
  contract like `SqlDriver` (`connect`, `exchange`, `close`), and the
  runtime would mediate every call to it. That needs Go in the runtime for
  every protocol's capability type, which is what ADR 0042 moved away
  from. ADR 0084 accepted it for `Database` only because generics forced
  it.
- **Make `connect` reuse and `close` keep, implicitly.** No existing
  caller would change. But every existing caller would silently start
  receiving connections that are already set up. The Postgres driver would
  send a `StartupMessage` to a connection past its startup. "Close" would
  quietly mean "maybe keep", which is the decision that most needs to be
  visible. The protocol
  also couldn't tell a reused connection from a new one, which §6's retry
  needs.
- **A pool the program holds** (Rust's `deadpool` or `sqlx::Pool` in an
  axum `State`). Every gateway signature would carry it. Rejected for the
  same reason as ADR 0084's program-held connection.
- **A pool written in fiig, as an actor** (Redix runs a process per
  connection). Needs long-lived mutable state shared by concurrent calls,
  which fiig doesn't have and ADR 0084 already declined to add for the
  same purpose.
- **Key by host and port only.** Simplest, and right for HTTP. Wrong for
  every protocol with a login: a Redis connection authenticated as one
  user and `SELECT`ed to database 2 would serve a call expecting database
  0. redis-py avoids this only because each client object has its own
  pool. Fiig has no client object, hence `session`.
- **Key per binding, not per package.** Two packages sharing a `Socket`
  binding would share logged-in connections. Rejected in §2 as a
  credential crossing a package boundary.

## Consequences

- `Socket` gains `reuse` and `keep`, in the typed signature table and in
  the Go provider; the provider holds the pool, keyed per §2.
- ADR 0060 §5 is amended: a connection belongs to the call that opened or
  reused it until it calls `keep`, and from then on to `Socket`'s pool
  until a later `reuse`.
- `Socket`'s capabilities-file entry gains `idle` (§5). `fiig config`
  lists nothing new: the fields aren't secrets and have defaults.
- `fiig.http` keeps connections (§6); `docs/known-limitations.md` drops
  "opens a connection per request".
- The scripted Socket (`socket-script`) gets a pool like the real one, so
  a script plays several requests over one connection by listing them on
  one peer, and a test of the stale-connection retry gives that peer a
  `hangup` where the second reply would be. A `SocketProvider` built in
  Go without a `Pool`, as most tests do, keeps nothing, so their scripts
  run unchanged.
- A `fiig.redis` package can be written on this with no further runtime
  work. It would keep connections with `session` naming the user and
  database, and never keep one that ran `SUBSCRIBE` or left a `MULTI`
  open.

## Open questions

1. **A `maxLifetime` for kept connections**, like ADR 0084's. *(Closed by
   §5a, 2026-11-21: `idle.maxLifetime`, 10 minutes by default.)* It's useful
   when DNS moves a host to a new address. The idle timeout covers most of
   that for HTTP, so it's left out until a protocol needs it.
2. **A connection owned by something that outlives every call.** HTTP/2,
   gRPC, subscriptions, and stream sources (ADR 0047) all need a connection
   that one long-running reader owns and many calls use. This should be
   designed with ADR 0047's adapter, not as a second kind of pool.
   *(Answered 2026-11-21: sessions, ADR 0114, built by ADR 0117.)*
3. **Should a package see why `keep` declined?** Right now it can't tell a
   kept connection from one closed for unread bytes, which is the case most
   likely to be a bug in the protocol. A counter in runtime metrics
   (Milestone 8) may be enough, without changing `keep`'s type.
4. **Should ADR 0084's pool be rebuilt on this one**, keeping only
   transaction pinning special? It would leave one pool implementation, but
   gains nothing a program can see, so it's only worth doing if the two
   start to drift.

## Implementation notes (2026-11-20)

- **Runtime** (`compiler/runtime/socket_pool.go`): `SocketPool`, held by
  `SocketProvider.Pool`; `NewSocketProvider` and both capabilities-file
  factories (`tooling/runprogram/socket.go`) give the provider one, and
  `CapabilityRegistry.Close` closes it. A `connHandle` now records its
  host, port, the TLS server name it was verified for, whether an
  operation failed, and whether it was kept. The two operations are typed
  in `compiler/types/methods.go` beside the other six.
- **The package in the key is the root of the running scope's
  `Universe`.** A file with private members runs under a file scope of
  its own (ADR 0054), so the scope's `Universe` itself would have split
  one package into a key per file.
- **Watching an idle connection** is Go's `Transport` technique: a
  goroutine reads one byte from each idle connection. If the read returns
  (bytes, or the peer closing), the connection is closed straight away.
  `reuse` stops it by setting a deadline in the past; a timeout means
  nothing came, so the connection is clean. That works over TLS, because
  `crypto/tls` treats a read timeout as temporary, which the real-server
  test exercises. The scripted socket can't be read out of turn without
  failing its script, so it reports staleness from its script instead
  (`staleWhileIdle`: the peer closed, or the next step is a reply or a
  hangup).
- **The idle timeout is a timer per kept connection**, not a janitor, and
  each connection is claimed under the pool's lock by exactly one of
  `reuse`, the timer, the watch, the total limit, or `Close`.
- **`fiig.http`:** `retry` (ADR 0088) and `idempotent` (ADR 0014) are
  keywords, and a capability group can't have a `private` block, so the
  two helpers are conversions in their own files: `safeToResend` and
  `closedByServer`. `send` gets its connection with `mapErr` (ADR 0095),
  which also removed a level of nesting. `QUERY` counts as safe to resend:
  its draft defines it as safe and idempotent. `Method` has no `TRACE`.
- **Tests:** `compiler/runtime/socket_pool_test.go` (reuse by key, an idle
  connection closed or written to by the server, keep after a failure,
  use after keep, the three limits, per-package keys, a driver's
  connection); `tooling/runprogram/http_client_test.go`
  (`TestFiigHttpKeepsConnections`: reuse, the stale resend, a `POST` not
  resent, `Connection: close` and HTTP/1.0 honoured); and
  `http_keepalive_test.go`, where three calls against Go's own HTTP
  server, plain and over TLS, open one connection between them. The
  capabilities schema gained `idle`.
