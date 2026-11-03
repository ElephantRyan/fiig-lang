# ADR 0059: `while` Loops

Status: Accepted (2026-10-25, on the author's approval: `while` is legal in
every kind; see "Decisions taken at review").
Date: 2026-10-25
Related: ADR 0042 (the native floor is the socket), ADR 0053 (`for`, typed
reassignment, `break`/`continue`/labels), ADR 0054 (call-depth limit), ADR
0058 (bytes and binary patterns).
Milestone 7: prerequisite for "One reference wire protocol written in fiig"
and "Postgres wire protocol in fiig".

## Context

ADR 0042 says incremental framing (read a length, then read that many bytes)
"is a loop inside that single gateway body rather than gateway recursion".
Fiig has no such loop. Every iteration form it has is bounded by something
already in hand:

- `for x in xs` runs once per element of a `List<T>` or `bytes` (ADR 0053,
  ADR 0058 §1) that exists before the loop starts.
- `concurrent(x in xs)` likewise.
- Recursion is capped at a call depth of 1000 (ADR 0054), and a gateway may
  not call itself anyway.

Reading a Postgres message off a socket needs a loop whose iteration count
is not known in advance: read, try to frame, and if the buffer is short,
read again. Without one, the gateway can't be written at all:

```
gateway readMessage(connection: Connection, pending: bytes) -> Result<Frame, WireError>
    requires Socket
{
    match readFrame(pending) {
        found -> Ok(found)
        None -> {
            chunk = Socket.read(connection)
            // ...and if this still isn't a whole frame? There is no way
            // back to the top. Nesting a second match handles two reads,
            // a third handles three; a 64 KB DataRow can take dozens.
        }
    }
}
```

The same shape recurs through the rest of the arc: an HTTP/1.1 chunked body
(read chunks until the zero-length one), Postgres's simple query protocol
(read `DataRow` messages until `CommandComplete`, then until
`ReadyForQuery`), and any connection retry or drain loop.

### Pure parsing does not need this

The investigation that led here first claimed a JSON scanner had the same
problem. That was overstated. A scanner that recurses once per token does
hit the depth cap. But a scanner that consumes at least one byte per step
can use the input's own length as its bound, and this checks and runs
today:

```
conversion skipSpaces(s: string) -> int {
    rest: bytes = s.utf8()
    for b in s.utf8() {
        match rest {
            <<32: u8, tail: bytes>> -> {
                rest = tail
            }
            _ -> {
                break
            }
        }
    }
    return rest.length()
}
```

Nesting (array inside object inside array) is ordinary conversion recursion,
one frame per nesting level, so the depth cap limits nesting to roughly
500 levels, not document size. serde_json's default limit is 128, so this
is not a practical constraint.

So the effectful read loop is the case that is *impossible*. The pure
scanner is merely *awkward*: `for b in input` where `b` is never read, and
which reads as "once per byte" while the body may consume many. Whether
that awkwardness is worth admitting an unbounded loop into pure kinds is
the main question this ADR has to answer (§3).

### Precedent

| Language | Conditional | Infinite | Notes |
| --- | --- | --- | --- |
| C, Java, C#, JS | `while (c)` | `while (true)`, `for (;;)` | plus `do ... while` |
| Python | `while c:` | `while True:` | no do-while |
| Kotlin, Swift | `while c` | `while true` | plus `do-while` / `repeat-while` |
| Go | `for c { }` | `for { }` | one keyword, three forms; for-each is `for x := range xs` |
| Rust | `while c { }` | `loop { }` | `loop` is an expression: `break value` |
| Zig | `while (c) : (step)` | `while (true)` | continue-expression on the loop |
| Erlang, Elixir | none | none | tail recursion; Postgrex's receive loop is a tail-recursive function |
| Starlark | none | none | deliberately: no `while`, no recursion, so every build file terminates |

Two of these need a closer look because they argue against adding anything.

**Erlang/Elixir.** ADR 0042 cites Postgrex, Redix and Mint as proof that a
database ecosystem can live above a socket. That proof rests on unbounded
tail recursion, which the BEAM guarantees runs in constant stack. Fiig has
neither half: recursion is capped at 1000 frames, and ADR 0054 forbids a
gateway (or a rule) calling itself. Adopting the Erlang answer would mean
tail-call elimination in the interpreter *and* reopening the composition
table for self-calls, which is two decisions to avoid one keyword. ADR 0053
also already chose the Java shape (for-each plus reassignable locals) over
the accumulator-threading style that recursion-as-iteration implies.

