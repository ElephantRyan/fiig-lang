# ADR 0068: Random Operations, One Secure Source

Status: Proposed (2026-11-03). Drafted for one-decision-at-a-time review;
nothing here is decided.
Date: 2026-11-03
Related: ADR 0010 (one capability per gateway), ADR 0042 (`Random` is in
the closed primitive set), ADR 0048 (float), ADR 0051 (no warnings), ADR
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

**Randomness is an effect.** A rule that calls a random function gives a
different answer for the same input, which ADR 0029's isolation exists to
rule out. ADR 0042 already settled this by making `Random` a capability:
only a gateway touches it (B10).

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
Random.bytes(count: int) -> bytes      // count unpredictable bytes
Random.int(from: int, to: int) -> int  // uniform, from <= n < to
Random.float() -> float                // uniform, 0.0 <= f < 1.0
Random.uuid() -> string                // version 4, "8-4-4-4-12" lowercase hex
```

- **`int` is half-open**, as `slice(from, to)` is, so `Random.int(from: 0,
  to: xs.length())` is always a valid index. A die is `Random.int(from: 1,
  to: 7)`. Uniform with no modulo bias (rejection sampling, as Go's
  `crypto/rand.Int`). `to <= from` faults: an empty range has no value to
  give. Go's `rand.IntN(n)` and Kotlin's `nextInt(from, until)` are
  half-open; Python has both, a closed `randint(a, b)` and a half-open
  `randrange(a, b)`.
- **`float`** has 53 random bits, so every value is a multiple of 2⁻⁵³ and
  1.0 never appears.
- **`bytes`** faults below 0 or above 65,536, the same ceiling as
  `Socket.read` (ADR 0060 §3): a program asking for more is a bug, not a
  token.
- **`uuid`** is version 4 (122 random bits, RFC 9562), as text, since fiig
  has no newtypes (ADR 0055) and every boundary (JSON, SQL, URLs) carries a
  UUID as text. Version 7, which starts with a timestamp so that new IDs
  sort together in a database index, is open question 2: it needs the
  clock as well, and ADR 0010 gives a gateway one capability.

**None of them returns a `Result`.** The operating system's generator
doesn't fail in any way a program could handle; Go 1.24 made
`crypto/rand.Read` crash rather than return an error, for that reason. If
it ever did fail, that would be a fault.

### 3. Tokens are bytes, encoded: `bytes.toHex()`, `toBase64()`, `toBase64Url()`

```
gateway newSessionToken() -> string
    requires Random
{
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
their own, and the size and encoding are what a reader of the gateway
should see.

### 4. Only a gateway draws; rules take randomness as an argument

`Random` is a primitive capability, so B10 already applies: a rule,
conversion, activity, or workflow that calls `Random.int` directly is B10,
and a gateway that requires something else and calls it is P7. The B10
message names `Random` the way it names `Socket`.

A rule that needs a random choice takes the drawn value as a parameter:

```
rule pickServer(servers: List<string>, draw: int) -> string {
    return servers.at(draw)
}
```

and an activity draws it through a gateway and passes it in. The rule stays
deterministic, so its test passes a value instead of faking a generator.

### 5. Operations are typed in the checker

`Random`'s four signatures join a compiler-known table beside `Socket`'s
(ADR 0060 §3), so `Random.int("a", 2)` is A1 and `Random.choose(xs)` is K1
with the four names listed. `Clock` stays the one untyped primitive.

### 6. Providers: secure by default, seeded for tests

- **`RandomProvider`** in `compiler/runtime`, over `crypto/rand`. It needs
  no configuration, so `fiig run` and `fiig serve` register it whenever a
  program requires `Random`, without a capabilities-file entry. A
  capabilities file may still name a provider for it, which replaces the
  default.
- **A seeded provider**, `random-seeded` in the capabilities file
  (`{"Random": {"provider": "random-seeded", "seed": 42}}`), for tests and
  `fiig run` demos: the same seed gives the same draws, so a test of a
  gateway that makes tokens can assert the exact token. It is
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

**E. A closed range for `int` (Python's `randint`).** Reads naturally for
dice; half-open matches `slice` and indexes, and off-by-one in the closed
form (`randint(0, len(xs))`) is a classic IndexError.

## Consequences

- `compiler/checker`: a `randomOps` table beside `socketOps`; B10's
  message covers `Random`.
- `compiler/types`: `bytes` gains `toHex`, `toBase64`, `toBase64Url`.
- `compiler/runtime`: `RandomProvider` (crypto/rand), the seeded provider
  (`math/rand/v2`'s ChaCha8 with a fixed seed), the faults in §2.
- `tooling/runprogram`, `servecmd`: the default registration and the
  `random-seeded` factory; `serve` refuses the seeded one.
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
   rule assembles it from `Clock.now()` and `Random.bytes(10)`, which
   needs bit operations fiig lacks.
3. **`shuffle` and `choose`.** Fisher-Yates is a short loop over
   `Random.int` in a gateway, and the corpus may show it's enough. A
   generic capability operation (`Random.shuffle(xs: List<T>)`) would be
   the first generic one.
4. **Decoding** hex and base64 back to `bytes`: with item 4 (Basic auth).
