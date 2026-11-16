# ADR 0075: Hashing, HMAC, and Password Hashes

Status: Accepted and implemented (2026-11-11; see Implementation notes)
Date: 2026-11-11
Related: ADR 0010 (effects), ADR 0042 (closed primitive set), ADR 0049
(prelude groups, `math`), ADR 0058 (`bytes`), ADR 0068 (`Random` as a
local effect, `toHex`/`toBase64`), ADR 0069 (`fromHex`/`fromBase64`,
cookies), ADR 0071 §4 (`Secret`, `.matches`), ADR 0073 (`json`), charter
principle 10.

## Context

A fiig program can't compute a hash. Three things the first application
needs are each one call away in Go, Java, Elixir, or Rust, and impossible
in fiig today:

- **Checking a webhook's signature.** Stripe, GitHub, Shopify, and Slack
  sign each delivery with HMAC-SHA-256 over the raw body, using a secret
  shared ahead of time. A receiver that doesn't check it accepts forged
  events. (Reading a JSON body raw is item 8b; this ADR is the hash.)
- **Signing a value the program hands out and gets back:** a session
  cookie, an unsubscribe link, a pagination cursor. The program appends an
  HMAC of the value and refuses one whose HMAC doesn't match.
- **Storing a key without storing the key.** An API key issued to a
  customer (`Random.bytes`, ADR 0068) is stored as its SHA-256, so a leaked
  table doesn't leak working keys; a presented key is hashed and looked up.

ADR 0071 §4 already planned for it: a `Secret` "may be passed to a rule,
since checking a signature is pure computation (alpha item 8a's
`hmacSha256(key: Secret, ...)`)".

A fourth need is different in kind. **A password** can't be stored as a
SHA-256: passwords are guessable, and SHA-256 is fast, so a leaked table
is cracked at billions of guesses a second. Passwords need a hash that is
deliberately slow and memory-hard, with a random salt per password. The
owner decided on 2026-11-07 that the alpha includes one if accounts store
passwords.

### Precedent

Verifying a GitHub webhook (`X-Hub-Signature-256: sha256=<hex>`):

```go
// Go: crypto/hmac + crypto/sha256; hmac.Equal compares in constant time.
mac := hmac.New(sha256.New, []byte(secret))
mac.Write(body)
want, err := hex.DecodeString(strings.TrimPrefix(header, "sha256="))
ok := err == nil && hmac.Equal(mac.Sum(nil), want)
```
```java
// Java: javax.crypto.Mac; MessageDigest.isEqual is constant time.
Mac mac = Mac.getInstance("HmacSHA256");
mac.init(new SecretKeySpec(secret.getBytes(UTF_8), "HmacSHA256"));
byte[] got = mac.doFinal(body);
boolean ok = MessageDigest.isEqual(got, HexFormat.of().parseHex(header.substring(7)));
```
```elixir
# Elixir: Erlang's :crypto, Plug.Crypto.secure_compare for the check.
got = :crypto.mac(:hmac, :sha256, secret, body) |> Base.encode16(case: :lower)
ok = Plug.Crypto.secure_compare(got, String.trim_leading(header, "sha256="))
```
```rust
// Rust (hmac + sha2 crates): verify_slice compares in constant time.
let mut mac = Hmac::<Sha256>::new_from_slice(secret.as_bytes())?;
mac.update(&body);
let ok = mac.verify_slice(&hex::decode(header.trim_start_matches("sha256="))?).is_ok();
```

All four take bytes and give bytes, encode separately, and give a
constant-time comparison beside the ordinary one, because `==` stops at
the first differing byte and leaks how much of a forged signature was
right.

Passwords: Go's `x/crypto` has `bcrypt`, `scrypt`, and `argon2` (the Go
team maintains it but keeps it out of the standard library). Java has
none built in (Spring Security's `Argon2PasswordEncoder` and
`BCryptPasswordEncoder`). Elixir uses `argon2_elixir` or `bcrypt_elixir`
through Comeonin's `hash_pwd_salt`/`verify_pass`. Rust uses the `argon2`
crate. OWASP's Password Storage Cheat Sheet ranks Argon2id first, at
19 MiB of memory, 2 passes, 1 lane. Every one of them stores the result
as one self-describing string (`$argon2id$v=19$m=19456,t=2,p=1$<salt>$<hash>`,
the PHC string format), so the parameters can be raised later and old
hashes still check.

## Decision

### 1. A prelude group, `crypto`

```fiig
rule signatureValid(secret: Secret, body: bytes, header: string) -> bool {
    match header.removePrefix("sha256=").fromHex() {
        None -> false
        sent -> crypto.equal(crypto.hmacSha256(key: secret, data: body), sent)
    }
}
```

