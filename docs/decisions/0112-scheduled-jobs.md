# ADR 0112: Scheduled Jobs Are a Fourth Adapter Kind

Status: Proposed, implemented (2026-11-20)
Date: 2026-11-20
Related: ADR 0044 (workflow as the adapter boundary), ADR 0047 and ADR
0111 (stream adapters, which this follows), ADR 0068 (`Clock`), ADR 0079
§6a (per-package bindings), ADR 0110 (`fiig.redis`).

## Context

The charter's first scope lists "scheduled jobs" beside HTTP services and
queue consumers (`fiig-lang.md` §2). A program can't run a workflow every
fifteen minutes today except from outside, with a Kubernetes CronJob or a
crontab calling `fiig run`. That puts when a job runs in a second artifact,
the same problem ADR 0047 §5 found with stream dispositions.

### What a schedule has to settle

| System | Where the schedule lives | Several replicas | A missed firing | A run still going at the next |
| --- | --- | --- | --- | --- |
| Kubernetes CronJob | cluster YAML | one Job per firing | caught up within `startingDeadlineSeconds` | `concurrencyPolicy`, default Allow |
| Spring `@Scheduled` + ShedLock | annotation, lock in a DB or Redis | lock per job name for `lockAtMostFor` | skipped | skipped by the lock |
| Oban cron | app config | the leader node enqueues | skipped | a unique job |
| Sidekiq-cron / sidekiq-scheduler | YAML, Redis | a Redis key per firing | skipped | allowed |
| Quartz | code or XML, a JDBC store | row lock per trigger | misfire policy | `@DisallowConcurrentExecution` |

Two things decide the design. Every system that runs inside a
replicated service needs something shared to stop each replica running
the job, and the cheap version keys a lock by the job *and the firing
time*, which needs every replica to compute the same firing times. And no
in-process scheduler catches up on firings missed while nothing was
running, because a restart after a long outage would run them all at once.

## Decision

### 1. `adapter schedule`

```fiig
adapter schedule Maintenance {
    every "15m" -> refreshRates() {
        Ok(_) -> done
        Err(_) -> done
    }
    cron "0 3 * * *" -> purgeSessions(firedAt: int) {
        _ -> done
    }
}
```

```ebnf
schedule-route    ::= ( 'every' | 'cron' ) string-literal '->' route-call schedule-mapping
schedule-mapping  ::= '{' { pattern '->' ( 'done' | 'halt' ) } '}'
```

- **`every "15m"`** is a Go duration (`30s`, `15m`, `1h30m`) of at least
  1 s that divides a day evenly, so the firings fall at the same times
  every day. They are aligned to midnight UTC: `every "15m"` fires at :00,
  :15, :30, and :45.
- **`cron "0 3 * * *"`** is the five-field form: minute, hour, day of
  month, month, day of week (0 or 7 is Sunday), each `*`, a number, a
  range `a-b`, a list `a,b`, or a step `*/n` or `a-b/n`. When both day
  fields are restricted, either matching is enough, as in Vixie cron.
  Names (`MON`, `JAN`), `L`, `W`, `#`, `?`, and seconds are out.
- **Times are UTC.** A schedule in a time zone moves an hour twice a year
  in most of them, and fiig has no time zone data yet (open question 1).
- **The workflow takes nothing, or one `int`**, the firing time in Unix
  milliseconds (as `Clock.now()` gives it). A job that must not do a
  firing's work twice keys its writes by that.
- `schedule`, `every`, `cron`, and `done` are contextual words.

### 2. The mapping is required, and `done` or `halt`

