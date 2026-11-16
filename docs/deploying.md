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
  "Database": {"type": "sql", "driver": "sqlite", "dsn": "file:${DB_PATH}"},
  "Socket":   {"type": "socket"}
}
```

A capability can also be bound for one package's workflows, so each
domain of a modular monolith keeps its own database (ADR 0079 §6a); see
[A modular monolith in fiig](modular-monolith.md):

```json
{
  "Database": {"type": "sql", "driver": "sqlite", "dsn": "file:orders.db"},
  "packages": {"shop.payments": {"Database": {"type": "sql", "driver": "sqlite", "dsn": "file:payments.db"}}}
}
```

`fiig serve` takes every adapter the process serves, a dependency's by
its package path: `fiig serve . ordersApi shop.payments.paymentsApi`.
They share the listener, the limits below, and the CORS policy, and two
routes that would serve the same requests stop it at startup.

`-addr` defaults to `$LISTEN_ADDR`, then `:8080`, `-allow-origin` to
`$ALLOW_ORIGINS`, and the request limits to theirs (see below); all read
the same lookup. SIGINT or SIGTERM stops
accepting connections and gives in-flight requests `-shutdown` (10s) to
finish.

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
  scheme's default port. `null` and wildcards are refused at startup.
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
