# Deploying a fiig application

`fiig serve` checks a program and serves one of its `adapter http`
declarations. It refuses a program with checker diagnostics, so a deploy
can't start a program that doesn't check.

```
fiig serve [-addr :8080] [-caps caps.json] [-env-file .env] [-allow-origin ORIGIN]... [-workspace DIR]... <root> <adapterName>
```

## Configuration

A program reads its settings from the environment, through `config`
declarations (ADR 0071), and a package declares its own, so an
application sets its dependencies' variables without declaring them.
`fiig config` lists every variable the program reads, from every package
and the caps file, which is the deployment's checklist:

```
$ fiig config -caps caps.json . accountsApi
ACCOUNTS_EXPORT_TOKEN  Secret   required        config Sessions.exportToken
SESSION_MAX_AGE        int      = 24 * 60 * 60  config Sessions.maxAge
ALLOW_ORIGINS          origins  optional        fiig serve: browser origins allowed to call it (ADR 0070)
LISTEN_ADDR            address  = :8080         fiig serve
MAX_BODY_BYTES         int      = 2097152       fiig serve: the most bytes a request body may have (ADR 0076)
MAX_REQUESTS           int      = 256           fiig serve: requests run at once, as many more waiting (ADR 0076)
REQUEST_TIMEOUT        duration = 30s           fiig serve: how long a request may run (ADR 0076)
```

`fiig serve` and `fiig run` read every value the entry point can reach
before they start, and refuse to start with **every** problem listed at
once: an unset required variable, a value that doesn't parse as its type
(`SESSION_MAX_AGE=86400s` isn't an int), an unset `${NAME}` in the caps
file, and a bad origin. A value is never re-read, so changing one means a
restart. A `Secret`'s value is never printed, in the report, a trace, or a
fault; it shows as `Secret(NAME)`.

**For development**, `-env-file .env` reads `NAME=value` lines (with an
optional `export `, `#` comments, and optional matching quotes) into the
same lookup. A variable set in the process wins, and no file is read
without the flag, so production behaves the same with one lying around.
Keep `.env` out of version control and commit a `.env.example`, which
`fiig config`'s output tells you how to fill.

**Renaming a dependency's variable.** When a package reads a name that
doesn't fit the deployment (two libraries wanting one database URL under
different names, or a company convention), the application's
`fiig.pkg.json` maps it, and the loader refuses a rename of a variable the
package doesn't read:

```json
"environment": {"acme.stripe": {"STRIPE_API_KEY": "CLIENTVERTICAL_STRIPE_API_KEY"}}
```

**One application per vertical.** A deployment that must not contain a
provider, for a legal reason say, is its own small application package
over shared ones, depending only on what it may use: it can't import the
rest, never loads its code, and `fiig config` lists none of its keys.
`examples/checkout-clientvertical` beside `examples/checkout-app` is the
worked case.

## Capabilities

`-caps` is the ADR 0027 capabilities file. Any string in it may name an
environment variable as `${NAME}`, read through the same lookup as config
(an `-env-file` included), so paths and secrets live in the environment,
not the file:

```json
{
  "Database": {"type": "sql", "driver": "sqlite", "dsn": "file:${DB_PATH}"}
}
```

The primitives need no entry: `Socket` is the real network, and `Clock`
and `Random` the real clock and the secure generator, unless the file
names a replacement.

### Certificates

Every TLS connection, a Postgres one, an `https` call through
`fiig.http`, or any other, goes through `Socket`, and `Socket`'s entry
says which certificate authorities to trust beyond the system's and which
client certificate to present to whom (ADR 0086):

```json
{"Socket": {"type": "socket",
  "trust": ["/etc/ssl/rds-global-bundle.pem", "${CORP_CA_FILE}"],
  "clientCertificates": [
    {"hosts": ["db.internal", "*.mesh.internal"],
     "certificate": "/var/run/secrets/db/tls.crt",
     "key": "/var/run/secrets/db/tls.key"}]}}
```

