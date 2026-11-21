# ADR 0127: Starting Degraded

Status: Proposed
Date: 2026-11-21
Answers: ADR 0124 open question 4.
Related: ADR 0071 (config), ADR 0122 (`config ... when`), ADR 0124 (startup
checks).

## Context

A startup route's `proceed` lets the process start after a failed check, and
an `Err` mapped to it is logged (ADR 0124 §1). Nothing tells the program
afterwards. A health endpoint can report "storage was unreachable at start"
only if the workflow wrote that somewhere a later request can read, and a
process has no memory of its own: the program would have to put it in a
database the failing check may be about.

Spring's runners can catch a failure and carry on, and the application then
carries a flag or a bean of its own. fiig has no process state (ADR 0114 is
about connections, not values), but it does have one kind of value that is
decided before the program starts and fixed for its life: config.

## Decision

### 1. `degrade`

A startup mapping has a third disposition:

```fiig
adapter startup Preflight {
    run -> verifyStorage() {
        Ok(_) -> proceed
        Err(StorageError.NoSuchBucket(_)) -> halt
        Err(StorageError.Unreachable(_)) -> degrade
    }
}
```

`degrade` starts the process, as `proceed` does, and records the route as
degraded. It is logged as `startup Preflight run -> verifyStorage: starting
degraded, for Err(...)`. A route that proceeds, or doesn't run, is not
degraded. `halt`, a fault and a deadline refuse to start, as before.

### 2. A config field read from startup

```fiig
config Health {
    storageDown: bool from startup "Preflight.verifyStorage"
}
```

```ebnf
config-field ::= identifier ':' type 'from' 'startup' string-literal
```

- The field is a **bool, with no default**, and the string names a route as
  `Adapter.workflow` of the **same package** as the config. Anything else is
  V1.
- It is `true` once that route has ended degraded, and `false` otherwise. It
  is set after the startup routes finish and before anything serves, so every
  request sees the final value, and a startup workflow that reads it sees
  `false`.
- **It is config in every other way.** It is read as `Health.storageDown`,
  in an activity or a gateway (B10), passed to rules and workflows as a value,
  and faked in a test (`fake Health { storageDown: true }`, ADR 0091 §4).
  Nothing needs setting in the environment, so `fiig config` doesn't list it.
- It needs no change to the adapters: a health route, or any route that
  depends on the storage, reads it like any other config and answers
  accordingly.

### 3. With `once`

A `once` route that ended degraded is **not recorded as done**. It did not
pass, and the replicas waiting for it would otherwise start without knowing
they should be degraded too. They take the claim over when it lapses (the
route's deadline and 30 s), run the check themselves, and find out for
themselves. A degraded start therefore holds the other replicas back for as
long as a failed `once` route does.

## Alternatives considered

- **A prelude function** (`degraded()` listing the names). It adds a built-in
  that reads process state, and a test would need a new kind of fake. Config
  is already read before the program starts and already fakeable.
- **Only logging it.** The start is visible to an operator, and the program
  still can't act on it, which is the open question.
- **A condition on `config ... when`** (ADR 0122). That decides whether a
  config is present, from other config; this is a value, not a presence.

## Consequences

- `degrade` in the parser and formatter; `from startup` on a config field;
  V1 widened (not a bool, a default, a route that isn't there); the loader's
  rename check, `fiig config` and the registry's variable list skip startup
  fields.
- `fiig serve` marks the degraded routes after the startup routes run
  (`Interp.MarkDegraded`).
- It is decided **once, at start**. A storage that recovers doesn't clear it.
  For that, a route checks again, as a health endpoint does (ADR 0124 §4).
- `fiig serve -check` runs the routes and reports a degraded one as a log
  line; it exits 0, since the process could start.

## Open questions

1. **A route of another package.** The name resolves only in the config's own
   package. A health package reading the storage package's route would need
   the path in the name.
2. **Clearing it.** A way for a later check to say the route has recovered, so
   a long-running process stops reporting degraded.

## Implementation notes (2026-11-21)

- `compiler/syntax` (`ConfigField.Startup`), the parser (`degrade`, `from
  startup`), the formatter, `compiler/checker/config.go`
  (`checkStartupField`), `compiler/runtime/envconfig.go`
  (`Interp.MarkDegraded`), `compiler/adapter/startup.go` (`Degraded`,
  `Route`), and `tooling/cli/servecmd`.
- Tests: the runners (`TestStartupDegradeStartsAndIsReported`,
  `TestStartupDegradedOnceRouteIsNotRecordedDone`), the checker
  (`TestStartupConfigFieldChecks`), the formatter, and an end to end one
  through `load` and an HTTP route (`TestADegradedStartupIsReadThroughConfig`).
  Skipping the marking in `fiig serve` made the last fail.
- Dogfooded: a scratch app whose route degrades answered `"degraded"` from a
  `GET /status` under `fiig serve`, `fiig test` passed with
  `fake CacheState { degraded: true }`, and `fiig config` lists no variable
  for the field.
