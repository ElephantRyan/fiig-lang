# ADR 0098: OpenAPI Statuses From Conversions, and Cookies as Security Schemes

Status: Proposed (built 2026-11-19 under the owner's goal to work through
the abs-recommendations gaps; awaiting review)
Date: 2026-11-19
Amends: ADR 0017's OpenAPI description of an expression arm, ADR 0069 §1
(cookie arguments in the document).
Related: ADR 0026 (default mapping), ADR 0069 (headers and cookies),
abs-recommendations GAPS G28, G29.

## Context

**G28.** A route that answers through a conversion,

```
POST "/session" -> passwordLogIn(input: PasswordLogin)
{
    Ok(session) -> signedIn(session)      // HttpResponse<Identity>, status 200
    Err(error) -> apiErrorResponse(error)
}
```

was described with one `default` response, because the status is only
known when the conversion runs. Both arms wrote to `default`, so the
error's schema replaced the session's, and a generated TypeScript client
had no type for the body the server sends. A redirect (`status: 302`)
was described the same way.

**G29.** A `from cookie "sid"` argument was a cookie parameter, so the
generated client offered `params.cookie.sid`. A browser's code can't set
a cookie on a request (`Cookie` is a forbidden header in `fetch`); the
browser sends it by itself.

## Decision

### 1. A conversion's constant statuses are its responses

When an arm calls a conversion (or rule) whose body builds its
`HttpResponse(...)` values with constant `status:` arguments, each status
is a response with the conversion's `B` as its body:

- `signedIn` builds `status: 200` → a `200` with `Identity`'s schema.
- `redirectToIdp` builds `status: 302` with `headers: ["Location": ...]`
  → a `302` described as a redirect, with a `Location` header and no
  body.
- An error conversion that builds 404, 409 and 500 gets all three.

If any construction's status is computed, nothing is read from that
conversion, since describing only the constant ones would hide the rest.
Then an `Ok` arm is `2XX` and any other arm `default`. One arm never
replaces another's response.

### 2. A cookie argument is a security scheme

Each cookie a route reads is a security scheme, `{type: apiKey, in:
cookie, name: <cookie>}`, under `components.securitySchemes`, and the
operation lists it in `security`, with an empty alternative (`{}`) when
the argument is `T | None`. It is no longer a parameter.

## Rationale

- **The conversion already says the status.** Reading a constant from it
  is what a reader does; only a computed status is unknowable.
- **OpenAPI's cookie security scheme is how codegen tools model a cookie
  the browser carries:** `openapi-fetch`, the app's client, then neither
  asks for it nor sends it.

## Alternatives considered

- **Declaring the statuses on the arm** (`Ok(s) -> 200 signedIn(s)`).
  Repeats what the conversion says, and can drift from it.
- **A marker on the argument** (`from cookie "sid" as session`), so only
  some cookies become schemes. More syntax for a distinction the browser
  doesn't make: no cookie is settable by page code.

## Consequences

- GAPS G28 and G29 close.
- A cookie that isn't a credential, like abs-recommendations' `oidc`
  state cookie, is also described as a security scheme. That's harmless
  to clients but reads oddly; see open question 1.

## Open questions

1. **Cookies that aren't credentials.** Whether they deserve a different
   description, and how a route would say so.

## Implementation notes

Built 2026-11-19 in `compiler/adapter/openapi.go` (`builtResponses` and
the cookie branch of `buildOperation`). On a copy of abs-recommendations,
`POST /session` now documents `200` with `Identity`'s properties,
`GET /auth/login` a `302` with `Location`, and `DELETE /session` has
`security: [{sid: []}, {}]` and no cookie parameter.