- `trust` lists PEM files of CA certificates, added to the system's. With
  `"systemRoots": false`, only the listed ones are trusted.
- A client certificate is presented only to the hosts listed for it: a
  name, or `*.domain` for one label below it. Any other server that asks
  gets none.
- Everything is a file, as a Kubernetes secret, cert-manager, or a Vault
  agent writes it; a path may come from the environment. Files are
  reread when they change, so a renewed certificate is used without a
  restart (a renewal that doesn't parse keeps the last good one, logged).
- A missing file, PEM with no certificate, or a key that doesn't match
  its certificate stops the program at startup, in the one configuration
  report.
- Host names are always verified, and nothing turns verification off; a
  development server's CA goes in `trust`.

`Socket` also keeps idle connections between requests, so `fiig.http`
doesn't pay a new TCP and TLS handshake for every call to the same host
(ADR 0109). The defaults suit most services; `idle` changes them:

```json
{"Socket": {"type": "socket", "idle": {"perKey": 10, "total": 100, "timeout": "90s", "maxLifetime": "10m"}}}
```

- `perKey` is how many idle connections are kept for one host, port, and
  package; `total` is the most kept across all of them.
- `timeout` closes a connection idle that long. Keep it below the idle
  timeout of the servers you call (or of a proxy between), or the first
  request after a quiet spell finds a closed connection. `fiig.http` sends
  a `GET`, `PUT`, or `DELETE` again when that happens, but never a `POST`
  or `PATCH`.
- `maxLifetime` closes a connection that old, counted from when it was
  opened, even if it was never idle, so a busy service still follows a
  DNS change or a load balancer's new addresses (ADR 0109 §5a). `"0s"`
  keeps connections however old they are.

`fiig.http` reads at most `HTTP_CLIENT_MAX_REPLY_BYTES` of a reply (16 MiB
by default; ADR 0074 §7a). A reply past it, or one whose `Content-Length`
says it will be, ends as `HttpError.ReplyTooLarge` and its connection is
closed. Raise it for a program that downloads large bodies, such as big
`fiig.s3` objects.

A capability can also be bound for one package's workflows, so each
domain of a modular monolith keeps its own database (ADR 0079 §6a); see
[A modular monolith in fiig](modular-monolith.md):

```json
{
  "Database": {"type": "sql", "driver": "sqlite", "dsn": "file:orders.db"},
  "packages": {"shop.payments": {"Database": {"type": "sql", "driver": "sqlite", "dsn": "file:payments.db"}}}
}
```

`Database` can also be backed by a driver written in fiig (ADR 0084): a
package the application depends on whose manifest provides
`fiig.sql.SqlDriver`. The runtime keeps a pool of its connections, opened
on first use and reused across requests, holds one for each `transaction
Database` block, and closes them when the process stops:

```json
{"Database": {"type": "driver", "driver": "examples.sqlrelay",
              "settings": {"host": "127.0.0.1", "port": "${RELAY_PORT}"},
              "maxConnections": 10, "idleTimeout": "5m", "maxLifetime": "30m"}}
```

Postgres is such a driver, built into `fiig` as `fiig.postgres` (ADR
0085): add `"fiig.postgres": "1.0.0"` to the application's dependencies
and bind it.

```json
{"Database": {"type": "driver", "driver": "fiig.postgres",
              "settings": {"host": "db.internal", "user": "app",
                           "password": "${DB_PASSWORD}", "database": "orders"}}}
```

Its settings are `host` and `user` (required), `port` (5432), `password`,
`database` (the user's name), `sslmode` (`verify-full`, the default, or
`disable`), and `applicationName`. It logs in with SCRAM-SHA-256, a
password in clear text only over TLS, or none, and refuses MD5. Programs
keep writing `?`; the driver numbers them `$1`, `$2`, ... (write `??` for a
literal `?`, as JSONB's operators need).

Inside a `transaction Database` block, a call that handles its conflicts
(ADR 0087) runs inside a savepoint, so Postgres's transaction survives a
conflict the program handled. On SQLite the same names come back as the
column list, `customers.email`, since SQLite names no constraint; match
on the variant to stay portable.

`settings` is handed to the driver's `connect` as strings; the other
three are the pool's, shown with their defaults. A call that finds every
connection busy waits for one until its request deadline. The
application's manifest lists `Socket` among its capabilities, since the
driver's gateways require it.

`fiig serve` takes every adapter the process serves, a dependency's by
its package path: `fiig serve . ordersApi shop.payments.paymentsApi`.
They share the listener, the limits below, and the CORS policy, and two
routes that would serve the same requests stop it at startup.

`-addr` defaults to `$LISTEN_ADDR`, then `:8080`, `-allow-origin` to
`$ALLOW_ORIGINS`, and the request limits to theirs (see below); all read
the same lookup. SIGINT or SIGTERM stops
accepting connections and gives in-flight requests `-shutdown` (10s) to
finish, then closes a driver's pooled connections.

## Logs and request ids

Every request has an id (ADR 0128). `fiig serve` uses the `X-Request-Id` the
caller sent when it is a token of at most 64 letters, digits, `-`, `_` and `.`,
so a trace through a proxy keeps its id, and makes one otherwise. The reply
carries it back as `X-Request-Id`.

- **A fault is a bare 500**, `{"error":"internal error","requestId":"..."}`.
  What went wrong is in the server's log under that id, and never in the
  reply, since a fault names declarations and can name data:
  `fiig: request cc18879cb00d86b2 faulted: division by zero (in boom)`.
- **An access line** for each request follows it:
  `fiig: GET /boom 500 3ms id=cc18879cb00d86b2`. The path is logged and the
  query isn't, since it can carry a token. `-access-log=false` turns the line
  off; faults are always logged.

## Stream adapters

An `adapter stream` is served the same way, beside any HTTP adapters:
`fiig serve -caps caps.json . OrderIntake ordersApi`. Its elements come
from the source the capabilities file binds (ADR 0111 §2):

```json
{"StreamSource": {"type": "source", "source": "fiig.redis"}}
```

`fiig.redis` reads Redis Streams at `REDIS_URL`, on Redis 6.2 or later.
Each process is one consumer, named after its host. Each route's `group` is
a consumer group, so every replica shares the work, and a producer writes
an element as `XADD orders * payload <json>`. The group is created the
first time the route runs, at the stream's first entry (`from earliest`)
or after its last (`from latest`); after that the route's `from` has no
effect, and renaming the group starts a new one.

- An element that a route retries, or that a stopped process left
  unsettled, comes back after `REDIS_STREAM_RETRY_AFTER_MS` (5 minutes).
  It must be longer than one element may take, `REQUEST_TIMEOUT` plus
  10 s; `fiig serve` halts the route at its first read when it isn't, and
  reads no more at once than fit in it (ADR 0111 §5a).
- A route that halts (its mapping says `halt`, a `retry` runs out of
  attempts with no `else`, the workflow faults, or a payload doesn't
  decode) stops `fiig serve` with an error naming the element, which stays
  unsettled. Your orchestrator restarts the process, and the element is
  delivered again, so the halt keeps showing until someone deals with it.
- Shutdown finishes the element in hand and settles it before stopping.
- A route receives up to `STREAM_BATCH_SIZE` elements at a time (1 to
  1000, default 16), waiting up to `STREAM_WAIT_MS` for the first (1 to
  60000, default 2000). Both apply to every stream route in the process,
  and a value out of range stops `fiig serve` from starting. A source may
  still take fewer, so each is handled before it would redeliver it (ADR
  0111 §5a).

`fiig.kafka` reads a Kafka topic as a consumer group (ADR 0117), with
`{"StreamSource": {"type": "source", "source": "fiig.kafka"}}`. The route's
`source` is the topic and its `group` the consumer group. It speaks
KIP-848's group protocol only, so the cluster must run **Kafka 4.0 or
later**; an older one halts the route at its first read, saying so. It
reads `KAFKA_BOOTSTRAP` and the rest of the producer's settings (below),
and an application that depends on `fiig.kafka` lists `Session` among its
manifest's capabilities, beside `Socket`.

- Each partition is handled in order. A `retry` seeks the partition back
  to the record and pauses it for `KAFKA_RETRY_AFTER_MS` (1000), so
  nothing after it is handled first; its attempts are counted by this
  process, so a restart or a rebalance starts them again at 1.
- An acked record's offset is committed within `KAFKA_COMMIT_INTERVAL_MS`
  (1000), and before a partition moves or the process stops, so a crash
  delivers again at most that long's records.
- `deadLetter "topic"` produces the record's value to that topic, keyed
  `<source>/<partition>/<offset>`, then acks it. The topic must exist.
- A partition with no committed offset starts at the log's first record
  (`from earliest`) or after its last (`from latest`).
- Shutdown leaves the group, so its partitions move to the other
  replicas at once. `KAFKA_FETCH_MAX_BYTES` (1 MiB) bounds one fetch.
- List several servers in `KAFKA_BOOTSTRAP`: the consumer tries them in
  order when the first can't be reached.

To investigate one element, such as the one a route halted on, replay it
by its id: the Redis entry id, or `partition/offset` for Kafka:

```
fiig run -caps caps.json -replay 1763704800000-0 . OrderIntake
```

`fiig run` fetches the element from the route's source, runs the route's
workflow on it with the program as it now is, and prints what the mapping
chose: `ack`, `retry` with the attempt, `deadLetter` and its target, or
`halt` and why. Add `-source <name>` when the adapter reads more than one
source, `-trace` for the call tree, and `-json` for an object. **Replay
settles nothing**: the element stays where it was. The workflow's own
effects do happen, so replaying an element that writes writes again. A
Kafka replay joins no consumer group, so it moves no partitions.

## Schedule adapters

An `adapter schedule` is served the same way too. Its jobs fire in UTC,
at the same times on every replica, and each firing is claimed first so
one replica runs it (ADR 0112 §4). The capabilities file says how:

```json
{"ScheduleLock": {"type": "lock", "lock": "fiig.redis"}}
```

claims each firing with a Redis key at `REDIS_URL`, and

```json
{"ScheduleLock": {"type": "local"}}
```

runs every firing in this process, for a single replica. A program with a
SQL database and no Redis can claim in the database instead, with
`{"ScheduleLock": {"type": "lock", "lock": "fiig.sqllock"}}` (the program
depends on `fiig.sqllock`): the lock keeps its
claims in a `fiig_locks` table, which it creates the first time it is used,
so there is nothing to migrate (ADR 0125). Each claim deletes the rows that
have expired, so the table holds only live claims and the last seven days
of `once` completions. With none of these, `fiig serve`
refuses to start the adapter.

- A firing missed while no process ran is skipped; the next one runs.
- A run is stopped when the next firing comes, and logged as an overrun.
- A failed run that the mapping sends to `done` is logged, and so is a
  run that faults; the next firing runs as usual. One the mapping sends to
  `halt` stops `fiig serve` with an error.
- `SCHEDULE_JITTER_MS` (0 to 300000, default 0) spreads out services that
  all fire at the same moment: each firing waits a random time up to it,
  never as long as the time to the next firing, before it is claimed and
  run. `firedAt` is still the scheduled time, and replicas still run each
  firing once.

To run a job now, by hand, run the route's workflow with `fiig run`,
passing the firing time the route would pass, in Unix milliseconds (ADR
0112 open question 2):

```
fiig run -caps caps.json -args '{"firedAt": 1763704800000}' . nightlyReport
```

The argument is named as the route names it. This run claims no lock, so
it runs alongside whatever the schedule fires.

## Startup adapters

An `adapter startup` checks the deployment before anything serves (ADR
0124), for what config can't say: that the bucket exists, that the database
answers. Name it to `fiig serve` like any adapter:

```
fiig serve . app.Api app.Preflight
```

Each `run -> workflow()` route runs once, after config is read and before
any listener, stream or schedule starts. All of them run at once, each for
at most 30 s.

- The mapping says what each outcome means: `proceed` starts the process
  (an `Err` mapped to it is still logged), and `halt` refuses to.
- `degrade` also starts the process, and tells the program it started that
  way (below).
- A fault or a check that runs out of time refuses to start, whatever the
  mapping says, since a check that crashed hasn't said the thing is fine.
- When any check refuses, nothing starts, and the one error names every
  check that did.
- Every replica runs its own checks, with no lock, unless the route says
  `once` (below). A check that changes something, such as a seed, runs on
  each replica at once, so it must be safe to repeat.
- It runs once. For whether the process is still healthy later, use a
  health route, which a readiness probe can call.

**A route's own deadline.** `run within "2m" -> slowCheck() { ... }` gives
that route 2 minutes in place of 30 s (a Go duration, above zero).

**One replica runs it, once per deploy.** `run once -> migrate() { ... }`
is claimed with the `ScheduleLock` the capabilities file binds, as a
schedule's firings are (`{"ScheduleLock": {"type": "lock", "lock":
"fiig.redis"}}`).
- One replica runs the route. The others **wait for it to finish** and then
  start, without running it, so a route that must finish before anything
  serves, such as a data migration, can say `once`.
- A route that proceeds is remembered for 7 days. One that halts, faults or
  times out isn't, so the replicas that wait take it over one at a time.
- A replica that can't reach the lock refuses to start, and one that waits
  twice as long as the route and 30 s allow gives up the same way.
- **A deploy** is named by `$FIIG_DEPLOY` when you set it (a release tag or a
  commit), and otherwise by a hash of the program's `.fiig`, `.sql` and
  `.json` files, so shipping changed code, which is what a migration is
  shipped with, runs the route again, and restarting the same code doesn't.

**Checking without serving.** `fiig serve -check . app.Preflight` runs only
the startup routes, then exits 0 when the program could start and 1 when it
could not, naming every check that refused. Nothing listens, no migration
is applied, and a `once` route runs here without claiming, so a CI job or a
Kubernetes init container can call it. It reads the same config, so a
variable that is missing fails it as it would fail `fiig serve`.

**Starting degraded.** A check that fails can let the process start and say
so, in place of `proceed`, which says nothing afterwards:

```fiig
adapter startup Preflight {
    run -> verifyStorage() {
        Ok(_) -> proceed
        Err(StorageError.NoSuchBucket(_)) -> halt
        Err(StorageError.Unreachable(_)) -> degrade
    }
}

