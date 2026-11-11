# ADR 0074: An HTTP Client Package Written in Fiig

Status: Accepted and implemented (2026-11-10; see Implementation notes)
Date: 2026-11-10
Related: ADR 0032 (package versions), ADR 0043 (capabilities written in
fiig), ADR 0059 (`while`; the call's deadline), ADR 0060 (`Socket`,
`Connection`), ADR 0068 (`Clock` as a local effect), ADR 0069 (`HttpResponse`,
CR/LF refusals), ADR 0071 (`Secret`), ADR 0073 (`json.encode`,
`json.decode`), charter principle 10.

## Context

A fiig service can receive HTTP, but it can't call one. The only client is
`examples/acme-http`, written by hand to prove ADR 0060: it sends `POST`,
only over TLS, only to port 443, with one optional header, and reads the
reply by waiting for the server to hang up. It doesn't read
`Content-Length` or chunked framing, so it can't tell a whole reply from
one cut short, and it has no way to stop waiting.

Alpha item 8 asks for a real client, written in fiig over `Socket` so no Go
is involved: every verb, request headers, `http` and `https`, any port,
`Content-Length` and chunked replies. The services the first application
calls (payment providers, webhooks out, internal APIs) all need it.

### Precedent

```go
// Go: a request value, a client, Do. A 404 is a response, not an error.
req, _ := http.NewRequest("POST", "https://api.stripe.com/v1/charges", body)
req.Header.Set("Authorization", "Bearer "+key)
client := &http.Client{Timeout: 30 * time.Second}
resp, err := client.Do(req)
```
```java
// Java (java.net.http, JDK 11): the same shape; redirects default to NEVER.
HttpRequest req = HttpRequest.newBuilder(URI.create("https://api.stripe.com/v1/charges"))
    .header("Authorization", "Bearer " + key)
    .timeout(Duration.ofSeconds(30))
    .POST(BodyPublishers.ofString(body))
    .build();
HttpResponse<String> resp = client.send(req, BodyHandlers.ofString());
```
```rust
// Rust (reqwest, a crate): builder, then send; status errors are opt-in.
let resp = client.post("https://api.stripe.com/v1/charges")
    .bearer_auth(key)
    .form(&params)
    .timeout(Duration::from_secs(30))
    .send().await?;
```
```elixir
# Elixir (Req, a package): one call with options; a 404 is {:ok, resp}.
{:ok, resp} = Req.post("https://api.stripe.com/v1/charges",
  auth: {:bearer, key}, form: params, receive_timeout: 30_000)
```

What the four agree on, and where they split:

| | Go `net/http` | Java `HttpClient` | Rust reqwest | Elixir Req |
|---|---|---|---|---|
| Shape | request value + `Do` | request value + `send` | builder + `send` | one call + options |
| 4xx/5xx | a response | a response | a response (`error_for_status` opt-in) | a response |
| Redirects | followed, up to 10 | **not followed** by default | followed, up to 10 | followed |
| Timeout default | none | none | none | 15 s per receive |
| Connections | pooled | pooled | pooled | pooled (Finch) |
| Where it lives | standard library | standard library | a crate | a package |
| Query/form encoding | `url.Values` | none built in | `.query` / `.form` | `params:` / `form:` |

## Decision

### 1. A package shipped with the toolchain: `fiig.http`

The client is an ordinary fiig library package, `fiig.http`, written
entirely in fiig: a capability, its models, and the conversions that write
requests and read replies. A program depends on it in its manifest and
uses it like any other package:

```json
"dependencies": { "fiig.http": "1.0.0" }
```
```fiig
use fiig.http
use fiig.http.HttpClient
```

- **It ships inside the `fiig` binary.** Its source is embedded, and the
  loader searches it as one more workspace root (ADR 0037), so a program
  needs no `-workspace` flag to find it and a release stays one file. Go
  ships `net/http` the same way, inside the toolchain.
- **It versions on its own** (ADR 0032), as reqwest and Req do, so a
  breaking change to the client is a major version of `fiig.http`, not of
  the language.
- **The `fiig.` prefix is reserved** for packages the toolchain ships, as
  Java reserves `java.*`. A workspace package whose path starts with
  `fiig.` is a loader error.

