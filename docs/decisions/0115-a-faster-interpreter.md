# ADR 0115: A Faster Interpreter, Without a Build Step

Status: Proposed (2026-11-20)
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

To be filled in by each step of §7.
