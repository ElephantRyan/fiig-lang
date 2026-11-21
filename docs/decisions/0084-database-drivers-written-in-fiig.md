# ADR 0084: Database Drivers Written in fiig, and the Runtime's Pool

Status: Accepted and implemented (2026-11-16; see Implementation notes)
Date: 2026-11-16
Related: ADR 0010 (one capability per gateway), ADR 0015 (implicit
transaction binding), ADR 0025 (`libraries/sql`), ADR 0027 (declarative
capability configuration), ADR 0042 (the socket floor), ADR 0043
(interfaces and `provides`), ADR 0060 §5 and "Relation to `transaction`"
(connections owned by the call; C1), ADR 0074 (`fiig.http`, a package
built into the toolchain), ADR 0076 (request deadlines), ADR 0082 (typed
`Database` calls). Milestone 7: "Postgres wire protocol in fiig, replacing
`libraries/sql`".

## Context

Milestone 7 moves the native boundary down to the socket, and the next
step is a Postgres driver written in fiig. The protocol itself is within
reach: `examples/postgres-wire` already upgrades a connection to TLS and
parses frames with binary patterns, and `crypto`, `Random`, and `bytes`
cover SCRAM-SHA-256 authentication. What fiig can't do yet is keep a
connection between requests:

- **ADR 0060 §5 closes every socket when the call that opened it ends.**
  For Postgres that means every request pays a TCP connect, a TLS
  handshake, and a SCRAM exchange, and the server forks a backend process
  for it. No production driver works that way.
- **A transaction needs one connection across several calls.**
  `transaction Database { }` runs many gateway calls, and Postgres scopes
  `BEGIN`/`COMMIT` to a connection.
- **`Database.queryOne`'s result type comes from its call site** (ADR
  0082 §5), which no fiig `interface` can declare: fiig has no generics.
  So a fiig package can't simply "be" `Database`.

ADR 0060 deferred the first two to "the capability-state ADR". This is it.

### How drivers are split elsewhere

Every widely used Postgres driver is written in its own language, not
over libpq: pgx (Go), Postgrex (Elixir), node-postgres, PgJDBC (Java),
rust-postgres. All of them separate three jobs:

| Job | Postgrex | Go | Java |
|---|---|---|---|
| Wire protocol, auth, types | `Postgrex.Protocol` | pgx | PgJDBC |
| Pool, checkout, holding a connection for a transaction | `DBConnection` (generic, shared by MySQL and others) | `database/sql`'s pool, or pgxpool | HikariCP |
| What a program calls | `Postgrex.query/3`, `Repo` | `db.Query` | JDBC `Connection` |

Postgrex is the closest model for fiig: its protocol is written with
Erlang binary patterns, as fiig's would be, and the pool and transaction
bookkeeping live in a separate library every driver implements a small
behaviour for (`connect`, `handle_execute`, `handle_begin`, ...).

## Decision

### 1. A driver is a fiig package that provides `fiig.sql.SqlDriver`

A new built-in package, `fiig.sql`, embedded in the toolchain as
`fiig.http` is (ADR 0074), declares the contract a driver fulfils:

```fiig
interface SqlDriver {
    connect(settings: Map<string, string>) -> Result<Connection, DatabaseError>
    run(connection: Connection, sql: string, values: List<SqlValue>) -> Result<SqlResult, DatabaseError>
    close(connection: Connection) -> None
}
```

and the plain data the contract passes:

```fiig
enum SqlValue {
    Null
    Int(value: int)
    Float(value: float)
    Text(value: string)
    Bytes(value: bytes)
    Bool(value: bool)
}

model SqlResult {
    columns: List<string>
    rows: List<List<SqlValue>>
    affected: int
}

enum DatabaseError {
    Server(code: string, message: string)   // the server's error, with its SQLSTATE
    Broken(failure: SocketError)            // the connection failed or closed
    Protocol(reason: string)                // the server sent something the driver can't read
}
```

A driver package declares a `capability` group whose three gateways
`require Socket`, and names it in its manifest, as ADR 0043 providers do:
`"provides": {"fiig.sql.SqlDriver": "Postgres"}`. `Connection` is
Socket's opaque handle (ADR 0060 §2); a driver keeps whatever per-
connection state it needs by reading from the server, not in fiig values.
*(Amended 2026-11-21: or in a value the pool keeps for it, §1a.)*