This makes `fiig.http` the first library written entirely in fiig. Where it
needs something fiig can't express, that's a language gap to close, not a
reason to drop to Go: the same rule as ADR 0025's "nothing go-only".

### 2. One request value, one `send`

```fiig
capability HttpClient {
    gateway send(request: Request) -> Result<Reply, HttpError>
        requires Socket
}
```
```fiig
model Request {
    method: Method
    url: string
    headers: List<Header> | None      // left out: none
    body: bytes | None                // left out: no body
    timeoutMillis: int | None         // left out: 30000 (§5)
}

model Header {
    name: string
    value: string
}

enum Method { Get, Head, Post, Put, Patch, Delete, Options, Query }

model Reply {
    status: int
    headers: List<Header>             // in the order received, repeats kept
    body: bytes
}
```

Every field but `method` and `url` may be left out, as any `| None`
field of a model may be, so a plain `GET` is
`http.Request(method: http.Method.Get, url: "https://example.com/")`.

A gateway calling another service then reads:

```fiig
use fiig.http
use fiig.http.HttpClient

capability Stripe {
    gateway charge(request: ChargeRequest) -> Result<Charge, PaymentError>
        requires HttpClient
    {
        sent = HttpClient.send(http.Request(
            method: http.Method.Post,
            url: "https://api.stripe.com/v1/charges",
            headers: [
                http.Header(name: "Authorization", value: "Bearer ${StripeAccount.apiKey.reveal()}"),
                http.Header(name: "Content-Type", value: "application/x-www-form-urlencoded"),
            ],
            body: http.formEncode([
                http.Header(name: "amount", value: "${request.amount}"),
                http.Header(name: "currency", value: request.currency),
            ]),
        ))
        match sent {
            Ok(reply) -> {
                if reply.status != 200 {
                    return Err(PaymentError.Rejected(status: reply.status))
                }
                match json.decode(reply.body) {
                    Ok(charge) -> Ok(charge)
                    Err(failure) -> Err(PaymentError.Unreadable(failure))
                }
            }
            Err(failure) -> Err(PaymentError.Unreachable(failure))
        }
    }
}
```

- **Bodies are `bytes`** both ways. JSON goes through `json.encode` and
  `json.decode` (ADR 0073), text through `.utf8()`, and the client never
  guesses a content type.
- **The reply is its own type, not `HttpResponse<B>`.** `HttpResponse` is
  what a route *builds*: cookies to set, an optional body, one value per
  header. A reply is what a caller *reads*: every header as received,
  including repeated ones such as `Set-Cookie`, and a body that is always
  there (empty when the server sent none). Go draws the same line between
  `ResponseWriter` and `http.Response`.
- **Headers are a list, not a `Map`**, because names repeat and a server
  may care about order. `http.headerValue(reply.headers, "content-type")`
  returns the first value whose name matches ignoring ASCII case, or `None`.
- **`Method` lists every verb a service sends.** `TRACE` and `CONNECT` are
  left out: `CONNECT` opens a proxy tunnel, which §7 leaves out, and
  `TRACE` is disabled on most servers because it echoes credentials.
  `Query` is the verb ADR 0021 already serves.

### 3. A status is data; `Err` means no reply arrived

A `404` or a `503` is `Ok(reply)` with that status, as in all four
precedents. `Err` means there is no HTTP reply to read:

```fiig
enum HttpError {
    InvalidRequest(reason: string)     // §6: refused before connecting
    Socket(failure: SocketError)       // DNS, refused, reset, TLS, timed out
    MalformedReply(reason: string)     // the bytes back aren't HTTP/1.1
}
```

This keeps one meaning for `Err` across fiig: something failed, not "the
other side said no". Whether a `409` is a failure is the caller's business
logic, written in the caller's `match`.

### 4. Redirects are not followed

A `301`, `302`, `303`, `307` or `308` comes back as a reply, and the caller
decides whether to send a second request to its `Location`. This is Java's
default.

