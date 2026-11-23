# ADR 0148: A Batch Is Read Like a Map, in Input Order

Status: Accepted (2026-11-23, by the owner). Implemented the same day (see
Implementation notes).
Date: 2026-11-23
Answers: Hearsay GAPS G77.
Amends: ADR 0024 (`preserveOrder`, retired), `fiig-lang.md` §3.7
("completion order is the default for speed").
Related: ADR 0016 (one `concurrent` syntax), ADR 0053 (`for`), ADR 0065
§7 (`for (key, value in m)`), ADR 0093 (list forms), ADR 0142 (statement
syntax), bug classes D1, D2, F2, I1, J1, K1.

## Context

`concurrent(x in xs): body` runs `body` for each element on a bounded pool
of goroutines and produces a `BatchResult<K, S, F>`: for each element `K`,
the body's `Result<S, F>`. A Fault in any body cancels the rest; a typed
`Err` is that element's outcome.

Nothing in fiig code can read a `BatchResult`. It has no methods, `for`
refuses it, and J1 keeps it out of JSON:

```
[I1] for ... in expects a List, got BatchResult<int, int, string>
[K1] BatchResult<int, int, string> has no methods, so it has no "length"
```

So a batch can only be returned, and its caller can't read it either. Every
`concurrent` in the corpus is `return concurrent(...)`, including the
`retrieveCustomers` workflow that was Milestone 4's vertical slice, which no
caller and no route can use. The runtime half has worked since Milestone 4
(`compiler/runtime/concurrent.go`); the language half was never finished.

Hearsay found this (G77) looking for where it could run network calls at
once. It makes every Audiobookshelf and linked-server call one at a time:

- `RefreshSeries`: one ABS call per cached series, daily.
- `FetchOnSchedule`: each linked server in turn, so one slow server holds
  up the rest every hour.
- `RefreshBooks`, `IndexLibrary`, `ApplyPlans`: batches and pages that
  don't depend on each other.

Each fans out, then acts on each outcome in a plain loop. The saves stay in
the loop because SQLite takes one writer.

### Completion order buys nothing once a batch is read

`fiig-lang.md` §3.7 made completion order the default "for speed", with
`preserveOrder` (ADR 0024) to ask for input order. The speed only exists
for a consumer that handles each result as it arrives. A `BatchResult` is
collected whole: `evalConcurrentBody` returns only once every body has
finished, so a loop over it starts after the last result either way.
Completion order makes no program faster. It only makes a loop over the
batch run in a different order each time:

- which `Err` a loop returns first changes from run to run;
- the order rows are saved in changes;
- a test that reads a batch passes or fails by timing, against the
  determinism fiig's tests are built on (the seeded `Random` provider, the
  test clock).

Keeping input order costs one slot per element, which the runtime already
allocates for `preserveOrder`.

### Precedent

| | Bounded | Result | Order |
| --- | --- | --- | --- |
| **Elixir** `Task.async_stream(xs, f, max_concurrency: n)` | yes | a stream of `{:ok, v}` / `{:exit, r}`, read with `Enum` | **input** by default; `ordered: false` for completion |
| **JavaScript** `Promise.allSettled(ps)` | no | `[{status, value \| reason}]` | **input** |
| **Rust** `join_all(fs)` / `buffer_unordered(n)` | the second | `Vec<T>` / a stream | **input** / completion, as a stream |
| **Java** `StructuredTaskScope` | no | a `Subtask` per fork, read after `join()` | the program keeps its own list, so **input** |
| **Go** `errgroup.Group` with `SetLimit(n)` | yes | none: the program writes into a slice by index | **input**, by the program |
| **Kotlin** `awaitAll` | no | `List<T>` | **input** |

