# ADR 0146: Public-Key Signatures in `crypto`, and a `fiig.jwt` Library

Status: Accepted (2026-11-25, by the owner), as written: `exp` required
with no switch, signing in this ADR, and keys kept by a session that
connects only to write (§5a). Implemented the same day (see Implementation
notes).
Date: 2026-11-23 (revised 2026-11-25: §5 keys are kept by a session that
connects only to write, which amends ADR 0117 §2)
Related: ADR 0042 (the socket floor), ADR 0049 (prelude groups), ADR 0071
§4 (`Secret`), ADR 0073 (`json`), ADR 0074 (the HTTP client), ADR 0075
(the `crypto` group; this answers §6's "public-key signatures" and "JWT
itself"), ADR 0078 (base64url), ADR 0080 (an enum's text form), ADR
0114/0117 (sessions), ADR 0137/0141/0143/0144 (wires, bearer scheme),
charter principle 10, improvement idea D1.

## Context

A fiig program can't check a token signed with a public key. ADR 0075
gave `crypto` HMACs, so an HS256 token signed with a shared secret can be
checked by hand, but almost no one outside the program shares that
secret. The tokens services actually receive are signed with a private
key the receiver never sees:

- **Sign-in through a provider.** Auth0, Okta, Entra ID, Google,
  Keycloak and Authentik sign OpenID Connect id tokens and access tokens
  with RS256 or ES256, and publish the public keys as a JSON Web Key Set
  (JWKS) at a URL their discovery document names. An API behind any of
  them has to verify each request's bearer token against those keys.
  Today a fiig API has two choices: call the provider's userinfo endpoint
  on every request (slow, and rate-limited), or trust the token unchecked.
- **Service-to-service tokens.** Kubernetes service account tokens, AWS
  and GCP workload identity, and GitHub Actions' OIDC token are all
  RS256 JWTs checked against a published JWKS.
- **Signing to call someone else.** A GitHub App authenticates with an
  RS256 JWT it signs with its private key, a Google service account the
  same, and Apple Push Notifications and Sign in with Apple with ES256.
  ADR 0120 already ran into this: an SMTP login through a Google service
  account needs an RS256 signature fiig can't make.

`known-limitations.md` lists it: no public-key signatures, so no RS256
JWTs. Hearsay signs in through Authentik's OIDC code exchange (GAPS G17
and nearby). Verifying the id token itself, instead of trusting the
channel it came over, is the next step for an app like that.

Two things are needed: the signature algorithms, and the JWT rules on
top of them. The second is where real systems get hurt.

### How JWT verification goes wrong

RFC 8725 (JWT Best Current Practices) exists because libraries kept
making the same mistakes:

- **`alg: none`.** The token's header names its algorithm. A library
  that believes the header accepts an unsigned token.
- **Algorithm confusion.** A library that picks the algorithm from the
  header and the key from configuration can be handed `alg: HS256` with
  the RSA *public* key as the HMAC secret. The attacker knows the public
  key, so they can sign anything. This was CVE-2015-9235 in
  node-jsonwebtoken, and the same bug was found in libraries for most
  languages.
- **Skipped claims.** A valid signature from the right issuer, for a
  different audience: a token a provider issued to another app is
  accepted by this one. Or an expired one, because checking `exp` was a
  separate call nobody made.
- **Weak signatures accepted.** Java's ECDSA accepted r = s = 0 for every
  message (CVE-2022-21449, "psychic signatures").

Each of these comes from the verifier trusting what the token says
about how to verify it. Charter principle 10 says the same: fiig is
strict about what it reads.

### Precedent

| | Algorithm chosen by | Claims checked | Remote JWKS |
| --- | --- | --- | --- |
| **Go** `golang-jwt/jwt` v5 | key function returns the key; `WithValidMethods([...])` restricts the header's `alg`, and is optional | `exp`/`nbf` by default; `iss`/`aud` by option | separate library (`keyfunc`), background refresh |
| **Java** Nimbus JOSE+JWT, Spring `NimbusJwtDecoder` | `JWSKeySelector` built for an expected algorithm set | `exp`/`nbf` by default; issuer by `withIssuerLocation` | `RemoteJWKSet` with a cache; Spring discovers it from the issuer |
| **Node** `jose` | `algorithms` option on `jwtVerify`, else any the key fits | `exp`/`nbf` always; `issuer`/`audience` options | `createRemoteJWKSet`: 10-minute max age, re-fetch on an unknown `kid` at most every 30 s |
| **Rust** `jsonwebtoken` | `Validation::new(Algorithm::RS256)`; the header's `alg` must be in the list | `exp` required by default; `iss`/`aud` by setting | none; the program fetches and caches |
| **Elixir** Joken + `joken_jwks` | the signer configured for the key | by the token config's validators | a polling strategy process |

