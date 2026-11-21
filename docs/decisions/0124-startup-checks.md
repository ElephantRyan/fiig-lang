# ADR 0124: Startup Checks: `adapter startup`

Status: Proposed
Date: 2026-11-20
Related: ADR 0044 (a workflow is the adapter boundary), ADR 0047 (stream
adapters: a route has no caller, so its mapping must be exhaustive), ADR 0071
(config is read and checked before the program starts), ADR 0111 §4 (`halt`),
ADR 0112 (`adapter schedule`, which this follows), ADR 0122 open question 2
(a startup hook).

## Context

fiig reads and checks every config value before it starts (ADR 0071), so a
missing `S3_BUCKET` is found at the first second, not at the first upload.
What config can't say is whether the value is any good: the bucket may not
exist, the database may not answer, the Redis password may be the wrong one.
Today a program finds out at the first request that needs it, which is when a
user is waiting and when the process has already told its load balancer it is
ready.

A program can check this itself, but only after it starts: an HTTP route that
a deploy script calls, or a check inside the first request. The language has
nowhere to say "before you serve, make sure of this, and if it fails, don't
start."

### How others handle it

| Ecosystem | The place for "before serving" |
| --- | --- |
| Spring Boot | `ApplicationRunner` and `CommandLineRunner` beans run once the context is up, before the application reports ready; an exception ends the start |
| Django | the system check framework: functions registered with `@register` run at start and in `manage.py check`, and an error stops `runserver` |
| Phoenix and OTP | a supervisor starts its children in order, and a child whose `start` fails stops the boot |
| ASP.NET Core | hosted services start before the server listens; health checks are a separate, continuing thing |
| Rails | initializers; migrations run in a release phase, once per deploy, outside the process |
| Kubernetes | init containers run to completion before the app container, and a `startupProbe` holds traffic until it passes |
| Go | whatever `main` does before it calls `ListenAndServe` |

Three things repeat. **The check is code the program supplies**, since the
framework can't know what "the bucket exists" means. **A failure stops the
start by default**, and the exception is a deliberate choice (Spring lets a
runner catch what it throws). And **it is separate from health**: a startup
check runs once, before traffic, and a health endpoint runs for as long as the
process does (Kubernetes names the two apart for this reason).

fiig already has the shape. A route with no caller, whose outcome the program
must say what to do about, is what a stream (ADR 0047) and a schedule (ADR
0112) are.

## Decision

### 1. `adapter startup`

```fiig
adapter startup Preflight {
    run -> verifyStorage() {
        Ok(_) -> proceed
        Err(StorageError.NoSuchBucket(_)) -> halt
        Err(StorageError.Unreachable(_)) -> proceed
    }
}
```

```ebnf
startup-route   ::= 'run' '->' route-call startup-mapping
startup-mapping ::= '{' { pattern '->' ( 'proceed' | 'halt' ) } '}'
```

- The route calls a **workflow that takes nothing**, as every adapter route
  calls one (ADR 0044). The workflow does the checking through activities and
  gateways like any other.
- The mapping is required and exhaustive: `Ok` and every variant of the `Err`
  enum, or the value when the workflow doesn't return a `Result` (I7, as for
  a schedule).
- **`halt`** refuses to start. **`proceed`** carries on, and an `Err` mapped
  to it is still logged with the route and the error: the check failed, and
  the mapping says the process may start anyway. That is the choice a Spring
  runner makes by catching its exception, written down where a reader sees it.
- `startup`, `run`, `proceed` are contextual words.

### 2. When it runs

After config is loaded and before any adapter starts: no HTTP listener, no
stream consumer, no schedule claim. `fiig serve` runs **every startup route of
every adapter it will serve, concurrently, each with a deadline of 30 s**, and
waits for all of them:

- all of them finishing with a `proceed`: serving begins;
- any `halt`, any fault, or any deadline: nothing starts, every failure is
  reported together (as config reports every missing variable, ADR 0071), and
  the process exits with an error.

