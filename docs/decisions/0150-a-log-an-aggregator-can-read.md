# ADR 0150: A Log an Aggregator Can Read

Status: Accepted (2026-11-25, by the owner). Implemented the same day (see
Implementation notes).
Date: 2026-11-25
Answers: ADR 0128 open questions 1 (the calls behind a failed request) and 3
(a structured log); improvement idea A3.
Related: ADR 0128 (request ids, faults stay on the server), ADR 0111 and
ADR 0117 (stream adapters), ADR 0112 (schedules), ADR 0124 and ADR 0127
(startup routes), ADR 0051 (no severity level for diagnostics),
`compiler/runtime/trace.go` (spans), charter principle 10.

## Context

Everything `fiig serve` logs is a line of text from Go's `log.Printf`, on
stderr:

```
2026/11/25 09:14:03 fiig: request cc18879cb00d86b2 faulted: division by zero (in boom)
2026/11/25 09:14:03 fiig: GET /boom 500 3ms id=cc18879cb00d86b2
2026/11/25 09:15:00 schedule refreshDaily: refreshSeries failed with Down("connection refused")
```

That's readable by a person, but an enterprise log platform needs more:

- **No fields.** To find every 500, or every line for one request id, a
  platform has to be configured with a parsing rule (a Grok pattern in
  Datadog, a regex stage in Loki's pipeline, a parse in CloudWatch Logs
  Insights) written against fiig's wording, which breaks when the wording
  changes.
- **No level.** Alerting and retention are routed by level ("page on
  error, keep info for 7 days"), and every line looks the same.
- **No service.** A platform holds the logs of many services. Without a
  `service` field, which service a line came from depends on how the
  collector was configured.
- **No trace id.** A request that passed through a traced gateway or load
  balancer can't be joined to its trace.

There's a second gap, in what is logged at all. A request that answers 503
because a gateway returned `Err(AbsFault.Down(...))` logs only the access
line's `503`. The runtime knows which gateway failed, with what, and how
long it took: every call is already a span (`compiler/runtime/trace.go`),
but only `fiig run -trace` records spans, so `fiig serve` throws that away.
Most logging calls in services written in other languages exist to say
exactly this ("calling X", "X failed after 10s"), and in fiig the runtime
can say it without anyone writing a line.

fiig code has no way to log at all, and this ADR doesn't add one (open
question 1).

### What the platforms read

None of these platforms needs fiig to send it anything. Each runs an agent
or driver that reads a container's output, and each reads JSON lines with
no configuration:

| Platform | How it collects | What it does with a JSON line |
| --- | --- | --- |
| **Datadog** | The Agent reads container stdout and stderr; "Logs coming from container `Stderr` have a default status of `Error`" | Preprocessing for JSON logs takes, by default: the message from `message`, `msg` or `log`; the status from `status`, `severity`, `level` or `syslog.severity`; the service from `service`, `syslog.appname` or `dd.service`; the date from `@timestamp`, `timestamp`, `_timestamp`, `Timestamp`, `eventTime`, `date`, `published_date` or `syslog.timestamp`, in ISO 8601, UNIX milliseconds or RFC 3164 (**not** from `time`: without one of these, it stamps the line when it arrives); the trace and span ids from `dd.trace_id` / `dd.span_id` or OpenTelemetry's `trace_id` / `span_id`, 32 and 16 lowercase hex characters. Any other name needs a remapper configured |
| **Grafana Loki** | Grafana Alloy or Promtail read container output | Stores the line. LogQL's `\| json` extracts its properties as labels at query time, flattening nested keys with `_` and skipping arrays |
| **AWS CloudWatch Logs** | ECS's `awslogs` driver passes a container's stdout and stderr; on EKS, Fluent Bit or the CloudWatch agent | Logs Insights discovers a JSON event's fields by itself, up to 200, nested ones in dot notation, in the Standard log class |
| **Elastic** | Filebeat or Elastic Agent | Decodes JSON. Elastic Common Schema names fields `@timestamp` (required), `log.level`, `message` |
| **Google Cloud Logging** | The logging agent in GKE and Cloud Run | Reads `severity` and `message` specially. *Not checked against Google's docs: the pages couldn't be fetched while this was written (see Sources).* |