Every library now has the shape RFC 8725 asks for: **the verifier names
the algorithms it accepts**, and the header can only choose among them.
The newer ones (`jose`, `jsonwebtoken`) make the list required, or check
`exp` without being asked. The older ones made each check opt-in, and
those are the ones the CVEs are against.

For keys, the common shape is `jose`'s: a cache per JWKS URL, a maximum
age, and a re-fetch when a token names a key id the cache doesn't have
(that is how a provider's key rotation reaches a verifier), with a
cooldown so a stream of tokens with made-up `kid`s can't make the
verifier hammer the provider.

## Decision

Two layers, following ADR 0116's split: the signature algorithms are
prelude `crypto` members in Go, and JWT is a library written in fiig
over them.

### 1. `crypto.verify`: public-key signatures, verify only from public keys

```fiig
crypto.verify(key: PublicKey, algorithm: SignatureAlgorithm, data: bytes, signature: bytes) -> bool
```

`SignatureAlgorithm` and `PublicKey` are prelude types, like
`DecompressError` (ADR 0116):

```fiig
enum SignatureAlgorithm {
    Ed25519,
    EcdsaP256Sha256,   // JOSE ES256
    EcdsaP384Sha384,   // ES384
    RsaPkcs1Sha256,    // RS256
    RsaPkcs1Sha384,    // RS384
    RsaPkcs1Sha512,    // RS512
    RsaPssSha256,      // PS256
    RsaPssSha384,      // PS384
    RsaPssSha512       // PS512
}

enum PublicKey {
    Rsa(modulus: bytes, exponent: bytes),
    EcP256(x: bytes, y: bytes),
    EcP384(x: bytes, y: bytes),
    Ed25519(key: bytes)
}
```

- **The algorithm is an argument, never read from the data.** There is
  no `none` variant and no HMAC variant: HMACs stay `crypto.hmacSha256`
  and friends, which take a `Secret | bytes` key. A `PublicKey` can't be
  passed where an HMAC key goes and a `Secret` can't be passed where a
  public key goes, so algorithm confusion can't be written, not merely
  isn't written.
- **`PublicKey` is a plain value**, its fields JWK's own decomposition
  (RFC 7518 §6: `n`/`e`, `x`/`y`, RFC 8037's `x`). It can be built from a
  JWK, compared, kept in a session's state as JSON, and written in a
  test. It isn't checked when built. `verify` checks it every time.
- **`false` for anything that isn't a valid signature by that key under
  that algorithm**: a wrong signature, a signature of the wrong length,
  an algorithm that doesn't fit the key's type (`RsaPkcs1Sha256` with an
  `EcP256` key), a point not on the curve, an RSA modulus under 2048
  bits or an exponent that isn't odd and between 3 and 2^31 (as Go's
  `crypto/rsa` limits it). The key usually comes from a third party, so
  a bad one is a value, not a fault (unlike ADR 0075 §5's
  `passwordMatches`, whose stored hash is the program's own).
  `crypto.checkPublicKey` (§2) says *why* a key is unusable, for a
  program that needs to report it.
- **ECDSA signatures are the fixed-length `r || s` form**, 64 bytes for
  P-256 and 96 for P-384, which is what JOSE (RFC 7518 §3.4) and
  COSE use. The DER form (TLS, X.509, WebAuthn's attestation) is open
  question 2. r or s of zero, or not below the curve order, is `false`;
  Go's `crypto/ecdsa` already refuses them, which is the check
  CVE-2022-21449 was missing.
- **PSS uses a salt as long as the hash**, RFC 7518 §3.5's rule.
- **It is pure**, like every `crypto` member but `hashPassword`, so a
  rule can verify. All of it is Go's standard library (`crypto/rsa`,
  `crypto/ecdsa`, `crypto/ed25519`): no new dependency.
- **P-521 (ES512) and secp256k1 (ES256K) are out.** ES512 is rare and
  secp256k1 is a blockchain curve with no standard-library support.
  Each is a variant, not a design, if a caller appears.

### 2. Reading public keys

```fiig
crypto.checkPublicKey(key: PublicKey) -> Result<None, KeyError>
crypto.publicKeyFromPem(pem: string) -> Result<PublicKey, KeyError>

enum KeyError {
    Malformed(reason: string),
    Unsupported(reason: string)    // an RSA key under 2048 bits, a curve not in §1
}
```

- `checkPublicKey` applies `verify`'s key checks and says which failed.
  `fiig.jwt` uses it to skip a JWKS key it can't use (§5) and to say why.
- `publicKeyFromPem` reads a `-----BEGIN PUBLIC KEY-----` block (SPKI,
  RFC 7468), the form a provider gives when it hands out one key instead
  of a JWKS, and `-----BEGIN RSA PUBLIC KEY-----` (PKCS #1). It is the
  one reader in Go because SPKI is DER, and an ASN.1 reader in fiig to
  read three key types isn't worth its size. The `PublicKey` it returns
  has passed `checkPublicKey`.

### 3. `crypto.sign`, from a private key the program holds as a `Secret`

```fiig
crypto.sign(key: Secret, algorithm: SignatureAlgorithm, data: bytes) -> bytes
```

- **The private key is a `Secret` holding PEM**, as GitHub, Google and
  Apple hand it out: PKCS #8 (`BEGIN PRIVATE KEY`, Google's and Apple's
  `.p8`), PKCS #1 (`BEGIN RSA PRIVATE KEY`, GitHub Apps'), and SEC 1
  (`BEGIN EC PRIVATE KEY`). It reaches the program through a wire (ADR
  0143), and like an HMAC key (ADR 0075 §2) it never becomes a `string`.
- **A key that can't be read, or doesn't fit the algorithm, is a fault.**
  It is the program's own configuration, as `passwordMatches`'s stored
  hash is (ADR 0075 §5). The fault names the problem and never quotes
  the key. A startup check (ADR 0124) is where a program catches it
  before the first request: `crypto.sign(key, algorithm, <<>>)` once.
- **Deterministic algorithms only, so `sign` is pure**: Ed25519, RSA
  PKCS #1 v1.5, and ECDSA with RFC 6979 nonces (Go's `crypto/ecdsa` signs
  deterministically when given no random source). The PSS variants
  verify but don't sign: PSS needs a random salt, which would make
  `sign` a local effect like `hashPassword`, and none of the callers in
  the Context needs PS256 signing. Open question 2.

### 4. `fiig.jwt`, a library in fiig: verifying

`fiig.jwt` is a package in `compiler/stdlib/jwt`, written in fiig like
`fiig.http` and `fiig.kafka`. Its core is pure:

```fiig
use fiig.jwt;

conversion verify(token: string, keys: List<jwt.Jwk>, expect: jwt.Expect, now: int)
    -> Result<jwt.Verified, jwt.JwtError>;

conversion verifyHmac(token: string, key: Secret, expect: jwt.Expect, now: int)
    -> Result<jwt.Verified, jwt.JwtError>;
```

```fiig
model Expect {
    issuer: string;
    audience: string;
    algorithms: List<Algorithm>;          // what this program accepts; never empty
    tokenType: string | None;             // the header's typ, e.g. "at+jwt" (RFC 9068)
    leewaySeconds: int | None;            // None is 60
}

enum Algorithm {                          // the JOSE names, by ADR 0080's text form
    RS256, RS384, RS512, PS256, PS384, PS512, ES256, ES384, EdDSA,
    HS256, HS384, HS512
}

model Verified {
    issuer: string;
    subject: string | None;
    audience: List<string>;
    expiresAt: int;                       // Unix milliseconds, as Clock.now()
    issuedAt: int | None;
    id: string | None;                    // jti
    keyId: string | None;                 // the header's kid
    claims: JsonValue;                    // the whole payload, to json.decode (ADR 0073 §4)
}
```

The checks, in order, each a `JwtError` variant:

1. **Size.** A token over 16 KiB is `TooLarge` before anything is read.
   Bearer tokens with many group claims (Entra ID) reach a few KiB.
2. **Shape.** Three base64url segments (RFC 7515 §7.1). A JWE (five
   segments) is `Malformed`, not supported. Header and payload are
   decoded by `json.decode`, so a duplicate member name is refused (ADR
   0073 §3), which closes the parser-differential attack RFC 7515 §5.2
   warns about.
3. **Algorithm.** The header's `alg` must be one of `expect.algorithms`
   (`AlgorithmRefused` otherwise, naming it). `none` isn't an `Algorithm`,
   so it is always refused. `verify` refuses the HS variants and
   `verifyHmac` refuses everything else, so a public key is never used as
   an HMAC secret (the CVE-2015-9235 shape). An empty `algorithms` refuses
   every token: the program forgot to say.
4. **Header extensions.** A `crit` header (RFC 7515 §4.1.11) is refused,
   since `fiig.jwt` understands no extensions and the RFC requires
   refusing what isn't understood. `expect.tokenType`, when given, must match
   `typ` ignoring case and an `application/` prefix (RFC 8725 §3.11).
5. **Key.** By the header's `kid`: the key with that `kid` whose type
   fits `alg`, and whose own `alg`, if the JWK states one, is the same.
   No `kid`: the one key that fits; if more than one fits, `NoKey` (try
   each in turn hides which key a provider meant, and costs a signature
   check per key).
6. **Signature.** `crypto.verify` over the first two segments as sent
   (RFC 7515 §5.2), not as re-encoded.
7. **Claims.** `iss` equals `expect.issuer` exactly. `aud`, a string or
   a list (RFC 7519 §4.1.3, lenient about what it receives), contains
   `expect.audience`. `exp` is required, and past `now` plus leeway is
   `Expired`. `nbf`, and `iat` beyond the leeway in the future, are
   `NotYetValid`. A numeric date with a fraction (`1700000000.5`) is read
   to the millisecond.

```fiig
enum JwtError {
    TooLarge,
    Malformed(reason: string),
    AlgorithmRefused(algorithm: string),
    NoKey(keyId: string | None),
    BadSignature,
    WrongType(found: string | None),
    WrongIssuer(found: string),
    WrongAudience(found: List<string>),
    MissingClaim(name: string),
    Expired(at: int),
    NotYetValid(at: int),
    KeysUnavailable(reason: string)      // §5
}
```

- **`exp` required, with no switch.** RFC 7519 makes every claim
  optional, but every token in the Context carries `exp`, and RFC 8725
  §3.10 and Rust's `jsonwebtoken` default treat a token that never
  expires as a mistake. If a real issuer sends tokens without it, that
  is the time to add a switch (open question 3).
- **`issuer` and `audience` are required fields of `Expect`**, so
  skipping them can't be written by leaving out an option. A program
  that truly accepts any audience is rare enough to write its own rule
  over `jwt.decodeUnverified` (§7).
- **`now` is a parameter**, so `verify` stays pure and a test passes the
  time it wants. The caller passes `Clock.now()`.
- **Errors never quote the token**, only names and times, since a token
  in a log is a working credential.
- **Custom claims are the program's own model**, decoded from
  `claims` by `json.decode` (ADR 0073 §2). `fiig.jwt` doesn't need
  generics for it:

  ```fiig
  model IdToken {
      sub: string;
      email: string | None;
      groups: List<string> = [];
  }

  idToken: IdToken = mapErr(e in json.decode(verified.claims)): Refusal.Malformed?;
  ```

### 5. Keys from a JWKS URL, kept by a session

Verifying every request against keys fetched for that request would
double every request's latency and get the program rate-limited by its
provider. Keys have to outlive a request, and fiig's way of keeping state
between calls is a session (ADR 0114, 0117): `fiig.kafka` already keeps
each topic's layout in a broker session's state (ADR 0113 §4, amended),
answering most asks without the broker.

A session today lives exactly as long as its connection: when the
connection ends, its state goes with it. An HTTPS server closes an idle
connection after a minute or two, so a key cache in that kind of session
would be emptied within minutes of each fetch. The JWKS session is
therefore one that **connects only to write** (§5a): its state lasts the
whole process, and a connection exists only while a fetch is under way.
That is the shape of every library in the precedent. `jose`'s
`createRemoteJWKSet`, PyJWT's `PyJWKClient`, .NET's `ConfigurationManager`
and Rust programs over `jsonwebtoken` all keep the keys in a value in the
program's memory and fetch through an HTTP client that opens connections
on its own. None of them ties the cache to a connection.

`fiig.jwt` provides `fiig.session.SessionProtocol` with a JWKS protocol,
and a capability over it:

```fiig
capability JwtKeys {
    // The issuer's discovery document (OpenID Connect Discovery §4) names
    // its jwks_uri; both are kept by sessions.
    gateway forIssuer(issuer: string, keyId: string | None) -> Result<List<Jwk>, JwtError> requires Session;
    // A JWKS URL given directly, for issuers without discovery.
    gateway fromUrl(url: string, keyId: string | None) -> Result<List<Jwk>, JwtError> requires Session;
}
```

- **One session per URL.** Its settings are the URL's host, port, `tls`,
  and path, and `"connect": "onWrite"` (§5a), so each issuer's discovery
  document and each JWKS URL is its own session. `forIssuer` asks the issuer's session for its `jwks_uri`
  (kept for the session's life), then asks that URL's session for keys.
  Google's discovery document is on `accounts.google.com` and its keys
  on `www.googleapis.com`, so the two sessions are often on different
  hosts. Both asks are answered from state almost always, so a
  verification costs two in-process calls, not a network round trip.
- **The discovery document's `issuer` must equal the issuer asked**
  (OpenID Connect Discovery §4.3), or the answer is `KeysUnavailable`:
  otherwise a document that names another issuer would hand this one's
  verification its keys.
- **One fetch at a time.** The runtime calls a session's operations one at
  a time, so while a fetch is under way the asks that arrive are accepted
  and answered when the reply comes, not sent as fetches of their own. A
  burst of requests after a cold start costs the provider one request,
  with no lock in sight.
- **When it re-fetches,** with `jose`'s numbers:
  - a set older than 10 minutes is re-fetched by `tick`, before anyone
    waits for it. The server will usually have closed the last connection
    by then, so each refresh opens a new one: one TLS handshake per issuer
    every 10 minutes;
  - an ask for a `kid` the set doesn't have re-fetches at once, unless
    the last fetch was under 30 seconds ago. A provider's new key reaches
    the verifier on the first token that uses it, and made-up `kid`s
    cost the provider one request per 30 seconds at most.
- **A failed re-fetch keeps the old set,** for up to an hour past its
  10 minutes, so a provider's brief outage doesn't refuse every signed-in
  user. After that the set is dropped and asks answer `KeysUnavailable`:
  verification fails closed, it never falls back to not checking.
- **Strict about what it reads from the URL:** `https` only, with `http`
  allowed for loopback hosts so a test can serve keys locally. A reply
  over 256 KiB, a redirect, or a status other than 200 is
  `KeysUnavailable` with the reason. Redirects aren't followed: a JWKS
  URL that moves is a configuration change to make on purpose.
- **Lenient about what's in the set:** a key with `use: "enc"`, a `kty`
  §1 doesn't support, or one `crypto.checkPublicKey` refuses is skipped,
  not an error for the whole set. Providers publish keys for other uses
  beside signing ones.
- **The HTTP exchange reuses `fiig.http`'s pure `readReply` (ADR 0074
  §7)**, so the session protocol is a state machine around the same
  reply reader the HTTP client uses, not a second HTTP implementation.
- **A connection that ends mid-fetch** reaches the protocol as `ended`
  (§5a). It marks the fetch failed, which is the same as a failed
  re-fetch above: the old set is kept if there is one, and otherwise the
  waiting asks are answered `KeysUnavailable`.
- **Keys are cached per process.** Each instance of a program keeps its
  own set and fetches it on its own. That is fine for a cache, since keys
  can always be fetched again and a fresh instance only waits for its
  first fetch.

### 5a. Sessions that connect only to write (amends ADR 0117 §2)

A session's settings may say `"connect": "onWrite"`. Such a session keeps
its state from its first ask until the process stops, and has a
connection only while it has something to send or a reply to read:

1. **Starting.** On its first ask the runtime calls `opened(settings,
   now)` with no connection. The state it returns is the session's for
   the life of the process. `opened` runs once, not once per connection.
2. **Connecting.** `asked` and `tick` run with or without a connection.
   When a step has bytes to `write` and there is no connection, the
   runtime opens one, to `"host"` and `"port"` with `"tls"` and the
   alternates as today, and writes. A protocol that needs to send
   something first on each new connection (a login, a version
   handshake) puts it in front of its own `write`; HTTP needs nothing.
3. **Ending.** When the connection ends, because the server closed it,
   it broke, or a step's `close` was `Done` or `Failed`, the runtime calls
   a new operation:

   ```fiig
   ended(state: bytes, reason: string, now: int) -> Step;
   ```

   Its step's state is kept. A `write` in it opens a new connection, so
   a protocol that wants to retry can, and the asks still waiting stay
   waiting for it. With no write, the asks the step doesn't answer get
   `SessionError.Broken`, as today. A connection that ends six times in a
   row with nothing received on any, or a defect in the protocol (an
   operation that faults), ends the session as ADR 0117 §2 has it: the
   state goes, and the next ask starts it again.
4. **Ticking** runs whether or not there is a connection, so a refresh
   can be scheduled while idle.
5. **`MoveTo`** points the next connection at the new place and asks
   every waiting message again, which opens it. **`closing`** runs at
   shutdown, and its `write` is sent only if a connection is open.

A session without the setting is unchanged: it connects on its first
ask, `opened` runs on each connection, and its state ends with the
connection. `fiig.kafka` keeps that behaviour. Its state is tied to the
connection in ways that must not outlive it (a SASL login, a group
member's epoch), and it needs no change but an `ended` that returns its
state unchanged, which the runtime never calls for it, since every
member of `SessionProtocol` must be written.

The setting goes in the settings, not the manifest, because a session's
settings are already where its package says what kind of session it is
(`fiig.kafka`'s `"role"`, §3 of ADR 0117), and they are written by the
package's own gateways, so no application has to know about it.

In `compiler/runtime/session.go` this is: `open` called from `apply`
when a step writes with no connection, not from the ask; `drop` keeping
the state and calling `ended` for these sessions; and the timer firing
with no connection.

### 6. In a program: a bearer wire

```fiig
// src/accounts/Me.fiig
wire me: Identity from header "Authorization" scheme "Bearer" by signedIn else refused;
```

```fiig
// src/accounts/SignedIn.fiig
workflow signedIn(token: string | None) -> Result<Identity, Refusal> {
    return match (token) {
        None -> Err(Refusal.NotSignedIn),
        given -> verifiedIdentity(given)
    }
}
```

```fiig
// src/accounts/VerifiedIdentity.fiig
use fiig.jwt;

activity verifiedIdentity(token: string) -> Result<Identity, Refusal> {
    keys = mapErr(e in JwtKeys.forIssuer(Oidc.issuer, jwt.keyIdOf(token))): Refusal.Unavailable?;
    verified = mapErr(e in jwt.verify(token, keys, expect: Expect(
        issuer: Oidc.issuer,
        audience: Oidc.clientId,
        algorithms: [jwt.Algorithm.RS256, jwt.Algorithm.ES256]
    ), now: Clock.now())): Refusal.BadToken?;
    idToken: IdToken = mapErr(e in json.decode(verified.claims)): Refusal.BadToken?;
    return Ok(identityFor(idToken));
}
```

The binder stays a workflow (ADR 0107, and the owner's rule that
adapters call only workflows), the keys are fetched by an activity
through a gateway, and the check itself is a conversion a test can call
with a key it made, with no network:

```fiig
test "an expired token is refused" {
    expect jwt.verify(expiredToken, [testKey], expect: testExpect, now: 1800000000000)
        is Err(jwt.JwtError.Expired(_));
}
```

### 7. Issuing and inspecting

```fiig
conversion sign(claims: JsonValue, key: Secret, algorithm: Algorithm, keyId: string | None) -> Result<string, JwtError>;
conversion signHmac(claims: JsonValue, key: Secret, algorithm: Algorithm) -> Result<string, JwtError>;
conversion keyIdOf(token: string) -> string | None;
conversion decodeUnverified(token: string) -> Result<Unverified, JwtError>;
```

- `sign` writes the header itself (`alg`, `typ: "JWT"`, `kid` when
  given), so the caller can't put `alg: none` in it. It covers the
  Context's signing callers: a GitHub App's RS256 JWT, a Google service
  account's assertion, an APNs ES256 token. An algorithm `crypto.sign`
  doesn't sign (an HS one, which is `signHmac`'s, or PSS) is
  `AlgorithmRefused`; a key that can't be read faults in `crypto.sign`.
- `signHmac` is for a program issuing its own tokens to itself, with a
  `Secret` shared by its instances.
- `keyIdOf` reads the `kid` to choose keys before verifying. It reads
  only the header and trusts nothing in it beyond the choice of key.
- `decodeUnverified` returns header and payload **unchecked**, for
  debugging tools and the rare program that inspects before choosing an
  issuer. Its type, `Unverified`, has no field a `Verified` has, so one
  can't be passed where the other is wanted. The name says what it is,
  as Go's `ParseUnverified` does.

### 8. Scope

In: §1–§7. Out, each until a caller needs it: JWE (encrypted tokens) and
AES-GCM; DER ECDSA signatures; ES512 and ES256K; PSS signing; X.509
certificate chains in a JWK's `x5c` (a key is taken from its `n`/`e` or
`x`/`y`, and `x5c` ignored); token revocation and introspection (RFC
7662), which is an HTTP call a program can make with `fiig.http`;
issuing OIDC tokens as a provider.

## Alternatives considered

**A. Algorithm-named members (`crypto.verifyRs256(...)`) instead of an
enum.** Nine members, then nine signing ones, and `fiig.jwt` would need a
nine-arm match to call them. The enum lets a JOSE `alg` map to an
algorithm by ADR 0080's text form. ADR 0075 chose named members for
hashes because each has its own output size; signatures all answer a
`bool`.

**B. An opaque `PublicKey`, checked once when made.** Go's and Java's
shape: parse once, verify many times with no re-check. But an opaque
value can't be kept in a session's state, which is bytes (ADR 0114 §4),
written in a test, or compared. Checking an RSA key costs microseconds
next to the signature check. Rejected for a plain value checked by
`verify`.

**C. `crypto.verify` returns `Result<bool, KeyError>`.** Says why a key
is bad at every call. But every caller then handles two kinds of "no",
and the reason is only needed where keys enter the program, which
`checkPublicKey` covers. Rejected.

**D. JWT in Go, as a prelude group.** Faster to write, and the only
choice if fiig couldn't express it. But nothing in JWT is slow or
impossible in fiig: it is base64url, JSON and a signature check, all of
which fiig has. ADR 0116's exception was for work measured too slow. A
library in fiig is the dogfooding the language needs, and its rules are
readable by the programs that use it. Rejected.

**E. Keys from a wire, not fetched.** `wire Keys { jwks: string from env
"OIDC_JWKS" }`: no session, no network. But providers rotate keys, and
the new key would refuse every user until someone redeployed. It stays
possible: `verify` takes any `List<Jwk>`, and `publicKeyFromPem` reads a
configured key. As the main path, rejected.

**F. Fetch the keys on every request, through `HttpClient`.** No
session, and simple. Doubles latency and gets the program rate-limited.
Rejected; §5's cache is what every library in the precedent does.

**G. A general in-process cache capability** (`Cache.get(key)`,
`Cache.put(key, value, ttl)`), with JWKS one user of it. Many programs
would use one, and it may be worth its own ADR. But a JWKS cache's rules
(re-fetch on unknown `kid`, cooldown, stale-on-error, fail closed) are
the hard part, and a general cache leaves each to the caller. Sessions
already keep state with those rules written once. Not rejected, but not
this ADR (open question 4).

**H. One `verify` that accepts HS algorithms too, taking `Secret |
List<Jwk>`.** One function, and a union key type. But the union is
exactly what allows a public key where a secret goes. Two functions make
confusion something that can't type-check. Rejected.

**I. Make `algorithms` optional, defaulting to the key's type.** `jose`
does this, and it is safe there because the key type limits the
algorithm. But a stated list documents the program's policy, and a
default is what a later change can widen silently. Rejected: the list is
required and an empty one faults.

**J. Keep every session's state across a reconnect**, with a protocol
operation saying it reconnected, instead of §5a's opt-in. One kind of
session, not two. But `fiig.kafka`'s state holds things that belong to
one connection: a SASL login, a group member's epoch, correlation ids in
flight. Each would have to be sorted into "survives" and "doesn't", and a
mistake there is a consumer that thinks it is still logged in. And the
JWKS session still wouldn't fit: it would hold a connection it has no use
for between fetches. Rejected for §5a, where only a session that asks for
it changes.

**K. Shared state and locks in the language**, so a program keeps its
own key cache, as a Rust program does with `Arc<RwLock<JwkSet>>`.
Rejected. Locks are where races, deadlocks and a lock held across a
network call come from, the kind of bug fiig exists to remove; values in
fiig never change (ADR 0023) and a `concurrent` body may not reassign
(ADR 0053) for the same reason. A session is already fiig's answer to
"state between calls": one owner, operations one at a time, as an Erlang
process holds its state. §5a only stops tying that state to a
connection.

## Consequences

- An API behind any OIDC provider can verify its callers' tokens
  locally, with key rotation handled, and a bearer wire (ADR 0144) plus
  one activity is the whole setup.
- A program can sign as a GitHub App, a Google service account or an
  APNs client. ADR 0120's XOAUTH2 limitation (no RS256 to mint a
  service-account token) becomes solvable in fiig.