**Starlark.** Starlark gives up `while` to guarantee that evaluation
terminates. Fiig today has an accidental, nominal version of that property:
every list is finite and recursion is capped, so every evaluation ends.
"Ends" is doing a lot of work, though. Branching recursion under a
1000-frame cap can take 2^1000 steps, and Starlark only makes its guarantee
real by banning recursion too, which fiig does not and should not (ADR 0042
relies on recursive descent). The property is not worth preserving for its
own sake. Whether it is worth preserving *in pure kinds* as a registry
fact is §3.

## Decision

### 1. `while condition block`

```
while-statement ::= [ identifier ':' ] 'while' expression block
```

- The condition is evaluated before every iteration, including the first,
  and must be a `bool`. A non-`bool` condition is an A1 type mismatch,
  exactly as for `if`.
- `while` is a statement, like `for`. It produces no value.
- `while` becomes a reserved keyword. No identifier in the corpus uses it.

The read loop from the Context, written with it:

```
gateway readMessage(connection: Connection, pending: bytes) -> Result<Frame, WireError>
    requires Socket
{
    buffer: bytes = pending
    while true {
        match readFrame(buffer) {
            found -> {
                return Ok(found)
            }
            None -> {
                chunk = Socket.read(connection)?
                buffer = <<buffer: bytes, chunk: bytes>>
            }
        }
    }
}
```

(Updated 2026-10-26: ADR 0060 defined `Connection` and `Socket.read`. A
peer hanging up is `Err(SocketError.Closed)`, which `?` propagates, so the
draft's `chunk.length() == 0` check is gone. This gateway is now
`examples/postgres-wire/gateways/read_message.fiig`, returning
`Result<Frame, SocketError>` rather than the placeholder `WireError`.)

And the scanner, which now says what it means:

```
rest: bytes = s.utf8()
while rest.length() > 0 {
    match rest {
        <<32: u8, tail: bytes>> -> {
            rest = tail
        }
        _ -> {
            break
        }
    }
}
```

### 2. `break`, `continue` and labels apply to every loop

ADR 0053 §4 defines `break`, `continue`, labels, and the rule that a jump
may not cross a `concurrent` body, all in terms of "a `for`". Each of those
rules now reads "a loop", meaning a `for` or a `while`. A label names either
kind, and `break outer` from inside a `while` nested in a `for` works as
expected. `continue` in a `while` goes back to evaluating the condition.

### 3. Legal in any block, including pure kinds

`while` is legal wherever `for` is: `rule`, `conversion`, `formula`,
`gateway`, `activity`, `workflow`. The recommendation is not to restrict it
to effectful kinds, for three reasons:

- The pure case is real (every protocol parser in this milestone is a
  conversion) and the `for b in input` workaround obscures intent in
  exactly the code the milestone is about to write the most of.
- A restriction would buy a termination property pure kinds don't
  actually have (the Starlark point above). A rule may call a conversion,
  and a conversion may recurse exponentially.
- It would make `while` the first statement whose legality depends on the
  declaration kind, splitting a rule that is currently uniform: "a
  statement is legal in any block; what a *call* may reach depends on the
  kind."

