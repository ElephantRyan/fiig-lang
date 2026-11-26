# ADR 0115: A Faster Interpreter, Without a Build Step

Status: Accepted (2026-11-20, by the owner). Implemented 2026-11-20, all
seven steps (73e81df through 77f65f1); see Results.
Date: 2026-11-20
Answers: Milestone 7's "interpreter performance work for byte-stream
workloads".
Related: ADR 0042 (the socket floor: drivers and protocols in fiig),
ADR 0053/0059 (loops), ADR 0058 (bytes), ADR 0063 (int overflow faults),
ADR 0084 (drivers in fiig), ADR 0088 §4 (a rerun transaction restores its
locals), ADR 0113 (the Kafka producer), ADR 0116 (compression, to come).

## Context

ADR 0042 puts drivers and protocols in fiig, so every byte a Postgres,
Redis or Kafka connection carries goes through the interpreter. Python's
drivers are C (psycopg, librdkafka), so its interpreter never touches the
wire; fiig's does, so the interpreter's speed decides whether that design
works under load.

It works today for programs that mostly wait on a socket. It doesn't for
programs that move data. Measured on 2026-11-20 (AMD Ryzen 9 7950X, a Go
benchmark calling the interpreter directly):

```fiig
rule walk(data: bytes) -> int {
    sum: int = 0
    for b in data {
        sum = (sum + b) % 65521
    }
    return sum
}
```

| Input | Time | Per byte | Allocations |
|---|---|---|---|
| 1 MiB | 227 ms | ~216 ns | 6 and ~400 B per byte |

The same loop in Go, overflow check included, takes about 1 ns a byte. A
service reading 50 MB/s of Postgres replies (5,000 requests a second,
10 KB each) would spend about ten cores only parsing them.

The CPU profile says where the time goes:

| Cost | Share | Cause |
|---|---|---|
| Allocation and garbage collection | ~40% | every `Int` above 255 boxed into a `Value` interface; a new scope map per loop iteration; `for b in data` copying the bytes into a `[]Value` first |
| Variables by name | ~30% | `Env` is a `map[string]Value` per scope, chained to its parent; every read hashes the name and walks the chain; `Env.Child` allocates a map per iteration |
| Walking the tree | ~10% | a type switch and a `constValue` check per node, three results per call |
| Parsing literals | ~6% | `65521` read from its text on every iteration |
| The addition and modulo | ~2% | |

So 98% is overhead, and most of it is memory and names, not the tree walk
itself.

## Decision

Make the interpreter faster without adding a build step. A program is
still parsed, checked and run from source when `fiig run`, `fiig test` or
`fiig serve` starts; the new work runs once at that point, in
milliseconds, and nothing needs a Go toolchain.

### 1. Behaviour doesn't change

Every result, fault message, fault stack, trace span, overflow fault (ADR
0063), bounds fault, depth-limit fault (ADR 0054) and deadline stop (ADR
0059) stays the same. The existing tests across all three Go modules, and
`fiig test` over every example and stdlib package, are the oracle: each
step lands only when they pass unchanged. A test may only change to fix
the test itself.

### 2. A benchmark suite, first

`compiler/runtime/bench_test.go` holds the workloads the steps are judged
by, each reporting time and allocations:

- **bytes**: the loop above, 1 MiB
- **build**: appending to a list and to bytes (`<<data: bytes, x: u8>>`)
- **records**: reading and building model fields in a loop
- **murmur2**: the Kafka partitioner (ADR 0113), over 10,000 keys
- **calls**: a rule calling a rule calling a formula, a million times
- **parse**: decoding a Postgres DataRow stream with binary patterns

Their numbers before and after each step go in this ADR's results
section. A step that makes any workload slower than it was doesn't land.

### 3. Variables live in slots, one frame per call

A resolve pass, run once per declaration when the program loads, gives
each local a slot number. `sum` becomes slot 0 of its declaration's
frame, read and written by index. A call allocates one frame, a `[]Value`
sized for every local in the body, nested blocks included. Inner
blocks reuse slots, so `for` and `if` bodies allocate nothing per run.

Scoping rules stay as they are: the checker already rejects a name used
outside its block, so slot sharing can't be observed. A rerun transaction
(ADR 0088 §4) restores its locals by copying the frame's reassignable
slots, not by snapshotting maps.

`Env` stays as the public type `compiler/adapter` uses for response
mappings and pattern matches, built from a frame when an adapter asks for
one. It is no longer what a body runs in.

### 4. Each node is turned into a Go function once