config Health {
    storageDown: bool from startup "Preflight.verifyStorage"
}
```

- `degrade` is logged, and the process starts. `Health.storageDown` is
  `true` from then on, read like any config in an activity or a gateway, so a
  health route can report it and a route that needs the storage can refuse
  with a clear error.
- It is `false` when the route passed, or didn't run (the adapter isn't
  served). A test sets it with `fake Health { storageDown: true }`.
- It is decided once, at start. A storage that comes back later doesn't turn
  it off; for that, check again in the route, as a health endpoint does.
- On a `once` route, a degraded start isn't recorded as done, so the replicas
  waiting for it run the check themselves.

## Kafka

A program that sends to Kafka with `fiig.kafka` reads where the cluster
is from the environment (ADR 0113):

- `KAFKA_BOOTSTRAP`: `host:port[,host:port...]`, as `bootstrap.servers`.
- `KAFKA_TLS` (`false`), `KAFKA_CLIENT_ID` (`fiig`), and
  `KAFKA_TIMEOUT_MS` (30000), the most one send takes.
- `KAFKA_COMPRESSION`: `none`, `gzip`, `snappy`, `lz4` or `zstd`, as
  `compression.type` (ADR 0116). Unset sends batches uncompressed.

A send is **at least once**: every leader acknowledges with `acks=all`,
but there is no idempotence, so if a reply is lost after the broker
wrote the records, sending again writes them twice. A keyed record goes
to the partition a Java producer would choose. The topic must exist:
`fiig.kafka` never creates one. There is no SASL yet, so a cluster that
requires it can't be reached.

## Mail

A program that sends mail with `fiig.smtp` reads where the server is from
the environment (ADR 0120):

- `SMTP_URL` (a secret): `smtp://[user[:password]@]host[:port]` (587,
  upgraded with STARTTLS), `smtps://...` (465, TLS from the first byte),
  or `smtp+plain://...` (25, never TLS, for a server on the same machine).
  Percent-encode an `@` in the user: `smtps://ann%40gmail.com@smtp.gmail.com`.