Go, reqwest, and Req follow redirects, and each has had to decide which
headers survive the hop: Go copies them but drops `Authorization` and
`Cookie` when the host changes. Getting that wrong is a known leak: curl
(CVE-2018-1000007) and Python's requests (CVE-2018-18074) both sent
credentials to the redirected host before fixing it. A request that goes somewhere the code never
named is exactly what fiig avoids (charter principle 10: strict about what
it says). Following can be added later as an explicit field without
breaking anyone; not following can't be added back once code relies on it.

### 5. A timeout on every request, enforced by `Socket`

`timeoutMillis` bounds the whole exchange, from resolving the host to the
last byte of the body, as Go's `Client.Timeout` does. Left out, it is
30 000 ms. Running out is `Err(HttpError.Socket(SocketError.TimedOut))`.

All four precedents have a timeout and three default to none, which is how
a client hangs forever on a server that accepts and never answers. In fiig
today nothing can stop it either: ADR 0060 §4 leaves waiting to the call's
deadline, which only the host sets, and `fiig run` sets none. So fiig picks
a default, as Req does.

That needs `Socket` to stop waiting, so this ADR amends ADR 0060 §3:

```
Socket.connect(host, port, deadline: int | None)
Socket.connectTls(host, port, deadline: int | None)
Socket.startTls(connection, serverName, deadline: int | None)
Socket.read(connection, deadline: int | None)
Socket.write(connection, data, deadline: int | None)
```

- **`deadline` is a point in time**, Unix milliseconds as `Clock.now()`
  returns them (ADR 0068), not a duration. The client computes it once,
  `Clock.now() + timeoutMillis`, and passes the same value to every call,
  so the timeout covers the whole exchange however many reads it takes.
  Go's `SetDeadline` takes a time for the same reason.
- **It is optional and named.** Left out, an operation waits as it does
  today, until the call's deadline. Existing calls don't change.
- **Running out is `Err(SocketError.TimedOut)`**, a new variant. ADR 0060
  §4 made waiting too long a fault because only the host could set the
  limit; a deadline the program chose is one the program can act on, by
  retrying or reporting, so it is a value. The host's deadline stays a
  fault.

Adding a variant to the prelude `SocketError` breaks every exhaustive
`match` on it; the corpus's one, `describeSocketError` in
`examples/acme-http`, gains an arm.

`timeoutMillis` is an `int` because `Clock` has no time type yet (known
limitations). When it gets one, the field changes with it, as a major
version of `fiig.http`.

### 6. What `send` writes, and what it refuses

The request is HTTP/1.1:

```
POST /v1/charges HTTP/1.1
Host: api.stripe.com
User-Agent: fiig-http/1
Content-Length: 31
Connection: close
<the caller's headers>

amount=1250&currency=usd
```

- **The URL** is `http://` or `https://`, a host, an optional port
  (default 80 or 443), a path (default `/`), and an optional query, kept as
  written. A fragment (`#...`) is dropped, since it never goes to a server.
  The host may be a name, an IPv4 address, or a bracketed IPv6 address.
- **The client owns `Host`, `Content-Length`, `Transfer-Encoding`, and
  `Connection`.** It writes them, and a request that sets one of them is
  `InvalidRequest`, as adapter-owned headers are refused on the server
  side (ADR 0069 §6, W1).
- **`User-Agent: fiig-http/1`** is sent unless the request sets its own.
  GitHub's API refuses requests without one; Go, Java, and Req send
  theirs by default.
- **`Content-Length`** is sent whenever there is a body, and as `0` for a
  `POST`, `PUT`, or `PATCH` without one, which some servers require. The
  client never sends a chunked body: it always has the whole body.
