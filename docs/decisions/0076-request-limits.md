# ADR 0076: Request Limits in `fiig serve`

Status: Accepted (2026-11-15, with §2's bound on waiting requests and §1's
64 KiB header limit added in review)
Date: 2026-11-11
Related: ADR 0021 (request bodies), ADR 0059 §4 (the call's deadline),
ADR 0069 (routes, `consumes`), ADR 0070 (`ALLOW_ORIGINS`, deployment
input), ADR 0071 (`fiig config`), ADR 0074 (open question 2, a reply's
size), ADR 0075 §5 (`hashPassword`, open question 5), charter principle 10.

## Context

ADR 0075 noted that each `crypto.hashPassword` takes 19 MiB on purpose, and
that nothing limits how many run at once. Looking at `fiig serve` for that
found a wider gap: nothing a client sends is bounded.

- **The body.** The adapter reads a request body with `io.ReadAll`, so one
  POST of 10 GB is read into memory whole. One client, one request, and
  the process is gone.
- **How many requests run at once.** Every connection gets a goroutine and
  runs its workflow, so memory grows with whatever arrives: a burst of
  log-ins is a burst of 19 MiB hashes.
- **How long a request runs.** The workflow runs under the request's
  context, which has no deadline; it ends only when the client hangs up.
  ADR 0059 §4 says a runaway loop "stops at the call's deadline", and under
  `fiig serve` there isn't one. Only the headers have a timeout (10 s); a
  client sending the body one byte a minute holds its request forever.

### Precedent

Most servers bound the body by default and leave concurrency and time to
the deployment:

| | Body | Requests at once | Time |
|---|---|---|---|
| Go `net/http` | none (`MaxBytesReader` is opt-in) | none | none (`ReadTimeout`, `TimeoutHandler` opt-in) |
| Java, Spring Boot on Tomcat | 2 MB for forms; none for JSON | 200 threads, then 100 queued, then refused | none by default |
| Elixir, Plug | 8 MB (`Plug.Parsers` `:length`) | 16,384 connections (Ranch) | 15 s per body read |
| Rust, axum | 2 MB (`DefaultBodyLimit`) | none (tower's `ConcurrencyLimitLayer` is opt-in) | none (`TimeoutLayer` is opt-in) |
| Node, Express | 100 KB (`body-parser`) | none | none |

The opt-in ones are where real services get hurt: the limit exists, and
the program never turned it on.

## Decision

Every limit is on by default and set by the deployment, like
`LISTEN_ADDR` and `ALLOW_ORIGINS` (ADR 0070): an environment variable,
or the `fiig serve` flag of the same meaning, and `fiig config` lists each.

### 1. A request body is at most 2 MiB

- `MAX_BODY_BYTES` / `-max-body`, default 2 MiB (2,097,152), axum's and
  Spring's figure.
- A `Content-Length` above it is answered **413 Content Too Large** before
  any of the body is read. A body without one (chunked) is read up to the
  limit and answered 413 at the byte that passes it.
- The request line and headers together are at most 64 KiB, not Go's
  1 MB default (`MaxHeaderBytes`); more is answered **431**. Real clients
  send a few KiB, and §2's waiting requests each hold their headers.
- Per-route limits, for an upload route that needs more than the JSON
  routes beside it, are open question 1.

### 2. At most 256 requests run at once; the rest wait, briefly

- `MAX_REQUESTS` / `-max-requests`, default 256.
- A request arriving when 256 are running waits for a slot up to 5 s, then
  is answered **503 Service Unavailable** with `Retry-After: 1`, without
  its body being read.
- At most `MAX_REQUESTS` more may wait; one arriving when that many are
  already waiting is answered 503 at once. The wait is bounded in time and
  in number, so a flood inside the 5 s window holds at most 256 waiting
  requests' headers (16 MiB at the 64 KiB limit), not as many as arrive. A short wait absorbs a burst; refusing after it keeps
  a backlog from growing without bound, which Tomcat's queue and Ranch's
  connection limit both do.
- CORS preflights (ADR 0070) don't take a slot: they run no workflow.
- At the defaults, request bodies take at most 512 MiB.
- A `Content-Length` over §1's limit is answered 413 before the request
  waits for a slot.

### 3. A request runs for at most 30 s

- `REQUEST_TIMEOUT` / `-request-timeout`, a duration, default 30 s.
- It starts when the request gets its slot (§2), and is the call's
  deadline (ADR 0059 §4): reading the body, the workflow,
  and every `Socket` call in it stop when it passes. A request stopped by
  it is answered **503** with a body naming the limit, not 500 with the
  fault, since the program did nothing wrong. Go's `TimeoutHandler`
  answers 503 too.
- The server's own read and write timeouts follow it (the 5 s wait plus
  the limit, and 5 s more to write the answer), so a body sent one byte a
  minute is cut off.
- A gateway's own `timeoutMillis` (ADR 0074) still applies inside it, and
  whichever ends first wins.

### 4. At most one password hash per CPU runs at once

- `crypto.hashPassword` and `crypto.passwordMatches` take one of
  `GOMAXPROCS` slots, shared by the process, and wait for one while the
  call's deadline allows. Argon2id is all CPU, so running more at once than
  there are CPUs finishes none sooner; it only multiplies the 19 MiB.
- This is in the runtime, so `fiig run` and tests are bounded too, and it
  needs no setting.
- With §2 and §3, a burst of log-ins costs at most 19 MiB per CPU, waits
  its turn, and is refused after 30 s, rather than taking the process
  down.

### 5. What is out

- **Rate limiting per client** (requests per minute from one address or
  key): a policy of the deployment's proxy, and of the application for
  log-in attempts, which a later ADR can give fiig.
- **A reply's size in `fiig.http`** (ADR 0074 open question 2): the same
  kind of gap on the client side, left to its own ADR so this one stays
  about the server.

## Alternatives considered

**A. Limits declared in source, on the adapter or route.** The program
knows which route takes uploads. But the right numbers depend on the
machine (memory, CPUs) and the traffic, which the deployment knows; ADR
0070 put CORS origins there for the same reason. Per-route bodies stay
open question 1.

**B. Refuse at once when full (no wait).** Simplest, but a burst of 300
requests on a quiet server gets 44 refusals that a 50 ms wait would have
served.

**C. Queue without a bound (Go's default).** Memory grows with the
queue; it is the gap this ADR closes.

**D. Limit connections, not requests** (`netutil.LimitListener`, Ranch).
Keep-alive connections that are idle would hold slots, and a refused
connection gives the client no status to act on.

**E. Off by default, like Go and axum.** Rejected: the opt-in limit is the
one programs forget, and a fiig service should be safe as written
(charter principle 10).

## Consequences

- A `fiig serve` process has bounded memory for what clients send: at
  the defaults, 512 MiB of bodies plus 19 MiB per CPU of hashes, plus
  what workflows build.
- A route taking files over 2 MiB needs `MAX_BODY_BYTES` raised for the
  whole service until open question 1 is settled.
- A workflow that takes over 30 s, which before ran until the client gave
  up, now stops and answers 503; a long job belongs in a stream (ADR
  0047) or a queue.
- Three new deployment variables, listed by `fiig config`.

## Open questions

1. A per-route body limit, in source (`consumes "application/pdf" up to
   50 MiB`) or as deployment input.
2. ~~Whether a 503 from §2 or §3 should say which limit.~~ Settled in
   review: it does, and so does a 413. "Busy" or "too slow" tells an
   attacker nothing `Retry-After` and the status don't already, and an
   operator reading a client's report needs to know which variable to
   raise.

## Implementation notes (2026-11-15)

- `compiler/adapter/limits.go`: `Limits`, and a limiter that wraps each
  route's handler, not the mux, so a preflight or a 404 takes no slot.
  `HTTPHandler` serves under `DefaultLimits`; `fiig serve` calls
  `HTTPHandlerWithLimits`.
- §3 needed the deadline on the connection as well as the context:
  dogfooding found a slowly sent body ran past a 2 s limit to the server's
  7 s read timeout, because `io.ReadAll` doesn't watch the context, and
  starved the request waiting behind it. The limiter now sets the
  connection's read and write deadlines with `http.ResponseController`
  (`corsWriter` gained `Unwrap` for it); `ServerTimeouts` is the backstop.
- §4 is a channel of `GOMAXPROCS` slots in `compiler/runtime/crypto.go`.
- Checked against a running `fiig serve` of `examples/event-inbox`:
  413 for a declared and a chunked 3 MiB body, 431 for 100 KiB of
  headers, a slow body stopped at 2 s, with one slot an immediate 503 for
  the request past the waiting cap and the workflow's own answer for the one that
  waited, and CORS headers on the 413.