The strongest case for the restriction is the registry: "this rule always
finishes" would be a derivable, displayable catalog fact, the way purity is
derived today. It was weighed at review and declined (see "Decisions taken
at review").

### 4. What bounds a runaway loop

Each `while` iteration checks for cancellation before evaluating the
condition, as `for` already does per element (`checkCancelled` in
`compiler/runtime/interp.go`). A loop that never ends therefore stops at the
same deadline that stops a socket read that never returns, and with the same
fault. There is no iteration cap. A cap in the spirit of the call-depth
limit would break legitimate loops (reading a 10 MB response), and the
depth limit exists to bound the Go stack, not to bound time.

### 5. No `loop`, no `do ... while`

`while true` is the infinite form. Rust needs `loop` separately because it
is an expression (`let x = loop { break 5; }`) and because its
definite-initialization analysis has to know the body runs. Fiig's loops are
statements and fiig has no definite-initialization or missing-return
analysis, so neither reason applies. `do ... while` is rare in every
language that has it and is `while true` with a `break` at the bottom.

## Alternatives considered

**A. Go's single keyword: `for condition { }` and `for { }`.** One keyword
for three forms, and ADR 0053's `for`-scoped rules would need no
rewording. Rejected for one concrete cost: `for x in xs {` and a condition
`for x in xs {` are the same tokens if `in` ever becomes a membership
operator. Go avoids this because its for-each uses `range`, not `in`. Fiig
would be foreclosing `x in xs` as an expression permanently to save one
keyword. `while` is also the spelling in every other language in the table,
which matters for a language whose code is meant to be written by LLMs
trained on all of them.

**B. Rust's `while` plus `loop`.** See §5. A second keyword for a form
`while true` already covers.

**C. Tail-call elimination and self-recursive gateways (Erlang).** See
"Precedent". It reverses ADR 0054's self-call rule (confirmed at review
2026-10-19) and the existing ban on gateway-to-gateway calls, adds an interpreter guarantee that is
easy to break silently (a call that looks like a tail call but isn't), and
still leaves fiig with two iteration styles.

**D. A bounded loop with explicit fuel: `for attempt in range(0, maxReads)`.**
Keeps every evaluation finite and is honest about it. Rejected: there is no
good value for `maxReads`. Any number is either too small for a large result
set or so large it bounds nothing, and the real bound is already the
deadline (§4).

**E. Restrict `while` to effectful kinds.** Rejected at review; see §3.

## Decisions taken at review

- `while` is legal in every declaration kind, pure ones included
  (Alternative E rejected). The termination guarantee a restriction would
  buy is nominal while recursion is unbounded in time, and every parser in
  this milestone would otherwise carry a `for b in input` workaround.

## Open questions

1. **Bytes accumulation is quadratic.** `buffer = <<buffer: bytes, chunk:
   bytes>>` copies on every read. That is not this ADR's problem, but this
   ADR is what makes the pattern common. ADR 0057's claim-the-slot trick for
   `append` likely applies to bytes concatenation in the same position. It
   belongs to the interpreter-performance deliverable.

## Relation to ADR 0042

ADR 0042's Consequences say incremental framing is "a loop inside that single
gateway body" and that "iteration, not a matrix change, is what unblocks
streaming protocols". The second half is right. The first half assumed a loop
the language didn't have. This ADR supplies it, with no change to the
composition table.

## Consequences

- Grammar: `while-statement` added to `statement`; the `break`/`continue`
  wording in `grammar.md` §5 changes from "a `for`" to "a loop".
- Lexer: `while` reserved (`KwWhile`).
- Parser: a `WhileStmt` with an optional label. `name: while` is
  distinguished from a typed binding `name: Type = expr` the same way
  `name: for` is, by the keyword after the colon.
- Formatter: prints `while condition {`.
- Checker: condition checked against `bool` (A1); the label joins
  `c.loops`, so L1/L2/L3 apply unchanged, with messages reworded from "for
  loop" to "loop".
- Runtime: `evalWhile` beside `evalFor`, sharing its `loopJump` handling,
  with `checkCancelled` per iteration.
- Corpus: an attested example before acceptance, ideally the gateway read
  loop from §1 once `Socket` has operations, and a `while`-based scanner in
  the JSON library.
- No new bug class.

## Implementation notes (2026-10-25)

Implemented as above. `KwWhile`, `syntax.WhileStmt`, `parseWhileStmt`, the
formatter case, `checkWhileStmt` (sharing a new `checkLoopBody` with `for`,
which now reports L1/L2/L3 as "loop" rather than "for loop"), and
`evalWhile` (sharing a new `loopExit` with `evalFor`). `syntax/callnames.go`'s
two statement walkers also visit the condition and body, so calls inside a
`while` reach the registry's dependency graph.

The corpus example is `examples/postgres-wire/conversions/read_frames.fiig`:
a conversion that splits one read into every complete backend message plus
the unfinished tail, run by `compiler/runtime`'s
`TestCorpusPostgresReadFrames`. It is the pure half of §1's gateway read
loop; the gateway itself waits on `Socket` operations.
