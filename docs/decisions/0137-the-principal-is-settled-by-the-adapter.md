# ADR 0137: The Principal Is Settled by the Adapter, Before the Request Is Read

Status: Proposed
Date: 2026-11-21
Related: charter principle 11 (fail at the earliest point that has the
information), principle 10 (strict about what a program says it reads), ADR
0069 (HTTP adapters read headers and cookies), ADR 0070 (CORS), ADR 0076
(request limits), ADR 0079 §6 (one adapter per domain), ADR 0107 (a workflow
calls activities, not gateways), ADR 0051 (no severity), ADR 0103 (tests call
workflows), ADR 0133 (`fiig review`), Hearsay GAPS G29, G57, G61.

## Context

Hearsay's 17 signed-in workflows each start by turning a cookie into a
person:

```fiig
workflow deleteMyRating(session: string | None, itemId: string) -> Result<int, RatingError> {
    me = mapErr(e in sessions.currentUser(session)): ratingErrorOfIdentity(e)?
    ...
}
```

with `session: string | None from cookie "sid"` on every route. Four things
are wrong with it.

1. **It fails slow.** The HTTP adapter (`compiler/adapter/http.go`) matches
   the route, applies the request limits, checks the Content-Type, reads and
   parses the whole body and builds every argument, and only then calls the
   workflow whose first line asks who is there. An anonymous request with a
   bad body gets a 400 that describes the route, not a 401. Hearsay tried to
   prevent exactly that in a comment ("a stranger learns nothing about valid
   sorts"), but could only order things inside the workflow, after the
   adapter had already answered.
2. **Authentication is a convention.** Forgetting the first line makes a
   route public, and nothing notices (G61). Hearsay's workaround is a
   scenario test with one line per route, which catches a forgotten gate only
   if someone remembers to add the line.
3. **It costs a file per domain and repeats itself.** Each domain has its own
   one-arm `...ErrorOfIdentity`, and eight adapters repeat the same 401 text
   (G57).
4. **Neither the catalog nor OpenAPI can see it.** Which routes need a signed-in
   person is knowable only from each workflow's body, and the cookie shows up
   as a parameter of every route (G29).

### How others handle it

| | Where it is declared | How the handler gets the principal | Runs before the body is read | Default-deny |
| --- | --- | --- | --- | --- |
| Spring Security | a filter chain with URL matchers, in one configuration class | ambient (`SecurityContextHolder`, thread-local) | yes | yes (`anyRequest().authenticated()`) |
| ASP.NET Core | middleware plus `[Authorize]`, with a fallback policy | ambient (`HttpContext.User`) | yes | optional |
| Phoenix | a `pipeline :auth` on a router scope | `conn.assigns` | yes | per scope |
| Rails | `before_action :authenticate!` | an instance variable | yes | per controller |
| FastAPI | `Depends(current_user)` per handler, or on a router | a typed parameter | no, dependencies run alongside body parsing | no |
| Axum | an extractor type in the handler's arguments, or a router layer | a typed parameter | depends on extractor order | no |

What to take from them:

- From **Spring**: the principal is settled before anything else is read, by
  something that isn't the handler. That position is what makes it fail fast.
- From **Phoenix**: one declaration covers a group of routes and sits beside
  them, so a reader of the routes sees what guards them.
- From **Axum and FastAPI**: the handler receives the principal as a **typed
  parameter**, so a handler that needs a person says so in its signature.

What to leave behind:

- **Spring's ambient context.** A handler reads the principal without declaring
  it, so its contract doesn't say it needs a signed-in person (principle 10).
  Scheduled work runs with no context, and code that assumes one fails at
  run time.
- **Spring's URL matchers, kept away from the routes.** The matchers live in
  one security class and the routes live elsewhere. When their path matching
  disagrees, a route is left unprotected, and this has been a recurring
  source of Spring Security advisories.

## Decision

### 1. An adapter declares the principals it settles

```fiig
use hearsay.accounts
use hearsay.sessions

adapter http ratingsApi {
    principal me: accounts.Identity from cookie "sid" by sessions.signedIn else sessions.signInFirst

    PUT "/ratings/{itemId}" -> putRating(me, itemId: string, input: RatingInput)
        consumes "application/json"
        produces "application/json"
    {
        Ok(rating) -> 200 rating
        Err(error) -> ratingErrorResponse(error)
    }
    DELETE "/ratings/{itemId}" -> deleteMyRating(me, itemId: string) {
        Ok(_) -> 204
        Err(error) -> ratingErrorResponse(error)
    }
}
```

```ebnf
adapter-decl   ::= 'adapter' adapter-kind identifier '{' { principal-decl } { route } '}'
principal-decl ::= 'principal' identifier ':' type
                   'from' ( ( 'header' | 'cookie' ) string-literal | identifier )
                   'by' qualified-name 'else' qualified-name
route-arg      ::= identifier ':' type [ 'from' ( 'header' | 'cookie' ) string-literal ]
                 | identifier                                  -- a declared principal, by name
http-route     ::= http-method string-literal [ 'public' ] '->' route-call { media-clause } [ response-mapping ]
```

- `principal` is contextual: a keyword only at the start of an adapter body
  line. `public` is contextual after a route's path.
- **Why `principal`.** It is the security term for whoever or whatever is
  calling: a person, a service, or an API key's owner (`java.security.Principal`,
  Spring Security, .NET's `ClaimsPrincipal`, Kerberos, AWS IAM). `identity`
  reads as a person, and an API key isn't one. Considered and rejected:
  `identity` (too narrow), `subject` (the OAuth/JWT and Kubernetes term, but
  in plain English it already means a topic), and `settle`, `require`, `who`.
  The cost is that it sounds like "principle", and the charter numbers its
  principles. Fiig's own text says "principle" only for the charter's, and a
  diagnostic about this feature always says "principal (who is calling)" the
  first time. The keyword is fiig's choice; the type is the application's,
  so Hearsay keeps `accounts.Identity`, and a script-facing adapter might
  write `principal caller: ApiClient from header "Authorization" by ...`.
- **`by`** names a **workflow**. Adapters call only workflows (ADR 0079, ADR
  0107), so the binder is one, and it fits the layer: it decides what a
  step's result means. The session gateway returns `Identity | None`, and the
  binder decides what that `None` means, so the binder is not an empty
  workflow.

  ```fiig
  workflow signedIn(token: string | None) -> Result<accounts.Identity, IdentityError> {
      match loadSession(token) {
          None -> return Err(IdentityError.NotSignedIn)
          identity -> return Ok(identity)
      }
  }
  ```

  Its one parameter has the type of its source: a header or cookie is
  `string | None`, and `from <principal>` is that principal's type. It returns
  `Result<T, E>`, where `T` is the declared type.
- **`else`** names a **conversion** from `E` to an `HttpResponse`. It is the
  one place that says how a refusal looks on the wire, so the 401 text lives
  once, in the package that owns `IdentityError`, and not in every adapter.
- A route takes a principal by naming it as an argument (`putRating(me, ...)`).
  The workflow's parameter is the declared type: `workflow putRating(me:
  accounts.Identity, itemId: string, input: RatingInput)`. The cookie never
  reaches it.

### 2. Roles are principals settled from another principal

```fiig
adapter http adminApi {
    principal me: accounts.Identity from cookie "sid" by sessions.signedIn else sessions.signInFirst
    principal admin: Admin from me by signedInAdmin else notAnAdmin

    GET "/admin/settings" -> showSettings(admin) { ... }
}
```

`from me` passes an earlier principal to the binder. `signedInAdmin(me:
accounts.Identity) -> Result<Admin, AdminRefusal>` is a workflow that decides
whether this person is an admin, and `Admin` is a model that only that
decision produces in practice. A workflow that takes `admin: Admin` can't be
reached by anyone else through this adapter, and Hearsay's
`requireAdmin(session)?` first lines go away.

This settles **who you are and what role you hold**. Whether you may act on
**this particular thing** (is this rating yours?) needs the thing, so it stays
a workflow decision, as it does in Spring (URL rules versus
`@PostAuthorize`).

### 3. Default-deny, within an adapter that asks for it

If an adapter declares any `principal`, each of its routes either takes one or
is marked `public`:

```fiig
    GET "/libraries/{id}/cover" public -> showCover(id: string)