- `crypto` gains `verify`, `sign`, `checkPublicKey` and
  `publicKeyFromPem`; the prelude gains `SignatureAlgorithm`, `PublicKey`
  and `KeyError`, which become reserved type names (K2).
- No new Go dependency: all of it is the standard library.
- Sessions gain a second kind (§5a): state for the process's life, a
  connection only while there is something to send. ADR 0117 §2 is
  amended, `SessionProtocol` gains `ended`, and `fiig.kafka` gains a
  one-line `ended` and is otherwise unchanged. Any later protocol over
  HTTP or another short-lived connection (an OAuth token endpoint, a
  discovery document) can use the same kind.
- `fiig.jwt` is a second user of sessions as a cache, after
  `fiig.kafka`, and the first that connects only to write.
- An application using `JwtKeys` lists `"Session"` in its manifest's
  capabilities (ADR 0035), as `fiig.kafka`'s users do.
- `known-limitations.md`'s "no public-key signatures" entry goes, and
  improvement idea D1 is done but for AES-GCM.

## Open questions

*(Resolved 2026-11-25, by the owner: a session's state ending with its
connection, which emptied the key cache whenever an HTTPS server closed
an idle connection, is answered by §5a's sessions that connect only to
write. Alternatives J and K are the other answers considered.)*

