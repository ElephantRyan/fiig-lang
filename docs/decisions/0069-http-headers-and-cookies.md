# ADR 0069: HTTP Headers, Cookies, and Content Types

Status: Accepted and implemented (2026-11-04; see Implementation notes),
after one-decision-at-a-time review: §1,
§2, and §4 accepted as drafted; §3 revised to a builtin `HttpResponse<B>`;
§5 (content types declared on the route, with `produces` and `consumes`)
added in review; §6 gained eight refusals; §7's decoding details settled;
§8 gained the localhost note, and CORS moved to its own ADR (open
question 6).
Date: 2026-11-04
Related: ADR 0009 and ADR 0021 (route binding by name), ADR 0017 (response
mapping), ADR 0026 (default response, which assumed JSON), ADR 0044 (a workflow is the adapter
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
        produces "application/json"

    GET "/orders/{id}" -> readOrder(
        id: string,
        authorization: string from header "Authorization",
        requestId: string | None from header "X-Request-Id",
    )
        produces "application/json"
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

### 3. A response is a builtin `HttpResponse<B>`

The adapter turns a workflow's result into a response, and that mapping
stays the adapter's job (ADR 0017). What changes is the envelope around
the body. Until now a response-mapping arm's expression produced a model
the program declared, read by its `status` and `body` field names, so a
route that sets a cookie would need its own model (`LoginResponse`,
`LogoutResponse`, ...) repeating the same envelope fields. Instead the
envelope is one builtin generic, beside `Result<T, E>`, `List<T>`, and
`Map<K, V>`:

```
HttpResponse<B> {
    status: int
    body: B | None                       // omitted: no body
    headers: Map<string, string> | None  // omitted: none
    cookies: List<Cookie> | None         // omitted: none
}
```

```
conversion loggedIn(session: Session) -> HttpResponse<User> {
    return HttpResponse(
        status: 200,
        body: session.user,
        cookies: [Cookie(name: "sid", value: session.token, maxAge: 86400)],
    )
}

adapter http accountApi {
    POST "/login" -> logIn(credentials: Credentials)
        consumes "application/json"
        produces "application/json"
    {
        Ok(session) -> loggedIn(session)
        Err(error) -> 401
    }
}
```

- **`B` is the body's type**, inferred from `body` as `Ok(x)` infers a
  `Result`'s, or taken from the expected type (a conversion's declared
  return type). A body type of its own (`PublicUser` rather than `User`)
  is the API's choice, the same under any envelope.
- **Every field but `status` may be left out.** A response with no body
  and no expected type is `HttpResponse<None>`; one conversion can return
  `HttpResponse<CustomerError>` with a body on some arms and none on
  others, as `customerErrorToHttpResponse` does.
- **A response arm's expression must produce an `HttpResponse`**, checked
  by the checker as a type, where until now the adapter read any record's
  fields by name at run time and ignored the rest.
- **The `200 customer` shorthand stays** for a response with no headers or
  cookies, and means `HttpResponse(status: 200, body: customer)`.
- **An `HttpResponse` has no content type.** The route declares it (§5),
  so a conversion that builds a response, such as
  `customerErrorToHttpResponse`, says what to send and not how to encode
  it, and serves a JSON route and an XML route alike.
- **`HttpResponse` becomes a prelude name**, so a program may no longer
  declare its own; `customer-service`'s model of that name, the one
  attested, migrates.

This settles what ADR 0017's Consequences left open ("whether it's a
genuine built-in or just an ordinary model").

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

### 5. Content types are declared on the route: `produces` and `consumes`

Until now every response was JSON and every request body was read as JSON,
because the adapter had only a JSON encoder and ADR 0026's default
response assumed it. Nothing decided that a fiig service speaks only JSON;
a SOAP endpoint, a CSV export, or an HTML page is an ordinary HTTP route.
So a route says what it sends and what it takes, as OpenAPI declares
`content` per operation and Spring writes `produces` and `consumes`:

```
adapter http customerApi {
    GET "/customers/{id}" -> readCustomer(id: string)
        produces "application/json"
    {
        Ok(customer) -> 200 customer
        Err(error) -> customerErrorToHttpResponse(error)
    }

    POST "/customers" -> createCustomer(input: NewCustomer)
        consumes "application/json"
        produces "application/json"

    GET "/customers/export" -> exportCustomers()
        produces "text/csv"
}
```

