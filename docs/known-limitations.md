# Known limitations of the alpha

fiig is an alpha: syntax and behavior can change between releases, with no
compatibility promise. These are the gaps known at release, so a program
doesn't have to discover them.

## Capabilities and data

- **SQLite is the only database built in.** `libraries/sql` works with any
  Go `database/sql` driver, but `fiig` bundles only SQLite. No Postgres
  (Milestone 7 writes its wire protocol in fiig), Redis, Kafka, or queues.
- **`Database` calls are not checked.** `Database` is a host capability with
  no fiig declaration, so its calls' arguments and results aren't
  type-checked, and a gateway's `requires` line isn't checked against it.
  Capability groups written in fiig are checked (P7).
- **Rows are untyped.** A query returns a record whose field types come from
  the database's storage, and a decimal written to SQL reads back as a
  `string`; convert it with `toDecimalOrNone()`. Declared row types are ADR
  0066, proposed.
- **No migrations.** Create tables from a gateway, as the getting-started
  example does.
- **`FileSystem` has no runtime provider.** `Socket`, `Clock`, and
  `Random` do; `Clock` and `Random` need no capabilities-file entry (ADR
  0068 §4, §6).
- **`Clock` is untyped:** `Clock.now()` is Unix milliseconds as an `int`,
  and the checker doesn't type its calls (ADR 0068 §5). A time type is
  open.

## HTTP

- **CORS origins are listed exactly** (ADR 0070 §9): no wildcard
  subdomains for per-tenant front ends, and one policy per adapter.
- **One content type per route**, and no `Accept` negotiation (ADR 0069
  open question 4). A non-JSON body is a `string` or `bytes` the program
  parses or writes itself; there's no XML library (open question 5).
- **SHA-256 is the only hash** (ADR 0075 §6): no SHA-1 or SHA-512 HMACs
  (Twilio signs with SHA-1), no encryption, no public-key signatures (so
  no RS256 JWTs), and no HKDF. A `Secret` can't be read as base64 or hex
  outside a gateway, as Standard Webhooks' `whsec_` secrets need.
- **`crypto.equal` isn't enforced:** comparing a signature with `==` checks
  clean and leaks timing (ADR 0075 §3).
- **Two models, or `int | decimal`, in one union** decode by the order
  the members are written (ADR 0081 open questions 1 and 2). Enums that
  read the same JSON are refused (J3).
- **The HTTP client opens a connection per request** (ADR 0074 §8):
  `fiig.http` has no pool, so every `send` pays a TCP and, for `https`, a
  TLS handshake. No HTTP/2, compression, proxies, cookie jar, streaming
  bodies, multipart, or client certificates (§9), and redirects come back
  as replies (§4).
- **No limit on a reply's size** (ADR 0074 open question 2): the timeout
  bounds how long a reply takes, not how much memory it uses.
- **Request limits are per service, not per route** (ADR 0076 open
  question 1): a route taking uploads over 2 MiB needs `MAX_BODY_BYTES`
  raised for every route. No rate limit per client (§5); put it in the
  proxy.
- **No package-internal visibility** (ADR 0079 open question 1). A
  declaration is private to its file or public to every importer, so the conversions `fiig.http`'s gateway
  calls (`readReply`, `outgoingRequest`, and two helpers) are public too.
- **A `bytes` field is always written as standard base64** (ADR 0078
  open question 1): an API that demands base64url for a field (Gmail's
  `raw`) needs a `string` field filled with `toBase64Url()`.
- **No streaming JSON and no pretty printing** (ADR 0073 §9): a decode reads
  the whole input, and `json.encode` writes no whitespace.

## Configuration

- **The environment is the only source** (ADR 0071 §8): no YAML, TOML, or
  properties files, no remote secret stores (inject them into the
  environment), and no reloading while running.
- **No lists in config.** Read a `string` and split it in a rule.
- **One account per provider.** A package's config has one value per
  program, so two Stripe accounts in one program need ADR 0043's
  parameterized providers, which aren't designed.
- **No `fiig test`** to set config per test; run an activity with `fiig run
  -env-file test.env`.

## Language

- **Strings aren't normalized** (ADR 0067 §8): `é` as one code point and
  as `e` plus a combining accent are different strings. No locale-aware
  sorting, and no regular expressions.
- **No user-defined generics** and no lambdas (ADR 0056).
- **No model invariants:** validate in a rule.
- **Streams and queue consumers** (ADR 0047) are designed, not built.
- See `docs/specification/grammar.md`'s "Known gaps" for open syntax
  questions.

## Tooling

- **The interpreter walks the tree.** It isn't tuned for throughput.
- **No editor support** yet (Milestone 9). `fiig fmt -w` formats files.