1. DER-encoded ECDSA signatures, for WebAuthn and X.509, as a second
   `crypto.verify` form or a converter.
2. PSS signing, as a local effect drawing its salt from `Random` like
   `hashPassword`.
3. A switch to accept tokens without `exp`, if a real issuer needs one.
4. A general in-process cache capability (Alternative G), if a second
   program needs the same kind of cache. It would be a capability whose
   lookups always name how to fetch the value again, with the runtime
   owning expiry and one fetch at a time, not state and locks
   (Alternative K).
5. Pre-fetching keys at startup (ADR 0124) so the first request doesn't
   wait for discovery and the JWKS, and so a wrong issuer URL stops the
   program from starting instead of refusing every request.

## Implementation notes (2026-11-25)

- **Sessions that connect only to write** (§5a): `compiler/runtime/session.go`
  (`start`, `connectToWrite`, `ended`, `reset`, `moveToWrite`).
  `SessionProtocol` gained `ended`, and `fiig.kafka`'s returns its state
  unchanged. Tested in `tooling/runprogram`'s `session_on_write_test.go`: an
  answer from state opens no connection, a write opens one, a dropped
  connection runs `ended` and keeps the state, and a server that can't be
  reached is an `ended` too.
- **`crypto`** (§1-§3): `verify`, `sign`, `checkPublicKey` and
  `publicKeyFromPem` in `compiler/runtime/signature.go`, all of it Go's
  standard library and `x/crypto/cryptobyte`, which the module already
  had. `hmacSha384` joined the group for HS384. The prelude gained
  `SignatureAlgorithm`, `PublicKey` and `KeyError`. Tested against keys
  and signatures Go makes, including r = s = 0 (CVE-2022-21449), a point
  off the curve and a 1024-bit RSA key, and HMAC-SHA-384 against RFC
  4231's test case 2.