- `SMTP_HELO_NAME` (`localhost`), and `SMTP_TIMEOUT_MS` (30000), the most
  one send takes.
- `SMTP_AUTH`: `auto` (the default), `plain`, `login` or `xoauth2`. `auto`
  uses XOAUTH2 when a token is set and the server offers it, then AUTH
  PLAIN, then AUTH LOGIN.
- `SMTP_OAUTH_TOKEN` (a secret): an OAuth 2.0 access token for XOAUTH2,
  sent for the URL's user, so the URL needs no password. It is read once
  at start and never refreshed: restart the program with a fresh token
  before it expires, from whatever rotates your secrets.

The server's certificate is checked against the capabilities file's
`trust`. Connections are kept between mails, one per login.

## Migrations

A package's schema lives in `migrations/<number>_<name>.sql`, plain SQL
(ADR 0096). `fiig serve` and `fiig run` apply pending migrations at
startup, each in its own transaction, and record them in the
database's `fiig_migrations` table. Two instances starting at once don't
both run one: the second waits on the first's record and finds it
applied. A deployment refuses to start, listing every problem, when an
applied migration was edited or is missing, or a new one is numbered
before one already applied.

To run migrations as their own step instead, as a release job:

```
fiig migrate -caps caps.json .           # apply, printing each one
fiig migrate -check -caps caps.json .    # list what's pending; exit 1 if any
fiig serve -migrate=check -caps caps.json . api   # refuse to start while any are pending
```

