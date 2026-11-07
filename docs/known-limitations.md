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
- **No signed cookies or password hashing:** fiig has no hash functions
  yet, so a session cookie should carry a random token the service looks
  up (ADR 0069 §4), and `examples/accounts` stores its keys as given.
- **`GenerateOpenAPI` isn't on the command line.** The adapter package
  builds the document; no `fiig` command prints it yet.
- **An enum or `Result` in a response body** is written in the tagged form
  `fiig run -json` uses (`{"$enum": ...}`); a model is a plain object.
  Map errors to a status and a message, as the getting-started adapter does.
- **No HTTP client library.** `examples/acme-http` shows one written in fiig
  over `Socket`, but it only sends POST over TLS to port 443, with an
  `Authorization` header and no others.
- **No JSON parsing in fiig code.** Request bodies decode into declared
  types at the adapter, but a program can't parse JSON it received some
  other way (ADR 0062's library).

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

- **A JSON object decoded into a `Map` takes its members in name order**,
  not the order they arrived in (ADR 0062 decides that).
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
