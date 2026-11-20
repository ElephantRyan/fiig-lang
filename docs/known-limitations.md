# Known limitations of the alpha

fiig is an alpha: syntax and behavior can change between releases, with no
compatibility promise. These are the gaps known at release, so a program
doesn't have to discover them.

## Capabilities and data

- **SQLite and Postgres are the databases built in.** SQLite through
  `libraries/sql` (Go), Postgres through `fiig.postgres`, written in fiig
  (ADR 0085). No MySQL. Redis is `fiig.redis` (ADR 0110): one command
  per call, string arguments only, no pipelining, pub/sub, Sentinel, or
  Cluster.
- **No certificate revocation checks** (CRL, OCSP), certificate pinning,
  keys in an HSM or cloud KMS, or encrypted private keys (ADR 0086 open
  questions). Custom CAs and client certificates are configured on
  `Socket` (ADR 0086).
- **`fiig.postgres` doesn't cancel a query whose call timed out (the server finishes it), caches no prepared
  statements, normalizes no non-ASCII passwords (SASLprep), and has no
  `COPY` or `LISTEN`/`NOTIFY`. One `execute` runs one statement.
- **No savepoints or read-only transactions** in fiig source (ADR 0088
  open questions); isolation levels and retrying a whole transaction are
  `transaction Database serializable retry(attempts: N)`. Two packages
  bound to one database get a pool each (ADR 0084 open question 4).
- **Queries aren't checked against the schema.** A query's rows read into
  the row type its context gives (ADR 0082), but whether the columns fit is
  found at the query, as a fault naming the column, not by `fiig check`:
  that needs tables declared in fiig (ADR 0082 open question 3).
- **Only conflicts are values.** A `Database` call that opts in (ADR 0087)
  gets a unique, foreign-key, not-null, or check violation, a
  serialization failure, or a deadlock as `Err(DatabaseConflict...)`; bad
  SQL and a lost connection still fault, and a conflict at `COMMIT` faults.
  A whole transaction retries with `retry(attempts: N)` on the block
  (ADR 0088).
- **No SQL whose shape depends on data.** The SQL text is a literal (Q1), so
  a sort column or table chosen at run time has no form; an `IN` list of
  varying length can bind one parameter with SQLite's `json_each(?)` (ADR
  0082 open question 1).
- **Other host capabilities are untyped.** `EmailSender`, `PaymentGateway`,
  and the like are held to a gateway's `requires` (B10, P7), but their
  arguments and results aren't checked; only `Database`'s are.
- **Migrations are forward only and plain SQL** (ADR 0096): one set of
  files for every database, so SQL that differs between SQLite and
  Postgres needs care, and a data change SQL can't express has no home.
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
- **The HTTP client is HTTP/1.1 only** (ADR 0074 §9): no HTTP/2,
  compression, proxies, cookie jar, streaming bodies, multipart, or client
  certificates, and redirects come back as replies (§4). Connections are
  kept between requests (ADR 0109), one request at a time each.
- **`fiig.smtp` sends plain-text mail only** (ADR 0120): no HTML or
  attachments, Cc, Bcc, Reply-To or display names, no login but AUTH PLAIN
  (so no XOAUTH2 for Gmail or Microsoft 365), no internationalised
  addresses, and one connection per mail. A mail to several recipients goes
  to all of them or to none.
- **No connection outlives every call** (ADR 0109 open question 2): a
  connection is kept idle between calls, but nothing can hold a
  subscription, Postgres `LISTEN`, or an HTTP/2 connection shared by
  concurrent requests.
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

## Language

- **Strings aren't normalized** (ADR 0067 §8): `é` as one code point and
  as `e` plus a combining accent are different strings. No locale-aware
  sorting, and no regular expressions.
- **No user-defined generics** and no lambdas (ADR 0056). List work is
  done with forms whose body is written in place, `filter(x in xs): ...`
  and the rest (ADR 0093); a function can't be stored or passed.
  An error type is changed with `mapErr(e in result): newError` (ADR
  0095), which needs neither.
- **Stream adapters read Redis Streams only** (ADR 0111): no Kafka or
  NATS (they keep state on a connection, ADR 0109 open question 2), no
  replay of one element by id, and one element at a time per route, with
  a fixed batch of 16 and a 2 s wait.
- **Schedules are UTC only** (ADR 0112 open question 1), with no jitter
  and no catching up on firings missed while nothing ran; cron has no
  names (`MON`), `L`, `W`, `#`, or seconds.
- See `docs/specification/grammar.md`'s "Known gaps" for open syntax
  questions.

## Testing

`fiig test` (ADR 0091) runs a package's `tests/`. What it doesn't do yet:

- **Postgres-only SQL** fails against a test's in-memory SQLite database;
  point `-caps` at a real Postgres for those tests.
- **No tests of workflows or adapters**, such as an HTTP request to an
  adapter in the same process. Test what the workflow calls.
- **`calls` only counts.** A test that wants the arguments a fake saw
  stores them in its `Database`.
- **No shared fakes.** A fake used by many tests is written in each.
  A test file may have a `private` block of helpers, for building values
  the test needs, but the helpers are that file's alone.
- **No literal patterns**: `expect r is Err(e)` then `expect e == ...`,
  two lines where one would do. A literal in a pattern is one error that
  says so.
- **A test of a rule that chooses a provider fakes every provider** it
  can return, so it can't run one provider's real gateway against a fake
  of what that provider uses (`Client` under `Stripe`).
- No snapshot tests, coverage, or benchmarks.

## Tooling

- **The interpreter walks the tree.** It isn't tuned for throughput.
- **IntelliJ IDEA 2026.2 and 2026.3 only** (ADR 0090 §1a): the plugin
  ([docs/intellij.md](intellij.md)) adds each IntelliJ release as it
  ships, and supports none before 2026.2. Other editors can use `fiig
  lsp` (ADR 0089) directly. Completion, rename, quick fixes, and a
  debugger aren't built. Go to declaration on a local or parameter
  works within its declaration; find usages covers declarations, fields,
  variants, and gateways, not locals.