- `crypto` is a prelude group like `math` and `json` (ADRs 0049, 0073):
  visible in every file without a `use`, so no declaration, alias,
  parameter, or binding may take the name.
- Its hash members are pure, so rules, conversions, and formulas may call
  them. `hashPassword` (§5) is the one exception.
- Named `crypto` after Go's and Erlang's packages, not `hash`, which
  would be wrong for §5 and taken from a likely local name.

### 2. SHA-256 and HMAC-SHA-256, bytes in, bytes out

- `crypto.sha256(data: bytes) -> bytes`, 32 bytes.
- `crypto.hmacSha256(key: Secret | bytes, data: bytes) -> bytes`, 32
  bytes (RFC 2104).
- **Bytes only, no `string`.** A string's bytes depend on its encoding
  (the owner's reason for `s.utf8().length()` over a `byteCount()`, ADR
  0067), and a signature is over exact bytes: `crypto.sha256(token.utf8())`
  says which.
- **The result is `bytes`.** `.toHex()`, `.toBase64()`, and
  `.toBase64Url()` (ADR 0068) write it, `string.fromHex()` and the base64
  readers (ADR 0069) read a sent one; providers disagree on which they
  use, so no encoding is built in.
- **A `Secret` key is its value's UTF-8**, which is how every provider
  above documents its secret. The `Secret` never becomes a `string`
  outside a gateway (ADR 0071 §4), and an HMAC doesn't reveal its key.
  A `bytes` key is for keys the program derives or generates
  (`Random.bytes(count: 32)`, or AWS Signature V4's chained HMACs).
- **Nothing else for now:** no SHA-1, SHA-512, or SHA-3 (open question
  1). Adding one is a member, not a design.

### 3. A constant-time comparison: `crypto.equal`

- `crypto.equal(a: bytes, b: bytes) -> bool` takes time that depends only
  on the lengths, like Go's `hmac.Equal` and Java's `MessageDigest.isEqual`.
  Like `Secret.matches` (ADR 0071 §4), different lengths answer `false`
  at once, revealing the length and nothing else.
- `==` on `bytes` stays as it is. The checker can't tell a signature from
  any other bytes, so using `crypto.equal` for one is the program's
  responsibility; the docs and §1's example say so. A typed digest
  (open question 2) would let the checker refuse `==`.

### 4. A key the program issues is stored as its SHA-256

No new feature: §2 is enough, and the docs show it.

```fiig
activity issueApiKey(customer: CustomerId) -> string {
    key = Random.bytes(count: 32).toBase64Url()
    ApiKeys.insert(customer, crypto.sha256(key.utf8()).toHex())
    return key
}
```

A random 256-bit key can't be guessed, so a fast hash is the right one
here; §5's slow hash is for secrets a person chose.

### 5. Passwords: Argon2id, as a PHC string

```fiig
activity register(email: string, password: string) -> Result<None, SignUpError> {
    Accounts.insert(email, crypto.hashPassword(password))
    ...
}

activity logIn(email: string, password: string) -> Result<Session, LogInError> {
    stored = Accounts.passwordHash(email)?
    if not crypto.passwordMatches(password, stored) {
        return Err(LogInError.Refused)
    }
    ...
}
```

- `crypto.hashPassword(password: string) -> string` hashes with Argon2id
  (RFC 9106) at OWASP's parameters, 19 MiB, 2 passes, 1 lane, with a
  16-byte salt, and returns the PHC string.
- **The salt comes from the program's `Random` provider** (ADR 0068), so
  `hashPassword` is a local effect like `Random`: legal in activities and
  gateways, B10 elsewhere, and deterministic under the seeded test
  provider. Go's `bcrypt`, Elixir's `hash_pwd_salt`, and Spring all make
  the salt themselves; Rust's `argon2` makes the caller pass one, which
  lets a program pass the same one every time.
- `crypto.passwordMatches(password: string, stored: string) -> bool` reads
  the algorithm and parameters from `stored`, so hashes made before a
  parameter change still check, and compares in constant time. It is
  pure.
- **A `stored` that isn't an Argon2id PHC string is a fault**, not
  `false`: the program wrote it, so a malformed one is a bug or corrupted
  data, and answering "wrong password" would hide it. A parameter set
  above a ceiling (1 GiB, 10 passes, 16 lanes) is a fault too, so a
  tampered row can't make a login run for minutes.
- **The implementation is `golang.org/x/crypto/argon2`**, the compiler
  module's first dependency outside the standard library. The Go team
  maintains it; Argon2 isn't in the standard library, and writing it by
  hand is the riskier choice. Alternative B is the one with no
  dependency.
- Each call costs about 19 MiB and tens of milliseconds, by design. A
  service that hashes on an unauthenticated route should limit how many
  run at once. `fiig serve` has no per-route limit yet (open question
  5), so the docs will say to rate-limit such routes in front of it.

### 6. Scope

In: §1–§5. Out, each until a caller needs it: other hashes and HMACs
(open question 1); encryption (AES-GCM); public-key signatures (Ed25519,
RSA, so no RS256 JWT checks); key derivation (HKDF); JWT itself; a `Secret`
read as base64 or hex (open question 3); rehashing on login when the
parameters change (open question 4).

## Alternatives considered

**A. Methods on `bytes` (`body.sha256()`, `body.hmacSha256(key:)`).** ADR
0068 made encodings methods. But a keyed operation reads backwards as a
method on its data, `equal` has no subject, and `hashPassword` doesn't
belong on `string`. One group keeps them findable together.

**B. PBKDF2-HMAC-SHA-256 (Go 1.24's `crypto/pbkdf2`) instead of Argon2id.**
In the standard library, so no dependency, and FIPS-approved. But it
isn't memory-hard, so a GPU tries guesses far more cheaply, and OWASP
needs 600,000 iterations to rank it last of four. Rejected unless the
dependency is unacceptable.

**C. bcrypt or scrypt.** bcrypt truncates passwords at 72 bytes and isn't
memory-hard; scrypt is OWASP's second choice and needs the same
dependency as Argon2id. Neither gains anything over it.

**D. `string` data.** Convenient for `crypto.sha256(token)`, but hides an
encoding choice inside a security check. Rejected (§2).

**E. A streaming hasher (`update`, `finish`).** Go's and Rust's shape, for
data too large to hold. Fiig reads a whole body into `bytes` anyway, and
`<<a: bytes, b: bytes>>` concatenates. Rejected until a caller streams.

**F. A salt parameter on `hashPassword`.** Keeps `crypto` wholly pure, but
lets a program reuse a salt, which is the mistake salts exist to prevent.
Rejected (§5).

## Consequences

- Webhook signatures, signed values, and stored API keys work with
  existing `bytes` encodings; 8b then lets a JSON webhook be checked.
- Passwords can be stored safely, and the format survives a parameter
  change.
- `crypto` joins the reserved prelude names, so a program with a binding
  or declaration named `crypto` must rename it.
- The compiler module gains `golang.org/x/crypto` (§5).
- `crypto.equal` is advice the checker can't enforce (§3).

## Open questions

1. SHA-1 and SHA-512 HMACs, for providers that still sign with them
   (Twilio: SHA-1) and for HS512 tokens.
2. A typed digest that refuses `==`, so §3 becomes a check.
3. Reading a `Secret` as base64 or hex outside a gateway (Standard
   Webhooks' `whsec_<base64>` secrets), without making it a `string`.
4. `crypto.passwordNeedsRehash(stored)`, so a login can upgrade a hash made
   at older parameters.
5. A limit on how many requests a route serves at once, so a burst of
   log-ins can't take 19 MiB each without bound.

## Implementation notes (2026-11-11)

- **Accepted as written**, with the `golang.org/x/crypto` dependency (§5).
  It pulls in `golang.org/x/sys` indirectly; `cmd/fiig` and
  `libraries/sql` build with `GOWORK=off` too.
- **Where it is.** `compiler/types/crypto.go` is the table the checker
  types calls against (`checkMethodArgs`, so named arguments work as for
  every builtin) and `compiler/runtime/crypto.go` the bodies;
  `TestEveryCryptoFormulaIsImplemented` keeps them in step. `crypto` joined
  `IsPreludeGroup` (G4) and `PreludeName` (K2). The catalog has a card per
  member, `Pure` but for `hashPassword`.
- **`hashPassword`'s salt** is `Random.bytes(count: 16)` from the
  program's provider, so a test's seeded provider hashes deterministically
  (`TestPasswordHash`). Its caller check is B10, worded like `Random`'s.
- **`passwordMatches`'s faults** say what is wrong with the stored hash
  and never quote it or the password. Beyond §5's ceilings it requires a
  salt of 8 to 64 bytes and a hash of 16 to 64, and refuses a parameter
  given twice or one it doesn't know.
- **Tested** against FIPS 180-2's and RFC 4231's vectors and GitHub's
  documented webhook example (§1's rule), with every refusal in the
  checker and the runtime.
- **Dogfooded** in `examples/accounts`, which stored keys as given "because
  fiig has no hash functions yet": keys are now Argon2id PHC strings,
  session rows hold the SHA-256 of the cookie's token, and an unknown name
  is checked against a decoy hash. Under `fiig serve` with SQLite, a right
  key logged in, the cookie read `/me`, logout revoked it, and a wrong key
  and an unknown name each answered 401 in the same 14 ms.
- Open question 5 (a limit on concurrent requests) is taken up by ADR
  0076 at the owner's request.