The resolve pass also turns each expression and statement into a Go
closure: `sum + b` becomes a function that reads two slots and adds them,
with no type switch, no `constValue` check and no name. Literals are
parsed once, when the closure is built.

Where a type is known locally, the pass builds a typed closure. That
covers declared parameters, typed locals (`sum: int = 0`), int literals,
`for b in data` over bytes, and arithmetic on those. An int closure
returns an `int64` and never boxes, so `(sum + b) % 65521` allocates
nothing; the result is boxed only when it leaves the typed path, for
example when it's passed to a call. The checker isn't changed: the pass
infers only from what the declaration says, and falls back to the generic
closure wherever it can't tell.

`for b in data` reads the bytes in place rather than copying them into a
list first.

### 5. Less bookkeeping per call

Call depth and the name scope move from `context.WithValue` into the
frame. The context still carries cancellation, deadlines, transactions,
resource owners and trace spans.

Cancellation is checked on every call and every 256 loop iterations,
rather than on every statement. A runaway loop still stops at its
deadline, within microseconds.

### 6. Targets

- **bytes**: at most 20 ns a byte (about 10x), with no allocations per
  byte.
- **Every workload**: none slower than its baseline.
- **Load time**: no more than 5% longer for the largest example program.

Hitting the target makes reply parsing about 50 to 100 MB/s a core: the
example service above would spend under a core on parsing, not ten.

### 7. Order

1. The benchmark suite and baseline (§2).
2. `for` over bytes without copying (§4, last paragraph): small, alone.
3. Slots and frames (§3).
4. Closures, typed int paths and literals parsed once (§4).
5. Call bookkeeping (§5).

Each step is its own commit, with its benchmark numbers in the message.

## What this doesn't do

- **Native speed.** At 10 to 20 ns a byte, a compressor written in fiig
  would manage 10 to 30 MB/s, against more than 1 GB/s in Go. ADR 0116
  puts Kafka's four codecs in Go, in the prelude `compress` group; they
  could move to fiig behind the same names if these benchmarks ever get
  close.
- **Record layout.** Records keep `map[string]Value` fields. Fields by
  index is a separate change, if the **records** benchmark shows it's
  needed.
- **Numerics.** Loops over millions of floats stay far from NumPy, as a
  pure-Python loop does. That needs native vector kernels or compiled
  code, decided when the numerics work starts.

## Alternatives considered