- **`produces` is required on every HTTP route.** The adapter encodes
  every response the route gives, from any arm or from ADR 0026's default
  mapping, to that type, and writes the `Content-Type` header. A response
  with no body (a 204, or `Err(error) -> 401`) has no `Content-Type`.
- **`consumes` is required on a route that binds a body** (ADR 0021 §3),
  and an error on one that doesn't. A request whose `Content-Type` is a
  different type is a 415 (Unsupported Media Type) before the workflow
  runs. Parameters such as `charset` aren't compared.
- **The type decides the encoding, and the checker checks the body
  against it**, since the route states the type where the checker can see
  it:

| Declared type | Body type (`B`, or the bound argument) | On the wire |
|---|---|---|
| `application/json`, or any `+json` type (`application/problem+json`) | any type JSON can carry: a model, enum, `List`, `Map`, or primitive | JSON, as ADR 0026 and ADR 0062 encode and decode it |
| any other type (`text/xml`, `application/soap+xml`, `text/csv`, `text/html`, `application/pdf`) | `string` or `bytes` | the text as UTF-8 (a `text/*` type gets `; charset=utf-8`), or the bytes as they are |

- **A non-JSON `consumes` binds the body raw**, as a `string` or `bytes`
  argument. That narrows A7 (ADR 0021 §4): a body read as JSON must still
  bind a model, since JSON into a primitive skips decoding, but a body
  declared as XML or CSV is text for a rule to parse, and there is nothing
  for the adapter to decode it into. Until fiig has an XML library, that
  parse is the program's.
- **One type per route.** OpenAPI allows a type per status (errors as
  `application/problem+json`); a route that needs it can wait for a real
  case (open question 4). A route doesn't look at the request's `Accept`
  header: it produces what it declares.
- **The type is a constant string the checker validates** as a media type
  (`type/subtype`, RFC 9110 §8.3.1).
- **The adapter's own responses** (a 400 for a missing header, a 415) are
  `application/json`, as today, since the program never shaped them.
- CLI routes have neither clause.

### 6. What the adapter refuses to write

An `HttpResponse` the program builds can be wrong in ways HTTP can't
carry. Each
is a fault (a 500, with the reason in the trace), and the checker reports
the ones it can see in constants:

- **A header value with a control character other than tab** (RFC 9110
  §5.5). CR and LF are header injection (CWE-113): a value from user input
  could add headers of its own. Go's `net/http` quietly turns the line
  break into a space; fiig faults, since quietly changing a value hides
  the bug that put it there. Non-ASCII text is allowed and written as
  UTF-8.
- **A header name that isn't an RFC 9110 token**, or one the adapter owns:
  `Content-Type` (the route's `produces`, §5), `Content-Length` (the
  adapter writes the body), and `Set-Cookie` (through `cookies`, so a
  cookie can't skip §4's defaults).
- **Two header names that differ only by case** (`"X-Id"` and `"x-id"`),
  since HTTP reads them as one header.
- **A status outside 200 to 599.** Go panics below 100 and above 999, and
  sends the 1xx responses itself; none is a program's final answer.
- **A body on a status that can't carry one** (204, 304). Go returns an
  error that is easy to drop.
- **A cookie name that isn't an RFC 9110 token.** A name such as
  `"sid; Domain=evil.com"` would write attributes of its own.
- **A cookie value outside RFC 6265's cookie-octets** (no space, comma,
  semicolon, double quote, backslash, or control character). Go's
  `http.SetCookie` drops such a cookie and logs it; fiig faults. A
  base64url token always fits.
- **A cookie `path` with a `;` or a control character, or a `domain` that
  isn't a hostname.** Go drops the bad bytes of a path, and a bad domain
  attribute, both quietly.
- **A negative `maxAge`.** §4 gives 0 as delete, so a negative age means
  nothing.
- **A cookie browsers would ignore**: `sameSite: AnySite` with
  `sentOverPlainHttp: true` (`SameSite=None` requires `Secure`); a
  `__Secure-` name without `Secure`; a `__Host-` name without `Secure`,
  with a `domain`, or with a `path` other than `/`. The browser drops it
  without a word, and the bug surfaces later as a login that doesn't
  stick.
- **Two cookies in one response with the same name, path, and domain**,
  since the browser keeps only one.

These are program bugs, not client errors, so each is a 500 rather than a
check that picks some other response.

### 7. Decoding text back to bytes