### 1a. A driver may keep a value per connection

*(Amended 2026-11-21.)* Two things a driver needs live as long as one
connection and no longer: the key a CancelRequest names (§3a, ADR 0085
§3a) and the statements the server has prepared on it (ADR 0085 §3b).
Neither can be read back from the server cheaply, so `fiig.sql` gains a
second contract, which a driver provides instead of `SqlDriver`:

```fiig
interface SqlConnectionDriver {
    open(settings: Map<string, string>) -> Result<SqlConnection, DatabaseError>
    execute(on: SqlConnection, sql: string, values: List<SqlValue>) -> SqlRun
    cancel(settings: Map<string, string>, key: bytes) -> None
    close(connection: Connection) -> None
}

model SqlConnection {
    connection: Connection
    cancelKey: bytes
    // and the statement cache's fields, ADR 0085 §3b
}

model SqlRun {
    connection: SqlConnection   // the value after the statement, failed or not
    result: Result<SqlResult, DatabaseError>
}
```

The pool keeps the `SqlConnection` that `open` returned beside the
connection, passes it to `execute`, and keeps the one `execute` returns
for the next call. That is all the state is: a value threaded through
calls, never shared, since a connection carries one statement at a time
(§3, §5). It dies with its pooled connection, closed or discarded, so it
is per connection by construction, and nothing in the language becomes
mutable. When a manifest provides both contracts the pool takes this one;
`SqlDriver` stays for a driver with nothing to keep (`examples/sql-relay`).

- **Typed fields in `fiig.sql`, not `state: bytes`** as ADR 0114's
  `SessionProtocol` threads. `bytes` is the generic-free choice there
  because a session protocol's state is anything. A SQL driver's is not:
  a cancel key and a set of prepared statements are what pgx, PgJDBC,
  and MySQL's drivers keep (MySQL's `KILL QUERY` also needs a second
  connection and the first one's ID), and a typed list is read without
  decoding it on every call.
- **Not `execute` returning `Result<SqlRun, ...>`.** The value must come
  back when the statement fails too: a `Parse` that succeeded before an
  `Execute` that failed left a prepared statement on the server, and a
  driver that forgot it would prepare it again under a new name, leaking
  one per failure.

### 2. Programs still call `Database`; the runtime calls the driver

Nothing a program writes changes. `Database.execute`, `queryOne`,
`queryMany`, and `transaction Database { }` keep ADR 0082's types and
checks. The capabilities file binds `Database` to a driver (ADR 0027):

```json
{"Database": {"type": "driver", "driver": "fiig.postgres",
              "settings": {"url": "${DATABASE_URL}"},
              "maxConnections": 10}}
```

The runtime turns each `Database` call into a `run`: it binds the values
as ADR 0082 §4 says (`decimal` as `Text` in plain notation, a payload-free
enum as `Text` of its text form, `None` as `Null`), and reads the
returned `SqlResult` into the row type with ADR 0082's reader, so a
driver never sees fiig's row types and the reader stays one place.
`execute` returns `affected`.

Go-backed providers keep working: `libraries/sql`'s `"type": "sql"` is
another way to bind `Database`, and stays the way to reach SQLite (open
question 1).

### 3. The runtime owns the pool

A `Database` bound to a driver is a pool the runtime creates when the
program starts (`fiig serve`, `fiig run`) and closes when it stops:

- **Size.** At most `maxConnections` (default 10, as HikariCP's; pgxpool
  defaults to the CPU count, at least 4). Connections open on first use,
  not at start.
- **Checkout.** A `Database` call outside a transaction takes an idle
  connection, or opens one, or waits for one. The wait is bounded by the
  call's deadline (ADR 0076's request timeout); running out of it is a
  fault naming the pool, as an exhausted pool is in every driver above.
- **Return.** After `run` returns `Ok` or `Err(Server(...))`, the
  connection goes back to the pool: the server reported an error and the
  connection is still in a known state. After `Err(Broken(...))`,
  `Err(Protocol(...))`, a fault, or a cancelled call, it is closed and
  discarded, since the driver may have stopped mid-message.
- **Idle.** A connection idle for 5 minutes is closed (`idleTimeout`),
  and none is kept past 30 minutes (`maxLifetime`), so failovers and
  server restarts are picked up. Both are settings beside
  `maxConnections`.