- **Compiling fiig to Go.** It reaches near-native speed, about 200x on
  this loop, but needs a Go toolchain to build, and builds take seconds
  where today they're instant, slowing `fiig test` and the editor. It
  also doubles the implementations, since the interpreter stays for tests
  and tooling. It's kept in reserve as an opt-in release mode, `fiig
  build --native`, for when a real workload needs more than this ADR
  gives. It never goes into the edit-and-run loop.
- **A bytecode VM.** Comparable speed, but more machinery to write: an
  instruction set, an encoder, a dispatch loop, and a disassembler to debug
  it. Closures get most of the gain with less of that.
- **Only fixing the hot spots** (pre-parsed literals, no per-iteration
  map). Cheap, but it leaves names and boxing, about 70% of the cost.
- **Typed closures from the checker's types.** More paths could skip
  boxing, but the checker would have to record a type per expression,
  which it doesn't today. That can come later if the local inference in §4
  proves too narrow.

## Results

Each step's numbers, from `go test ./compiler/runtime -run '^$' -bench
. -benchmem -count 3` (median of three, AMD Ryzen 9 7950X). The suite is
`compiler/runtime/bench_test.go`.

### Baseline (2026-11-20, before step 2)

| Workload | Time per run | Allocations per run |
|---|---|---|
| bytes (1 MiB) | 222 ms (4.7 MB/s) | 6,285,000 |
| build (10,000 each) | 4.09 ms | 79,861 |
| records (10,000) | 8.05 ms | 119,399 |
| murmur2 (10,000 keys) | 366 ms | 4,952,500 |
| calls (100,000) | 186 ms | 2,399,450 |
| parse (10,000 DataRows, 459 KB) | 47.3 ms (9.7 MB/s) | 459,975 |

The calls workload puts one fiig call at about 900 ns and 12
allocations. The profile in Context skipped the checker, which folds
constants onto the syntax tree; the suite runs it, so parsing literals
isn't among its costs.

### Step 2: `for` over bytes in place

16 MB less allocated per MiB looped over; time within noise.

### Step 3a: scopes as short lists, not hash maps

A scope is one allocation, its first four bindings held inside it, and
a lookup compares strings from the newest binding; a scope past 16 names
gets an index. Every caller of `Env` is unchanged.

| Workload | Time | vs baseline | Allocations |
|---|---|---|---|
| bytes | 137 ms (7.6 MB/s) | 1.6x | 4,188,000 |
| build | 3.54 ms | 1.2x | 69,857 |
| records | 6.02 ms | 1.3x | 99,395 |
| murmur2 | 243 ms | 1.5x | 4,099,860 |
| calls | 126 ms | 1.5x | 1,899,442 |
| parse | 37.3 ms (12.3 MB/s) | 1.3x | 319,971 |

### Steps 3b and 5: reused scopes, and one allocation a call

- **Reused scopes.** A loop's body, a `match`'s arms, a list form's body
  and `sorted`'s keys each get one scope, emptied before every run,
  rather than a new one each time. Nothing outlives its block: fiig has
  no closures, and a concurrent expression waits for every body.
- **One allocation per call.** A call's context layer, carrying its depth
  and scope as one value, its root scope, and its first four arguments
  are one allocation (`callFrame`). Arguments go into it in parameter
  order, with no maps built.

| Workload | Time | vs baseline | Allocations |
|---|---|---|---|
| bytes | 95.4 ms (11.0 MB/s) | 2.3x | 3,139,480 |
| build | 2.98 ms | 1.4x | 59,855 |
| records | 5.92 ms | 1.4x | 89,393 |
| murmur2 | 220 ms | 1.7x | 2,980,496 |
| calls | 75.9 ms | 2.5x | 799,438 |
| parse | 33.6 ms (13.7 MB/s) | 1.4x | 289,969 |

Bytes allocates 25 MB per run, down from 419 MB. What's left is mostly
boxing: an `int` above 255 is a heap allocation each time it becomes a
`Value`, three times a byte here. That, and the tree walk itself, are
step 4.

### Step 4: bodies compiled to closures

A declaration's body is compiled to Go closures the first time it is
called, cached per `Interp` (`compile.go`, `compile_call.go`). Compiled:
- **Statements:** bindings, expression statements, `return`, `if`,
  `while`, `for`, `match`, `break` and `continue`.
- **Expressions:** names, constants (boxed once, when compiled), strings,
  parentheses, `not`, `?`, list literals, field reads on local records,
  `Ok` and `Err`, calls to declarations by name, and built-in methods on
  local values.
- **Arithmetic and comparisons:** these run in `int64` while both
  operands are ints, with `applyBinary`'s fault messages, so an
  expression boxes only its final value.

Everything else (bytes literals and patterns, capability calls, list
forms, `concurrent`, `retry`, transactions) is handed to the tree walk.

This differs from §3 and §4 in one way: **compiled code uses the same
`Env` as the tree walk, not numbered slots.** That keeps the two
paths in step by construction, since a fallback node sees exactly the
scopes compiled code does. A block that binds no names runs in its
parent's scope. Slots remain possible as a later step, and are what's
left between this and §6's bytes target.

Three smaller changes go with it:
- **Cancellation.** A call reads its context's `Done` channel once, and a
  check is a nil test when the context can't be cancelled.
- **Small ints.** Ints from -1024 to 65535 are boxed once at start-up,
  as CPython and the JVM cache theirs. That's about 1 MB, and best-of-eight
  timings show no cost to the garbage collector.
- **Loop variables.** A loop binds its variable without first searching
  the scope it has just emptied.

`TestCompiledBodiesMatchTheTreeWalk` runs 16 programs both ways,
covering overflow and division faults, labelled loops, `match`, `?`,
named arguments, recursion and method faults. It compares values and
fault messages.

The machine was shared while these ran, so these are each workload's
fastest of eight runs, not medians:

| Workload | Time | vs baseline | Allocations |
|---|---|---|---|
| bytes | 39.8 ms (26 MB/s, 38 ns a byte) | 5.6x | 5 |
| build | 2.56 ms | 1.6x | 50,109 |
| records | 5.11 ms | 1.6x | 59,750 |
| murmur2 | 149 ms | 2.5x | 1,608,030 |
| calls | 44.8 ms | 4.2x | 462,167 |
| parse | 25.5 ms (18 MB/s) | 1.9x | 282,596 |

Bytes allocates nothing per byte. §6's target of 20 ns a byte isn't met
yet: what's left is name lookups in `Env` and the closures' own calls.
murmur2 and parse are held back by what still goes to the tree walk:
bytes literals, binary patterns, and murmur2's XOR, which fiig spells as
bytes since it has no bit operators.

Writing the compiled `if` and call paths turned up two existing bugs,
which this step reproduces so it changes no behaviour: a `?` that fails
inside an `if` or `while` condition, or inside a call's arguments,
returns no value, so the declaration returns `None`, not the `Err`. They
are fixed separately.

### Step 6: slots

§3's slots, built so compiled code still shares the tree walk's `Env`:
- **Remembered positions.** Each compiled name keeps a slot: how many
  scopes up it was found and its position there. The next lookup checks
  that position, and searches only when the name isn't there. A block
  binds its names in the same order each run, and only a loop variable or
  a pattern can hide an outer name, each in a scope of its own, so a slot
  almost always holds: the bytes loop misses five times in total, all on
  its first run. A slot is one atomic word, so concurrent bodies may share
  compiled code.
- **Unboxed ints.** A binding can hold an `int64` in place of a `Value`.
  Arithmetic assigned to a local, and a `for` over bytes, store ints this
  way, and arithmetic reads them back without boxing. The tree walk boxes
  one when it reads it, so it still sees every binding.
- **Interned names.** The lexer keeps one string per identifier, so a
  slot's name check is a pointer comparison.
- **Loop variables in place.** A `for` loop's variable keeps its place in
  the loop's scope, and each iteration only sets its value.
- **Direct operands.** Arithmetic whose right operand, or both operands,
  are a local or a constant reads them directly rather than through a
  closure each.

`TestCompiledBodiesMatchTheTreeWalk` now runs each program twice on one
interpreter, so the second run reads the slots the first set, and adds
cases for names found at different places by different callers, match
arms binding different names, unboxed ints used as values, scopes past
16 names, and faults from each arithmetic fast path.

Fastest of eight runs:

| Workload | Time | vs baseline | Allocations |
|---|---|---|---|
| bytes | 25.2 ms (42 MB/s, 24 ns a byte) | 8.8x | 5 |
| build | 2.30 ms | 1.8x | 50,109 |
| records | 4.73 ms | 1.7x | 59,750 |
| murmur2 | 139 ms | 2.6x | 1,598,049 |
| calls | 39.6 ms | 4.7x | 368,939 |
| parse | 22.7 ms (20 MB/s) | 2.1x | 260,003 |

Bytes is within 20% of §6's 20 ns. What's left is a closure call and a
slot check for each operand and statement; closing it would mean fusing
statements into larger closures, which isn't worth the code for this
benchmark alone. The next step is the tree walk's remaining hot paths in
protocol code: bytes literals and binary patterns.

### Step 7: binary expressions and patterns

The hot paths of protocol code, which step 4 left to the tree walk:
- **Whole bytes at once.** A field that is whole bytes on a byte
  boundary, as nearly every protocol field is, is written and read a byte
  at a time rather than a bit at a time. This speeds up the tree walk too.
  `TestAlignedFieldsMatchTheBitPath` checks every width and byte order
  against `encoding/binary` and against the bit path.
- **Pattern bindings in order.** A binary pattern collects its names in a
  short list rather than a map, and binds them in segment order, so a
  match allocates no map and the slots of the names it binds hold.
- **Compiled binary expressions.** An int segment's value is computed as
  an `int64`, and the buffer is sized for the fixed-width segments up
  front.
- **Methods on any value.** A built-in method whose target is an
  expression, such as `<<a: u32be>>.xor(...)` or `data.slice(0, 4)` used as
  a match subject, is compiled. Calls without named arguments skip
  `placeArgs`, which built a fault label for every call.
- **Reused nested scopes.** A scope keeps one child for its nested blocks
  (an `if` body, a loop's body, a `match`'s arms), since they run one at a
  time and nothing outlives one.

Fastest of eight runs:

| Workload | Time | vs baseline | Allocations |
|---|---|---|---|
| bytes | 25.9 ms (24 ns a byte) | 8.6x | 5 |
| build | 1.76 ms | 2.3x | 40,107 |
| records | 4.79 ms | 1.7x | 59,750 |
| murmur2 | 99.5 ms | 3.7x | 1,361,368 |
| calls | 40.8 ms | 4.6x | 368,938 |
| parse | 12.1 ms (38 MB/s) | 3.9x | 110,003 |

Parse allocates 3.3 MB per run, down from 14.8 MB: what's left is one
`Value` per matched `bytes` segment, which boxing a two-word struct costs.
murmur2's `match` runs in a new call each time, so it can't reuse a
scope; its cost is fiig spelling XOR as bytes. Dogfooded against
Postgres 17 through `examples/relay-notes`: schema setup, a transactional
batch with non-ASCII text, and typed reads.