As for a stream (ADR 0047 §5) there's no caller. The mapping must cover
`Ok` and every variant of the `Err` enum (or the value, when the workflow
doesn't return a `Result`).

- **`done`** waits for the next firing. **An `Err` that maps to `done` is
  still logged**, with the job and the error: it failed, and the mapping
  only says the process should carry on.
- **`halt`** stops `fiig serve` with an error, as a stream's halt does
  (ADR 0111 §4).
- **There is no `retry`.** The next firing is the retry. A job that needs
  to try again sooner fires more often and checks whether there is work.

**A fault fails its firing only**: it is logged and the next firing runs
*(amended 2026-11-20 after review; this first said a fault halts, as on a
stream route)*. A stream halts on a fault because the element would
otherwise be lost or retried forever; a schedule's next firing is already
its retry, as an overrun's is (§3), so stopping a process that may also
serve requests buys nothing. `halt` stays for what the mapping asks.

### 3. A run ends by the next firing

A run's deadline is the next firing time. A run that reaches it is
cancelled, logged as an overrun, and the next firing runs as usual: an
`every "1m"` job that sometimes takes 70 s is a sizing problem, not a
reason to stop a process that is also serving requests. So no two runs of
one job ever overlap, on one replica or across them.

**A firing missed because no process was running is skipped**, as in
Spring, Oban, and Sidekiq. The first firing after startup is the next one
due, never one in the past.

### 4. One replica runs each firing: `ScheduleLock`

A built-in package, `fiig.schedule`, declares what a lock provides:

```fiig
interface ScheduleLock {
    claim(job: string, firedAt: int, holder: string, holdMillis: int) -> Result<bool, LockError>
}

enum LockError {
    Unavailable(reason: string)
}
```

`claim` returns `true` to exactly one caller per `job` and `firedAt`. The
runtime claims each firing before running it, and runs it only on `true`.
Since every replica computes the same firing times (§1), the key needs no
leader election and no heartbeat, and a replica whose clock runs a little
late simply loses the claim. `holdMillis` is the time to the next firing,
so the claim outlives the run and then expires, and never less than a
minute *(added after review, 2026-11-20)*: an `every "2s"` claim held 2 s
would let a replica whose clock runs 3 s late claim a firing that already
ran.

The capabilities file binds it, and **a schedule adapter with no lock
bound doesn't start**:

```json
{"ScheduleLock": {"type": "lock", "lock": "fiig.redis"}}
{"ScheduleLock": {"type": "local"}}
```

- `"lock"` names a package that provides `fiig.schedule.ScheduleLock`.
  `fiig.redis` does, with `SET <key> <holder> NX PX <holdMillis>`.
- `"local"` always claims, which is right when one process runs the
  schedule (a single replica, or a deployment that runs schedules in a
  process of their own). Choosing it is a line in the file, so running
  every job on every replica is a decision someone wrote down, not the
  default.

A claim that fails (`Err(Unavailable)`) is logged and the firing is
skipped: running it anyway risks running it everywhere.

The job's name is the adapter's name, its package path first for an
adapter in a dependency, then the route as written: `Maintenance every
"15m"`.

### 5. Bug classes, in I

I becomes "Stream and schedule adapter errors":

- **I5**: an `every` duration that isn't a duration, is under 1 s, or
  doesn't divide a day; a `cron` expression that isn't five valid fields.
- **I6**: a schedule route's workflow takes anything but nothing or one
  `int`, or the route's arguments don't match it.
- **I7**: a schedule route's mapping is missing, or doesn't cover every
  outcome.

## Alternatives considered

- **Leave schedules to the platform** (Kubernetes CronJob, crontab). It
  is what fiig had. It puts the schedule in another file, makes every run
  pay a process start, and needs the job exposed as a CLI command.
- **A leader replica that runs every schedule** (Oban). It needs election,
  heartbeats, and handover, which is a distributed-systems component to
  get right for a feature whose per-firing lock is three Redis words.
- **Run on every replica unless told otherwise.** Spring's `@Scheduled`
  does, and ShedLock exists because people found out in production.
- **Catch up missed firings** (Kubernetes' `startingDeadlineSeconds`,
  Quartz's misfire policies). Useful for a job whose firings each matter,
  but such a job is better written to do everything pending when it runs,
  keyed by `firedAt`, than to be run N times in a burst after an outage.
- **`retry` as on streams.** A stream's element has nowhere else to be
  done; a schedule has its next firing.

## Consequences

- New built-in package `fiig.schedule`; capability types `"lock"` and
  `"local"`; `fiig.redis` provides `RedisScheduleLock`.
- `adapter schedule` in the syntax, parser, formatter, and checker
  (I5-I7); a cron and duration parser in `compiler/schedule`, used by both
  the checker and the runtime; `fiig serve` runs schedule adapters beside
  HTTP and stream ones.

## Open questions

1. **Time zones** (`cron "0 3 * * *" in "Europe/Paris"`), which need a
   time zone database in the toolchain and a rule for the hour that
   happens twice.
2. **Running a job now**, by hand: a `fiig run` of the route's workflow
   already does it, with `firedAt` passed in `-args`.
3. **Jitter**, for many services all firing at :00 against one database.
4. **`fiig config <root> <entry>` finds only root declarations**, so an
   adapter in a folder package (ADR 0105), of any kind, isn't found by
   name. Found while dogfooding this; it belongs to the folder-package
   work, not to schedules.

## Implementation notes (2026-11-20)

- `compiler/schedule` parses `every` and `cron` and computes `Next`, used
  by the checker (I5, including a cron that never fires, such as `0 0 31 2
  *`) and the runner.
- Syntax: `AdapterSchedule`, `parseScheduleRoute`, and `done` as a
  disposition kind; the formatter needed no change. Checks are
  `compiler/checker/stream_schedule.go`, reusing ADR 0111's
  `uncoveredOutcomes` for I7.
- `fiig.schedule` is `compiler/stdlib/schedule`. `compiler/runtime`'s
  `LockProvider` (`"lock"`) calls a lock's `claim` as a top-level call, as
  `SourceProvider` calls a source; `LocalLock` is `"local"`.
- The runner is `compiler/adapter/schedule.go`, with an injectable clock.
  `fiig serve` runs stream and schedule routes through one list of loops,
  so both log, halt, and finish on shutdown the same way.
- `fiig.redis` provides `RedisScheduleLock` (`SET ... NX PX`), keyed
  `fiig:schedule:<job>:<firedAt>`.
- Tests: `compiler/schedule` (firings, Vixie's day rule, refusals);
  `compiler/checker/stream_schedule_test.go` (I5-I7);
  `tooling/runprogram/schedule_test.go` on a fake clock (two replicas
  sharing four firings through the Redis lock, a `Busy` firing logged, a
  `halt`, a run that overruns, no lock bound).
- **Dogfooded against Redis 7:** two `fiig serve` replicas of an `every
  "2s"` job for 9 s ran four firings, aligned to even seconds, each once
  between them; with no lock bound, `fiig serve` refused to start.