### 3a. A statement past its deadline is cancelled

*(Amended 2026-11-21.)* A call whose deadline passes while the driver runs
a statement stops at once: `Socket` reads end at the deadline (ADR 0060
§4), the call faults, and the connection is discarded (§3). Before, the
server went on running the statement to its end. Now, for a driver that
provides `SqlConnectionDriver`, the pool calls its `cancel` with the
driver's settings and the connection's `cancelKey`, then closes the
connection.

- **The pool decides, the driver sends.** Only the pool knows the
  deadline passed mid-statement and that the connection is going; only
  the driver knows the wire format. And a driver couldn't send it from
  inside `execute`: every `Socket` call there fails once the call's
  deadline has passed. `cancel` runs under a deadline of its own, 5
  seconds, on a connection of its own that it closes.
- **Discarded, not drained to ReadyForQuery.** The bytes `execute` had
  read and not yet framed were in its local buffer, gone when it
  returned, so where the connection stands in the protocol is unknown.
  A connection is cheaper to replace than that is to recover; pgx closes
  a connection whose context was cancelled too.
- **The call doesn't wait for it.** The cancel runs in the background, so
  a server that can't be reached doesn't add a second wait to the call
  that already timed out. The pool's slot stays taken until the cancel
  is sent, so `maxConnections` still bounds the backends the pool keeps
  busy on the server.
- **In a transaction too:** the block's pinned connection (§5) is
  cancelled and discarded when the block ends, and the server rolls the
  transaction back as the connection closes.

### 4. A connection opened for the pool belongs to the pool

ADR 0060 §5 is amended: a `Socket` connection opened while the runtime
runs a driver's `connect` belongs to the pool, not to the call that
happened to need it, and is closed by the pool (§3). Every other
connection still belongs to its top-level call. A program never holds a
pooled `Connection`: the driver's gateways see it, and `Database` calls
don't return it.

### 5. `transaction Database` holds one connection