ADR 0068 §3 left decoding for here, because Basic auth is the first thing
that needs it. `string` gains three methods, the inverses of `bytes`'s:

```
s.fromHex() -> bytes | None
s.fromBase64() -> bytes | None
s.fromBase64Url() -> bytes | None
```

`None` when the text isn't valid in that alphabet. Following ADR 0062's
"play nice with foreign data", each reads what real producers send:

- **Hex in either case.** Odd-length hex is `None`.
- **Base64 with or without its `=` padding**, and with CR and LF skipped
  anywhere, since MIME and PEM wrap base64 every 64 or 76 characters. Any
  other whitespace is `None`: a stray space means the text was damaged,
  not wrapped.
- **Each method reads only its own alphabet.** `-` or `_` in `fromBase64`,
  or `+` or `/` in `fromBase64Url`, is `None`: text in the other alphabet
  usually means something upstream is mislabeled, and the two methods
  mirror the two encoders.
- **Leftover bits are ignored.** Base64 that doesn't end on a byte
  boundary should zero its last few bits, and loose encoders don't; `"QQ"`
  and `"QR"` both decode to `"A"`, as Go's default decoder reads them. The
  bits carry no data.

With `bytes.utf8() -> string | None`, a Basic auth header is then a rule:

```
rule basicCredentials(header: string) -> Credentials | None {
    if not header.startsWith(prefix: "Basic ") {
        return None
    }
    decoded = header.removePrefix(prefix: "Basic ").fromBase64()
    ...
}
```

### 8. Scope

- **HTTP routes only.** `from header`, `from cookie`, `produces`, and
  `consumes` on a CLI route are errors.
- **No middleware.** Each route names its own credentials, and the
  workflow calls an activity that checks them. That repeats one argument
  per route, and in return a reader sees from each route what it
  authenticates with, as ADR 0017 chose composition over a `using` clause.
- **OpenAPI** (`compiler/adapter/openapi.go`) lists the new arguments with
  `in: header` and `in: cookie`, and each operation's request and
  response `content` under the declared types, all of which it supports
  natively.
- **Secure cookies in development.** §4's `Secure` default doesn't need
  `sentOverPlainHttp: true` to try a service locally: Chrome and Firefox
  treat `http://localhost` as secure and keep the cookie. An opt-out
  written for development is one that ships.
- Not in this ADR: reading all headers as a map, `Vary`, and response
  headers listed in OpenAPI (they come from a run-time `Map`, so the
  generator can't see their names). CORS is needed, and gets its own ADR
  (open question 6).

## Alternatives considered

**A. Placeholders in the route string.** Something like `"/me" header
{Authorization}` keeps one binding style. Rejected: header names aren't
identifiers, so the placeholder would still need a mapping to an argument,
and the route string would mix the URL with things that aren't in it.

**B. A `Request` value passed to the workflow** (Express's `req`, Go's
`*http.Request`). Any header is reachable. Rejected: the workflow would
depend on HTTP, which ADR 0044 keeps out of it, and the checker couldn't
tell which headers a route needs.

**C. Response models the program declares, read by field name.** The
first draft's §3, extending ADR 0017's convention with `headers` and
`cookies` fields: no new builtin. Rejected in review: every response
shape with a cookie or a header needs a model of its own repeating the
envelope fields, and the shape is a naming convention rather than a type.
The body's type is a separate choice that neither option removes.

**D. Cookie attributes named after the RFC** (`httpOnly: bool`, `secure:
bool`), defaulting to true. Familiar. Rejected in favour of §4's names:
`httpOnly: false` reads like a technicality, and `readableByScripts: true`
says what it costs.

**E. A response-shorthand extension** (`200 user with cookie "sid" =
token`). Shorter. Rejected: it would be the adapter's third response
syntax, and the attributes in §4 don't fit on one line.

**F. A content type on each `HttpResponse`.** Considered in review as the
first way to make the type explicit: a field the response carries, so
each arm could send its own type. Rejected: the format would live in
whatever builds the response, so a shared conversion such as
`customerErrorToHttpResponse` would fix its routes' wire format, and as a
run-time string the checker couldn't match it against the body's type.

**G. JSON unless declared otherwise.** Shorter for the common case.
Rejected: what a route sends should be stated, as everything else about it
is, and an implicit default is how "always JSON" came to look like a rule.

## Consequences

- Parser: a route argument's type may be any type (today the parser reads
  it as an expression, so `string | None` doesn't parse), followed by an
  optional `from header|cookie string-literal`; after the call, optional
  `consumes string-literal` and `produces string-literal` clauses.
  `header`, `cookie`, `from`, `consumes`, and `produces` are contextual
  there only.
- Checker: §1's binding joins `checkRouteBinding` (a name given twice,
  `from` on a CLI route, a non-primitive type); a response arm's
  expression must be an `HttpResponse` (§3); §5's required clauses,
  media-type validity, and body types against the declared type, with A7
  narrowed for non-JSON bodies; §6's constant checks.
