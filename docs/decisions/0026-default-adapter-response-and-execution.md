# ADR 0026: Default Adapter Response Mapping, and Adapter Execution

Status: Accepted
Date: 2026-10-07

## Context

ADR 0017 added response-mapping syntax but never actually settled what
happens to a route with none — `Route.Mapping` is nil-able by design
("nil if the route has no response-mapping block"), and five of the six
attested adapter examples (`http_customer_api.fiig`, `cli_customer.fiig`,
`http_customer_create_api.fiig`, `http_customer_email_api.fiig`,
`http_customer_query_api.fiig`) have no mapping at all — only
`http_customer_api_with_mapping.fiig` does. `compiler/checker` doesn't
check response-mapping arms in any way (confirmed by grep: zero references
to `Mapping`/`ResponseArm` anywhere in the checker package), so this was
never even an implicit convention anyone wrote down; it's simply never
been executed before.

Milestone 5 needs this answered to wire adapters to real execution at all.

## Decision

**Default (no `Mapping`)**: the target workflow's result is inspected
dynamically. If it's a `Result`:

- `Ok(value)` → HTTP 200 / CLI exit 0, body = `value`
- `Err(value)` → HTTP 500 / CLI exit 1, body = `value`

If it isn't a `Result` at all (a workflow with some other return shape),
the value is returned as-is with success status (200 / exit 0) — there's
no Ok/Err distinction to make.

Every body (default path or an explicit `200 customer`-style shorthand) is
JSON-encoded via `runtime.EncodeJSON` (`compiler/runtime/json.go`), for
both HTTP and CLI — CLI output isn't specially pretty-printed, matching
`tooling/run-cli`'s own existing JSON-by-default convention rather than
inventing a second output format for adapters specifically.

**`ExprResponse`** (`Err(error) -> customerErrorToHttpResponse(error)`)
evaluates the expression and expects a Record with a `status` field (an
`Int`) and optionally a `body` field — reusing `HttpResponse`'s existing
shape (`{status: int, body: string | None}`) for CLI exit-code mapping too,
since no separate "CliResponse" shape is attested anywhere and inventing
one with zero precedent would be guessing. This is explicitly a narrower
reading than ADR 0017 left open ("whether [`HttpResponse`] is a genuine
built-in... isn't resolved") — flagged as provisional, not a closed
question.

**Execution**, both now implemented in a new `compiler/adapter` package:

- HTTP: `net/http`'s own pattern routing (`mux.HandleFunc("GET
  /customers/{id}", ...)`) is used directly — Go 1.22+'s `{name}` path
  segment syntax is already identical to this grammar's placeholder
  syntax, so a route's path string needs no translation at all. Path
  values come from `r.PathValue`; query values from `r.URL.Query()`;
  a body-bound parameter is decoded via `runtime.DecodeJSON` against its
  resolved parameter type (the same decoder `tooling/run-cli` uses for
  `-args`, moved into `compiler/runtime/json.go` so both can share it).
- CLI: routes are dispatched by matching `os.Args` against the route's
  command pattern (`"customer get {id}"`), positionally — no flag parsing,
  since nothing attested needs it.

## Rationale

The default mapping is the same convention essentially every JSON-API
framework defaults to absent explicit configuration — boring and
unoriginal on purpose, the same reasoning ADR 0011 already used for
comparison operators. Reusing `HttpResponse`'s shape for CLI rather than
inventing a parallel type avoids adding a new model with zero attested
need; if a real CLI example later needs something `HttpResponse` can't
express, that's a concrete reason to revisit, not a guess made now.

## Consequences

- `compiler/runtime`: `MatchPattern` exported (adapters match a route's
  response arms the same way a `match` statement does); new `Interp.
  EvalExpr` (adapters evaluate a response-mapping value outside any
  declaration body, where a `?` has nowhere to return to — treated as a
  Fault, unattested anywhere).
- `compiler/runtime/json.go`: `DecodeJSON`/`EncodeJSON`/`IsOptional`/
  `DecodeUntypedJSON` — moved here from `tooling/run-cli` (now a thin
  caller) so `compiler/adapter` can share the same codec instead of a
  second copy.
- New `compiler/adapter` package: `HTTPHandler` and a CLI dispatcher,
  both described above.
- No grammar or checker changes — this is purely a runtime-execution
  decision, the same "parse/check now, give it real meaning later"
  split this project has used since Milestone 1 vs. Milestone 2.

## Known issue (2026-10-29)

*Fixed 2026-11-21 by ADR 0128: a fault answers a bare 500 with a request id,
and the cause is logged by the server under that id.*

Found during ADR 0065's review. On a Fault, the HTTP adapter writes
`{"fault": "<message> (in <declaration stack>)"}` to the client with a 500
(`compiler/adapter/http.go`). This ADR never decided that body; it is an
implementation default. It sends internal declaration names to the caller,
and ADR 0065's duplicate-key fault names a key, which is often outside data
such as an email or an ID. The likely fix: the client gets a bare 500, and the
full fault goes only to the tracer (`compiler/runtime/trace.go` already records
it on every span it unwinds through). Unfixed.
