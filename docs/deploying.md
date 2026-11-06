# Deploying a fiig application

`fiig serve` checks a program and serves one of its `adapter http`
declarations. It refuses a program with checker diagnostics, so a deploy
can't start a program that doesn't check.

```
fiig serve [-addr :8080] [-caps caps.json] [-allow-origin ORIGIN]... [-workspace DIR]... <root> <adapterName>
```

## Capabilities and configuration

`-caps` is the ADR 0027 capabilities file. Any string in it may name an
environment variable as `${NAME}`, and an unset one stops the server before
it starts, so paths and secrets live in the environment, not the file:

```json
{
  "Database": {"type": "sql", "driver": "sqlite", "dsn": "file:${DB_PATH}"},
  "Socket":   {"type": "socket"}
}
```

`-addr` defaults to `$LISTEN_ADDR`, then `:8080`. SIGINT or SIGTERM stops
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
docker run -p 8080:8080 -v app-data:/data -e DB_PATH=/data/app.db my-app
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

## Not yet

- Enum and `Result` values in an HTTP body are still written in the tagged
  form `run-cli -json` uses; a model is a plain object.