```

A route that does neither is an error (U3, §6). The adapter chose to settle
who is calling, so a route without it is a mistake in every reading, and the rule
passes ADR 0051's test. An adapter that declares no `principal` is unaffected,
and all its routes are public, as today. The language doesn't decide that an
application has users. The application says so, one adapter at a time.

### 4. The order of a request is part of the runtime's contract

For an HTTP route, the runtime does these steps in this order, and stops at
the first refusal:

1. Match the route. CORS preflight is answered before this (ADR 0070), and
   needs no principal.
2. Apply the request limits (ADR 0076): concurrency, deadline, body size.
3. **Settle the principals the route takes**, in declaration order, each binder
   at most once per request, and only the ones the route (or a principal it
   depends on) needs. A refusal is the `else` conversion's response.
4. Check the Content-Type, read and decode the body, and build the other
   arguments.
5. Call the route's workflow, and map its result with the route's arms.

The binders run inside the request's deadline (step 2) and are traced as
their own workflow calls. A public route runs no binder.

This order is a rule of the runtime, not a convention, and a test pins it: a
request with no cookie and an invalid body gets the `else` response, and the
body is never read.

### 5. What can see it

- **The registry**: an adapter card lists its principals (name, type, source,
  binder, refusal) and marks each route with the principals it takes, or
  `public`.
- **OpenAPI**: a `cookie` or `header` source becomes a `securityScheme` (an
  `apiKey` `in: cookie`); each protected route has a `security` requirement
  and the `else` conversion's statuses among its responses. Public routes
  have none. The cookie is no longer listed as a parameter of every route,
  which closes G29.
- **`fiig review`** (ADR 0133 item 6) shows the column.
- **Tests** call a feature workflow with a principal value directly (ADR
  0103), so a test of "rate a book" no longer needs to start a session first.
  "No session is refused" becomes one test of the binder, not one line per
  route.

### 6. Bug classes, in U

| # | Bug class |
| --- | --- |
| U1 | `by` names something other than a workflow, or a workflow whose parameter or result doesn't fit the source and the declared type |
| U2 | `else` names something other than a conversion from the binder's error type to an `HttpResponse` |
| U3 | A route of an adapter that declares a `principal` takes no required one and isn't marked `public`; or a `public` route takes a required one (§9) |
| U4 | A route argument names a principal the adapter doesn't declare, or one whose type isn't the workflow parameter's |
| U5 | `from` names a later principal, or principals depend on each other in a cycle |

### 7. Other adapter kinds

`stream`, `schedule`, `startup` and `cli` adapters declare no principals, and
this ADR doesn't add them. Their workflows can't take an `Identity`, because
nothing could produce one. That is the point: a scheduled job runs as no
one, and its signature shows it, instead of finding an empty context at run
time.

### 8. Several ways in, and how a binder fails fast

A route may need to accept a person's cookie or a script's API key. Spring
and ASP.NET try their schemes in a configured order, and the first success
wins. That fails slow or fails open. A request with an expired cookie and a
valid key costs two lookups, and the precedence is in configuration order,
where no reader sees it. A request with no credential at all can reach I/O
before anything notices. A malformed header can reach a database lookup.

Fiig's answer is **one principal with several sources and one binder**,
under three rules. Rules 1 and 2 apply to every binder from the start. The
grammar for a second source is decided here and built when an application
needs it:

```fiig
principal me: accounts.Identity
    from cookie "sid" or header "Authorization"
    by sessions.signedIn else sessions.signInFirst
