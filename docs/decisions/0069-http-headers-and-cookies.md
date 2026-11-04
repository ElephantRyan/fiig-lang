# ADR 0069: HTTP Headers and Cookies, Read and Set

Status: Proposed (2026-11-04). To be reviewed one decision at a time.
Date: 2026-11-04
Related: ADR 0009 and ADR 0021 (route binding by name), ADR 0017 (response
mapping), ADR 0026 (default response), ADR 0044 (a workflow is the adapter
boundary), ADR 0051 (no warnings), ADR 0062 (play nice with foreign data),
ADR 0065 (`Map`), ADR 0068 (session tokens, and decoding deferred to here).

## Context

An `http` route binds its workflow's arguments from three places: the path,
the query string, and the body (ADR 0021). A response is a status and a
body (ADR 0017, ADR 0026). Nothing reads a request header or a cookie, and
nothing sets one, so a fiig service can't:

| Task | Needs |
|---|---|
| Accept an API key or OAuth token | read `Authorization: Bearer <token>` |
| Accept HTTP Basic auth | read `Authorization: Basic <base64>`, and decode base64 |
| Keep a browser logged in | set a session cookie on login, read it on every request, clear it on logout |
| Correlate logs across services | read `X-Request-Id` |
| Tell a cache or client what to do | set `Cache-Control`, `Location`, `Retry-After` |

ADR 0068 gives a service its session tokens (`Random.bytes(count:
32).toBase64Url()`); this ADR carries them.

Two things make it more than a binding rule.

**Header and cookie names aren't fiig identifiers.** Path and query
placeholders bind by name (`{id}` to `id`), which works because a URL
template's names are the program's to choose. A header's name belongs to
the protocol (`Authorization`, `X-Request-Id`, `If-None-Match`), usually
with a hyphen, and a cookie's name to whoever set it first. A route has to
say which header feeds which argument.

