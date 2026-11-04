# ADR 0068: Random Operations, One Secure Source

Status: Accepted (2026-11-03, one-decision-at-a-time review) and
implemented (2026-11-04; see Implementation notes). §1, §3,
and §6 accepted as drafted; §2 revised to an inclusive `Random.int(min,
max)`, with named arguments on every builtin (§5); §4 revised: `Clock`
and `Random` are local effects, called directly from activities and
gateways, never from rules (amends ADR 0042).
Date: 2026-11-03
Related: ADR 0010 (one capability per gateway), ADR 0029 (rule
isolation), ADR 0042 (`Random` is in the closed primitive set; amended by
§4), ADR 0048 (float), ADR 0051 (no warnings), ADR
0055 (no newtypes), ADR 0058 (`bytes`), ADR 0060 (`Socket`: the pattern of
typed primitive operations and B10), ADR 0067 (`slice(from, to)` is
half-open).

## Context

ADR 0042 put `Random` in the closed set of primitive capabilities because
randomness can't be written in fiig: it has to come from the operating
system. It named the capability and gave it nothing else: no operations,
no provider, no types. A program today can't make a session token, an API
key, a password-reset link, a CSRF token, or a UUID, and it can't shuffle a
list or add jitter to a retry.

What a web service needs, by how often it comes up:

| Task | Needs |
|---|---|
| Session IDs, API keys, reset links, CSRF tokens | unguessable bytes, as text |
| Record IDs made by the service | a UUID |
| Jitter on a retry, picking a server, sampling a log line | an int in a range |
| A/B split, a probability | a float in [0, 1) |
| Shuffling a playlist or a quiz | a shuffle (built from ints) |

Two things make this more than a list of functions.

**The most common mistake with randomness is a security bug.** Most
languages ship two generators: a fast one that is predictable, and a
secure one under a different name. Tokens made with the fast one can be
predicted from a few earlier outputs, and this is common enough to have
its own CWE (338).

| Language | Default (what people reach for) | Secure | Default is secure? |
|---|---|---|---|
| Python | `random` (Mersenne Twister) | `secrets` | no |
| JavaScript | `Math.random()` | `crypto.getRandomValues` | no |
| Java | `java.util.Random` | `SecureRandom` | no |
| PHP | `mt_rand` | `random_bytes`, `random_int` | no |
| Go | `math/rand/v2` (ChaCha8, not promised secure) | `crypto/rand` | no |
| Rust (`rand`) | `thread_rng()` (ChaCha12) | the same | **yes** |
| Swift | `SystemRandomNumberGenerator` | the same | **yes** |
| Ruby | `rand` | `SecureRandom` | no |

**Randomness is an effect, but not a trip outside.** A rule that calls a
random function gives a different answer for the same input, which ADR
0029's isolation exists to rule out. But a gateway, in the
ports-and-adapters sense fiig took it from, is where the program reaches
another system: a database, a payment service, a file. Drawing a number
from the machine's own generator, or reading its own clock, isn't that.
ADR 0042 put `Random` and `Clock` in the primitive set beside `Socket`
and `FileSystem`, so B10 sends all four through a gateway; §4 separates
them.

## Decision

### 1. One source, and it is always secure

