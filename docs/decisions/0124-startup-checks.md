# ADR 0124: Startup Checks: `adapter startup`

Status: Accepted (2026-11-20; the owner approved the design in review).
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
phase, an init container), as Rails does it. §6 adds `once` for a route that
asks.

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

(I8 is already the stream route's `group` and `from`, ADR 0111 §3a. A draft
had an I10 for a mapping that uses another kind's disposition (`done`,
`ack`, `retry`), or an adapter that mixes route kinds. The parser refuses
both outright, as it does for a schedule, so there is nothing left for the
checker to class.)

### 6. `-check`, `once`, and `within` (added 2026-11-20)

The first three open questions, decided and built.

```ebnf
startup-route ::= 'run' [ 'once' ] [ 'within' string-literal ] '->' route-call startup-mapping
```

- **`fiig serve -check`** runs the startup routes of the adapters it is
  named with, then exits 0 (the program could start) or 1 (one error naming
  every refusal). Nothing listens, and **no migration is applied**: a
  check that only asks a question must not change the database. It reads and
  checks config as `fiig serve` does, so a missing variable fails it too. A
  `once` route runs without claiming, since one process is asking whether it
  could start.
- **`within "10s"`** is a route's own deadline, in place of 30 s, written as
  a Go duration above zero (I10 otherwise). The fault-or-deadline rule is
  unchanged: running out of time halts.
- **`once`** runs the route on one replica per deploy, and holds every other
  replica until it has finished. The route is claimed with the
  `ScheduleLock` bound for its package (ADR 0112 §4), under the route's name
  and the deploy's id (below):
  - The replica that wins the claim runs the route. If its mapping says
    `proceed`, it **completes** the route, which the lock remembers for 7
    days. If it halts, faults or runs out of time, it records nothing and
    refuses to start.
  - A replica that loses the claim **waits**, asking once a second whether the
    route has been completed, and starts without running it when it has.
  - The claim is held for the route's deadline plus 30 s. A winner that dies
    leaves nothing completed, so its claim lapses, and a waiting replica's
    next claim wins and runs the route itself. A replica gives up after twice
    that long, and refuses to start.
  - A claim or a question that can't be answered, whether it faults or the
    lock is unavailable, **halts**, since whether the route has run is
    unknown. A `once` route with no lock bound is refused at load, as a
    schedule is.
  - A failure to record the completion is logged and doesn't stop this
    replica, which passed; the others wait out their time and run it.
- **The deploy's id.** `once` means once per deploy, so a deploy needs a name
  every replica of it agrees on and no other deploy shares. `fiig serve`
  takes `$FIIG_DEPLOY` when it is set, as a release tag or commit would be
  for a team that wants a redeploy of unchanged code to run its routes
  again. Otherwise it is a hash of the program's `.fiig`, `.sql` and `.json`
  files under the root, with line endings normalised: the same code is the
  same deploy, and changed code, which is what a migration ships with, is a
  new one. A timestamp or a process id would be different on every replica.