Elixir's is fiig's `concurrent` almost exactly (a bounded fan-out over a
collection, with an ordering switch), and its result is something a loop
reads. Every collected result in the table is in input order. Completion
order appears only where results are read as they arrive (Rust's
`buffer_unordered`, Elixir's `ordered: false` stream), which a collected
`BatchResult` can't do.

## Decision

### 1. `for (key, outcome in batch)`

A `BatchResult<K, S, F>` is read with `for`'s two-name form, as a `Map` is
(ADR 0065 §7): the first name is the element the body ran for, `K`, and
the second is its outcome, `Result<S, F>`.

```fiig
activity refreshSeries() -> Result<SeriesRefreshed, absclient.AbsFault> {
    now = Clock.now();
    read: int = 0;
    removed: int = 0;
    fetched = concurrent(id in cachedSeriesIds(), limit: 4): fetchSeries(id);
    for (id, outcome in fetched) {
        match (outcome) {
            Ok(series) -> {
                saveSeriesCache(series, now);
                read = read + 1;
            },
            Err(absclient.AbsFault.Missing) -> {
                forgetSeries(id);
                removed = removed + 1;
            },
            Err(fault) -> {
                return Err(fault);
            }
        }
    }
    return Ok(SeriesRefreshed(read: read, removed: removed));
}
```

- `_` discards either name, as for a map: `for (_, outcome in fetched)`.
- One name over a batch is I1, with a message that names the two-name form,
  as one name over a map already is. One name would have to mean the key,
  the outcome, or a pair, and fiig has no pair type; ADR 0065 §7 refused to
  guess for maps for the same reason.
- **It isn't a `Map`.** A map would merge an element that appears twice in
  the input, and would require `K` to be comparable (ADR 0052), which
  `concurrent` never has. A batch has one entry per element of the input,
  duplicates included.

### 2. `.length()`

`batch.length()` is the number of entries: the input's length, since a
Fault ends the block without a batch. It is `BatchResult`'s only method.

### 3. A batch is in input order, always

The entries are in the order of the list `concurrent` walked, whatever
order the bodies finished in. This replaces §3.7's completion-order
default.

`preserveOrder` is retired: it is a syntax error naming this ADR
(`results are always in input order (ADR 0148); remove preserveOrder`),
and `fiig fmt` drops it. Its one use,
`examples/customer-service/workflows/retrieve_customers_in_order.fiig`,
becomes the same as `retrieve_customers.fiig` and is removed. The word
stays reserved, so it can't come back as a name with a different meaning.

The bodies still run in whatever order the pool takes them, and still at
most `limit` at once (D1, D2 unchanged). Only the batch's order is fixed.

### 4. Unchanged

- A Fault in a body cancels the others, and the block faults.
- A typed `Err` is that element's outcome, and every body runs. A `?` in
  the body makes the `Err` the outcome, not the enclosing declaration's
  (F2).
- J1: a `BatchResult` is still not JSON. A route returns what a loop built
  from it.

## Alternatives considered

**A. A `BatchItem<K, S, F>` model with `.key` and `.outcome`, read with
one name** (G77's suggestion). Works, and reads well. But it is a second
way to iterate pairs, beside the map form fiig already has, and a new
prelude type (K2) for it. Rejected for the map form, which says the same
thing with nothing new.

**B. `concurrent` produces `List<Result<S, F>>` in input order, with no
`BatchResult`.** The smallest type, and list forms (ADR 0093) work on it at
once. But the key is lost: a loop that needs it (`forgetSeries(id)`) has
to index the input list in step, and fiig has no `zip`. Rejected; §1 keeps
the key at no cost.

**C. Keep completion order as the default and `preserveOrder` as the
switch.** No migration. Rejected: §Context shows completion order gives a
collected batch no speed and makes reading it nondeterministic, and a
default that only ever costs is one nobody should have to remember to turn
off.

**D. Keep `preserveOrder`, accepted and ignored.** Old code keeps working.
Rejected: it would say something that is no longer a choice, and fiig is
strict about what it reads (charter principle 10).

**E. `.successes()` and `.failures()`.** Hearsay's suggestion. Each is
three lines with §1, and they would be the first list-like methods on a
type that otherwise has one. Not now; see open question 2.

**F. Read results as they arrive** (a `for` that starts before the last
body ends). The one design where completion order is worth having, and
what Rust's `buffer_unordered` does. It needs the loop body to run while
the pool still holds goroutines, which is a different runtime from today's,
and nothing in Hearsay needs it: every loop above waits for SQLite one
write at a time anyway. Out of scope.

## Consequences

- `concurrent` becomes usable: Hearsay's five sequential loops can fan out.
  The proof is converting `src/series/RefreshSeries.fiig` and
  `src/links/FetchOnSchedule.fiig` there.
- `compiler/checker`: `for` over a `BatchResult` binds `K` and
  `Result<S, F>`; one name is I1 with the two-name suggestion; `length` is
  in its method table (K1 otherwise unchanged).
- `compiler/runtime`: `evalConcurrentBody` always collects by index, and
  `for` walks a `BatchResult`'s items. The completion-order path goes.
- `compiler/parser`, `compiler/format`: `preserveOrder` is an error the
  formatter drops, like ADR 0147's comma.
- Docs: `fiig-lang.md` §3.7's "completion order is the default for speed"
  and "`preserveOrder` is an explicit modifier" lines, `grammar.md`'s
  `concurrent-modifier`, `tour.md`'s example, and `terminology.md`.
- A program whose correctness depended on completion order breaks
  silently. None in the corpus does: no `BatchResult` could be read.

## Open questions

1. **Stopping early on some errors.** G77's `refreshSeries` goes on past
   `Missing` but stops on anything else, and today every call runs even
   after one the loop will return on. Go's `errgroup` and Java's
   `StructuredTaskScope.ShutdownOnFailure` cancel on the first error. A
   `failFast` modifier would have to say what the block produces when it
   stops (a `Result<BatchResult<...>, F>`?), and "stop on some errors"
   needs the body to turn the tolerated ones into `Ok` first, usually a
   helper declaration per call site. Decide after the Hearsay conversion
   shows what an outage actually costs.
2. **`successes()` / `failures()`**, or list forms on a batch, if a second
   program writes the same filtering loop.
3. **`xs.chunks(n)`**, G77's third suggestion, for `RefreshBooks`'s batches
   of 50. A list method that is independent of `concurrent`, for ADR
   0093's list forms.

## Implementation notes (2026-11-23)

- **Checker** (`compiler/checker/stmt.go`): `checkMapFor` became
  `checkPairFor`, shared by a `Map` and a `BatchResult`; each has its own I1
  message for one name. Two names over anything else names both kinds.
  `compiler/types/methods.go` gives `BatchResult` a table with `length`
  alone, so any other method is K1 as before.
- **Runtime**: `evalConcurrentBody` puts each result in its input slot, and
  the completion-order path is gone. `evalMapFor` became `evalPairFor`,
  used by the tree walker and the compiled loop (`compileFor`) alike.
  `BatchResult.length` joined the builtin table, which
  `TestEveryBuiltinMethodIsImplemented` keeps in step with the types.
- **Parser and formatter**: `preserveOrder` is reported and left out of the
  tree, and `parser.FormatFixes` (which also covers ADR 0147's comma) lets
  `fiig fmt` format the file and drop it. `ConcurrentExpr.PreserveOrder`
  is gone. The lexer keyword stays, so the word stays reserved.
- **Tested**: `compiler/runtime`'s `TestBatchPrograms` reads a 40-element
  batch over a conversion with `limit: 3` in fiig code, in input order,
  with `_` and `.length()`, and an empty batch. `TestABatchIsInInputOrder`
  (formerly ADR 0024's test) shows plain `retrieveCustomers` is in input
  order even when earlier elements finish last. `compiler/checker`'s
  `TestReadingABatch` covers the types, I1 and K1.
- **Removed**: `examples/customer-service/workflows/retrieve_customers_in_order.fiig`.
  `tour.md`'s concurrency example now reads its batch, checked against
  `examples/customer-service`.
- Not done here: converting Hearsay's `RefreshSeries.fiig` and
  `FetchOnSchedule.fiig`, which is Hearsay's change to make.