**Cookies are where web frameworks' defaults cause security bugs.** A
session cookie needs `HttpOnly` (so a script injected into the page can't
read it), `Secure` (so it never crosses plain HTTP), and `SameSite` (so
another site can't send it, the CSRF defense). Most frameworks leave the
first two off unless asked:

| Framework | `HttpOnly` by default | `Secure` by default | `SameSite` by default |
|---|---|---|---|
| Express `res.cookie` | no | no | not set |
| Go `http.SetCookie` | no | no | not set |
| Flask / FastAPI `set_cookie` | no | no | not set |
| Spring `ResponseCookie` | no | no | not set |
| ASP.NET Core `Cookies.Append` | no | no | not set |
| Rails `cookies[...]` | no | no | not set |
| Django session cookie | yes | no (a setting) | `Lax` |
| Browsers, for a cookie with no `SameSite` | | | treated as `Lax` (Chrome since 2020) |

OWASP's session management guidance asks for all three on every session
cookie.

## Decision

### 1. A route names the header or cookie each argument comes from

An argument's source follows its type, with `from header` or `from
cookie` and the name as it appears on the wire:

```
adapter http accountApi {
    GET "/me" -> currentUser(session: string from cookie "sid")

    GET "/orders/{id}" -> readOrder(
        id: string,
        authorization: string from header "Authorization",
        requestId: string | None from header "X-Request-Id",
    )
}
```

Path and query placeholders are unchanged. The source sits on the argument
because that is where every typed framework puts it (OpenAPI's `in:
header` and `in: cookie`, FastAPI's `Header()` and `Cookie()`, Spring's
`@RequestHeader` and `@CookieValue`, ASP.NET's `[FromHeader]`), and because
a header's name can't be the argument's name. ADR 0021's elimination rule
for the body still holds: an argument claimed by a placeholder or a `from`
clause isn't the body.

The workflow sees only strings (or ints, or `None`). It doesn't know about
HTTP, so the same workflow can sit behind a CLI route that takes the token
as an argument (ADR 0044).

### 2. Types, missing values, and repeats

- **The types are path and query's:** `string`, `int`, `float`, `decimal`,
  `bool`, decoded the same way, and a value that doesn't decode is a 400
  naming the header or cookie.
- **Missing:** a required argument whose header or cookie is absent is a
  400 (`missing header "Authorization"`), before the workflow runs. An
  argument typed `T | None` receives `None`.
- **A header sent more than once** is read as its values joined by `", "`,
  in order, which is how RFC 9110 §5.3 defines a repeated field's value.
- **A cookie sent more than once** (two cookies with one name but
  different paths) is read as the first, which is the most specific path
  in the order browsers send them (RFC 6265 §5.4).
- **Header names match case-insensitively** (RFC 9110 §5.1); cookie names
  match exactly (RFC 6265).

### 3. A response sets headers and cookies through two more fields

A response-mapping arm's expression form (ADR 0017) already produces a
record with a `status` and an optional `body`, read by field name. It gains
two optional fields:

- **`headers: Map<string, string>`**: each entry becomes a response
  header.
- **`cookies: List<Cookie>`**: each becomes a `Set-Cookie` header.

```
model LoginResponse {
    status: int
    body: User
    cookies: List<Cookie>
}

conversion loggedIn(session: Session) -> LoginResponse {
    return LoginResponse(
        status: 200,
        body: session.user,
        cookies: [Cookie(name: "sid", value: session.token, maxAge: 86400)],
    )
}

adapter http accountApi {
    POST "/login" -> logIn(credentials: Credentials) {
        Ok(session) -> loggedIn(session)
        Err(error) -> 401
    }
}
```

The response stays an ordinary model the program declares, as ADR 0017
left it, rather than a builtin `HttpResponse`: a body's type differs per
route, and fiig has no generic models a program can declare. The checker
now types the four field names wherever a response arm's expression
produces a record: `status` must be an `int`, `headers` a
`Map<string, string>`, `cookies` a `List<Cookie>`, and any other field is
an error, since the adapter would ignore it.

The `200 customer` shorthand stays for the common case with no headers or
cookies.

### 4. `Cookie` is a prelude model whose defaults are the secure ones

```
model Cookie {
    name: string
    value: string
    maxAge: int | None             // seconds; None ends with the browser session; 0 deletes
    path: string | None            // default "/"
    domain: string | None          // default: this host only
    sameSite: SameSite | None      // default SameSite.Lax
    readableByScripts: bool | None // default false: HttpOnly
    sentOverPlainHttp: bool | None // default false: Secure
}

enum SameSite { Strict, Lax, AnySite }
```

- **Leaving a field out gives the safe answer:** `HttpOnly`, `Secure`,
  `SameSite=Lax`, `Path=/`, and no `Domain`, so the cookie goes to this host
  only.
- **The opt-outs are named for what they allow**, not for the attribute
  they turn off: `readableByScripts: true` rather than `httpOnly: false`,
  and `sentOverPlainHttp: true` rather than `secure: false`. A reader
  sees what the cookie exposes.
- **`SameSite.AnySite` is written `SameSite=None`**, the attribute's real
  value, which fiig can't use as a variant name since `None` is a value.
  RFC 6265bis requires `Secure` with it, so `AnySite` together with
  `sentOverPlainHttp: true` is a fault.
- **Deleting a cookie** is a `Cookie` with the same name and path and
  `maxAge: 0`.
- **No signing or encryption.** A session cookie carries a random token
  (ADR 0068) that the service looks up, so it has nothing to protect but
  its secrecy. A cookie that carries data and must not be tampered with
  needs an HMAC, and fiig has no hash functions yet.

### 5. What the adapter refuses to write

A response the program builds can be wrong in ways HTTP can't carry. Each
is a fault (a 500, with the reason in the trace), and the checker reports
the ones it can see in constants:

- **A header value or cookie value with CR or LF.** That is header
  injection (CWE-113): a value from user input could add headers of its
  own. Go's `net/http` quietly turns the line break into a space;
  fiig faults, since quietly changing a value hides the bug that put it
  there.
- **A cookie value outside RFC 6265's cookie-octets** (no space, comma,
  semicolon, double quote, or backslash). Go's `http.SetCookie` drops such
  a cookie and logs it; fiig faults. A base64url token always fits.
- **A header name that isn't an RFC 9110 token**, or one the adapter owns:
  `Content-Type` and `Content-Length` (the adapter writes JSON), and
  `Set-Cookie` (through `cookies`, so a cookie can't skip §4's
  defaults).

### 6. Decoding text back to bytes

ADR 0068 §3 left decoding for here, because Basic auth is the first thing
that needs it. `string` gains three methods, the inverses of `bytes`'s:

```
s.fromHex() -> bytes | None
s.fromBase64() -> bytes | None
s.fromBase64Url() -> bytes | None
```

`None` when the text isn't valid in that alphabet. Following ADR 0062's
"play nice with foreign data", each reads what real producers send: hex in
either case, and base64 with or without its `=` padding. A Basic auth
header is then a rule:

```
rule basicCredentials(header: string) -> Credentials | None {
    if not header.startsWith(prefix: "Basic ") {
        return None
    }
    decoded = header.removePrefix(prefix: "Basic ").fromBase64()
    ...
}
```

### 7. Scope

- **HTTP routes only.** `from header` or `from cookie` on a CLI route is an
  error.
- **No middleware.** Each route names its own credentials, and the
  workflow calls an activity that checks them. That repeats one argument
  per route, and in return a reader sees from each route what it
  authenticates with, as ADR 0017 chose composition over a `using` clause.
- **OpenAPI** (`compiler/adapter/openapi.go`) lists the new arguments with
  `in: header` and `in: cookie`, which it supports natively.
- Not in this ADR: reading all headers as a map, CORS, `Vary`, and response
  headers listed in OpenAPI.

## Alternatives considered

**A. Placeholders in the route string.** Something like `"/me" header
{Authorization}` keeps one binding style. Rejected: header names aren't
identifiers, so the placeholder would still need a mapping to an argument,
and the route string would mix the URL with things that aren't in it.

**B. A `Request` value passed to the workflow** (Express's `req`, Go's
`*http.Request`). Any header is reachable. Rejected: the workflow would
depend on HTTP, which ADR 0044 keeps out of it, and the checker couldn't
tell which headers a route needs.

**C. A builtin `HttpResponse` with fixed fields.** One shape to learn.
Rejected for now: its `body` would need a type parameter fiig only gives
its builtin generics; it can be added if user-declared generics arrive.

**D. Cookie attributes named after the RFC** (`httpOnly: bool`, `secure:
bool`), defaulting to true. Familiar. Rejected in favour of §4's names:
`httpOnly: false` reads like a technicality, and `readableByScripts: true`
says what it costs.

**E. A response-shorthand extension** (`200 user with cookie "sid" =
token`). Shorter. Rejected: it would be the adapter's third response
syntax, and the attributes in §4 don't fit on one line.

## Consequences

- Parser: a route argument's type may be any type (today the parser reads
  it as an expression, so `string | None` doesn't parse), followed by an
  optional `from header|cookie string-literal`. `header`, `cookie`, and
  `from` are contextual there only.
- Checker: §1's binding joins `checkRouteBinding` (a name given twice,
  `from` on a CLI route, a non-primitive type); §3's typing of the
  response fields; §5's constant checks.
- Runtime and adapter: header and cookie reading in `http.go`; writing
  headers and `Set-Cookie` with §4's defaults; §5's faults.
- `compiler/types`: the prelude `Cookie` model and `SameSite` enum; the
  three `string` decoders.
- OpenAPI output; grammar, tour, bug classes.
- Corpus: a login that sets a session cookie, a route that reads it, a
  logout that clears it, a bearer-token route, and a Basic auth rule.

## Open questions

1. **Redacting secrets.** Trace spans record names and outcomes, not
   arguments, but an `Err` value's text is recorded, and a workflow that
   puts a token into its error would log it. A `secret` marker on a
   parameter is possible later.
2. **Signed cookies**, once fiig has HMAC.
3. **Reading every header**, for a proxy or a request log.