- **`ScheduleLock` grows two gateways**, beside `claim`:
  `complete(job, firedAt, holder, retainMillis)` and `completed(job,
  firedAt)`, each `Result<bool, LockError>`, with the deploy's id as
  `firedAt`. A schedule never calls them. `fiig.redis`'s lock sets a second
  key, `<claim key>:done`, with `SET key holder PX retain`, and `EXISTS` asks.
  A lock package written before this has no such gateways and is refused when
  a `once` route calls it (there is one such package, and it is this repo's).

**What `once` still does not promise.** It is once per deploy, and a replica
of a deploy that the lock has forgotten (after the seven days) runs it again.
A route that fails halts the winner, and the others wait until its claim
lapses (the deadline and slack) before one of them tries, so a route that
always fails takes the replicas down one at a time, an interval apart, where
running on every replica would have taken them down together. And the wait is
a poll, so a replica starts up to a second after the winner is done.

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
  other loops; `fiig catalog` lists a program's startup routes.
- `fiig test` needs nothing: a test calls the workflow like any other (ADR
  0102 §3a).
- `docs/deploying.md` gains a "Startup adapters" section.

## Implementation notes (2026-11-20)

- **Syntax:** `AdapterStartup`; `parseStartupRoute` reads `run -> workflow()
  { pattern -> proceed | halt }`, with `startup`, `run` and `proceed` as
  contextual words. A route has `Verb: "run"` and no `Path`. The formatter
  writes `run -> ...` and the catalog lists a route as `run`.
- **Checker** (`compiler/checker/startup.go`): I9, and I7 through the same
  `uncoveredOutcomes` a schedule uses. A route that calls a non-workflow is
  B6 as ever. The grammar and `bug-classes.md` are updated.
- **Runner** (`compiler/adapter/startup.go`): `StartupRunners` plans each
  route, `Check` runs one with a deadline, and `RunStartup` runs all of them
  at once and returns one error that names every failure, sorted so it reads
  the same each time. `fiig serve` partitions its loops, runs the startup
  one first, and exits 1 before it listens when it fails. Nothing else needed
  to know: the config a startup workflow reads is reached through the
  adapter, so `fiig config . app.Preflight` lists it.
- **Tests:** the parser and checker (I7, I9, B6, and a mapping or route
  keyword from another kind refused); the formatter; and
  `tooling/runprogram/startup_test.go` for the runner: a check that
  proceeds, a tolerated `Err` that is logged, and one report naming a halt,
  a fault and a deadline together, with what passed left out.
- **Dogfooded** with `fiig serve` on a scratch application (an HTTP route,
  two startup routes reading config): with `STORAGE_READY=false` it logged
  the tolerated cache failure, refused, named the storage check, and exit
  code 1 came before any listener; with `STORAGE_READY=true
  CACHE_READY=false` it started and served.
- **§6 (2026-11-20):** `Route.Once` and `Route.Within` in the syntax, parsed
  and formatted between `run` and `->`; I10 in the checker; the runner reads
  `within`, and runs a `once` route through claim, wait, run and complete in
  `Check` (`StartupRunners` takes the holder and the deploy id, and fails at
  load for a `once` route with no `ScheduleLock`); `StartupRunner.IgnoreOnce`
  is what `-check` sets; `migrate.Skip` is the mode `-check` loads with.
  `runtime.ScheduleLock` gained `Complete` and `Completed`, `LockProvider`
  calls any of its gateways through one `gatewayBool`, and
  `fiig.schedule.ScheduleLock` and `fiig.redis`'s lock gained the two
  gateways. `servecmd`'s `deployID` is `$FIIG_DEPLOY` or the program's hash.
- **Tests for §6:** the runner against a scripted lock (a loser that finds the
  route done, one that waits and then does, a winner that died and is taken
  over from, a winner that never finishes, a claim that fails, a route that
  halts and so isn't recorded); one end to end through `fiig.redis`'s real
  lock and a fake Redis, in which two replicas of a deploy run a route once
  and a new deploy runs it again, and which, with the `completed` lookup
  deliberately broken, fails; and the deploy id.
- **Dogfooded** on the scratch application: `-check` exited 1 naming the
  storage check and 0 when it passed, and a `once` route was refused without a
  lock and passed with `{"type": "local"}`.
- **Not needed:** no IntelliJ or LSP change. The contextual words aren't in
  their keyword lists (`every` and `done` aren't either).

## Open questions

1. ~~A way to run the checks alone~~, done: `fiig serve -check` (§6).
2. ~~Once per deploy, with the others waiting~~, done: `once` (§6) is run by
   one replica per deploy and awaited by the rest, with `complete` and
   `completed` on the lock. A lock for a sql program is `fiig.sqllock`'s
   `SqlScheduleLock` (ADR 0125). Still open: a deploy id that a platform
   supplies without an environment variable.
3. ~~A per-route deadline~~, done: `within` (§6).
4. **Starting degraded.** `proceed` lets the process start with a check
   failed, and nothing tells the program afterwards. A health endpoint can
   report it only if the workflow recorded it somewhere.