```

```fiig
workflow signedIn(cookie: string | None, header: string | None) -> Result<accounts.Identity, IdentityError> {
    match credentialOf(cookie, header)? {      // a pure rule: none, ambiguous, malformed
        Credential.Session(token) -> return mapErr(e in sessionOwner(token)): IdentityError.NotSignedIn
        Credential.ApiKey(key) -> return mapErr(e in keyOwner(key)): IdentityError.BadKey
    }
}
```

1. **The scheme is chosen from what was sent, before any I/O.** The binder
   receives every source the principal names, in declaration order, and a
   pure rule decides which credential applies. No credential, an ambiguous
   request, or a malformed header is refused without a lookup. A binder with
   one source follows the same order: it checks for a missing cookie before it
   looks up the session.
2. **A credential that is presented but invalid is a refusal, never a
   fallback** to another scheme. Precedence, such as "both present is
   ambiguous" or "the cookie wins", is the application's rule, written in its
   code where a reader can see it.
3. **Scope is a role principal, not a union.** A key that may only read is
   settled like any role (§2), so a write route refuses it before the body is
   read:

   ```fiig
   principal writer: Writer from me by mayWrite else readOnlyKey
   PUT "/ratings/{itemId}" -> putRating(writer, itemId: string, input: RatingInput)
   ```

   A route that accepts "a person or a script" as two principal types, with
   the workflow matching on `Identity | ApiClient`, is rejected. It moves
   authorization into the workflow, after the body has been read. A caller
   that really is a different kind of thing, such as a service acting for no
   user, is usually a different feature, on its own route or adapter with its
   own principal type.

In OpenAPI, each source is its own `securityScheme`, and the route's
`security` lists them as alternatives.

### 9. A principal that may be absent

A public page can show more to someone signed in: a book's page shows
everyone's recommendations, and also your own rating if you're signed in. A
principal whose declared type is `T | None` is **optional**:

```fiig
principal viewer: accounts.Identity | None from cookie "sid" by sessions.signedIn else sessions.signInFirst