- **`fiig.jwt`** (§4-§7): `compiler/stdlib/jwt`, in fiig. Its own tests
  check tokens Go's crypto signed, every refusal in §4, key sets, and the
  key session's time rules with the clock in the test's hands.
  `tooling/runprogram/jwt_test.go` checks what fiig signs with Go's crypto,
  HMAC tokens both ways, and `JwtKeys` against a local server: one fetch
  for many verifications, none after the server closes the connection,
  the 30-second cooldown on unknown kids, discovery, a discovery document
  naming another issuer, a 503, and an `http` URL refused.
- **Where the build differs from the text above,** each corrected in
  place:
  - Model fields have no defaults, so `Expect`'s are `tokenType: string |
    None` (`type` reads as a type name) and `leewaySeconds: int | None`,
    `None` being 60.
  - fiig code has no way to fault, so an empty `algorithms` refuses every
    token instead of faulting, which fails closed the same way, and
    `sign` and `signHmac` return `Result<string, JwtError>`, with
    `AlgorithmRefused` for an algorithm they don't sign.
  - §5a's waiting asks stay waiting when `ended` writes, and a session
    whose connection keeps ending with nothing received is reset after
    six times.
- **Not done:** pre-fetching keys at startup (open question 5), and a
  Hearsay change to verify its id token.