There are no down migrations: undo one with another.

## As a container

Each release publishes the image as `ghcr.io/elephantryan/fiig:<version>`.
The repository is private, so a server logs in once with a GitHub token
that has `read:packages`:

```
echo "$TOKEN" | docker login ghcr.io -u <github-user> --password-stdin
```

The repository's `Dockerfile` builds the same image: the static `fiig`
binary (no cgo; SQLite is pure Go) on distroless, running as a non-root
user.

```
docker build --build-arg VERSION=v0.1.0-alpha.1 -t fiig:v0.1.0-alpha.1 .
```

An application's own image adds its source:

```dockerfile
FROM ghcr.io/elephantryan/fiig:v0.1.0-alpha.1
COPY . /app
CMD ["serve", "-caps", "/app/caps.json", "/app", "myHttpAdapter"]
```

```
docker run -p 8080:8080 -v app-data:/data -e DB_PATH=/data/app.db --env-file prod.env my-app
```

## As a binary

Download a release archive, or with Go installed, from the repository
root:

```
cd cmd/fiig
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "-X fiig-lang/compiler/version.Version=v0.1.0-alpha.1" -o fiig .
```

The result is one static file; copy it and the application's source to the
server and run it under systemd or similar.

## Browser front ends on another origin (CORS)

A browser lets a page on one origin call a service on another only if the
service says so (ADR 0070). `fiig serve` says so for the origins it is
given, and for none by default:

