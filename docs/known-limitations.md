# Known limitations of the alpha

fiig is an alpha: syntax and behavior can change between releases, with no
compatibility promise. These are the gaps known at release, so a program
doesn't have to discover them.

## Capabilities and data

- **SQLite and Postgres are the databases built in.** SQLite through
  `libraries/sql` (Go), Postgres through `fiig.postgres`, written in fiig
  (ADR 0085). No MySQL, Redis, Kafka, or queues.
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
- **Streams and queue consumers** (ADR 0047) are designed, not built.
- See `docs/specification/grammar.md`'s "Known gaps" for open syntax
  questions.

## Tooling

- **The interpreter walks the tree.** It isn't tuned for throughput.
- **No IntelliJ plugin yet** (Milestone 9, ADR 0090). `fiig lsp` (ADR
  0089) gives any LSP editor errors as you type, hover, go to
  declaration, find usages, symbols, and formatting; completion, rename,
  and quick fixes aren't built. Go to declaration on a local or parameter
  works within its declaration; find usages covers declarations, fields,
  variants, and gateways, not locals.
