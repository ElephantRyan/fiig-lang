# ADR 0149: An Application Sets Its Concurrency Default and Maximum

Status: Accepted (2026-11-24, by the owner). Implemented the same day (see
Implementation notes).
Date: 2026-11-24
Prompted by: Hearsay's conversion to ADR 0148 (its GAPS G78, once filed).
Related: `fiig-lang.md` §3.7 (the limits on concurrency), ADR 0016 (one
`concurrent` syntax), ADR 0024, ADR 0148 (reading a batch), ADR 0071 §5
(an application-only manifest field), ADR 0140 (settings), ADR 0054 §6
(`MaxCallDepth`, the other runtime bound), bug classes D1, D2.

## Context

A `concurrent` block with no `limit:` runs at most 16 bodies at once. The
16 is `DefaultConcurrencyLimit`, set in `runtime.New`
(`compiler/runtime/interp.go`) and `NewDeterministic`
(`compiler/runtime/clock.go`). Nothing a program or a deployment controls
can change it: not `fiig.pkg.json`, not `caps.json`, not a flag of
`fiig serve`, `fiig run` or `fiig test`. Only fiig's own Go tests set it.

`fiig-lang.md` §3.7 already specifies more than that:

> Effective concurrency is limited by the lowest applicable value among:
> Application default, Application maximum, Capability maximum, Locally
> requested limit, Parent workflow budget.

