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
- **`Random` and `FileSystem` have no runtime provider.** `Socket` and
  `Clock` do.

## HTTP

- **Routes can't read request headers**, so there's no bearer-token or
  cookie authentication yet.
- **An enum or `Result` in a response body** is written in the tagged form
  `fiig run -json` uses (`{"$enum": ...}`); a model is a plain object.
  Map errors to a status and a message, as the getting-started adapter does.
- **No HTTP client library.** `examples/acme-http` shows one written in fiig
  over `Socket`, but it only sends POST over TLS to port 443 with no custom
  headers.
- **No JSON parsing in fiig code.** Request bodies decode into declared
  types at the adapter, but a program can't parse JSON it received some
  other way (ADR 0062's library).

## Language

- **A JSON object decoded into a `Map` takes its members in name order**,
  not the order they arrived in (ADR 0062 decides that).
- **Strings have few methods:** `contains`, `isBlank`, `utf8`, and
  `toDecimalOrNone`. No `length`, `split`, `trim`, or case conversion.
- **No user-defined generics** and no lambdas (ADR 0056).
- **No model invariants:** validate in a rule.
- **Streams and queue consumers** (ADR 0047) are designed, not built.
- See `docs/specification/grammar.md`'s "Known gaps" for open syntax
  questions.

## Tooling

- **The interpreter walks the tree.** It isn't tuned for throughput.
- **No editor support** yet (Milestone 9). `fiig fmt -w` formats files.
