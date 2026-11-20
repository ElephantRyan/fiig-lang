# ADR 0110: A Redis Client in fiig

Status: Proposed, implemented (2026-11-20)
Date: 2026-11-20
Related: ADR 0042 (the socket floor), ADR 0043 (capability groups), ADR
0058 (binary patterns), ADR 0060 (Socket), ADR 0061 (`\r`), ADR 0071
(config), ADR 0074 (`fiig.http`, the model this follows), ADR 0109
(connections kept between calls), ADR 0111 (stream sources, the first
user).

## Context

`fiig.redis` holds a RESP2 codec (`readResp`, `encodeCommand`, commit
702934d) and no gateway, so a program can't reach a Redis server yet. ADR
0042 named Redis's protocol as one of the things to write in fiig, and
ADR 0109 made it affordable: a Redis command answers in well under a
millisecond, so a new TCP and TLS connection for every command would cost
more than the command.

How clients elsewhere are shaped:

| Client | Commands | Connection |
| --- | --- | --- |
| redis-py | a method per command, plus `execute_command(*args)` | a pool per client object |
| go-redis | a method per command, plus `Do(ctx, args...)` | a pool per client |
| Redix (Elixir) | `command(conn, ["GET", "k"])` only | a process per connection |
| Lettuce (Java) | a method per command | one shared connection |

Redix shows that one generic operation is enough: every Redis command is
an array of strings, and the reply is one of six shapes. A method per
command is convenience, and Redis has over 200 commands.

## Decision

### 1. One gateway: `Redis.send`

```fiig
capability Redis {
    gateway send(args: List<string>) -> Result<RespValue, RedisError>
        requires Socket
}
```

```fiig
match Redis.send(["SET", "session:${id}", token, "EX", "3600"]) {
    Ok(_) -> ...
    Err(failure) -> ...
}
```

The reply is the codec's `RespValue`. A reply that is a server error
(`-ERR ...`, `-WRONGTYPE ...`) is `Err(RedisError.Server(message))`,
not `Ok(RespValue.Error(...))`: an error reply is a failed command, and
F1 then makes the caller handle it. Errors inside an array, which only
`EXEC` returns, stay `RespValue.Error`, since the command itself
succeeded.

```fiig
enum RedisError {
    Socket(failure: SocketError)       // connecting, writing, or reading failed
    Server(message: string)            // the server's error reply
    MalformedReply(reason: string)     // the bytes aren't RESP2
    InvalidUrl(reason: string)         // REDIS_URL can't be read
    InvalidCommand(reason: string)     // an empty command
}
```

The gateway is `send`, as `HttpClient.send` is (ADR 0074), because
`command` is already a keyword (a CLI route's).

Arguments are strings. A binary value (an image, a compressed blob)
would need a `bytes` form; nothing in the corpus asks for one yet.

### 2. Where the server is: `REDIS_URL`

```fiig
config RedisServer {
    url: Secret from env "REDIS_URL"
    timeoutMillis: int from env "REDIS_TIMEOUT_MS" = 10000
}
```

`timeoutMillis` bounds one call, connecting included, so a server that
stops answering can't hold a call that has no deadline of its own (`fiig
run`). It is longer than ADR 0111's 2 s blocking read.

`redis://[user[:password]@]host[:port][/database]`, or `rediss://` for
TLS: the form Heroku, Render, Upstash, ElastiCache, and redis-py's
`from_url` all use. It's a `Secret` because it usually carries the
password. The port defaults to 6379 and the database to 0. A URL the
client can't read is `Err(InvalidUrl(...))`, naming what is wrong but
never the URL itself.

### 3. A kept connection, set up once (ADR 0109)

A command first tries `Socket.reuse(host, port, tls, session)`. Only a
new connection sends `AUTH` (when the URL has a password) and `SELECT`
(when the database isn't 0). The `session` is `"user=<user> db=<n>"`,
which is what those two commands leave on the connection, so a
connection set up for database 2 never serves a call configured for
database 0.

After the reply, the connection is kept when the reply was whole with
nothing after it and the command left no state behind. It is closed
after `SUBSCRIBE`, `PSUBSCRIBE`, `SSUBSCRIBE`, `MONITOR`, `MULTI`,
`WATCH`, `SELECT`, `CLIENT`, and `HELLO`, which change the connection
for the next user.

**No resend on a stale connection**, unlike `fiig.http` (ADR 0109 §6).
`INCR`, `LPUSH`, and `XADD` aren't safe to send twice, and a list of the
commands that are would be long and easy to get wrong. ADR 0109 §4's
check before reuse catches most closed connections, and Redis doesn't
close idle connections by default (`timeout 0`).

### 4. Scope

In: one command per call over TCP or TLS, `AUTH` (with or without a
user, Redis 6 ACLs), `SELECT`, RESP2 replies.

Out: pipelining, `MULTI`/`EXEC` across calls, pub/sub (a connection that
carries a session, ADR 0109 §7), RESP3, Sentinel, Cluster redirects
(`MOVED`, `ASK`), and `bytes` arguments.

## Alternatives considered

- **A gateway per command** (`Redis.get`, `Redis.set`, ...). Over 200 of
  them, each a one-line wrapper. Conversions over `command` can add the
  common ones later without changing it.
- **Separate settings** (`REDIS_HOST`, `REDIS_PORT`, `REDIS_PASSWORD`).
  Every hosted Redis hands out a URL, so separate settings would make
  each deployment take it apart by hand.
- **`Ok(RespValue.Error(...))` for an error reply.** It reads as success
  at the call site, and a caller matching only `Err` would miss it.

## Consequences

- `fiig.redis` gains `Redis`, `RedisError`, `RedisServer`, and the URL
  parsing behind them; the codec is unchanged.
- ADR 0111's Redis Streams source is the first user, through `requires
  Redis`.

## Implementation notes (2026-11-20)

- `compiler/stdlib/redis/src`: `Redis` (the gateway), `RedisError`,
  `RedisServer`, `RedisTarget`, and the conversions it calls:
  `parseRedisUrl` (with percent-decoding for a user and password),
  `commandRequest` (AUTH and SELECT and the command in one write, their
  replies read in order), `sessionOf`, `commandReply`, `setupAccepted`,
  and `keepsConnection`.
- **A connection whose AUTH or SELECT was refused is closed, not kept**,
  since it isn't what its session says. The first draft kept it.
- Tests: `test/ParseRedisUrl.fiig` (the URL forms and refusals, the bytes
  a new and a kept connection write, error replies); the client against
  the in-memory Redis in `tooling/runprogram`'s
  `TestStreamAdapterOverRedisStreams`; and by hand against Redis 7 with a
  password and database 1, where three commands in one call used one
  connection and a wrong password came back as
  `Err(Server("WRONGPASS ..."))`.
- A package's own fiig tests can't run in place, since `fiig.` is
  reserved for the toolchain's packages; they run on a copy under another
  path.

## Open questions

1. **Pipelining.** Sending several commands before reading their replies
   needs a `List<List<string>>` form. That's worth adding when a program
   shows the round trips.
2. **Sentinel and Cluster.** Both discover servers at run time, so they
   are a second client shape, not a setting on this one.