- **Refused before connecting, as `InvalidRequest`:** a scheme other than
  `http` or `https`; a URL with user information (`https://user:pass@...`,
  which would put a password in a log line; send an `Authorization` header);
  a URL with a space, a control character, or a character outside ASCII
  (fiig doesn't encode international host names yet); a header name that
  isn't an HTTP token; and CR, LF, or NUL in a header value, which would let
  a value forge a header (ADR 0069 §6 refuses the same on the server).
- **`http.formEncode(fields: List<Header>) -> bytes`** percent-encodes
  name and value pairs as `application/x-www-form-urlencoded`, for a form
  body or, through `.utf8()`, a query string. Stripe and most OAuth token
  endpoints take forms, and Go, reqwest, and Req all build them.

### 7. What `send` reads

- **The status line** must be `HTTP/1.1` or `HTTP/1.0` and a three-digit
  status. A `1xx` interim reply (`100 Continue`, `103 Early Hints`) is
  skipped and the next one read.
- **Headers** end at the blank line. Lines folded onto the next line
  (obsolete since RFC 7230) are joined with a space; a line with no colon
  is `MalformedReply`.
- **The body's length** follows RFC 9112 §6.3, in order:
  1. a reply to `HEAD`, and a `204` or `304`, has no body;
  2. `Transfer-Encoding: chunked` is read chunk by chunk; trailers are read
     and dropped;
  3. otherwise `Content-Length` is read exactly, and two different
     `Content-Length` values are `MalformedReply` (RFC 9112 §6.3);
  4. otherwise the body runs until the server closes the connection.
  A connection that closes before the length it promised is
  `MalformedReply`, not a short body that looks whole, which is the bug in
  `examples/acme-http`.
- **No `Accept-Encoding` is sent**, so the server sends the body as is. A
  reply with a `Content-Encoding` anyway is returned with its header and
  its encoded bytes, as Go does when the caller set the header itself.

### 8. One connection per request, for now

Each `send` connects, writes, reads, and closes, sending
`Connection: close`. All four precedents pool connections, which saves a
TCP and TLS handshake per request (one to three round trips). Fiig can't
yet: ADR 0060 §5 makes a connection belong to the top-level call that
opened it, so nothing can keep one open between calls. Pooling is open
question 1.

### 9. Scope

In: HTTP/1.1 over TCP and TLS, every method in §2, any port, request
headers, `Content-Length` and chunked replies, a whole-exchange timeout,
form encoding.

Out: HTTP/2 and HTTP/3; compression; proxies (`HTTP_PROXY` and
`CONNECT`); a cookie jar (read `Set-Cookie` from `reply.headers`);
streaming a body in or out; multipart bodies; client certificates and
custom CAs (ADR 0060 open question 2); international host names; retries,
which belong to the caller, since only it knows whether a request is safe
to send twice.

## Alternatives considered

**A. Write the client in Go and expose it as a host capability.** It would
take a day and pool connections from the start. It is what fiig has done
for SQL, and ADR 0025's goal is to stop: every capability in Go is one a
fiig user can't read, fix, or fork, and the language gaps it hides (here,
deadlines) stay hidden. Rejected; item 8 says "written in fiig" for this
reason.

**B. Put the client in the prelude, like `json`.** No dependency line to
write. But the prelude versions with the language, and an HTTP client
changes more often than a JSON decoder; ADR 0032 exists so a library can
break on its own schedule. `json` is pure and small; this is a capability
with policy in it (redirects, timeouts, owned headers).

**C. Follow redirects by default, stripping `Authorization` across
hosts** (Go, reqwest). Convenient for downloads. Rejected in §4: the
stripping rule is where curl and requests leaked credentials, and
following can be added later without breaking callers.

**D. A builder (`http.post(url).header(...).send()`)** as in reqwest and
Java. Fiig has no methods on models and builds values with named
arguments, which already make every field optional and visible in one
expression. A builder would be a second way to spell the same record.

**E. A `Map<string, string>` for headers**, as `HttpResponse` uses on the
server. Shorter to write, but a reply can repeat a header (`Set-Cookie`,
`Link`) and a `Map` would keep only one, silently. The request takes a list
too, so both directions read alike.

**F. A duration (`timeoutMillis`) on each `Socket` call instead of a
deadline** (Java's `setSoTimeout`, Erlang's `recv/3`). Each read would get
the full allowance, so a server sending one byte every 29 seconds would
never time out. A deadline bounds the whole exchange with one number.

## Consequences

- A fiig service can call any HTTP/1.1 API with no Go, and
  `examples/acme-http` becomes a thin wrapper over `fiig.http` (or is
  replaced by it in `acme-stripe` and `acme-square`).
- `Socket` gains deadlines and `SocketError.TimedOut`, which the Postgres
  client (Milestone 7) needs for the same reason.