```
fiig serve -allow-origin https://app.example.com -allow-origin http://localhost:5173 -caps caps.json /app api
ALLOW_ORIGINS=https://app.example.com,http://localhost:5173 fiig serve -caps caps.json /app api
```

- Write each origin exactly as a browser sends it: `scheme://host` or
  `scheme://host:port`, with no path or trailing slash, and without the
  scheme's default port. `null` is refused at startup.
- For per-tenant front ends, `https://*.example.com` (or
  `http://*.example.com:5173`) allows every subdomain of `example.com`,
  at any depth, with that scheme and port, and never `example.com`
  itself; list the apex beside it if it serves a front end too. `*` may
  only be the whole first label: `https://a.*.example.com`,
  `*.example.com`, `https://*` and `https://*.com` are refused at
  startup. A matching origin gets cookies like an exact one, so use a
  pattern only for a domain whose every host you control.
- A listed origin may send cookies, so a cookie session works from it.
  `-allow-origin '*'` allows every origin without cookies, and is refused
  for an adapter whose routes read a cookie.
- Nothing else is configured. The adapter answers preflight `OPTIONS`
  requests from its routes, exposes the headers each response sets, adds
  `Vary: Origin`, and puts the same headers on its errors, so a front end
  sees a 401 rather than a network error.