Programs that already work well with all of these do it the same way:

- **The Twelve-Factor App** (XI, "Logs"): a program writes its log as an
  event stream to stdout and doesn't route or store it. The environment
  collects it.
- **Go's `log/slog`** `JSONHandler` writes one object per line, with keys
  `time`, `level`, `msg`, then the call's own fields. Its `time` is the one
  key Datadog doesn't read by default, which is why §2 doesn't copy it.
- **Spring Boot** has structured console logging, switched on by one
  property (`logging.structured.format.console`), in a choice of shapes
  (`ecs`, `gelf`, `logstash`).
- **Stripe's "canonical log lines"**: one line per request at its end,
  carrying everything worth asking about that request (status, duration,
  counts, ids), so one query answers most questions without joining lines.

## Decision

### 1. `-log-format json`

`fiig serve` takes `-log-format text|json`. `text` is the default and is
what is written today, unchanged. `json` writes every line `fiig serve`
writes as one JSON object per line: the access line, faults, startup,
schedule and stream lines, lifecycle lines, and the runtime's own notes
(a TLS reload, a session defect).

`fiig run -replay` takes it too, since it runs a stream route as
`fiig serve` does. Other commands are for a person at a terminal and keep
their text.

**JSON goes to stdout; text stays on stderr.** A collector reads either,
but Twelve-Factor's process "writes its event stream, unbuffered, to
`stdout`", and the Datadog Agent gives every line from a container's
stderr a default status of `Error`, which would make every access line an
error there. `fiig serve` writes nothing else to stdout, so nothing else
is in the way. Text stays on stderr, where it is today and where a person
expects Go's log to be.

The format is deployment configuration, not part of the manifest
(contrast ADR 0149): it is about where the program runs, not how it
behaves. The same build is run with text on a laptop and JSON in a
cluster. §3a says where it is set.

### 2. Fields

Every line has these, in this order:

| Field | Value |
| --- | --- |
| `timestamp` | RFC 3339 (an ISO 8601 profile) in UTC, to the millisecond: `2026-11-25T09:14:03.512Z` |
| `level` | `debug`, `info`, `warn` or `error` (§3) |
| `msg` | The same sentence the text format writes, without the `fiig:` prefix |
| `event` | What happened, as a stable name: `request`, `fault`, `call`, `startup`, `schedule`, `stream`, `lifecycle`, `runtime` |
| `service` | The root manifest's `path` (`hearsay`) |
| `version` | The root manifest's `version` |

Then the event's own fields, omitted when they don't apply:

| Field | On |
| --- | --- |
| `requestId` | `request`, `fault`, `call` in a request (ADR 0128's id) |
| `trace_id`, `span_id` | the same, when the request carried a trace (§5) |
| `method`, `path`, `status`, `durationMs` | `request` |
| `route` | `request` (the route's path pattern, `/series/{id}`, so the platform can group by route without the ids), `schedule`, `stream` |
| `adapter` | `request`, `schedule`, `stream`, `startup` |
| `elementId` | `stream` and `call` in a stream element (its source's id), `schedule` and `call` in a firing (the firing's time) |
| `declaration`, `kind`, `outcome`, `detail` | `fault`, `call` (§4) |
| `calls`, `callErrs`, `callMs` | `request` (§4) |
| `fiig` | `lifecycle`'s start line: the toolchain version |

The field names are fiig's, in the case fiig already uses for JSON
(`requestId` in ADR 0128's fault body), except where a platform reads a
name by default and fiig's own would need configuring there:

- `timestamp`, not slog's `time`: Datadog reads `timestamp` as the line's
  date and ignores `time`, stamping the line when it arrives instead.
- `level` and `msg`, as slog writes them: Datadog takes them as status and
  message.
- `trace_id` and `span_id`, in OpenTelemetry's spelling: Datadog joins a
  line to its trace through those names (or its own `dd.*`), and not
  through `traceId`.

Loki, CloudWatch and Elastic take every name as it is. The lines are flat,
with no nested objects or arrays, so Loki's `| json` and CloudWatch's
discovery each see every field under its own name.

Adding a field later isn't a breaking change. Renaming or removing one is,
and so is changing an `event` name, since alerts and dashboards are built
on them.

### 3. Levels

The level is the runtime's, fixed per event, and not something a program
chooses:

| Level | Lines |
| --- | --- |
| `error` | A fault. A request answered 5xx. A startup route that failed. A schedule job that faulted or overran. A stream element that faulted. A runtime defect (a session protocol's) |
| `warn` | A startup route that started degraded (ADR 0127). A stream source that can't be read and is being retried, or an element that couldn't be settled. A schedule job that returned an `Err`, or couldn't be claimed. A TLS reload that kept the last good files. The `call` lines of a failed request (§4) |
| `info` | The access line of a request answered below 500. Lifecycle lines: started, serving, stopped. A startup route that passed |
| `debug` | Every gateway call, with `-log-level debug` (§4) |

The level setting (§3a) drops lines below it, in either format, except
that a fault is always written. A 4xx is `info`, not `warn`: it is the
caller's mistake, and an alert on it would fire on every bad client.

### 3a. Setting the level and format, and what they cost

Both are set by `FIIG_LOG_LEVEL` (`debug`, `info`, `warn`, `error`;
default `info`) and `FIIG_LOG_FORMAT` (`text`, `json`; default `text`),
read from the environment or `-env-file` as config is (ADR 0071), or by
the flags `-log-level` and `-log-format`, which win over the variables. A
value that isn't one of these stops `fiig serve` at startup, in the same
report as the rest of its configuration (ADR 0124 §3), rather than
falling back to a default nobody chose.

They are environment variables because that is how a deployment sets a
service's logging everywhere else (a Kubernetes manifest's `env`, an ECS
task definition, a `.env` file), and how an operator changes it without
touching the command line the image runs.

The level also decides what the runtime records, so it is the switch for
the cost of §4:

| Level | Per call | Per request |
| --- | --- | --- |
| `debug` | a span recorded, and a `call` line written | the access line with its counts |
| `info` | a span recorded | the access line with its counts |
| `warn` | a span recorded | a failed request's `call` lines and access line; nothing for a request that succeeded |
| `error` | **nothing**: no spans are recorded, as today | a fault's line, and a 5xx's access line without its counts |

At `error`, `fiig serve` records nothing per call, as today, and writes no
access line for a request that succeeded. A service that wants every last
bit of speed runs at `error`, and loses the counts and the calls behind a
failed request, not its faults.

This isn't ADR 0051's question. ADR 0051 refused a severity level for the
checker's diagnostics, since a diagnostic a program can ignore is a rule
that isn't finished. A log line's level is for routing at the platform,
which needs it.

### 4. The calls behind a failed request

`fiig serve` records spans for each request and each stream or schedule
element, scoped to it: each request runs with a tracer of its own, which
collects its spans and is dropped when the request ends. Then:

- **Every access line counts them.** `calls` is the number of gateway
  calls the request made, `callErrs` how many returned an `Err` or
  faulted, and `callMs` the time spent in gateways (overlapping calls in a
  `concurrent` block each count). This is the canonical line: most
  questions about one request ("was it slow because of ABS?") are answered
  by it.
- **A request that faulted or answered 5xx also logs its failing calls**,
  one `call` line at `warn` for each gateway call that returned an `Err`
  or faulted, before its access line:

  ```json
  {"timestamp":"2026-11-25T09:14:03.502Z","level":"warn","msg":"absclient.catalog.AbsCatalog.seriesAsService returned Err(Down(\"connection refused\")) after 10003ms","event":"call","service":"hearsay","version":"0.4.0","requestId":"cc18879cb00d86b2","declaration":"absclient.catalog.AbsCatalog.seriesAsService","kind":"gateway","outcome":"err","detail":"Down(\"connection refused\")","durationMs":10003}
  {"timestamp":"2026-11-25T09:14:03.512Z","level":"error","msg":"GET /series/42 503 10013ms","event":"request","service":"hearsay","version":"0.4.0","requestId":"cc18879cb00d86b2","method":"GET","path":"/series/42","route":"/series/{id}","adapter":"seriesApi","status":503,"durationMs":10013,"calls":1,"callErrs":1,"callMs":10003}
  ```

  A request answered below 500 logs no `call` lines: an `Err` a workflow
  mapped to a 404 is the program working.
- **A stream element or schedule firing that failed** logs its failing
  calls the same way, with its `elementId`.
- **`-log-level debug` logs every gateway call**, failed or not, for
  finding out what a program does under load. It's off by default, since
  a fan-out of 200 calls is 200 lines.

Only gateways get `call` lines: they are the calls that leave the process.
Rules, conversions, activities and workflows stay in spans, which `-trace`
still shows in full. A fault's line keeps the declaration stack it has
today.

### 5. Trace ids

A request that carries a W3C `traceparent` header
(`00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01`: version, a
32-character trace id, a 16-character parent id, flags, in lowercase hex)
has its trace id and parent id in `trace_id` and `span_id` on its lines,
so a platform can show a request's logs beside its trace from whatever
traced it upstream (a load balancer, an API gateway, a service mesh). The
parent id is, in the spec's words, "the ID of this request as known by the
caller", so it is the span the request's lines belong under. An invalid
`traceparent` (wrong length, uppercase, all zeros, version `ff`) is
ignored, as the spec requires and as a malformed `X-Request-Id` is (ADR
0128).

fiig doesn't start a trace, export spans, or pass the trace on to the
calls it makes. Those are open questions 2 and 3.

### 6. Unchanged

- A `Secret` prints as it does today, redacted, in every format. A
  request's query string isn't logged (ADR 0128), and neither are headers
  or bodies.
- `-access-log=false` still turns off the access line, now in either
  format. The `call` lines of a failed request go with it, since they are
  part of the same record.
- `fiig run -trace` is as it is.
- A fault still answers the caller with a bare 500 and its request id.

## Alternatives considered

**A. Push logs to the platform from the process** (Datadog's HTTP intake,
Loki's push API, CloudWatch's `PutLogEvents`). Rejected: each platform
already has an agent that reads container output, and a process that
ships its own logs needs buffering, retries, credentials and a way to
fail that doesn't lose lines or block requests, once per platform.
Twelve-Factor's reason applies: routing logs is the environment's job.
OTLP, the one vendor-neutral push protocol, is open question 2.

**B. A format per platform** (Spring Boot's `ecs`, `logstash`, `gelf`).
Spring offers several because Java's logging ecosystem already had
several. Every platform in §Context reads one plain JSON object, so one is
enough. Rejected for now. If an Elastic shop needs `@timestamp` and
`log.level` exactly, `-log-format ecs` can be added beside `json` without
changing it.

**C. JSON on stderr, like the text.** One stream for both. Rejected for
Google Cloud's reading of stderr (§1), and because stdout is where a
collector expects it.

**D. A `call` line for every gateway `Err`, whatever the request's
status.** More complete. Rejected: an `Err` is how fiig says "not found"
or "refused" in the normal course of things (`AbsFault.Missing` makes a
404), so logging every one fills the log with the program working. A
failed request is the case where the calls explain something. `-log-level
debug` covers the rest.

**E. Let a program choose a line's level.** Only meaningful once a
program can write a line (open question 1).

**F. Only a flag, as `-addr` and `-shutdown` are.** Rejected for §3a:
logging is what an operator changes most often after deploying (turning
on `debug` to chase a problem, or down to `error` for speed), and a
variable is changed in a deployment's `env` without rebuilding the command
line an image runs. The flag stays, and wins, for a person at a terminal.

**G. Put the level in the manifest**, as ADR 0149 does the concurrency
bounds. Rejected: the level doesn't change what the program does, only
what it says about it, and the same build wants a different level in
different places.

## Consequences

- `tooling/cli/servecmd`: `-log-format` and `-log-level`. One logger,
  built from them, replaces every `log.Printf` there, and is what
  `adapter.Observer`, the stream, schedule and startup runners get as
  their `Logf`. Their calls gain fields instead of formatting them into
  the sentence (`Logf(format, args...)` becomes a call that takes the
  event's fields as well).
- `compiler/runtime`: the runtime's own notes (`session.go`,
  `tlsconfig.go`) go through a logger the `Interp` holds instead of the
  global `log`, so they come out in the chosen format. A `Tracer` can be
  set per request: the context carries it, so a request's spans don't mix
  with a concurrent request's, and `fiig run -trace` sets it once for its
  whole run as now.
- `compiler/adapter`: the access line gains `route`, `adapter`, `calls`,
  `callErrs`, `callMs`, and the `call` lines before it. `traceparent` is
  read beside `X-Request-Id`.
- `docs/deploying.md`'s "Logs and request ids" documents the JSON fields,
  levels, and the platforms' setup (which is: none), and ADR 0128's open
  questions 1 and 3 point here.
- The text format doesn't change, so nobody's existing parsing rules
  break.
- Recording a span per call costs an allocation per call in `fiig serve`
  that it doesn't pay today, at `warn` and below. At `error` it costs
  nothing (§3a). ADR 0115's benchmarks measure the cost at `info` before
  this is accepted as built.

## Open questions

1. **A program's own lines.** fiig code can't write a log line, and this
   ADR deliberately doesn't add a way. A log is an effect: as a capability
   only a gateway could write one, and as a built-in a rule couldn't stay
   pure. What's left after §4 is mostly domain events ("a user linked a
   server"), which may belong as audit records a program saves, not log
   lines. Decide when Hearsay, running with this ADR, shows what's still
   missing.
2. **OTLP export.** Sending spans, and maybe logs and metrics (idea A4),
   to an OpenTelemetry collector would give Datadog, Grafana Tempo,
   Honeycomb and others fiig's whole call tree as a trace, from one
   protocol. It's the push model alternative A rejects for logs, but for
   traces there's no output to tail, so it may be the right shape there.
3. **Passing the trace on.** `fiig.http` calls could send `traceparent`
   (and `X-Request-Id`, ADR 0128 open question 2), so the services fiig
   calls join the same trace.
4. **Sampling.** A busy service may want only a fraction of its 2xx access
   lines. Platforms can drop lines at ingestion, but they bill for what's
   sent. Wait for a program with that volume.
5. **Google Cloud's field names.** Cloud Logging is believed to read
   `severity`, not `level`, and to give a line without one a severity from
   its stream; neither is checked against Google's docs yet. If so, either
   a `-log-format gcp` (alternative B's form) or writing `severity` beside
   `level`. Check and decide when someone deploys there.
6. **Environment tags** (`env: production`). Datadog and others take them
   from the agent's or container's configuration today, which is where
   they belong while nothing in fiig knows the environment's name.
7. **Changing the level while running.** An operator chasing a problem
   wants `debug` now, without a restart. A soft setting (ADR 0140 §1a)
   could hold it, but settings are the program's, and this is the
   runtime's. Wait until a restart is shown to be the problem.

## Implementation notes (2026-11-25)

- **`compiler/logging`** (new): `Logger` with `Log(level, event, text,
  fields...)`. Text writes the line through the standard log, as before;
  JSON writes one object to stdout with `timestamp`, `level`, `msg` (the
  text without `fiig: `), `event`, `service`, `version`, then the fields,
  leaving out an empty one. A fault is written at any level. A nil
  `*Logger` writes text, so code that has none still logs.
- **Runtime**: `Tracer.RecordSpan` takes the span's context. A new
  `KindTracer` names the kinds it wants, and a call of another kind runs
  with no span at all, so serve's tracer, which wants gateways alone, costs
  rules, conversions, activities and workflows nothing. The runtime's own
  notes (`session.go`, `tlsconfig.go`) go through a package-level
  `runtime.Log`, not a logger on the `Interp` as §Consequences said: they
  are written from places that hold no interpreter, and the log is the
  process's anyway.
- **Adapter**: `CallTracer` adds each gateway span to the `calls` in its
  context. `Observer` gives each request its calls (at `warn` and below),
  reads `traceparent`, and learns the route and adapter from the route
  handler, which sets them before the limits run, so a request the limits
  refused is named too. The stream runner gives each element its calls and
  writes the failed ones when it is retried, dead-lettered or halts; the
  schedule runner does the same for a firing that faults, overruns or
  returns an `Err`. The runners and the observer take `Log`, and keep
  `Logf` as the text fallback their tests use.
- **Commands**: `fiig serve` and `fiig run -replay` take `-log-format` and
  `-log-level`, else `FIIG_LOG_FORMAT` and `FIIG_LOG_LEVEL` from the
  environment or `-env-file` (`runprogram.ReadLogger`); a bad value joins
  the startup configuration report. Serve's interpreter gets `CallTracer`
  only when the level counts calls. Its own lines, and an `error:` it stops
  on, go through the logger.
- **Cost** (`compiler/runtime/bench_test.go`, 32 cores, min of 5):
  `BenchmarkCallsTraced`, 100,000 non-gateway calls under a gateways-only
  tracer, is 40.5 ms, the same as untraced `BenchmarkCalls` (40.1 ms), with
  the same allocations. `BenchmarkGatewayCallsTraced`: 10,000 gateway calls
  take 2.10 ms untraced and 2.89 ms traced, about 80 ns and 2 allocations
  per call, against a real gateway call's network round trip.
- **Dogfooded** with `fiig serve` on a small app whose gateway returns
  `Err` for one id: JSON on stdout, nothing on stderr, a `traceparent`'s
  ids on its request, a 503 preceded by its `call` line at `warn`; the same
  requests in text on stderr; `FIIG_LOG_FORMAT=json FIIG_LOG_LEVEL=error`
  writing only the 503, without counts; `FIIG_LOG_LEVEL=loud` stopping
  startup with the problem named.
- **Tested**: `compiler/logging` (the line's shape, levels, text unchanged,
  parsing), `compiler/adapter/calls_test.go` (counts and route on a success,
  the failing call before a 503, nothing counted at `error`, every call at
  `debug`, `traceparent` valid and invalid, an element's calls carrying its
  id), `tooling/runprogram`'s `TestReadLogger`. One test in `libraries/sql`
  kept only the format string of what it logged; it formats it now.
- **Not done**: a startup route that passed writes no line of its own, as
  before; the startup lines its describe gives are `lifecycle` at `info`.

## Sources

Checked 2026-11-25:

- Datadog, preprocessing for JSON logs (the default date, message, status,
  service, trace id and span id attributes):
  https://docs.datadoghq.com/logs/log_configuration/pipelines/
- Datadog, reserved attributes:
  https://docs.datadoghq.com/logs/log_configuration/attributes_naming_convention/
- Datadog, container log collection ("Logs coming from container `Stderr`
  have a default status of `Error`"): https://docs.datadoghq.com/agent/docker/log/
- Datadog, OpenTelemetry `trace_id` / `span_id` formats:
  https://docs.datadoghq.com/tracing/other_telemetry/connect_logs_and_traces/opentelemetry/
- AWS, Logs Insights discovered fields:
  https://docs.aws.amazon.com/AmazonCloudWatch/latest/logs/CWL_AnalyzeLogData-discoverable-fields.html
- AWS, the `awslogs` driver:
  https://docs.aws.amazon.com/AmazonECS/latest/developerguide/using_awslogs.html
- Grafana Loki, LogQL's `json` parser:
  https://grafana.com/docs/loki/latest/query/log_queries/
- Elastic Common Schema, base and log fields:
  https://www.elastic.co/guide/en/ecs/current/ecs-base.html,
  https://www.elastic.co/guide/en/ecs/current/ecs-log.html
- Spring Boot, structured logging:
  https://docs.spring.io/spring-boot/reference/features/logging.html
- Go, `log/slog`: https://pkg.go.dev/log/slog
- W3C Trace Context (the `traceparent` format, and ignoring an invalid
  one): https://www.w3.org/TR/trace-context/
- The Twelve-Factor App, XI. Logs: https://12factor.net/logs
- Stripe, canonical log lines: https://stripe.com/blog/canonical-log-lines

Not checked: Google Cloud Logging's structured-logging page
(https://docs.cloud.google.com/logging/docs/structured-logging) couldn't be
fetched, so what §Context and open question 5 say about it is unconfirmed.