Entering `transaction Database { }` checks out one connection and runs
`BEGIN` on it through `run`; every `Database` call in the block uses that
connection (ADR 0015's implicit binding); leaving the block runs `COMMIT`,
or `ROLLBACK` when the block ends in an `Err` or a fault, exactly the
outcomes `compiler/runtime/transaction.go` already decides. A `COMMIT`
the server refuses (a serialization failure) is reported like the error
that refused it (§6). The connection then goes back to the pool by §3's
rule.

A driver needs no transaction operations of its own: `BEGIN`, `COMMIT`,
and `ROLLBACK` are SQL, and the runtime sends them. Isolation levels and
savepoints are out (open question 3).

### 6. Errors stay faults in programs, for now

A driver returns `DatabaseError` as a value, so the runtime can tell a
server error (keep the connection) from a broken one (discard it). To a
program, every `DatabaseError` is still a fault, as a `libraries/sql`
error is today, and its message names the SQLSTATE: `Database.execute:
23505 duplicate key value violates unique constraint "accounts_pkey"`.

Turning expected failures into values (`Result<int, DatabaseError>` from
`execute`, or a narrower `Conflict`) changes every `Database` call site
in every program, so it is its own decision, ADR 0082 open question 2.
This ADR makes it possible, since the error now arrives typed.

### 7. C1 is unchanged

ADR 0060 noted that once `Database` runs over `Socket`, C1 (no network
gateway inside `transaction Database`) and the driver collide. They
don't: C1 and C4 check the gateways a *program* calls inside the block,
and those still require `Database`. The driver's own `Socket` calls are
made by the runtime on the pool's behalf, outside the program's call
graph, as `libraries/sql`'s are today.

### 8. What a driver's code may do

A driver is ordinary fiig, checked like any package: its gateways require
`Socket`, may call its own rules and conversions, and may read `Clock` and
`Random` (SCRAM's nonce). The runtime calls `connect`, `run`, and `close`
on its own interpreter state, with the deadline of the program call that
caused them, so a slow server can't outlast a request.

## Alternatives considered

- **The driver owns its pool, in fiig.** Every driver would rewrite
  checkout, waiting, idle eviction, and discard-on-error, and fiig would
  need long-lived mutable state shared across concurrent requests, which
  the language doesn't have and Postgrex, pgx, and JDBC all avoid by
  putting the pool in one shared library.
- **No pool: a connection per request,** as `fiig.http` does (ADR 0074
  §8). Simple, and fine for HTTP to a load balancer, but a Postgres
  connection costs a server process and several round trips, and
  transactions still need §5.
- **Make `Database` an ordinary fiig interface** a driver provides
  directly. Impossible without generics: `queryOne`'s result type comes
  from its call site (ADR 0082 §5). The fixed `SqlResult` shape is what a
  non-generic contract can carry.
- **A connection the program holds** (`conn = Database.connect()`,
  passed around). Every gateway signature would carry it, and forgetting
  to release it is the leak ADR 0060 §5 exists to prevent.

## Consequences

- New built-in package `fiig.sql` (the interface and its data types); a
  `"driver"` capability type in `compiler/runtime`'s factory registry
  (ADR 0027) with the pool, checkout, and transaction pinning; ADR 0060
  §5 amended (§4).
- The pool settings live in the capabilities file; one written as
  `${NAME}` is listed by `fiig config` like any capabilities-file variable.
- The second ADR, `fiig.postgres` (protocol, SCRAM, type mapping,
  placeholders), builds on this one. `fiig.http` could later use a pool
  of the same kind; this ADR doesn't generalize one.

## Open questions

1. **SQLite.** It is a library linked into the process, not a protocol
   over a socket, so no fiig driver can replace `libraries/sql` for it.
   Milestone 7's exit criterion "no domain capability is backed by Go"
   either excepts embedded databases or drops SQLite from the toolchain.
2. **The program-level error channel** (§6; ADR 0082 open question 2).
3. *(Isolation levels: ADR 0088. Savepoints stay open.)*
   **Isolation levels and savepoints** (`transaction Database
   serializable { }`), and retrying a serialization failure.
4. **One pool per package** under ADR 0079's per-package bindings: two
   domains bound to the same database URL get two pools, which is
   correct but doubles the connections; sharing one is a later choice.

## Implementation notes (2026-11-16)

- **`fiig.sql`** is `compiler/stdlib/sql/`, embedded beside `fiig.http`.
  `DatabaseError`'s second variant is `Broken`, not `Connection` as first
  drafted, so it doesn't read as the prelude `Connection` type.
- **Runtime** (`compiler/runtime/driver.go`): `DriverFactory` is the
  `"driver"` capability type; `DriverProvider` is the pool and the
  `TransactionalProvider`. `runtime.New` hands the interpreter to every
  provider that runs fiig code, and the driver package is found lazily in
  `Interp.Packages` by its manifest's `provides`, which `types.Package`
  now carries (`Provides`, set by the loader). A driver's gateways run with
  the pooled connection's own resource owner in the context, which is all
  §4 needed: ADR 0060's owner was already per context. Idle and expired
  connections are closed by a janitor goroutine started with the first
  connection. `CapabilityRegistry.Close` stops pools; `fiig serve` calls it
  after draining requests and `fiig run` after its call.
- **`fiig run` never set `Interp.Packages`**, so a package the root
  doesn't import (the driver) couldn't run; it does now.
- **A real interpreter bug, found by the driver:** a bare name inside
  `Ok(...)` unwrapped a payload that was itself a `Result`, so
  `match decoded { Ok(reply) -> reply }` over a decoded
  `Result<Result<...>, JsonError>` returned the inner value, not the inner
  `Result`, while the checker typed it as the `Result`. The unwrapping is
  now only for a bare name at the top of an arm (`compiler/runtime`'s
  `TestNameInsideOkKeepsANestedResult`).
- **Testing without Postgres:** `examples/sql-relay` is a driver over a
  small relay protocol (length-prefixed JSON frames), served by
  `libraries/sql/sqlrelay` on SQLite with one session per connection; the
  relay rolls back a session whose client drops, as Postgres does.
  `libraries/sql`'s `driver_test.go` runs `examples/relay-notes` through it:
  one connection reused across calls, rollback on `Err`, a server error
  keeping the connection, a dropped connection discarded and replaced,
  expiry, and a full pool refusing at the deadline.
- **Dogfooded** with a `fiig` binary built from `cmd/fiig` and
  `fiig-sql-relay`: create, a transactional batch insert, typed reads, a
  rolled-back batch, a duplicate key faulting with its code, and `None`.