Of those five, only the locally requested limit exists. The application
default is a constant, and the application maximum, capability maximum
and parent budget (beyond D2's check of two literals) don't exist. The
field's own comment says the 16 was picked because "nothing in the spec
fixes it".

Hearsay found the cost when it converted to ADR 0148. Its three fan-outs
(`refreshSeries`, `refreshBooks`, `fetchOnSchedule`) each say
`limit: 4`. They say it because the app as a whole talks to one
self-hosted server and should be gentle with it, not because each block
chose 4 for its own reasons. The number belongs to the application, and
today it has to be repeated at every block, and kept in step by hand.

16 isn't wrong. A fixed number is wrong for every program at once. A
goroutine is cheap, so the bound doesn't protect the process. It protects
what the bodies call: a server's capacity, a pool's size, a peer's rate
limit. Only the application knows those.

### Precedent

| | Default bound | Who can change it | Applies to library code |
| --- | --- | --- | --- |
| **Kotlin** `Dispatchers.IO` | 64 threads | the application, by the system property `kotlinx.coroutines.io.parallelism`; one use narrows it with `limitedParallelism(n)` | yes |
| **Java** `ForkJoinPool.commonPool()` (parallel streams) | cores − 1 | the application, by `java.util.concurrent.ForkJoinPool.common.parallelism` | yes |
| **.NET** thread pool | derived from cores | the application, in `runtimeconfig.json` (`System.Threading.ThreadPool.MaxThreads`), a file built with the app | yes |
| **Tokio** blocking pool | 512 threads | the application, on the runtime builder (`max_blocking_threads`) | yes |
| **Elixir** `Task.async_stream` | `System.schedulers_online()` per call | each call (`max_concurrency:`) | no process-wide setting |
| **Go** `errgroup.SetLimit(n)` | none (unbounded unless set) | each group | no process-wide setting |

Every runtime that has a process-wide bound lets the application set it,
and in each it covers library code too, which is the point: an
application can't edit its dependencies. The two without one (Elixir, Go)
leave it to each call, which is what fiig does today, minus the ability
to say the number once.

.NET is the closest in shape: the setting is in a file that ships with
the application, not in code and not in the environment.

## Decision

### 1. A root application's manifest may set `concurrency`

```json
{
  "path": "hearsay",
  "kind": "application",
  "version": "0.4.0",
  "concurrency": {
    "default": 4,
    "maximum": 8
  }
}
```

- **`default`** is the limit of a `concurrent` block with no `limit:`.
  Omitted, it is 16, as today.
- **`maximum`** is the most any one block may run at once, whatever its
  `limit:` says. Omitted, there is none, as today.

Both are optional, and each must be a positive integer, with `default` no
greater than `maximum`. Anything else is a manifest error that names the
field. There is no way to write "unbounded" (D1 is unchanged): bounding is
the language's safe default, not the application's choice.

Only a manifest of `"kind": "application"` may have `concurrency`, as only
one may have `environment` (ADR 0071 §5). A library's manifest that has it
is an error. One process runs one application, and a number that bounds
the whole process can only be decided by whoever runs it. A library that
needs a particular bound writes `limit:` on its block.

### 2. The effective limit

For each block:

```
effective = min(requested, maximum)
requested = the block's limit:, or else default
```

This is §3.7's "lowest applicable value" over the two values this ADR
adds. A block's `limit:` was already a request in §3.7's terms, not a
promise.

`fiig serve`, `fiig run`, `fiig run -replay`, `fiig migrate` and
`fiig test` all read the root manifest's values, so a test fans out as
production does. A test still runs on the deterministic clock and random
source (`NewDeterministic`); only the two numbers come from the manifest.

### 3. Strict about the application's own source, lenient about its dependencies

A literal `limit:` above `maximum` in the application's own package is a
compile error, a new bug class **D3**:

```
[D3] concurrent limit 32 exceeds the application maximum of 8 (fiig.pkg.json)
```

The application's own source and its manifest contradict each other, and
only one of them can be meant. A literal `limit:` above `maximum` in a
dependency is clamped to `maximum` without error: the application can't
change that code, and the clamp is exactly what `maximum` is for. This is
charter principle 10, strict about what the program says and lenient
about what it receives, with a dependency's code in the role of what is
received.

A `limit:` that is an expression, not a literal, is clamped at run time
wherever it is, as D2 already only compares literals.

### 4. Unchanged

- D1: no block can be unbounded.
- D2: a nested literal limit may not exceed its parent's literal limit.
- `maximum` is per block, not a total for the process. Two blocks running
  at the same time, or one nested in another, can still have more than
  `maximum` bodies in flight between them. That total is §3.7's
  hierarchical budget, which this ADR doesn't build (open question 2).

## Alternatives considered

**A. Keep 16, and write `limit:` at every block.** No change. Rejected:
§Context shows the number is the application's, so repeating it at every
block is how the copies drift apart. It also leaves no way to bound a
dependency's blocks.

**B. A setting (ADR 0140) or an environment variable, so the deployment
can change it.** Kotlin's and Java's knobs are system properties, set at
launch. But a setting is read through a `Database` gateway at run time,
and the runtime needs the number before any fiig code runs. An
environment variable would fit, but it moves a property of how the
program behaves out of the files that ship with it, so `fiig test` and
production could fan out differently without anything in the repository
saying so. Rejected for now: the manifest is what the application
commits to. Whether an operator also needs an override is open question 1.

**C. Make a `limit:` above `maximum` an error everywhere, dependencies
included.** The strictest reading. Rejected: an application would then
have to fork a library to deploy it with a lower maximum, which is the
problem `maximum` exists to solve.

**D. Clamp silently everywhere, the application's own source included.**
Simpler, and what every runtime in §Precedent does. Rejected for the
application's own source: `limit: 32` that runs 8 at a time is code that
says something false, and fiig refuses that when it can see it (§3).

**E. A per-capability maximum ("at most 4 calls in flight to
`AbsCatalog`").** §3.7's capability maximum, and the precise version of
what Hearsay's `limit: 4` means: it holds across every block, and leaves
blocks that call something else alone. It needs a count shared by every
call to a capability in the process, which is new runtime machinery and
a new manifest shape keyed by capability. Not rejected, but not this ADR:
open question 3.

## Consequences

- Hearsay drops `limit: 4` from its three fan-outs and sets
  `"concurrency": { "default": 4 }` once.
- `compiler/manifest`: a `Concurrency` field with `Default` and `Maximum`,
  validated in `Validate` (positive, `default` ≤ `maximum`, application
  only), beside `Environment`.
- `compiler/runtime`: `Interp` gains `MaxConcurrencyLimit` (0 meaning
  none), and `resolveConcurrencyLimit` applies it to both the default and
  a requested limit.
- `tooling/cli` (`servecmd`, `runcmd`, `migratecmd`, `testcmd`, replay):
  each sets the two fields from the root manifest after `runtime.New`.
- `compiler/checker`: D3, which needs the root manifest's `maximum` and
  whether a file is in the root package. `compiler/loader` passes them
  in.
- `docs/specification/bug-classes.md` gains D3. `fiig-lang.md` §3.7 marks
  the application default and maximum as decided here.
- `docs/known-limitations.md`: `maximum` is per block, not a process
  total, and there is no per-capability maximum.
- An application with no `concurrency` behaves exactly as today.

## Open questions

1. **An operator's override.** Hearsay is self-hosted, so the person
   running it may have a weaker server than the one its authors tested
   against. Whether that needs an environment variable that can only
   *lower* the manifest's values (so it can't remove a bound the
   application relies on), or whether a rebuild is the right cost, waits
   for someone to ask.
2. **A process-wide total.** §3.7's hierarchical budget: nested and
   concurrent blocks sharing one count, so `maximum` would bound the
   whole process. Needs a budget carried in the context through every
   block, and a rule for what a nested block does when the budget is
   spent (wait, or run its body inline).
3. **A per-capability maximum** (alternative E). The next step if Hearsay
   finds that its fan-outs to ABS overlap, or a second program needs to
   bound calls to one server across blocks. Postgres's pool already bounds
   one capability this way.
4. **Showing the effective limit.** A clamped dependency block runs fewer
   at once than its source says. A trace span already exists per block;
   recording the requested and effective limits on it may be enough, or
   `fiig catalog explain` may need to show it.

## Implementation notes (2026-11-24)

- **Manifest** (`compiler/manifest`): `Concurrency` with `Default` and
  `Maximum` as `*int`, so an explicit `0` is an error and not "omitted".
  `Validate` reports a library that sets it, a value below 1, and a
  default above the maximum. `ConcurrencyLimits()` gives both, 0 for one
  not set. `docs/schemas/fiig.pkg.schema.json` has the field.
- **Runtime**: `Interp.MaxConcurrencyLimit` (0 for none).
  `resolveConcurrencyLimit` takes the block's limit or the default, then
  caps it, so a default of 16 under a maximum of 8 runs 8.
- **Commands**: `runprogram.UseConcurrency(in, root)` reads the root
  manifest, as `runprogram.Renames` does, and `fiig run`, `fiig serve`,
  `fiig run -replay`, `fiig migrate` and `fiig test` (each test's
  interpreter) call it after `runtime.New`.
- **D3** (`compiler/checker/concurrency_maximum.go`,
  `ApplicationMaximum`) walks each declaration's body in one file, private
  helpers included, and not by `syntax.Inspect`, which follows the
  checker's resolved pointers into other packages. The loader runs it in
  `buildModule` over the root module's packages only, with the root
  manifest's maximum; a dependency's module gets 0. The LSP's lenient load
  drops loader errors, so D3 shows in `fiig check`, not yet in the editor.
- **Found by dogfooding**: `loader.Wanted` returned false for a manifest
  that didn't validate, so `fiig check` skipped the loader and checked as
  if there were no manifest, reporting none of its problems (any of them,
  not only this ADR's). It now loads such a root, and the loader reports
  them.
- **Tested**: `TestConcurrency` (manifest), `TestALimitAboveTheApplicationMaximum`
  (D3 in the app, a dependency's `limit: 64` loads), `TestAnInvalidManifestIsWanted`,
  `TestTheApplicationMaximumCapsEveryBlock` and
  `TestABlockRunsNoMoreThanTheMaximumAtOnce` (runtime: 40 bodies asking for
  40 at once under a maximum of 3), `TestUseConcurrency` (runprogram).
- Not done here: Hearsay dropping its three `limit: 4`s for
  `"concurrency": { "default": 4 }`, which is Hearsay's change to make.