GET "/books/{itemId}" public -> showBook(viewer, itemId: string)
```

```fiig
workflow showBook(viewer: accounts.Identity | None, itemId: string) -> Result<BookPage, BookError> { ... }
```

- **No credential gives `None`, and the binder doesn't run.** If every source
  the principal names is absent, it is `None` with no I/O.
- **A credential that is presented but invalid is still refused,** by the
  `else` conversion, as in §8 rule 2. An expired or forged cookie is not
  quietly downgraded to anonymous. Spring (`permitAll()` with a `null`
  `@AuthenticationPrincipal`) and ASP.NET (`[AllowAnonymous]`) take the same
  shape but make that downgrade, which hides an attack or a bug behind a page
  that just looks logged out. The browser has to clear a dead cookie to be
  anonymous. Hearsay's sign-out already does that (`maxAge: 0`).
- **A route that takes only optional principals is marked `public`,** since
  anyone may call it. U3 reads: a route of an adapter that declares a
  principal takes a required one or is marked `public`, and a `public` route
  takes only optional ones.
- The binder is the same workflow a required principal uses. It returns
  `Result<T, E>`, and the runtime supplies the `None`.

Precedent for the shape: Spring, ASP.NET, and FastAPI's
`Depends(optional_user)` returning `None`. It is built with the rest of this
ADR only if an application has such a page by then. Otherwise it is recorded
here and built when one does, as with §8's `or`.

## Rationale

- **It fails at the earliest point that has the information** (principle
  11). The principal depends on a header or cookie, not on the body, so it is
  settled before the body is read.
- **The principal is part of the request without being ambient.** A workflow that
  needs a caller names one in its signature, which is the contract the
  catalog shows and an agent composes from.
- **Types do the guarding.** A workflow that takes `me: Identity` can't be
  reached without the binder having run, because nothing else in the adapter
  can produce the argument. This is stronger than a URL matcher, which only
  covers the paths it matches.
- **The layers stay as they are.** Adapters call workflows, workflows call
  activities, and the binder is a workflow with a real decision. ADR 0107 is
  not amended.
- **Policy stays with the application.** Default-deny applies only where an
  adapter declares a principal.

## Alternatives considered

- **A parameter bound per route (`me: Identity from cookie "sid" bound by
  currentUser` on each route).** This draft's first version. It repeats the
  scheme on every route and has no default-deny: a new route that forgets it
  is public.
- **The binder as an activity.** That needs adapters to call activities, an
  exception to ADR 0107 with no other use. Rejected: the binder decides what
  `None` means, which is a workflow's job.
- **Declared once in the `application` block (ADR 0132), for every adapter.**
  Closest to Spring's single filter chain. Rejected: an adapter's routes would
  take a parameter declared in another file and package, so reading the
  adapter wouldn't show where `me` comes from, which is the same separation
  that makes URL matchers go wrong. An adapter is one domain's surface (ADR
  0079 §6). Repeating one `principal` line in each adapter that needs it is
  the cost.
- **An ambient principal a workflow reads without declaring.** Rejected by
  principle 10.
- **A checker rule that every route authenticates.** Fails ADR 0051's test,
  because public routes exist. §3 is the version that passes: the rule holds
  only where the adapter has said it settles the principal.
- **Leave it, and keep Hearsay's scenario test.** It fails slow, and it guards
  only the routes someone remembered to list.

## Consequences

- Grammar: `principal-decl`, `public`, and a bare-name route argument. The
  IntelliJ keyword list is regenerated (`go run ./tooling/intellijgen`).
- Checker: U1-U5, and the workflow parameter check (U4) wherever route
  arguments are checked today.
- Runtime: `compiler/adapter/http.go` settles principals between the limits
  and the Content-Type check (§4). A per-request cache holds each binder's
  result, so `me` and `admin` look up the session once.
- OpenAPI, the registry, the language server's hover and Go to Declaration on
  a principal name.
- Hearsay: 17 workflows lose their first line and take `me` or `admin`, the six
  `...ErrorOfIdentity` conversions go, eight copies of the 401 text become
  one `else` conversion, `test/scenarios/EveryRouteNeedsASession.fiig` becomes
  one test of `signedIn`, and G29, G57 (the identity part) and G61 close.

## Open questions

None left from the review on 2026-11-21: several ways in (§8) and an optional
principal (§9) were settled there.
