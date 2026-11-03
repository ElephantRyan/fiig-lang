# ADR 0021: Adapter Routes Bind Path, Query, and Body Parameters Explicitly

Status: Accepted
Date: 2026-10-03

## Context

Two gaps converge here. First, bug-class catalog entry A7 (passing
undecoded external/untyped data directly into domain code) was left
partially open by ADR 0017: path-segment binding into a `value` type is
decoding by construction, but structural decoding of a whole request body
into a multi-field `model` was deferred as "a real violation... still
deferred as an implementation detail." Second, closing that gap exposed that
ADR 0009's path-parameter binding — `{id}` in a route path matching `id` in
the route's call-argument list — is unchecked by the compiler today;
`checkAdapterRoutes` only enforces bug class B6 (route must call a
workflow). "Whichever parameter isn't claimed by the path" only means
something once "claimed" is something the checker actually computes.

Separately, HTTP gained a new method during this project's lifetime: RFC
10008 (published) defines `QUERY` — safe and idempotent like `GET`, but,
unlike `GET`, explicitly designed to carry a request body. Its own stated
purpose is replacing `POST` for read-only requests whose filter criteria are
too rich for a URL (nested objects, arrays, large criteria sets) while
keeping the safe/idempotent/cacheable semantics `POST` never had. This
mattered directly to the binding design below: once a route can also carry
URL query-string parameters (`?role=admin`), "whichever parameter the path
didn't claim is the body" stops being unambiguous — there's now a second
place a parameter could come from.

## Decision

**1. `http-method` is extended**: `GET | POST | PUT | PATCH | DELETE | QUERY`.
The parser already accepted any identifier here (verb validation was a
checker concern, not a parser one); this decision is what gives the checker
a concrete allow-list to validate against, rejecting anything else.

**2. Query-string parameters bind by the same `{name}` notation as path
segments, in a distinct region of the route string.** A route path may carry
a `?`-prefixed query region whose keys use placeholders exactly like path
segments:

```
GET "/customers?role={role}" -> listCustomers(role: Role)
```

Both path and query placeholders bind by name match against the route's
call-argument list — this was always ADR 0009's stated rule for path
segments, and query parameters follow it identically. This decision also
makes that match a real, enforced checker rule for the first time: every
placeholder (path or query) must name a call argument that exists, and
every call argument not bound to the body (see #3) must be claimed by some
placeholder. Previously neither direction was checked at all.

**3. Body-carrying verbs are `POST`, `PUT`, `PATCH`, and `QUERY`** — not
`GET` or `DELETE`, whose bodies are either undefined or inconsistently
supported across real HTTP implementations. For a route using one of the
four body-carrying verbs, at most one call-argument parameter may remain
unclaimed after path and query placeholders are matched; that one parameter
is bound from the decoded request body. Zero unclaimed parameters means the
route has no body (legal — not every `POST` needs one). More than one
unclaimed parameter is a checker error: binding would be ambiguous. For
non-body-carrying verbs (`GET`, `DELETE`), every call-argument parameter must
be claimed by a path or query placeholder; an unclaimed parameter is an
error, since there's no body to fall back to.

```
QUERY "/products" -> searchProducts(filter: ProductFilter)
POST  "/customers" -> createCustomer(input: NewCustomer)
PUT   "/customers/{id}" -> replaceCustomer(id: CustomerId, input: NewCustomer)
```

In the third example, `id` is claimed by the path placeholder and `input` —
unclaimed by anything — is the body.

**4. A body-bound parameter's type must be a `model`, not a `value` or
primitive (bug class A7).** This is the same reasoning ADR 0017 already
applied in the other direction: a `value` type is a thin nominal wrapper
with no fields, so there is nothing for a decode step to structurally check
against — binding a request body straight into one skips decoding entirely
rather than performing it. Binding a body into a `model` is real decoding,
since the model's declared fields are exactly what gets validated.

## Rationale

Explicit-per-parameter source (path vs. query vs. body) is how every typed
precedent surveyed handles this split, never by inference: OpenAPI gives
each parameter its own `in: path`/`in: query`, with query parameters never
appearing in the path template at all; Spring MVC distinguishes
`@PathVariable`/`@RequestParam`/`@RequestBody`; ASP.NET Core distinguishes
`[FromRoute]`/`[FromQuery]`/`[FromBody]`. Fiig follows that same split for
path and query. Where Fiig diverges deliberately is the body: rather than
adding a third annotation, body binding stays implicit-by-elimination,
consistent with ADR 0009 (path binding) and ADR 0015 (transaction binding)
both already preferring "wire together what's already unambiguously
declared" over new syntax. That elimination is safe to leave implicit now
specifically *because* query parameters became explicit in this same
decision — with only two buckets (claimed-by-placeholder, or not), "not" is
unambiguous again, the same way it was before `QUERY`/query-string binding
existed at all.

Adopting `QUERY` itself, rather than treating body-carrying search as
exclusively `POST`'s job, follows the RFC's own rationale: `POST`'s
non-idempotent, mutation-flavored semantics actively get in the way of
retries and caching for what is, underneath, a read. Fiig's adapter grammar
already separates HTTP methods as a surface-level concern from the
workflow's own effect category (every route still must target an exported
`workflow`, bug class B6), so adding a fifth/sixth verb costs nothing
structurally — it's one more string in the allow-list.

## Consequences

- `docs/specification/grammar.md`: `http-method` production extended;
  `route`/path productions extended to describe the optional query region;
  new prose describing path/query/body binding and the A7 check, replacing
  the current "unattested" note on `response-mapping`'s neighbor section.
- `compiler/checker`: new validation logic — none of this existed before.
  Needs a placeholder-extraction step over `Route.Path` (split on the first
  `?`; extract `{name}` tokens from each half), a known-verb allow-list, the
  claimed/unclaimed matching described in #2–#3, and the A7 type check from
  #4. `checkAdapterRoutes` is the natural home for all of it.
- No lexer change: HTTP verbs are parsed as plain identifiers already
  (`p.expectName().Text`), so `QUERY`/`PUT`/`PATCH`/`DELETE` need no new
  keyword — only the checker's allow-list is new.
- New examples needed to attest this: a `POST` route with a body-bound
  `model` parameter, a `QUERY` route with a filter model, a route combining
  a path placeholder with a query placeholder, and — in
  `examples/invalid-programs/` — a route binding a body parameter to a
  `value` type, attesting A7's rejection.
- `docs/specification/bug-classes.md`'s A7 entry moves from "partially
  settled... not attested by an example" to attested, once the checker
  change and the invalid-programs example above both land — not done by
  this ADR alone, which only settles the design.
- RFC 10008 also discusses `QUERY` response cacheability (with cache keys
  that must incorporate the request body). Fiig has no HTTP caching model at
  any layer yet, so that aspect of the RFC is explicitly out of scope here.