- Runtime and adapter: header and cookie reading in `http.go`; writing
  headers and `Set-Cookie` with §4's defaults; encoding by the declared
  type and the 415 (§5); §6's faults.
- `compiler/types`: the builtin generic `HttpResponse<B>` (§3), the
  prelude `Cookie` model and `SameSite` enum, and the three `string`
  decoders.
- Corpus migration: every HTTP route (nine, in eight adapters) gains
  `produces "application/json"`, and each body-binding route `consumes
  "application/json"`. `customer-service/models/http_response.fiig` is
  deleted, and `customerErrorToHttpResponse` returns
  `HttpResponse<CustomerError>`. ADR 0017 gets a note that §3 settles its
  open question.
- OpenAPI output; grammar, tour, bug classes.
- Corpus: a login that sets a session cookie, a route that reads it, a
  logout that clears it, a bearer-token route, a Basic auth rule, and a
  route producing a non-JSON type (a CSV export).

## Open questions

1. **Redacting secrets.** Trace spans record names and outcomes, not
   arguments, but an `Err` value's text is recorded, and a workflow that
   puts a token into its error would log it. A `secret` marker on a
   parameter is possible later.
2. **Signed cookies**, once fiig has HMAC.
3. **Reading every header**, for a proxy or a request log.
4. **A content type per status**, such as `application/problem+json` for
   errors, and **content negotiation** on `Accept`, if a real route needs
   either.
5. **An XML library**, so a SOAP or XML route encodes and parses models
   rather than text; the HTTP client (alpha item 8) may need it first.
6. **CORS.** A browser front end on another origin can't call the service
   until it answers preflight `OPTIONS` requests and sends
   `Access-Control-Allow-*` headers, and nothing here lets a program do
   either. Owner's decision in review: fiig needs it, as its own ADR and
   alpha item 4a, rather than leaving it to a same-origin deployment or a
   reverse proxy.

## Implementation notes (2026-11-04)

Built in six steps: route syntax and binding, the prelude types, writing
headers and cookies with §6's refusals, the string decoders, OpenAPI, and
`examples/accounts` (a login cookie, `/me`, logout, a 401 challenge, a
bearer-token CSV export, and a Basic auth rule), dogfooded with `fiig
serve` against SQLite. Decisions made while building, beyond the text
above:

- **A route's `RouteArg` replaced its call expression** in the AST: an
  argument holds a type and a source, never a value. The checker now also
  reports a route argument that isn't a workflow parameter, one given
  twice, one whose written type isn't its parameter's (A1), and a route
  naming an undefined workflow, none of which it checked before.
- **Response arms are type-checked.** Each arm's pattern binds against the
  workflow's return type, as a `match` arm's does; calls inside an arm
  aren't held to a declaration kind's composition rules, as before.
- **§6's refusals are bug classes W1 (a response) and W2 (a cookie)**, in
  `compiler/httpspec`, which the checker applies to constants and the
  adapter to every response before writing any of it. The checker checks a
  cookie's browser rules only when every field they read is known; a
  left-out field is its known default.
- **A media type is written without parameters**; the adapter adds
  `; charset=utf-8` to a `text/*` string body itself.
- **A body that is `None`** (no body field, or ADR 0026's default mapping
  of `Ok(None)`) is sent as no body, with no `Content-Type`, where it was
  JSON `null`.
- **Header names go out as the program spelled them**
  (`WWW-Authenticate`), not as Go canonicalizes them.
- **A latent runtime bug fixed:** constructing a model without one of its
  optional fields faulted, though the checker allowed it; the field is now
  `None`. `Cookie(name:, value:)` depends on it.
- **A `POST` without the declared `Content-Type` is a 415**, so
  `docs/getting-started.md`'s `curl` now sends the header.
- **OpenAPI** is still reachable only from Go (`adapter.GenerateOpenAPI`);
  no `fiig` command prints it, which was already so.

