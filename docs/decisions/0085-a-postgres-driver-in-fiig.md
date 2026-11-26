# ADR 0085: A Postgres Driver Written in fiig

Status: Accepted and implemented (2026-11-17; see Implementation notes)
Amended by ADR 0152 (2026-11-26): the package is
`com.degeneratedevelopment.postgres`, bundled with the toolchain, not part of
the standard library.
Date: 2026-11-16
Related: ADR 0058 (binary patterns), ADR 0060 (`Socket`, in-band TLS;
open question 2, custom CAs), ADR 0067 §8 (no normalization), ADR 0074
(`fiig.http`, a package built into the toolchain), ADR 0075 (`crypto`),
ADR 0082 (typed `Database` calls, the row reader), ADR 0084 (drivers
written in fiig, the runtime's pool). Milestone 7: "Postgres wire protocol
in fiig, replacing `libraries/sql`".

## Context

ADR 0084 lets a fiig package back `Database` by providing
`fiig.sql.SqlDriver`, with the runtime pooling its connections and sending
`BEGIN`/`COMMIT`/`ROLLBACK`. This ADR is the driver Milestone 7 asked for:
Postgres's frontend/backend protocol, version 3.0, written in fiig over
`Socket`.

Two things fiig lacks are needed first. Postgres's default authentication
since version 14 is SCRAM-SHA-256 (RFC 5802, RFC 7677), which needs
PBKDF2-HMAC-SHA-256 with the server's iteration count (4096 by default)
and an XOR of two 32-byte strings. fiig has HMAC (ADR 0075) but no PBKDF2
and no bitwise operators. XOR could be spelled with arithmetic on each
byte, but 4096 rounds of it in a tree-walking interpreter would cost more
than the query.

### How existing drivers make the same choices

| | pgx (Go) | Postgrex (Elixir) | node-postgres | PgJDBC (Java) |
|---|---|---|---|---|
| Statements with values | extended protocol, statements cached | extended, cached | extended, unnamed | extended, cached after 5 uses |
| Placeholders a program writes | `$1` | `$1` | `$1` | `?`, rewritten to `$n` |
| Result format | binary | binary | text | text by default |
| Authentication | SCRAM, MD5, cleartext | SCRAM, MD5 | SCRAM, MD5 | SCRAM, MD5 |
| TLS | `sslmode`, six levels | `ssl: true` + options | `ssl` + options | `sslmode` |

## Decision

### 1. `fiig.postgres`, built into the toolchain

The driver is a package at `compiler/stdlib/postgres`, embedded in the
`fiig` binary as `fiig.http` is, path `fiig.postgres`, providing
`fiig.sql.SqlDriver` with a capability group `Postgres`. An application
depends on it and binds `Database` to it:

```json
{"Database": {"type": "driver", "driver": "fiig.postgres",
              "settings": {"host": "db.internal", "user": "app",
                           "password": "${DB_PASSWORD}", "database": "orders"}}}
```

### 2. Settings

| Setting | Default | |
|---|---|---|
| `host` | required | |
| `port` | `5432` | |
| `user` | required | |
| `password` | none | needed unless the server trusts the connection |
| `database` | the user's name | as Postgres defaults it |
| `sslmode` | `verify-full` | `verify-full` or `disable` (§5) |
| `applicationName` | none | shown in `pg_stat_activity` |
| `statementCacheSize` | `100` | prepared statements kept per connection; `0` keeps none (§3b, amended 2026-11-21) |

Each is a separate string. A `postgres://` URL is left out: it needs
percent-decoding to carry a password with `@` or `/`, and adds nothing the
separate keys don't. The startup message also sets `client_encoding` to
`UTF8` and `DateStyle` to `ISO`, so text results don't depend on the
server's configuration.

### 3. Every statement is one round trip of the extended protocol

`run` writes Parse (the unnamed statement, every parameter type left for
the server to infer), Bind, Describe, Execute, and Sync in one write, then
reads until ReadyForQuery. The simple query protocol has no parameters, so
values would have to be spliced into SQL text, which ADR 0082 §3 exists to
prevent. One consequence: a statement is one statement, since Parse
refuses two, so a schema of several tables is several `execute` calls.

Named, cached prepared statements (pgx, Postgrex, PgJDBC) save the server
a parse per call, but a cache must be per connection, invalidated when the
schema changes, and is state the runtime's pool would have to keep. Left
for later (open question 1).

*(Amended 2026-11-21: `fiig.postgres` now provides ADR 0084 §1a's
`SqlConnectionDriver`, so `run` is `execute` and `connect` is `open`,
which keeps BackendKeyData's body as the connection's `cancelKey` (§3a), and
named statements are cached, §3b.)*

### 3a. A statement past its deadline is cancelled

*(Amended 2026-11-21: closes open question 3.)* When the pool cancels a
connection's statement (ADR 0084 §3a), `cancel` connects to the same host
and port, negotiates as `open` does (with `verify-full`, SSLRequest and
`Socket.startTls`; refused, it sends nothing), writes CancelRequest
(`Int32 16`, `Int32 80877102`, then the key: the backend's process ID and
secret), and reads once, which returns when the server closes the
connection, as libpq waits. The server reads a CancelRequest after an
SSLRequest as it reads a startup message, and libpq 17's encrypted
cancellation (`PQcancelCreate`) negotiates the connection's own
`sslmode` the same way, so under `verify-full` a cancel never goes in the
clear. (The dogfood below ran without TLS; that path repeats `open`'s.)
The key is kept as bytes, not two
integers, since protocol 3.2 lets it be longer than 8. A server that sent
no BackendKeyData gets no cancel.

Cancelling is best effort, as everywhere: the statement may finish first,
and the server answers no CancelRequest. Dogfooded on Postgres 17:
`SELECT pg_sleep(10)` under a 500 ms deadline faults at 500 ms and is gone
from `pg_stat_activity` within 100 ms; without the cancel it stays active
for its 10 seconds. The same inside a `transaction Database` block. Tests:
`tooling/runprogram`'s fake server checks the key and that the connection
is replaced; `postgres_live_test.go` runs both cases against a real server
when `PGHOST` names one.

### 3b. Prepared statements are cached per connection

*(Amended 2026-11-21: closes open question 1.)* Each connection keeps up
to `statementCacheSize` prepared statements (default 100), in its
`SqlConnection` (ADR 0084 §1a): SQL text, the name the driver gave it
(`fiig_s0`, `fiig_s1`, ... from a per-connection count, so a name is
never reused), and whether the server still has it as prepared. Least
recently used first. `execute`:

- **A statement it has**: Bind (by name), Describe, Execute, Sync. No
  Parse, so the server neither parses nor plans the text again.
- **A new one**: if the cache is full, Close the least recently used, then
  Parse it by its new name, then Bind ... Sync, all in the one write, so a
  new statement still costs one round trip. It joins the cache only if
  ParseComplete came back; a Parse that failed (a syntax error, a failed
  transaction) left nothing on the server to remember.
- **One the server no longer has as cached**: `26000` (no statement by
  that name: the program ran `DEALLOCATE ALL` or `DISCARD ALL`) or `0A000`
  "cached plan must not change result type" (a schema change altered its
  result columns; the message is what pgx and PgJDBC match too). The entry
  is marked, and the statement is closed and parsed again under its name
  and run once more, in the same call. Both errors come at Bind, before
  anything executes, so running again repeats nothing. Only when
  ReadyForQuery said idle: inside a transaction the server refuses all
  but ROLLBACK after an error, so the error is the call's, as pgx leaves
  it, and the statement is parsed again on its next use.
- **`statementCacheSize` 0** is §3 as first written: every statement is
  the unnamed one, parsed each time.

Where the cache lives was open question 1's choice: in the driver keyed
by connection, or in the pool. Neither as asked: the driver decides what
to cache and speaks the messages, but it has no mutable state to key, so
the cache is a value the pool keeps per connection and hands back to each
`execute` (ADR 0084 §1a). It is per connection because a prepared
statement is (two connections are two backends), and it dies with its
connection: a discarded or expired connection takes its value with it,
and the server forgets the statements with the backend. A setting in the
capabilities file, not a `config` declaration read from the environment
as `fiig.http`'s limits are: it belongs to one `Database` binding (two
pools may want different sizes), and a driver's gateways run outside the
program's call graph, which is what decides the `config` a program reads
before it starts. An operator who wants it from the environment writes
`"statementCacheSize": "${PG_STATEMENT_CACHE_SIZE}"`.

Dogfooded on Postgres 17 through a proxy that records the frontend's
messages (`tooling/runprogram/postgres_live_cache_test.go`): 5 runs of one
query parse it once, and `pg_prepared_statements` shows the cached ones; a
`SELECT *` survives `ALTER TABLE ... ADD COLUMN` (prepared again, the new
column returned) outside a transaction, faults with the server's `0A000`
inside one and is prepared again after it, and survives `DEALLOCATE ALL`,
for 4 Parses of the `SELECT` across 7 runs, all on one connection. The fake
server's tests cover eviction order, an unparsable statement left out of
the cache, and size 0. `relay-notes`' transactional batch, through `fiig
run`, binds its one cached `INSERT` three times.

- **Found by it, fixed:** `copy(...)` of another package's model resolved
  the overridden field's type in the caller's package, so fiig.postgres's
  `copy(on, statements: ...)` of `fiig.sql`'s `SqlConnection` reported
  `unresolved type "List<SqlStatement>"`, at a position in the other
  package's file. It now resolves as reading the field already did
  (`compiler/checker`'s `TestCopyOfAnotherPackagesModelResolvesItsFieldThere`).

### 4. `?` is rewritten to `$n`

Programs keep writing `?`, so the same SQL runs on SQLite and on Postgres,
and the driver numbers them before Parse, as PgJDBC does. The rewrite skips
what isn't SQL code: `'...'` strings (with `''`), `E'...'` strings (with
backslash escapes), `"..."` identifiers, `--` and nested `/* */`
comments, and `$tag$...$tag$` bodies. JSONB's `?`, `?|`, and `?&`
operators are written `??`, `??|`, and `??&`, PgJDBC's escape. SQL with no
`?` is sent as written, so a program may also write `$1`. A count that
doesn't match the values is the server's error to report.

### 5. TLS: `verify-full` or `disable`

With `verify-full`, the driver sends SSLRequest and, on `S`, calls
`Socket.startTls(connection, host)`, which verifies the certificate and the
host name (ADR 0060); on `N` it refuses to continue. With `disable` it
never asks. libpq's `require`, which encrypts without verifying, is left
out: fiig's TLS always verifies, and `require` only protects against a
passive eavesdropper.

### 6. Authentication

- **SCRAM-SHA-256**, the default since Postgres 14, including checking the
  server's final signature with `crypto.equal`, so a server that doesn't
  know the password is refused.
- **Cleartext password**, only on a TLS connection; on a plain one the
  driver refuses rather than send it.
- **Trust** (AuthenticationOk with no exchange).
- **MD5 is refused** with an error naming it: it needs MD5, which fiig
  doesn't have, and Postgres has deprecated it.

SCRAM-SHA-256-PLUS (channel binding) is left out: it needs the server
certificate's hash, which `Socket` doesn't expose. The password is sent as
its UTF-8 bytes without SASLprep (ADR 0067 §8: fiig doesn't normalize),
which matches Postgres for any password that is unchanged by
normalization, every ASCII one included.

A server's iteration count above 1,000,000 is refused as a protocol
error, so a hostile server can't make each connection cost minutes.

### 7. Two builtins for SCRAM

```fiig
crypto.pbkdf2Sha256(password: Secret | bytes, salt: bytes, iterations: int) -> bytes   // 32 bytes
proof = clientKey.xor(clientSignature)                                                 // bytes
```

`crypto.pbkdf2Sha256` is RFC 8018's PBKDF2 with HMAC-SHA-256 and a 32-byte
output, pure, so rules may call it, as they may call `hmacSha256`. It
faults for fewer than 1 or more than 10,000,000 iterations. ADR 0075
chose Argon2id over PBKDF2 for storing passwords, and that stands; this is
for protocols that specify PBKDF2. `bytes.xor(other)` XORs two byte
strings of the same length and faults when the lengths differ.

### 8. Text results, read by type

Results are requested in text format. Each column's type comes from
RowDescription, and the driver reads its text into a `SqlValue`:

| Postgres type | `SqlValue` |
|---|---|
| `int2`, `int4`, `int8` | `Int` |
| `float4`, `float8` | `Float`; `NaN` and `Infinity` as `Text`, so a `float` field refuses them by name |
| `numeric` | `Text`, which a `decimal` field reads exactly (ADR 0082 §7) |
| `bool` | `Bool` |
| `bytea` | `Bytes`, from its `\x` hex form |
| NULL | `Null` |
| anything else | `Text` (`text`, `varchar`, `uuid`, dates, timestamps, `json`, arrays, enums) |

Binary format (pgx, Postgrex) is faster and needs a decoder per type,
`numeric`'s among the hardest; text needs one parse per type and keeps a
`numeric` exact without one. Parameters are sent as text, except `Bytes`,
which is sent in binary format so it needs no escaping.

`affected` is the count in CommandComplete's tag (`INSERT 0 3`, `UPDATE
2`, `DELETE 1`); a query's is 0.

### 9. Errors leave the connection ready

An ErrorResponse becomes `Server(code, message)` with its SQLSTATE and
message, and the driver keeps reading to ReadyForQuery before returning,
so the connection is ready for the next statement and the pool may keep
it (ADR 0084 §3). An error inside a transaction leaves Postgres refusing
further statements until ROLLBACK, which the runtime sends when the block
faults. NoticeResponse, ParameterStatus, and NotificationResponse may
arrive between any two messages and are skipped. A closed or failed
socket is `Broken`; a message the driver can't read is `Protocol`.

Deadlines need nothing in the driver: a `Socket` read or write stops when
the program call's deadline passes (ADR 0060 §4), and the pool discards
the connection (ADR 0084 §3). *(Amended 2026-11-21: and cancels the
statement through the driver's `cancel` first, §3a.)*

## Consequences

- New package `compiler/stdlib/postgres`; `crypto.pbkdf2Sha256` and
  `bytes.xor` join their tables (`compiler/types`, `compiler/runtime`).
- `libraries/sql` stays for SQLite; Postgres no longer goes through it.
  The `fiig` binary needs no Go Postgres driver.
- Tests run the driver against scripted sockets (`socket-script`) for the
  protocol and SCRAM, with RFC 7677's test vector, and against a real
  Postgres for the dogfood.
- The corpus gains a Postgres configuration for an existing example.

## Open questions

1. ~~**Cached prepared statements** (§3), and the cache's place: in the
   driver, keyed by connection, or in the runtime's pool.~~ Done
   2026-11-21 (§3b): the driver decides, the pool keeps the value (ADR 0084
   §1a).
2. **Custom certificate authorities** (ADR 0060 open question 2).
   *Answered by ADR 0086, on `Socket`, for every TLS connection.*
   Managed databases often sign with their own CA (Amazon RDS's isn't in
   the system roots), so `verify-full` against one needs a CA bundle, set
   in the capabilities file, not in fiig source.
3. ~~**Cancelling a running query.** When the deadline passes, the socket
   closes but the server finishes the query; Postgres's CancelRequest needs
   a second connection with the backend key.~~ Done 2026-11-21 (§3a, ADR
   0084 §1a, §3a).
4. **SASLprep** of passwords (§6) and SCRAM channel binding.
5. **`COPY`, `LISTEN`/`NOTIFY`, and binary-format results.**

## Implementation notes (2026-11-17)

- **The package** is `compiler/stdlib/postgres`: the `Postgres` group's
  three gateways only move bytes; each protocol step is a conversion
  (`handshakeStep`/`authStep` for §6, `replyStep` for §9,
  `statementMessages`, `rowDescription`, `dataRow`, `columnValue`,
  `numberPlaceholders`, `scramFinal`). With no package-internal visibility
  (ADR 0079 open question 1), they are all public, as `fiig.http`'s are.
- **Rows accumulate in `run`**, not in the reply state, so a large result
  is appended in place (ADR 0057) rather than copied per row.
- **Builtins:** `crypto.pbkdf2Sha256` (Go's `crypto/pbkdf2`) and
  `bytes.xor`. The runtime's check of `crypto` arguments learned `int`,
  which no member had taken before.
- **Tests** (`tooling/runprogram/postgres_test.go`): RFC 7677's SCRAM
  vector; a hostile server-first (foreign nonce, 5,000,000 iterations, no
  salt); the `?` numbering table (strings, `E''` escapes, identifiers, both
  comment kinds, nested comments, dollar quotes, `??`, non-ASCII); column
  reading by OID; and a fake server speaking the wire protocol, with an
  independent Go SCRAM: login, typed rows through ADR 0082's reader, every
  `SqlValue` kind bound (text and binary formats), a server error keeping
  the connection, BEGIN/COMMIT on one connection, a notice skipped, and the
  refusals (wrong password, a server that can't prove the password, MD5,
  cleartext on plain TCP, no TLS under `verify-full`, bad settings).
  `compiler/runtime`'s `TestPbkdf2AndXor` checks RFC 7914's vector.
- **Corpus:** `examples/relay-notes` depends on `fiig.postgres` too and has
  `caps.postgres.json`, reading the libpq variable names (`PGHOST`, ...).
- **Dogfooded on Postgres 17.12** (the `postgres:17` image, SCRAM-SHA-256
  password encryption), with a `fiig` binary built from `cmd/fiig`:
  `relay-notes` through `caps.postgres.json` (create, a transactional
  batch, typed reads with non-ASCII text, a batch rolled back on `Err`
  and confirmed absent with psql, no session left idle in a transaction,
  a duplicate key faulting as `23505`, `None`); a wrong password
  (`28P01`); `verify-full` against a server without TLS; every column type
  round-tripped (`numeric(10,2)` as exactly `19.90`, an `int8` above 2^53,
  `bytea`, `bool`, NULL, a date as text, `'?'` in literals left alone, a
  stored `NaN` refused by name); and 12 concurrent requests through `fiig
  serve` on a pool of 4, which Postgres saw as 4 connections and which
  took the expected 0.9 s.
- **Found by dogfooding:** `fiig serve` registered no default `Socket`,
  though `fiig run` did, so a served program using `fiig.postgres` or
  `fiig.http` failed until its capabilities file named `Socket`; the real
  `Socket` is now a default beside `Clock` and `Random`
  (`runprogram.RegisterDefaults`). And every `Protocol` error was
  prefixed "couldn't read the server's reply", wrong for a setting or a
  refused TLS; the driver's reason is now the message.