Every `Random` operation draws from the operating system's
cryptographically secure generator (Go's `crypto/rand`), as Rust's and
Swift's defaults do. There is no second, faster, predictable generator
behind another name, so no program can make a token from the wrong one.

**Cost: speed.** A secure draw costs more than a Mersenne Twister step.
For a web service, which draws a token per login, it doesn't show. For a
simulation drawing millions of floats it would, and a simulation also
wants a *seed*, so a run can be repeated. That is a different tool (a
seeded generator written in fiig, a pure value passed along) and belongs to
the numerics work, not to the capability; see open question 1.

### 2. Four operations

```
Random.bytes(count: int) -> bytes     // count unpredictable bytes
Random.int(min: int, max: int) -> int // uniform, min <= n <= max
Random.float() -> float               // uniform, 0.0 <= f < 1.0
Random.uuid() -> string               // version 4, "8-4-4-4-12" lowercase hex
```

- **`int` includes both ends, and says so with `min` and `max`.** A die is
  `Random.int(min: 1, max: 6)`. A closed range is how Python's
  `randint(1, 6)`, Kotlin's `(1..6).random()`, Rust's `gen_range(1..=6)`,
  Swift's `random(in: 1...6)`, and Ruby's `rand(1..6)` read; Go's
  `rand.IntN(n)` and Kotlin's `nextInt(from, until)` are half-open.
  `slice(from, to)` stays half-open, as slicing is in nearly every
  language, so the two use different names and no name has two meanings:
  `min` and `max` read as inclusive in plain English (C#'s
  `Next(minValue, maxValue)`, whose `maxValue` is excluded, is the
  well-known trap this avoids). Picking an element is
  `Random.int(min: 0, max: xs.length() - 1)`; on an empty list `max` is
  -1, and `max < min` faults: an empty range has no value to give.
  `min == max` gives that value. Uniform with no modulo bias (rejection
  sampling, as Go's `crypto/rand.Int`).
- **`float`** has 53 random bits, so every value is a multiple of 2⁻⁵³ and
  1.0 never appears. It stays below 1.0, as in every language here, so
  `Random.float() < 0.3` is exactly a 30% chance.
- **`bytes`** faults below 0 or above 65,536, the same ceiling as
  `Socket.read` (ADR 0060 §3): a program asking for more is a bug, not a
  token.
- **`uuid`** is version 4 (122 random bits, RFC 9562), as text, since fiig
  has no newtypes (ADR 0055) and every boundary (JSON, SQL, URLs) carries a
  UUID as text. Version 7, which starts with a timestamp so that new IDs
  sort together in a database index, is open question 2.

**None of them returns a `Result`.** The operating system's generator
doesn't fail in any way a program could handle; Go 1.24 made
`crypto/rand.Read` crash rather than return an error, for that reason. If
it ever did fail, that would be a fault.

### 3. Tokens are bytes, encoded: `bytes.toHex()`, `toBase64()`, `toBase64Url()`

```
activity newSessionToken() -> string {
    return Random.bytes(32).toBase64Url()   // 43 characters, 256 bits
}
```

A token is random bytes written as text, and the text form is the part
every boundary cares about: a cookie or URL needs base64url, a log or a
checksum is usually hex, and HTTP Basic auth (alpha item 4) is base64. So
the encodings join `bytes`'s method table (ADR 0049 §2.6), pure, and
usable on any bytes, not only random ones:

| Method | Alphabet | Padding | `<<0xfb, 0xff>>` gives |
|---|---|---|---|
| `toHex()` | `0-9a-f`, lowercase | none | `"fbff"` |
| `toBase64()` | RFC 4648 §4, `+` `/` | `=` | `"+/8="` |
| `toBase64Url()` | RFC 4648 §5, `-` `_` | none | `"-_8"` |

`toBase64Url` leaves out padding because `=` must be escaped in URLs and
cookies, and JWT, WebAuthn, and Python's `secrets.token_urlsafe` all write
it without. Decoding (`string` to `bytes`) waits for item 4, where Basic
auth is the first thing that needs it.

There is no `Random.token()`: it would join two jobs that each stand on
their own, and the size and encoding are what a reader of the call
should see.

### 4. `Clock` and `Random` are local effects: activities and gateways use them, rules take their values as arguments

The four primitives (ADR 0042) split in two:

| Kind | Primitives | What a call does | Who may call it |
|---|---|---|---|
| Leaves the program | `Socket`, `FileSystem` | reaches another system | a gateway that requires it (B10, P7) |
| Local effect | `Clock`, `Random` | reads the machine the program runs on | an activity or a gateway, directly |

A gateway is where the domain reaches another system, the port in ports
and adapters. Reading the program's own clock or drawing from its own
generator isn't a trip anywhere, so it doesn't take a gateway. It is still
an effect: the same call gives a different answer each time.

- **Rules, conversions, and formulas may not call `Clock` or `Random`**
  (B10, as for every primitive). They stay deterministic (ADR 0029): the
  same arguments give the same answer, so a test passes values and
  expects a result, with no fake clock or generator. A rule that needs
  the time or a draw takes it as a parameter.
- **Activities and gateways call them directly**, with no `requires` and
  no gateway in between. They are the program's shell, which coordinates
  effects.
- **A local effect doesn't count as a gateway's capability.** ADR 0010's
  one capability is the one other system a gateway reaches. A gateway
  that requires `Database` may also stamp the row with `Clock.now()` and
  `Random.uuid()`, and P7 doesn't apply to them.
- **Workflows may not call them directly** (B10). A workflow calls an
  activity that does. A workflow coordinates long-running steps, and if
  workflows are ever replayed after a crash, a clock read in workflow code
  would give a different answer on replay. Temporal forbids `time.Now()`
  and random numbers in workflow code for that reason.
- **`requires Clock` or `requires Random` is an error** (P8): there is
  nothing to require. ADR 0051 gives no warnings, and a clause that does
  nothing would mislead a reader into thinking the gateway reaches
  another system.

```
rule isExpired(token: Session, now: Instant) -> bool {
    return token.expiresAt < now
}

rule pickServer(servers: List<string>, draw: int) -> string {
    return servers.at(draw)
}

activity routeRequest(servers: List<string>) -> string {
    return pickServer(servers, Random.int(min: 0, max: servers.length() - 1))
}

gateway saveRating(input: NewRating) -> Result<Rating, RatingError>
    requires Database
{
    id = Random.uuid()
    createdAt = Clock.now()
    ...
}
```

Most time rules don't need the clock at all: `addBusinessDays(start,
days)` takes a date, and only a caller that means "from today" reads
`now`, once, and passes it in. Taking the time as an argument also makes
"is it overdue on February 29?" a one-line test.

This amends ADR 0042: `Clock` and `Random` stay in the closed primitive
set, since neither can be written in fiig and both need a provider (§6),
but they are no longer capabilities a declaration requires. The registry
still sees every call, so `fiig catalog` can list the declarations that
read the clock or draw.

### 5. Operations are typed in the checker, with named parameters

`Random`'s four signatures join a compiler-known table beside `Socket`'s
(ADR 0060 §3), so `Random.int("a", 2)` is A1 and `Random.choose(xs)` is K1
with the four names listed. `Clock` stays the one untyped primitive.

**Every builtin takes named arguments.** Until now a builtin method or
primitive operation took positional arguments only (`checkMethodArgs`
rejected a name), so `Random.int(min: 1, max: 6)` couldn't be written. The
builtin tables (ADR 0049 §2.6 methods, ADR 0060's `Socket`, and these)
gain parameter names, and a call to any builtin may name its arguments as
a call to a declaration does (ADR 0055: preferred, not required):

```
Random.int(min: 1, max: 6)        // named
Random.int(max: 6, min: 1)        // the names place the arguments
Random.int(1, 6)                  // positional, still legal
Random.int(low: 1, high: 6)       // error: Random.int's parameters are min and max
s.replaceAll(old: "-", new: "/")
Socket.connect(host: h, port: 5432)
```

The rules are a declaration call's: each name must be a parameter, none
twice, and positional arguments come before named ones. Names matter most
where two arguments share a type and could be swapped: `min` and `max`,
`slice`'s `from` and `to`, `replaceAll`'s `old` and `new`.

### 6. Providers: secure by default, seeded for tests

- **`RandomProvider`** in `compiler/runtime`, over `crypto/rand`. It needs
  no configuration, so `fiig run` and `fiig serve` register it whenever a
  program uses `Random`, without a capabilities-file entry. A
  capabilities file may still name a provider for it, which replaces the
  default.
- **A seeded provider**, `random-seeded` in the capabilities file
  (`{"Random": {"type": "random-seeded", "seed": 42}}`), for tests and
  `fiig run` demos: the same seed gives the same draws, so a test of an
  activity that makes tokens can assert the exact token. It is
  deterministic within one toolchain version, not across versions (Go
  makes the same promise for `math/rand/v2`'s seeded sources). `fiig
  serve` refuses it, so a seeded generator can't reach production by a
  copied config file.

## Alternatives considered

**A. Two generators, fast and secure (Python, Java, Go).** The fast one
would serve simulations. Rejected: it is the CWE-338 trap, and the cases
that need speed also need a seed, which a capability can't give without
becoming a value. A seeded generator for numerics is a separate,
pure tool (open question 1).

**B. A `Random.token(size)` returning text.** Shorter for the commonest
case. Rejected in §3: the encoding is needed for other bytes, and a reader
should see both the size and the encoding.

**C. Randomness as a pure function of a seed passed everywhere (Haskell's
`StdGen`, threading a generator through every call).** Deterministic by
construction. Rejected for the capability: a token from a seed is only as
unpredictable as the seed, so the seed would itself have to come from
`Random`. Kept as the shape of open question 1.

**D. A `Uuid` type.** Type-checks that an ID is a UUID. Rejected by ADR
0055's reasoning: no newtypes; validation belongs in a rule.

**E. A half-open `Random.int(from, to)`, matching `slice`.** The first
draft's choice: `Random.int(from: 0, to: xs.length())` is always a valid
index. Rejected in review: a die written `Random.int(from: 1, to: 7)`
misleads anyone who doesn't know the convention, and the languages that
avoid the confusion make the end visible (Kotlin's `until`, Rust's `..=`,
Swift's `...`). `min` and `max` make it visible by name. The closed form's
own off-by-one (`max: xs.length()`) faults on the last draw's index rather
than passing silently, since `at` faults out of range.

**F.`Random` and `Clock` as capabilities a gateway requires (ADR 0042
as written).** The first draft's §4. Consistent across all four primitives,
but it made a gateway out of every UUID and timestamp, and since ADR 0010
gives a gateway one capability, inserting a row with a fresh ID took two
gateways and an activity. Rejected in review: a gateway marks leaving the
domain, and these don't.

**G. Rules may read `Clock` and `Random` too.** No ceremony anywhere.
Rejected: rules would stop being repeatable, which is what makes them
testable by value, and the corpus's time rules (`addBusinessDays`,
expiry checks) take a date anyway.

## Consequences

- `compiler/checker`: a `randomOps` table beside `socketOps`. B10 lets
  activities and gateways call `Clock` and `Random`, and its message for
  a rule says to pass the value in; P7 skips local effects; new P8 for
  `requires Clock` or `requires Random`. The corpus has no `Clock` call
  or `requires Clock` to migrate. Every builtin table entry gains parameter
  names, and `checkMethodArgs` places named arguments by them;
  `compiler/runtime` reorders named arguments the same way before a
  builtin body runs.
- `compiler/types`: `bytes` gains `toHex`, `toBase64`, `toBase64Url`.
- `compiler/runtime`: `RandomProvider` (crypto/rand), the seeded provider
  (`math/rand/v2`'s ChaCha8 with a fixed seed), the faults in §2.
- `tooling/runprogram`, `servecmd`: the default registration and the
  `random-seeded` factory; `serve` refuses the seeded one. Registration
  follows use, not `requires`.
- ADR 0042: a note that `Clock` and `Random` are local effects (§4).
- `docs/known-limitations.md`, grammar, tour, bug-classes.
- Corpus: a session-token gateway and a retry with jitter, or a shuffle.

## Open questions

1. **A seeded generator for numerics and simulation.** A pure value
   (`Generator`, from a seed) with the same operations, passed along and
   returned with each draw, so a run repeats exactly. Needs either
   wrapping 64-bit arithmetic, which fiig's ints don't have (overflow
   faults, ADR 0063), or a builtin. Belongs to the numerics library.
2. **UUID version 7.** Time-ordered IDs index better in Postgres and
   SQLite. It needs both clock and randomness: either the `Random`
   provider reads the clock itself (a hidden effect, and it breaks the
   seeded provider's repeatability unless that also fixes the time) or a
   rule assembles it from a timestamp and `Random.bytes(10)` passed in,
   which needs bit operations fiig lacks.
3. **`shuffle` and `choose`.** Fisher-Yates is a short loop over
   `Random.int` in an activity, and the corpus may show it's enough. A
   generic capability operation (`Random.shuffle(xs: List<T>)`) would be
   the first generic one.
4. **Decoding** hex and base64 back to `bytes`: with item 4 (Basic auth).

## Implementation notes

**2026-11-04, §5 named arguments.**

- `types.Method`'s parameters are now `Param{Name, Type}`, so every
  builtin method has names: `slice(from, to)`, `replaceAll(old, new)`,
  `split(separator)`, `join(separator)`, `contains(text)` on a string and
  `contains(element)` on a list, `at(index)`, `put(key, value)`, and so
  on. `Socket`'s operations moved from the checker into the same package
  (`types.PrimitiveOps`), named `host`, `port`, `connection`,
  `serverName`, and `data`, so the runtime can place them too. `append`'s
  are `list` and `element` (`types.AppendParams`).
- One binder, `types.BindArgs`, is shared by builtins and `math` (whose
  `BindMathArgs` now calls it). The checker reports each way a call can
  fail to bind (a name that isn't a parameter, listing the parameters; one
  given twice; a positional argument after a named one; too many; a
  missing one, only when nothing else was wrong, since a wrong name is
  usually the missing one misspelled). The runtime evaluates arguments
  left to right as written, then places them by name.
- The two ordering rules §5 calls "a declaration call's" weren't enforced
  for declaration calls either: `add(a: 1, a: 2)` silently took the last
  value, and `add(a: 1, 2)` filled the next free parameter. Both are now
  errors there too. No corpus program did either.
- `Ok`, `Err`, and `copy` are unchanged: `copy`'s overrides were already
  named, and `Ok(x)`/`Err(e)` are constructors with one argument.
- Tests: `compiler/checker/builtin_args_test.go`,
  `compiler/runtime/builtin_args_test.go` (including `Socket.startTls`
  with its arguments named in the other order).

**2026-11-04, §4 and the `Random` table.**

- `compiler/capability` gains `IsLocalEffect` (`Clock`, `Random`). Every
  primitive call now goes through one checker path (`callPrimitive`):
  a local effect is legal in an activity or a gateway and B10 anywhere
  else, with one message for rules, conversions, and formulas (take the
  value as a parameter) and another for workflows (call an activity);
  `Socket` and `FileSystem` keep B10 and P7 as before.
- `types.PrimitiveOps("Random")` holds §2's four operations, so
  `Random.int("a", 2)` is A1, a wrong name lists `min` and `max`, and an
  unknown operation is K1 listing the four (`Socket`'s unknown operation is
  K1 now too, where it had no class). `Clock` stays untyped (§5).
- P8 is new: `requires Clock` or `requires Random`. The manifest
  capability allowlist (ADR 0035) is built from `requires` clauses, so a
  package that reads the clock or draws needs no manifest entry, which is
  §4's point: neither reaches another system. The registry still records
  every call in a card's `Uses`, so an activity that draws is not pure
  (ADR 0044) and `fiig catalog` shows the call.
- No corpus program called `Clock` or `Random`, or required either, so
  nothing migrated. `invalid-programs/random_error.fiig` attests B10
  (`rollDie`) and P8 (`newApiKey`).
- Tests: `compiler/checker/random_test.go`.

**2026-11-04, §3 encodings.**

- `bytes` gains `toHex`, `toBase64`, and `toBase64Url` in the shared table;
  the bodies are Go's `encoding/hex` and `encoding/base64` (`StdEncoding`
  and `RawURLEncoding`). Pure, so a rule may call them.
- Tests: `compiler/runtime/encoding_test.go` (RFC 4648 §10's vectors and
  §3's `<<0xfb, 0xff>>` table), `TestBytesEncodingsCheck`. Dogfooded with
  `fiig run`: a rule building a Basic auth header gives RFC 7617's own
  example, `QWxhZGRpbjpvcGVuIHNlc2FtZQ==`.

**2026-11-04, §6 providers.**

- `runtime.RandomProvider` draws every operation from one byte source:
  `crypto/rand.Reader`, or for `NewSeededRandomProvider` a ChaCha8
  (`math/rand/v2`) keyed by the seed. `int` is uniform by rejection: a
  draw in the last, partial copy of the range is redrawn, and a range as
  wide as `int` itself takes a draw as is. `float` is the top 53 bits of a
  draw over 2⁵³. `uuid` sets RFC 9562's version and variant bits on 16
  bytes. §2's faults: `max < min`, and `count` outside 0 to 65,536.
- The capabilities-file key is `type`, not the `provider` §6 first
  wrote, since every other entry uses `type` (ADR 0027):
  `{"Random": {"type": "random-seeded", "seed": 42}}`. The seed is a
  non-negative integer; anything else is a load error.
- `runprogram.RegisterDefaults` registers the secure `Random` in `fiig
  run` and `fiig serve` when the file has no entry. It also registers
  `Clock` (the existing `ClockProvider` over the wall clock): it too needs
  no configuration (§4), `docs/known-limitations.md` said it had a
  provider, and it had none: `Clock.now()` faulted in `fiig run` with "no
  provider registered".
- `fiig serve` refuses a seeded `Random` before it listens
  (`RefuseSeededRandom`), naming the fix.
- Tests: `compiler/runtime/random_test.go` (the four operations from an
  activity, a 6,000-roll spread, both ends of `int`'s range, faults, seeds
  repeating), `tooling/runprogram/random_test.go` (the factory, bad seeds,
  defaults, the refusal). Dogfooded: two `fiig run`s draw different values,
  two with seed 42 the same, and `fiig serve` with the seeded file exits
  with the refusal.

**2026-11-04, corpus.**

- `customer-service`'s `insertCustomer` drew its ID from
  `Database.nextId()`, which `libraries/sql` implements as a UUID made in
  Go: randomness hidden inside the Database capability. It now calls
  `Random.uuid()` beside `Database`, the case §4 was revised for. The
  runtime and adapter tests' fake databases lost their `nextId` and
  register a seeded `Random` instead. `libraries/sql` still provides
  `nextId` (ADR 0025); whether to drop it is left open.
- New: `activities/new_session_token.fiig` (§3's token),
  `rules/backoff_delay.fiig` (pure: the jitter is a parameter), and
  `activities/next_retry_delay.fiig` (draws the jitter and calls it).
  examples/README row 23. `fiig catalog` marks `backoffDelay` pure and
  `nextRetryDelay` not, with `uses: Random.int`.
- Dogfooded with `fiig run`: delays of 140, 807, and 10093 for attempts 0,
  3, and 20 (the cap), `backoffDelay(3, 7)` is 807 every time, and
  `newSessionToken` gives a 43-character token, the same one twice under
  seed 42.