- Removing an origin stops its browsers' writes within 30 minutes, the
  time a browser may reuse a preflight answer. To cut a user off at once,
  end their sessions.

For cookie sessions, serve the front end and the API as two hosts of one
site (`app.example.com` and `api.example.com`): a cookie's default
`SameSite=Lax` works between them. A front end on a different site needs
`sameSite: SameSite.AnySite`, and browsers increasingly block such
cookies anyway, so use a bearer token in `Authorization` there.

## The OpenAPI document

`fiig openapi` prints the OpenAPI 3.0.3 document for what `fiig serve`
would serve, given the same root and adapters, as JSON on standard
output:

```
fiig openapi -workspace .. . ordersApi shop.payments.paymentsApi > openapi.json
```

Several adapters are one document, as they're one listener. It reads no
capabilities file and no environment, so it runs in CI beside `fiig
check`, and a program with checker diagnostics is refused as `serve`
refuses it. Bodies and parameters are described as the adapter reads and
writes them: enums by their spellings (ADR 0080), a union of an enum and
`string` as `anyOf` (ADR 0081), and another package's models in full.

## Request limits

`fiig serve` bounds what clients can make it hold (ADR 0076). Every limit
is on by default; raise or lower each with its variable or flag:

| Limit | Variable | Flag | Default | Past it |
|---|---|---|---|---|
| Request body | `MAX_BODY_BYTES` | `-max-body` | 2 MiB | 413 |
| Requests running at once | `MAX_REQUESTS` | `-max-requests` | 256 | wait, then 503 |
| Time per request | `REQUEST_TIMEOUT` | `-request-timeout` | 30s | 503 |

- A body whose `Content-Length` is over the limit is refused before any
  of it is read; a chunked one at the byte that passes it.
- A request arriving when `MAX_REQUESTS` are running waits up to 5 s for
  one to finish. At most `MAX_REQUESTS` wait; past that, and past the 5 s,
  the answer is 503 with `Retry-After: 1`. CORS preflights don't count.
- The time limit starts when a request starts running and covers reading
  its body, its workflow, and every `Socket` call in it. A body sent
  slowly is cut off at it.
- The request line and headers are at most 64 KiB (431 past it), and at
  most one `crypto.hashPassword` or `crypto.passwordMatches` per CPU runs
  at once; neither has a setting.
- Every refusal's body names the variable to raise.

At the defaults, request bodies hold at most 512 MiB and password hashes
19 MiB per CPU. Size `MAX_REQUESTS` and `MAX_BODY_BYTES` to the
container's memory, and put per-client rate limits in the proxy in
front.

## Not yet

See [known limitations](known-limitations.md).