**A fault halts**, where a schedule's fault only fails its firing (ADR 0112
§2). A schedule has a next firing to try again; a check that crashes has told
you nothing about what it checks, and starting anyway is what the check exists
to prevent. **A deadline halts** for the same reason.

**There is no order.** The routes run at the same time, so a check must not
depend on another. A program that needs a sequence puts it in one workflow,
where the sequence is written down.

### 3. Every replica runs it

Unlike a schedule's firing (ADR 0112 §4), a startup check is claimed by no
lock: each replica needs to know that it can reach the bucket. Consequence: a
startup route that *changes* something (a migration, a seed) runs once per
replica, concurrently, so it must be safe to run several at once. The hook
gives no lock, and "run once per deploy" is the platform's job (a release
phase, an init container), as Rails does it. See open question 2.

### 4. Not health

A startup route runs once. Whether the process is still healthy an hour in is
a health endpoint, which an `adapter http` route already is (abs-recommendations'
`healthApi`), and a platform's readiness probe calls it. The two may call the
same workflow.

### 5. Bug classes, in I

I becomes "Stream, schedule and startup adapter errors":

- **I7** (widened): a route's mapping is missing or doesn't cover every
  outcome, for a schedule or a startup route.
- **I9**: a startup route's workflow takes arguments, or the route gives any.
- **I10**: a startup route's mapping uses a disposition from another kind
  (`done`, `ack`, `retry`), or an adapter mixes route kinds.

(I8 is already the stream route's `group` and `from`, ADR 0111 §3a.)

## Alternatives considered

- **A flag on an activity** (`startup activity checkStorage()`). It makes a
  declaration kind carry a lifecycle, and gives no place to say what a failure
  means. The mapping is the point, and a route has one.
- **A `startup` route in `adapter schedule`** (`startup -> check()`). It reuses
  a parser path, but a schedule's routes run beside serving, are claimed by a
  lock, and fault without stopping; a startup route is before serving, on every
  replica, and halts on a fault. One adapter with both would hold two sets of
  rules under one name.
- **A line in the capabilities file** (`"startup": ["orders.verifyStorage"]`).
  It is deployment configuration, which fits where the check *runs* and not
  what it *means*: the mapping (which failures halt) is program logic, and a
  program that can't start without it should say so in its own source.
- **Check lazily, on first use** (today). The program starts and fails at the
  first request, and a replica that can't reach the database is added to the
  load balancer.
- **Fail the start on any `Err`, no mapping.** Simplest, and it would make the
  Redis timeout that a program can live without a reason not to start. The
  exhaustive mapping is how fiig makes that choice visible.
- **Sequential, in declaration order.** Predictable, and it hides the first
  failure's neighbours: five broken things would be found one deploy at a
  time. Concurrent with every failure reported is what config does.

## Consequences

- `adapter startup` in the syntax, parser, formatter, and checker (I7-I9);
  the runner in `compiler/adapter`, called by `fiig serve` before it starts the
  other loops; `fiig catalog` lists a program's startup routes. I7-I10 become
  the classes above.
- The contextual words join the IntelliJ and LSP keyword sets.
- `fiig test` needs nothing: a test calls the workflow like any other (ADR
  0102 §3a).
- `docs/writing-a-client.md` and the getting-started page gain a short "checking
  at startup" section.

## Open questions

1. **A way to run the checks alone**, `fiig serve -check`, which runs the
   startup routes and exits with a status. A CI job or a Kubernetes init
   container wants it, and it costs a flag.
2. **Once per deploy.** Reuse `ScheduleLock` (ADR 0112 §4) for a route that
   says `once`, so a migration runs on one replica while the others wait for
   it. It couples startup to a lock provider, which is why it waits for a
   program that asks.
3. **A per-route deadline** (`run within "10s"`), for a check that is slow on
   purpose. 30 s is fixed until one asks.
4. **Starting degraded.** `proceed` lets the process start with a check
   failed, and nothing tells the program afterwards. A health endpoint can
   report it only if the workflow recorded it somewhere.
