# ADR 0085: A Postgres Driver Written in fiig

Status: Accepted and implemented (2026-11-17; see Implementation notes)
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
the connection (ADR 0084 §3).

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

1. **Cached prepared statements** (§3), and the cache's place: in the
   driver, keyed by connection, or in the runtime's pool.
2. **Custom certificate authorities** (ADR 0060 open question 2).
   Managed databases often sign with their own CA (Amazon RDS's isn't in
   the system roots), so `verify-full` against one needs a CA bundle, set
   in the capabilities file, not in fiig source.
3. **Cancelling a running query.** When the deadline passes, the socket
   closes but the server finishes the query; Postgres's CancelRequest needs
   a second connection with the backend key.
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
