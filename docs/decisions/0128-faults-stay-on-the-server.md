# ADR 0128: Faults Stay on the Server, and Every Request Has an Id

Status: Proposed
Date: 2026-11-21
Fixes: ADR 0026's "Known issue (2026-10-29)".
Related: ADR 0026 (default adapter response), ADR 0076 (request limits),
`compiler/runtime/trace.go` (spans).

## Context

When a workflow faults, the HTTP adapter wrote `{"fault": "<message> (in
<declaration stack>)"}` to the client with a 500. ADR 0026 recorded this as a
known issue and left it unfixed: the body names internal declarations, and a
fault's message can name data, such as the key a duplicate-key fault found
taken (ADR 0065). And nothing was logged: the adapter has no log call, and
`fiig serve` logged only its startup and its background loops. A 500 in
production left no line anywhere an operator could read.

### How others handle it

| Stack | What the client gets | What the operator gets |
| --- | --- | --- |
| Spring Boot | a generic error body, the cause hidden by default (`server.error.include-message=never`) | the stack trace, logged |
| Rails | a static 500 page | the exception and request id in the log |
| Phoenix | a generic body in production | the crash report in the log |
| Go `net/http` | whatever the handler writes | whatever the handler logs |
| Envoy and most proxies | pass the id on (`X-Request-Id`) | the same id in their access log |

Every one of them separates the two, and the id is what joins them.

## Decision

### 1. A fault is a bare 500 with the request's id

```json
{"error": "internal error", "requestId": "cc18879cb00d86b2"}
```

The cause goes to the server's log under that id:

```
fiig: request cc18879cb00d86b2 faulted: division by zero (in boom)
```

This covers a workflow's fault and a fault writing a response (an invalid
header, status or cookie, ADR 0069 §6). A deadline (ADR 0076 §3) and a
request the adapter refuses (400, 413, 415) are unchanged: those are the
caller's, and they already say so.

### 2. Every request has an id

`fiig serve` puts every request through an `Observer`:

- The id is the caller's `X-Request-Id` when it is a token of at most 64
  letters, digits, `-`, `_` and `.`, so a trace that passes through a proxy
  keeps its id. Anything else is replaced by a new 16-digit hex id, so a
  value a caller chose is never copied into a log line or a header.
- It is returned as the response's `X-Request-Id`.

### 3. An access line

```
fiig: GET /boom 500 0s id=cc18879cb00d86b2
```

One line per request: method, path, status, duration, id. **The query isn't
logged**, since it can carry a token. `-access-log=false` turns the line off;
a fault is logged either way.

## Alternatives considered

- **Keep the fault in the body in development.** It helps when a developer
  is the caller, and it is the mistake that ships: the flag is forgotten in
  production. The log has the cause on the developer's own terminal.
- **A configurable error body.** The program already maps its errors to
  responses (ADR 0017); a fault is by definition what it didn't map.
- **Log the span tree** (`compiler/runtime/trace.go`). It would show where in
  the call tree the fault was, not only the declaration stack in the message.
  The `Tracer` is per interpreter, not per request, so it needs scoping first
  (open question 1).

## Consequences

- A client that read `fault` from a 500's body reads `error` and
  `requestId` now. abs-recommendations' frontend reads only the status.
- A support request can quote the id, and the operator finds the line.
- `Observer` is the one place request-level behaviour hangs: metrics and a
  status route (docs/improvement-ideas.md A4, A5) go there.

## Open questions

1. **The span tree of a faulted request**, once a tracer can be scoped to one
   request. *Answered by ADR 0150 §4 for the calls that leave the process:
   a failed request writes each gateway call that failed.*
2. **Propagating the id** to `fiig.http` calls the request makes, and into a
   schedule's or a stream route's log lines.
3. **A structured log** (JSON lines) for an aggregator. The lines are text
   today, as every other `fiig serve` line is. *Answered by ADR 0150:
   `-log-format json`.*

## Implementation notes (2026-11-21)

- `compiler/adapter/observe.go` (`Observer`, `writeFault`) and the two
  call sites in `compiler/adapter/http.go`; `fiig serve` wraps its handler
  and takes `-access-log`.
- Tests: `observe_test.go` (a bare body that names neither the declaration
  nor the data in the path, the id in the body and the header, the cause in
  the log under it; a good id kept and bad ones replaced; the access line
  without the query). Two tests that asserted the leak
  (`TestTheAdapterRefusesWhatHTTPCantCarry`,
  `TestAWholeBytesBodyOnAJsonRouteIsSentRaw`) now read the cause from the log.
- Dogfooded under `fiig serve`: a faulting route answered the bare body with
  an id, the log had the fault and the access line under it, and a request
  with `X-Request-Id: trace-77` kept it.