- The toolchain carries fiig source for the first time, and the loader
  learns one built-in root and the reserved `fiig.` prefix.
- Every request opens a new connection until pooling is designed, so a
  service making many calls to one host pays a TLS handshake each time.
- Builtin operations gain optional named arguments (`deadline`), which no
  builtin has had before.

## Open questions

1. **Connection pooling.** Reusing a connection across calls needs ADR 0060
   §5 relaxed: a pool that owns connections beyond one call, keyed by
   scheme, host, and port, closing idle ones. Worth designing when a
   service's latency shows the cost.
2. **A reply size limit.** A server can send an endless body; the deadline
   stops it in time but not in memory. Go and Java have no default limit;
   a `maxReplyBytes` field could be added without breaking callers.
3. **Following redirects on request**, with which headers survive a change
   of host (§4).

## Implementation notes (2026-11-10)

- **Where it is.** `compiler/stdlib/http/` holds the package's fiig source
  and manifest, embedded by `compiler/stdlib`. `loader.Load` indexes it
  before any workspace root (`scanBuiltin`), names it `builtin:http` in
  errors and in `fiig.lock.json`'s `source`, and refuses a root or
  workspace package whose path is `fiig` or starts with `fiig.`.
  `loader.Wanted` makes `fiig check`, `run`, `serve`, `config`, and
  `catalog` load a root whose manifest names dependencies even with no
  `-workspace`, so `fiig.http` needs none.
- **The package** is a capability, `HttpClient`, whose one gateway
  connects, writes, and loops on `readReply` until it answers `Done` or
  `Malformed`. `readReply` is a pure conversion over the bytes read so far,
  re-read after every `Socket.read`; it never answers `NeedMore` once told
  the server has closed, which is what ends the loop. Headers are capped at
  1 MiB. `outgoingRequest` writes the request and applies §6's refusals.
- **Public helpers.** A capability file can't have a `private` block (ADR
  0054 §1), and fiig has no package-internal visibility, so the
  conversions the gateway calls are public: `outgoingRequest`,
  `readReply`, `isToken`, and `digitsValue`, with `Outgoing` and
  `ReplyRead`, beside the intended `formEncode` and `headerValue`. They are
  part of `fiig.http` 1.x as far as the bump check knows
  (`docs/known-limitations.md`).
- **Deadlines.** `types.Param` gained `Optional`, and `BindArgsOptional`
  binds a call that leaves a trailing optional parameter out; the runtime
  passes `None` for it. `SocketProvider.Call` derives a context with the
  program's deadline; when that deadline, and not the call's, is what
  stopped the operation, the result is `Err(SocketError.TimedOut)`, a
  value, and the call's own deadline is still a fault.
- **Three checker gaps the first caller found**, each fixed with a test
  (`tooling/runprogram`'s `TestCheckingAgainstAnotherPackage`):
  another package's enum variant couldn't be written (`http.Method.Get`
  was "undefined name http"), so `pkg.Enum.Variant` now works in
  expressions, calls, and patterns, checked and run in the enum's package;
  a qualified type whose alias the file never imports was silently
  unknown, so a model field `http.HttpError` without `use fiig.http`
  checked clean; and a field of another package's model resolved its type
  in the reader's package, so reading `reply.headers` reported
  `List<Header>` as unresolved.
- **Tested** against `runtime.ScriptedSocket` (TLS, Content-Length,
  chunked replies split across reads with extensions and trailers, `100
  Continue`, `HEAD`, a folded header, a body until close, a short body, a
  reply that isn't HTTP, a refused connection, every §6 refusal, and a
  stalled server against `timeoutMillis`), and with the `fiig` binary:
  `examples/inbox-sender` posts to `examples/event-inbox` under `fiig
  serve`, and a scratch program fetched example.com and google.com
  (chunked, 86 KB), api.github.com (which refuses requests without a
  `User-Agent`), an expired certificate (`Tls`), and an unresolvable host
  (`HostNotFound`).
- `fiig fmt` wraps a call's arguments but not a nested call's, so a long
  `http.Request(...)` inside `HttpClient.send(...)` is formatted onto one
  line.
