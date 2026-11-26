# Known limitations of the alpha

fiig is an alpha: syntax and behavior can change between releases, with no
compatibility promise. These are the gaps known at release, so a program
doesn't have to discover them.

## Capabilities and data

- **The drivers are bundled, not standard** (ADR 0152). Postgres, Kafka,
  Redis and S3 are `com.degeneratedevelopment.*` packages, shipped inside
  `fiig` but versioned on their own and outside the language. Until `fiig`
  can fetch a package, a driver fix arrives only with a toolchain release,
  and one toolchain carries one version of each.

- **SQLite and Postgres are the databases built in.** SQLite through
  `libraries/sql` (Go), Postgres through `com.degeneratedevelopment.postgres`, written in fiig
  (ADR 0085). No MySQL. Redis is `com.degeneratedevelopment.redis` (ADR 0110): one command
  per call, or a pipeline with `Redis.sendAll` (not a transaction:
  `MULTI`/`EXEC` are the caller's to send), and no pub/sub, Sentinel, or
  Cluster. Its stream source needs Redis 6.2 or later, for `XPENDING ...
  IDLE`.
- **Kafka is at least once** (`com.degeneratedevelopment.kafka`, ADR 0113): `acks=all`, but
  no idempotence or transactions, so a send that is retried after a lost
  reply writes its records twice. SASL is PLAIN, SCRAM-SHA-256 and
  SCRAM-SHA-512 only (no OAUTHBEARER or GSSAPI, and no KIP-368
  re-authentication: a broker that expires a login closes the connection,
  and the session logs in again). A topic's layout is cached per process
  for `KAFKA_METADATA_MAX_AGE_MS` and dropped when a produce says the
  leaders moved. No compression levels (ADR 0116).
- **The Kafka consumer is KIP-848 only** (ADR 0117): Kafka 4.0 or later.
  A retried record's attempts count again from 1 after a
  restart or a rebalance; a route sees a record's value, not its key or
  headers; and each receive reads the topic's metadata and fetches from
  its leaders one after another, with no fetch sessions (KIP-227).
- **No certificate revocation checks** (CRL, OCSP), certificate pinning,
  keys in an HSM or cloud KMS, or encrypted private keys (ADR 0086 open
  questions). Custom CAs and client certificates are configured on
  `Socket` (ADR 0086).
- **`com.degeneratedevelopment.postgres` normalizes no non-ASCII passwords (SASLprep), and has
  no `COPY` or `LISTEN`/`NOTIFY`.** One `execute` runs one statement. A
  query whose call timed out is cancelled with a CancelRequest and its
  connection discarded (ADR 0084 §3a); each connection caches up to
  `statementCacheSize` prepared statements (ADR 0085 §3b).
- **No savepoints or read-only transactions** in fiig source (ADR 0088
  open questions); isolation levels and retrying a whole transaction are
  `transaction Database serializable retry(attempts: N)`. Two packages
  bound to one database get a pool each (ADR 0084 open question 4).
- **Queries are checked against the schema only on request.** A query's rows
  read into the row type its context gives (ADR 0082), and `fiig check
  -schema` (ADR 0131) applies the migrations to an in-memory SQLite database
  and prepares each literal query, finding a misspelt table or column. It
  checks what SQLite can prepare, so Postgres-only SQL is counted and not
  checked, migrations SQLite can't run leave every query unchecked, and a
  program that makes its tables in a gateway has no migrations to check
  against. It doesn't compare a query's columns with the row type.
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
- **A moment is an `int`:** `Clock.now()` is Unix milliseconds as an `int`
  (ADR 0068 §5, typed since ADR 0123). A moment stays an
  `int`; `fiig.time` (ADR 0123) formats and parses it and reads it as a
  calendar date, in UTC only. There are no time zones, no date-only or
  time-only types, no pattern strings, and a span is an `int` of
  milliseconds, so the compiler can't tell a moment from a span.

## HTTP

- **One CORS policy per adapter** (ADR 0070 §9). Wildcard subdomains
  (`https://*.example.com`) match any depth but never the apex, and a
  two-label public suffix (`https://*.co.uk`) isn't detected, since the
  toolchain carries no Public Suffix List.
- **A principal has one source, and is never optional** (ADR 0137 §8, §9,
  decided and not built). A route can't accept a cookie *or* an API key
  header; put the script-facing routes in their own adapter with their own
  principal. A public route can't also see who is signed in.
- **One content type per route**, and no `Accept` negotiation (ADR 0069
  open question 4). A non-JSON body is a `string` or `bytes` the program
  parses or writes itself; there's no XML library (open question 5).
- **No encryption** (ADR 0075 §6): `crypto` has SHA-1, SHA-256 and
  SHA-512 with their HMACs, HKDF-SHA-256, and public-key signatures
  (Ed25519, ECDSA P-256 and P-384, RSA PKCS #1 v1.5 and PSS; ADR 0146),
  which `fiig.jwt` uses for JWTs. There is no encryption (AES-GCM), so no
  JWE, and no ES512, ES256K, PSS signing or DER ECDSA signatures (ADR 0146
  §8). SHA-1 is only for checking signatures a third party already makes
  with it, such as Twilio's. A `Secret` can't be read as base64 or hex
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
- **`fiig.smtp` logs in with a password or a token only** (ADR 0120):
  AUTH PLAIN, AUTH LOGIN and XOAUTH2 (Gmail, Microsoft 365), but no
  CRAM-MD5. An XOAUTH2 token is read once at start and never refreshed
  (§3b), so a long-running program must be restarted with a fresh
  `SMTP_OAUTH_TOKEN` before it expires (about an hour). No
  internationalised addresses, and Reply-To takes one address. HTML and
  attachments go as multipart, but there are no inline images
  (`multipart/related`), and an attachment's content type takes no
  parameters. A mail to several recipients (To, Cc and Bcc together) goes
  to all of them or to none. Connections are kept between mails; a mail on
  a kept connection isn't checked against the server's SIZE limit first.
- **`com.degeneratedevelopment.s3` covers single objects** (ADR 0121): put (optionally with
  user metadata, always with an `x-amz-checksum-sha256` for S3 to verify),
  get (whole, with its metadata, or a byte range), head, delete, and copy
  within or across buckets, and presigned URLs for GET, PUT, HEAD or
  DELETE (up to seven days, host the only signed header). No listing (it
  needs an XML reader), multipart upload, or server-side encryption
  headers. A
  copy's error inside a 200 reply is found by looking for `<Error>`, not
  by parsing XML, and metadata values must be printable ASCII. The whole
  object (or range) is held in memory, so a very large one isn't
  practical until `fiig.http` can stream. Not yet tried against AWS.
- **Only Kafka uses a session** (ADR 0114, built by ADR 0117): the
  runtime can own a connection past every call and run a protocol written
  in fiig over it, but no HTTP/2 connection is built on one yet.
- **No broadcasts** (ADR 0111 §8): Postgres `NOTIFY` and Redis pub/sub
  aren't stream sources, since they deliver at most once to every
  listener. A broadcast adapter kind, and a Postgres queue source (`FOR
  UPDATE SKIP LOCKED`), are decided but not built.
- **A reply is read whole into memory** (ADR 0074 §7a): `fiig.http` holds
  a reply's body in memory, up to `HTTP_CLIENT_MAX_REPLY_BYTES` (16 MiB by
  default); a larger reply is `HttpError.ReplyTooLarge`, and there is no
  way to stream a body in pieces, so a program reading big bodies (large
  `com.degeneratedevelopment.s3` objects) must raise the limit for the whole process.
- **Request limits are per service, not per route** (ADR 0076 open
  question 1): a route taking uploads over 2 MiB needs `MAX_BODY_BYTES`
  raised for every route. No rate limit per client (§5); put it in the
  proxy.
- **No package-internal visibility** (ADR 0079 open question 1). A
  declaration is private to its file or public to every importer, so the conversions `fiig.http`'s gateway
  calls (`readReply`, `outgoingRequest`, and two helpers) are public too.
- **A base64url `bytes` field is always written unpadded** (ADR 0078 §6):
  `from json base64url` writes RFC 4648 §5 without `=`, which JWT,
  WebAuthn and Google's APIs accept; an API that demands padded base64url
  has no form yet.
- **No streaming JSON** (ADR 0073 §9): a decode reads the whole input,
  and `json.encode` and `json.encodePretty` build the whole output in
  memory.

## Configuration

- **The environment and saved settings are the only sources** (ADR 0071
  §8, ADR 0140): no YAML, TOML, or properties files, and no remote secret
  stores (inject them into the environment). Only a setting saved in the
  app changes while the process runs.
- **A saved setting's write path is a `string`** (ADR 0126): the text a
  setup form sends reaches the gateway that saves it unprotected, and only
  what is read back is a `Secret`.
- **A pending hard setting needs a restart from outside.** `fiig serve`
  can't restart itself (ADR 0140 open question 1).
- **No lists in config.** Read a `string` and split it in a rule.
- **One account per provider.** A package's config has one value per
  program, so two Stripe accounts in one program need ADR 0043's
  parameterized providers, which aren't designed.

## Language

- **Strings aren't normalized** (ADR 0067 §8): `é` as one code point and
  as `e` plus a combining accent are different strings. No locale-aware
  sorting, and no regular expressions.
- **Building a string or a list is limited only by memory** (ADR 0129 §1a).
  `repeat`, `padLeft`, `padRight`, `append` and interpolation have no size
  cap. They fault only when a length overflows. A count or size that comes
  from a request must be bounded by a rule first. Running out of memory
  stops the whole process, not just the request, because a Go program can't
  recover from it.
- **No user-defined generics** and no lambdas (ADR 0056). List work is
  done with forms whose body is written in place, `filter(x in xs): ...`
  and the rest (ADR 0093); a function can't be stored or passed.
  An error type is changed with `mapErr(e in result): newError` (ADR
  0095), which needs neither.
- **Stream adapters read Redis Streams and Kafka** (ADRs 0111, 0117): no
  NATS, and one element at a time per route. `fiig run -replay` runs one
  element again by its id but settles nothing, and a Kafka record replays
  as attempt 1, since only a group member counts its retries. The batch and the wait are set for the whole process
  (`STREAM_BATCH_SIZE`, `STREAM_WAIT_MS`), not per route.
- **fiig code can't write a log line** (ADR 0150 open question 1). The
  runtime logs requests, faults and failed gateway calls, as text or as JSON
  lines (`-log-format json`). fiig exports no spans or metrics
  (OpenTelemetry is open question 2), doesn't pass a trace on to the calls
  it makes, and samples nothing. Google Cloud's `severity` field isn't
  written.
- **The concurrency maximum is per block** (ADR 0149 §4). An application's
  `concurrency.maximum` caps each `concurrent` block, not the process: two
  blocks running at once, or one nested in another, can have more bodies in
  flight between them. Nothing bounds calls to one capability across blocks
  (open question 3), and an operator can't lower the values without a
  rebuild (open question 1).
- **Schedules are UTC only** (ADR 0112 open question 1), with no catching
  up on firings missed while nothing ran; cron has no names (`MON`), `L`,
  `W`, `#`, or seconds. Jitter (`SCHEDULE_JITTER_MS`) is set for the whole
  process, not per route.
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
- **A test of a rule that chooses a provider fakes every provider** it
  can return, so it can't run one provider's real gateway against a fake
  of what that provider uses (`Client` under `Stripe`).
- No snapshot tests, coverage, or benchmarks.

## Tooling

- **The interpreter isn't a compiler** (ADR 0115): bodies are compiled to
  closures with unboxed ints, a few times faster than walking the tree,
  but there is no native build; a byte loop costs about 24 ns a byte.
- **IntelliJ IDEA 2026.2 and 2026.3 only** (ADR 0090 §1a): the plugin
  ([docs/intellij.md](intellij.md)) adds each IntelliJ release as it
  ships, and supports none before 2026.2. Other editors can use `fiig
  lsp` (ADR 0089) directly. Completion, rename, quick fixes, and a
  debugger aren't built. Go to declaration on a local or parameter
  works within its declaration; find usages covers declarations, fields,
  variants, and gateways, not locals.
