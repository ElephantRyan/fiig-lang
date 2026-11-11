# ADR 0060: Socket Operations and the Connection Handle

Status: Accepted (2026-10-26, on the author's approval of folding `TlsSocket`
into `Socket`; see "Decisions taken at review"). Implemented 2026-10-26;
see "Implementation notes". §3 and §4 amended by ADR 0074 §5 (2026-11-10):
every operation that waits takes an optional `deadline`, and running out
of it is `Err(SocketError.TimedOut)`.
Date: 2026-10-26
Related: ADR 0010 (a gateway requires exactly one capability), ADR 0027
(declarative capability configuration), ADR 0035 (manifest capability
allowlist), ADR 0042 (the native floor is the socket), ADR 0043 (capability
groups written in fiig), ADR 0052 (comparability), ADR 0058 (bytes), ADR
0059 (`while`).
Amends: ADR 0042's primitive set (`TlsSocket` folds into `Socket`).
Milestone 7: prerequisite for "One reference wire protocol written in fiig"
and "Postgres wire protocol in fiig".

## Context

ADR 0042 made `Socket` and `TlsSocket` the floor and implemented neither.
They have no operations, no Go provider, and no types. The only use in the
corpus, `acme-http`'s `Client`, calls an invented `TlsSocket.exchange(url,
body)` that stands in for "connect, handshake, write, read, close". The
checker does not type primitive capability calls at all: `Clock.now()` and
`TlsSocket.write(...)` both come back unmodeled.

To write a protocol in fiig, four things have to be decided:

1. **The operations** and their signatures.
2. **What a connection is.** Something has to come back from `connect` and
   go into every later `read` and `write`.
3. **How long a connection lives**, and what closes it if fiig code
   doesn't.
4. **How TLS fits in.** Here the corpus taught something ADR 0042 didn't
   know.

### Postgres does not start with TLS

ADR 0042 says TLS is "exposed as a socket that is already encrypted". That
fits HTTPS, which handshakes the moment the connection opens. Postgres
doesn't work that way. A client opens plain TCP, sends an 8-byte
`SSLRequest` (length 8, code 80877103), and reads one byte back. `S` means
the server agrees, and the TLS handshake then runs *on the same
connection*. `N` means carry on in plaintext. Until Postgres 17's
`sslnegotiation=direct`, this was the only way to reach Postgres over TLS.
SMTP's `STARTTLS`, IMAP, LDAP StartTLS and MySQL's handshake all work the
same way.

Every precedent treats upgrading as the basic operation, and connecting
with TLS from the start as shorthand for "connect, then upgrade":

| Runtime | Plain | TLS from the start | Upgrade a live connection |
| --- | --- | --- | --- |
| Go | `net.Dial` | `tls.Dial` | `tls.Client(conn, cfg)`: a `*tls.Conn` is a `net.Conn` |
| Erlang | `gen_tcp:connect` | `ssl:connect(Host, Port, ...)` | `ssl:connect(TcpSocket, ...)` |
| Python | `socket.create_connection` | `ctx.wrap_socket(...)` | the same `wrap_socket`, on a connected socket |
| Deno | `Deno.connect` | `Deno.connectTls` | `Deno.startTls(conn)` |

So a floor with no upgrade operation can't reach Postgres over TLS.

### ADR 0010 shapes the TLS question

A gateway requires exactly one capability (ADR 0010). If `Socket` and
`TlsSocket` stay separate capabilities, a gateway that opens a TLS
connection can't also read a plain one, and a gateway that sends
`SSLRequest` over `Socket` can't upgrade with `TlsSocket`. §1 turns on this.

## Decision

### 1. One `Socket` capability; TLS is an operation, not a capability

`TlsSocket` leaves the primitive set. `Socket` gains `connectTls` and
`startTls`. The primitive set becomes:

```
Socket   Clock   FileSystem   Random
```

TLS stays native for the reason ADR 0042 gave: it needs audited
constant-time cryptography, which fiig should not reimplement. Only its
standing as a separate *capability* goes. The plain and secure paths share
one connection type (§2), so a reading gateway is written once, whichever
way the connection was opened.

The Postgres opening, in one gateway:

```
gateway openSecure(host: string, port: int) -> Result<Connection, SocketError>
    requires Socket
{
    connection = Socket.connect(host, port)?
    Socket.write(connection, <<8: i32be, 80877103: i32be>>)?
    answer = Socket.read(connection)?
    match answer {
        <<83: u8>> -> Socket.startTls(connection, host)
        _ -> {
            Socket.close(connection)
            return Err(SocketError.Refused)
        }
    }
}
```

Keeping two capabilities would split that into a `Socket` gateway, a
`TlsSocket` gateway and an activity to sequence them. Worse, every reading
gateway in a protocol that supports both plaintext and TLS (Postgres,
Redis, HTTP and SMTP all do) would exist twice, once per capability. See
Alternative A for what keeping them separate would buy.

### 2. `Connection` is an opaque prelude type

`Connection` joins `Result`, `List` and `BatchResult` as a prelude name.

- **Opaque.** It has no fields, no methods and no constructor. `Socket`
  operations create and consume it; nothing else can.
- **One type for plain and secure.** After `startTls`, the same value is
  encrypted, the way Go's `*tls.Conn` is a `net.Conn`. `startTls` returns
  the connection rather than updating it in place, so the code reads as
  "this is now the secure one". The handle it was given refers to the same
  connection and must not be used for plaintext again (§4).
- **Not comparable** (ADR 0052). Comparing two handles would be identity
  comparison, which nothing else in fiig has, and no protocol in the
  milestone needs it.
- **May sit in a model.** A Postgres client keeps its connection next to
  its backend key and parameter status. A model with a `Connection` field
  is otherwise an ordinary model.
- **Never crosses an adapter boundary.** A `Connection`, or a model that
  contains one at any depth, may not be a workflow parameter or return
  type, or an adapter's request or response. It can't be serialized, and a
  handle arriving from outside would refer to nothing. This is new bug
  class **H5** (§7).
- **Pure code may hold one.** A conversion can take a model with a
  `Connection` field and pass it through. It can't do anything with it,
  because every operation needs `requires Socket`, which pure kinds can't
  declare. (This draft said that rule already existed. It did not: a direct
  capability call was unmodeled and passed in any kind. It is now B10; see
  §7 and "Implementation notes".)

Because handles are first-class values, a connection can outlive the
gateway that opened it. That is not optional. Gateways can't call
gateways, so "connect", "send the startup message" and "run a query" are
separate gateways with an activity passing the connection between them.

### 3. The operations

```
Socket.connect(host: string, port: int)               -> Result<Connection, SocketError>
Socket.connectTls(host: string, port: int)            -> Result<Connection, SocketError>
Socket.startTls(connection: Connection, serverName: string) -> Result<Connection, SocketError>
Socket.read(connection: Connection)                   -> Result<bytes, SocketError>
Socket.write(connection: Connection, data: bytes)     -> Result<None, SocketError>
Socket.close(connection: Connection)                  -> None
```

- **`connect`** resolves `host` and opens TCP. Name resolution happens
  inside `connect`, as in Go, Erlang, Python and Deno. DNS could in
  principle be written in fiig over UDP, but fiig has no UDP and nothing
  in the milestone needs it.
- **`connectTls`** is shorthand for `connect`, then `startTls(connection,
  host)`.
- **`startTls`** runs the TLS handshake on a live connection, checking the
  server's certificate against the host's trust store for `serverName`
  (also sent as SNI). An unverified handshake is not offered (open
  question 2).
- **`read`** waits until at least one byte is available, then returns
  between 1 and 65,536 bytes. It never returns empty bytes. A peer that
  closed cleanly is `Err(SocketError.Closed)`, as in Erlang's
  `{error, closed}`, not a zero-length success. ADR 0059's example checked
  `chunk.length() == 0` and should match on `Closed` instead. Taking no
  size argument matches Erlang's `recv(Socket, 0)`. Reading exactly N
  bytes is a loop with a binary pattern, which ADR 0058 and ADR 0059
  already make short.
- **`write`** sends every byte or fails. A partial write is never shown to
  fiig code; the Go provider loops, as Go's `net.Conn.Write` does.
- **`close`** cannot fail in any way a caller could act on. Closing an
  already-closed connection does nothing, as in Python.

These are the first typed primitive-capability signatures. The checker
gets them from a compiler-known table beside `compiler/capability`'s
closed set, not from fiig source. `Clock` stays unmodeled for now, though
the same table is the obvious place for it.

### 4. Errors and faults

```
enum SocketError {
    HostNotFound
    Refused
    Unreachable
    Reset
    Closed
    Tls(reason: string)
}
```

`SocketError` is a prelude enum (ADR 0040). It covers what the *network*
can do to a correct program. A program that misuses a connection gets a
**fault** instead, as an out-of-range `bytes.at` does:

- reading or writing a connection the program itself already closed;
- plaintext use of a handle that has been passed to `startTls`;
- using a connection after its owning call has ended (§5).

Timing out is not a `SocketError` either. A read that waits too long is
stopped by the call's deadline, with the same cancellation fault that
stops a `while true` (ADR 0059 §4).

### 5. Connections belong to the top-level call that opened them

Fiig has no destructors (Rust's `Drop`), no `defer` (Go) and no
`try`-with-resources (Java), and finalizers run at times nobody can
predict. Erlang's model fits best: a port is owned by the process that
opened it, and closes when that process exits, however it exits.

In fiig the owner is the top-level `Interp.Call`: one workflow run or one
adapter request. When it returns, faults or is cancelled, the runtime
closes every connection it opened that is still open. An explicit
`Socket.close` is still the normal path; this rule is the backstop that
stops a forgotten `close` or a fault mid-protocol from leaking file
descriptors.

This deliberately does **not** cover a connection that outlives a request,
such as a Postgres pool shared across HTTP requests. That needs an owner
longer-lived than a call, which is the capability-state question
(blocker 3 in the networking investigation) and its own ADR. Under this
ADR, using a connection after its owner has ended is a fault, so the later
ADR starts from a clean rule, not a silent leak.

### 6. Manifests and providers

- The manifest capability allowlist (ADR 0035) and the H1/H2 checks are
  unchanged, apart from `TlsSocket` no longer being a name to list.
- A Go `SocketProvider` in `compiler/runtime` (or `libraries/socket`, so
  the root module stays free of anything platform-specific) implements
  §3 over `net` and `crypto/tls`, including §5's close-on-exit bookkeeping.
- A scripted fake for tests and `run-cli -caps`: a script of expected
  writes and canned reads per connection. It fails a test whose fiig code
  writes bytes the script didn't expect. Without it, no protocol library
  can be tested without a live server.

### 7. Bug classes

- **H5:** a `Connection`, or a model containing one, appears in a workflow
  signature or an adapter's request or response.
- **B10:** a rule, conversion, activity or workflow calls `Socket` directly.
  Only a gateway touches a capability (§3.1). A gateway that requires
  something other than `Socket` and calls it is the existing P7.

Misusing a handle at runtime (§4) is a fault, not a bug class. The checker
can't see it without tracking what the program has closed, and ADR 0051
rules out a warning that guesses.

## Alternatives considered

**A. Keep `TlsSocket` as a separate capability, with a separate
`TlsConnection` type.** The one real benefit is that "this package never
sends plaintext" becomes a checkable manifest fact: allow `TlsSocket`,
leave out `Socket`. That is a genuine compliance property (encryption in
transit). It costs, under ADR 0010:

- every reading and writing gateway written twice in any protocol that
  supports both modes;
- a `Link { Plain(Connection), Secure(TlsConnection) }` enum, plus an
  activity that matches on it and dispatches to the right pair;
- an upgrade gateway that can't send `SSLRequest` itself.

Deno is the closest precedent that has a permission system at all. It
puts plain and TLS connections under one `--allow-net` and narrows
permission by *host*, not by encryption. Host restriction is also what
actually limits where data can go: TLS to an attacker's host is no safer
than plaintext to it. Rejected in favour of open question 1.

**B. Keep both capabilities but share one `Connection` type.** `TlsSocket`
would only `connectTls` and `startTls`, and reads and writes would all go
through `Socket`. Every TLS user then requires both capabilities, so A's
"no plaintext" property is lost anyway, while ADR 0010 still splits
`openSecure` above into two gateways and an activity. It has the costs of
both other options and the benefits of neither.

**C. Scoped connections: `Socket.open(host, port) as connection { ... }`.**
Like `transaction`, the block would guarantee `close` and the handle
couldn't escape it. Rejected because one block lives inside one gateway
body, and a protocol session spans several gateways (§2). It is the right
shape for a language whose effectful functions can call each other, which
fiig's gateways deliberately can't.

**D. The connection is the capability.** Erlang-flavoured: `connect`
returns a provider value (ADR 0043) and calls look like
`Postgres.read()`. Rejected because provider values are chosen by a rule
and checked structurally against an interface. They are compile-time
identities, not runtime resources, and making them carry a file
descriptor would hand ADR 0043's providers a lifecycle they were designed
without.

**E. `read(connection, max: int)`.** Go's `Read(buf)` takes a size.
Rejected as unnecessary: no protocol in the milestone can use a smaller
read to avoid buffering, and a parser takes what it needs from the front
of the buffer anyway (ADR 0059's `readFrames`).

## Decisions taken at review

- `TlsSocket` folds into `Socket` (§1, Alternative A rejected). This amends
  ADR 0042 and touches `acme-http`, `acme-stripe`, `checkout-app`'s
  manifest and lockfile, and two Go tests. The migration is small: every
  `TlsSocket` use is the invented `exchange`, which this ADR replaces
  anyway.
- Open questions 1–4 below stand as recommended: host allowlists are a
  follow-up, custom CAs wait for the Postgres example, the provider
  serializes per direction, and listening sockets are out of scope.

## Open questions

1. **Host allowlists.** If the security line is "which hosts", not
   "encrypted or not" (Alternative A), the place for it is ADR 0027's
   declarative capability configuration, e.g. `"Socket": {"hosts":
   ["db.internal:5432", "api.stripe.com:443"]}`, enforced by the provider
   at `connect`. Recommended as a follow-up, not part of this ADR.
2. **Custom CAs and unverified TLS.** A local Postgres with a self-signed
   certificate needs one or the other. Both belong in ADR 0027 provider
   configuration, not in fiig source, so a package can't quietly turn off
   verification. Deferred until the Postgres example needs it.
3. **Concurrent use of one connection.** Two `concurrent` branches reading
   the same connection would interleave bytes. One reader plus one writer
   is legitimate (pipelining). Recommended: the provider serializes
   operations per direction, and the question is revisited only if a
   protocol needs more.
4. **Listening sockets.** `listen` and `accept` would let the HTTP server
   adapter move into fiig. Every Milestone 7 exit criterion is client-side,
   and the adapters are the host boundary on purpose (ADR 0044), so this is
   left out.

## Relation to `transaction`

C1 forbids a network gateway inside `transaction Database { }`. Once
`Database` is a fiig capability over `Socket`, every query is a socket
operation, so that rule and this ADR collide. That belongs to the
capability-state ADR, which has to give fiig-written capabilities
transactions anyway. It is noted here so it isn't found by surprise.

## Consequences

- `compiler/capability`: `TlsSocket` removed from the closed set.
- `compiler/types`: `Connection` and `SocketError` added to the prelude.
- `compiler/checker`: typed signatures for `Socket`'s six operations, the
  first typed primitive calls; H5.
- Runtime: a real `Socket` provider with per-call connection ownership
  (§5), and the scripted fake (§6).
- Corpus: `acme-http`'s `Client` rewritten over `connectTls`/`write`/`read`
  in place of `exchange`, and `postgres-wire` gains the `openSecure`
  gateway and ADR 0059 §1's read loop, run against the fake.
- `docs/specification/bug-classes.md`: H5.
- ADR 0042: amended to the four-member primitive set, with a pointer here.

## Implementation notes (2026-10-26)

- **Primitive set.** `compiler/capability` drops `TlsSocket`. A manifest or
  gateway that still names it fails the loader's H2 check like any other
  non-primitive.
- **Prelude.** `compiler/types/prelude.go` adds `types.Opaque` with
  `Connection` as its only member, and declares `SocketError` in fiig source
  parsed once at start-up. `Universe.Lookup` falls back to the prelude after
  a package's own declarations, so `SocketError.Closed` constructs and
  matches like a declared enum in the checker and the runtime alike.
- **Checker.** `compiler/checker/socket.go` holds the six signatures
  (`checkMethodArgs`, positional only), H5 (workflow signatures, walking
  model fields at any depth), and the comparability rejection (uncoded,
  pending ADR 0052).
- **B10 was a real hole.** Writing the test for §2's "pure code can't call
  Socket" found that a `rule` calling `Socket.connect` checked clean. Before
  this ADR, every capability call outside a capability group was unmodeled,
  in any kind. B10 now covers primitive calls from non-gateways. Domain
  capabilities called directly (`Database.queryOne` from a rule) are still
  unmodeled. They are on the grandfather list and leave with it.
- **Runtime.** `compiler/runtime/socket.go`: `SocketProvider` over a
  `SocketBackend`, with `NetBackend` the real one. The provider lives in the
  root module, not `libraries/socket`, because it needs only the standard
  library, the same reason `ClockProvider` lives there. `Interp.Call`
  creates the §5 owner, and every exit path closes what it owns. A blocked
  read is released through `context.AfterFunc` setting a past deadline.
  Winsock reports refusal and reset with its own `WSAE` codes, which
  `syscall.ECONNREFUSED` does not match on Windows. The refused-connection
  test caught it, and `socket_errno_windows.go` maps them.
- **Fake.** `runtime.ScriptedSocket` (expect / send / startTls / hangup /
  stall steps per peer, with `Done()` reporting unfinished scripts), exposed
  to `run-cli -caps` as `{"type": "socket-script"}`. `run-cli` registers the
  real `Socket` by default.
- **Corpus.** `acme-http`'s `Client` is now an HTTP/1.1 POST over
  `connectTls`, read until the server closes. It returns `SocketError`, and
  Stripe and Square map it to text. `WebRequest` split `url` into `host` and
  `path`. `checkout-app`'s `LedgerApi` writes a line over `connect`.
  `postgres-wire` gained `openSecure`, `sendStartup`, `readMessage` (ADR
  0059 §1's loop) and an `authenticate` activity, run end to end against
  the fake by `TestCorpusPostgresAuthenticate`.
- **Found while writing the corpus.** Fiig strings had no `\r` escape, so
  `httpRequest` built CRLF as `<<13: u8, 10: u8>>`. Every text protocol in
  the milestone (HTTP, SMTP, Redis) needs CRLF; ADR 0061 added `\r` and
  `httpRequest` is now one string.
