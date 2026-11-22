# ADR 0141: A Principal Is Declared Once and Named by Each Adapter

Status: Accepted (2026-11-21; the owner approved it). Implemented 2026-11-21; see Implementation notes.
Date: 2026-11-21
Related: ADR 0137 (the principal is settled by the adapter; this amends §1
and §2), ADR 0079 §6 (one adapter per domain), ADR 0107 (adapters call
workflows), ADR 0043 (`as`), ADR 0051 (no severity), ADR 0132 (the
`application` declaration), charter principle 10.

## Context

ADR 0137 settles who is calling in the adapter, before the request is read.
Each adapter declares its principals in full:

```fiig
adapter http ratingsApi {
    principal me: accounts.Identity from cookie "sid" by sessions.signedIn else sessions.identityErrorResponse
    ...
}
```

Hearsay, the first application on it, has that same line in seven adapters
(admin, library, links, listening, rated, ratings, recommendations), and the
`admin` role line in two. Each copy repeats four facts: the type, the cookie
name, the binder, and the refusal.

ADR 0137 accepted the repetition on purpose. It rejected declaring the
principal once in the `application` block for every adapter, because an
adapter's routes would then take a parameter declared somewhere the adapter
never mentions. Reading the adapter wouldn't show where `me` comes from, the
same gap that lets Spring's URL matchers drift from the routes they protect.

That reason holds. What it doesn't cover is the cost the repetition carries:

1. **The copies can drift, and a drifted copy is a security bug.** Rename the
   cookie, or move to a binder that also checks the session's age, and seven
   files change. An adapter that keeps the old line keeps accepting the old
   cookie, or skips the new check, and nothing notices: each copy type-checks
   on its own.
2. **The scheme has no name.** OpenAPI already names it: every `me` line
   becomes the same `apiKey` security scheme. The catalog can't say "these
   seven adapters use the session principal", because to fiig they are seven
   unrelated declarations that happen to match.
3. **The line is long for what it says.** The adapter's reader needs to know
   *which* principal guards the routes. How it's settled is the sessions
   package's business.

### How others handle it

| | Declared once | Referenced at each use | The reference is visible where it guards |
| --- | --- | --- | --- |
| OpenAPI | `components.securitySchemes` | `security: [{ sessionCookie: [] }]` | yes, on the operation |
| Phoenix | `pipeline :auth do ... end` | `pipe_through :auth` | yes, on the scope |
| ASP.NET Core | `AddPolicy("Admin", ...)` | `[Authorize(Policy = "Admin")]` | yes, on the controller or action |
| FastAPI | `def current_user(...)` | `Depends(current_user)` | yes, on the handler |
| Axum | `impl FromRequestParts for CurrentUser` | the parameter's type | only through the type |
| Spring Security | the filter chain | nothing: ambient | no |

What to take: the common shape is a **named definition plus a reference by
name at each use**. Every row except Spring and Axum has it, and it is what
ADR 0137's rejected alternative lacked: there, the use site didn't refer to
anything.

What to leave behind: Axum's extraction-by-type (see Alternatives), and
Spring's ambient principal, which ADR 0137 already rejected.

## Decision

### 1. A `principal` declaration

A package may declare a principal at the top level, in its own file, like
any other declaration:

```fiig
// hearsay/sessions/src/Me.fiig
principal me: accounts.Identity from cookie "sid" by signedIn else identityErrorResponse
```

The grammar is ADR 0137's `principal-decl`, unchanged, now also allowed at
the top level. `by` and `else` resolve in the declaring package, as any name
does there, so the binder and the refusal are named without a package prefix
when they are the package's own. The declaration is public, so other
packages' adapters can name it.

A top-level principal is the package's statement of **how a caller is
recognised**: which credential, which workflow decides, and what a refusal
looks like. It belongs beside the binder and the refusal conversion, in the
package that owns the identity error.

### 2. An adapter names the principals it settles

```fiig
use hearsay.sessions

adapter http ratingsApi {
    principal sessions.me

    PUT "/ratings/{itemId}" -> putRating(me, itemId: string, input: RatingInput) { ... }
    DELETE "/ratings/{itemId}" -> deleteMyRating(me, itemId: string) { ... }
}
```

```ebnf
adapter-decl   ::= 'adapter' adapter-kind identifier '{' { principal-decl | principal-ref } { route } '}'
principal-ref  ::= 'principal' qualified-name [ 'as' identifier ]
top-level-decl ::= ... | principal-decl
```

- `principal sessions.me` brings the principal into the adapter under its
  own name, `me`. Routes take it by that name, as in ADR 0137.
- `as` renames it, for an adapter that names two packages' principals that
  share a name (`principal partners.me as partner`). It reads as ADR 0043's
  `as` does on a `use`.
- The reference is what the adapter's reader sees: one name, and Go to
  Declaration opens the definition. This is the difference from the
  `application`-block alternative ADR 0137 rejected. Nothing reaches an
  adapter that the adapter doesn't name.
- Everything else in ADR 0137 holds for a named principal exactly as for an
  inline one: the order a request is read in (§4), default-deny (§3, U3), the
  per-request cache, the optional `T | None` form (§9) and the several-sources
  rules (§8).

### 3. Roles are named the same way

A role principal is settled from another principal (ADR 0137 §2). At the top
level, `from` names a top-level principal:

```fiig
// hearsay/sessions/src/Admin.fiig
principal admin: accounts.Admin from me by accounts.signedInAdmin else accounts.notAnAdmin
```

```fiig
adapter http adminApi {
    principal sessions.me
    principal sessions.admin
    ...
}
```

- An adapter that names `admin` settles `me` too, because `admin` is settled
  from it; that's part of `admin`'s definition. A route can still take `me`
  only if the adapter names it (U4 is unchanged). The per-request cache means
  naming both looks the session up once.
- **Where a role lives follows the package graph.** In Hearsay, `sessions`
  imports `accounts` (the binder returns `accounts.Identity`), so `accounts`
  can't declare a principal settled from `sessions.me` without an import
  cycle. `admin` therefore lives in `sessions`, and names `accounts`'
  workflow and conversion, as ADR 0137 §1 already allows. An application
  whose roles are their own domain puts them in a package that imports the
  one that declares `me`.

### 4. The inline form stays, for a principal only one adapter settles

Hearsay's federation adapter settles a peer server from an `Authorization`
header, with a binder and a refusal that belong to the links domain and
nothing else:

```fiig
adapter http federationApi {
    principal peer: LinkedServer from header "Authorization" by callingServer else peerRefused
    ...
}
```

Moving that to its own file would give a one-use definition a name to look
up, for no reader's benefit. The two forms are the same declaration in two
places, the way a private helper and a public workflow are the same kind of
code: inline is local, top-level is shared.

### 5. What can see it

- **The registry**: a top-level principal gets its own card (name, type,
  sources, binder, refusal) and its own identity. The adapter card lists the
  principals it names, and each route the ones it takes, as ADR 0137 §5 has
  it. Find usages on `sessions.me` lists every adapter that settles it.
- **OpenAPI**: a named principal's source is one `securityScheme`, named
  after the principal (`sessions.me` becomes `sessionsMe`, an `apiKey` `in:
  cookie`). Every route that takes it lists that one scheme, so the document
  shows seven adapters sharing one scheme instead of seven matching ones.
- **The language server**: hover on `principal sessions.me` shows the full
  declaration, and Go to Declaration opens it.

### 6. Bug classes, in U

| # | Bug class |
| --- | --- |
| U7 | `principal x.y` names something that isn't a top-level principal |
| U8 | two principals in one adapter have the same name, whether inline, named, or renamed with `as` |
| U9 | a top-level principal's `from` names something that isn't a top-level principal, or top-level principals depend on each other in a cycle |

U1, U2 and U5 are checked where the principal is declared, whichever form it
takes. U6 (http only) applies to the adapter that names it.

## Rationale

- **One source of truth for a security decision.** The cookie name, the
  binder and the refusal are written once. Changing how a caller is recognised
  changes every adapter that settles that caller, and none can be left on the
  old scheme.
- **Locality is kept.** Each adapter still says, at the top, which principals
  guard its routes. What it no longer repeats is how they are settled,
  which is the identity package's knowledge, not the adapter's.
- **It is the shape the rest of the industry converged on**: named definition,
  named reference (OpenAPI, Phoenix, ASP.NET, FastAPI).
- **ADR 0137's guarantees are untouched.** Default-deny, settling before the
  body is read, typed parameters, and no ambient principal all hold, because
  the adapter still declares, by name, what it settles.

## Alternatives considered

- **Keep only the inline form (ADR 0137 as it stands).** Locality is perfect,
  but the copies drift, and a drifted copy guards nothing it was meant to.
  The repetition is a security risk before it's a style issue.
- **Declared once in the `application` block, for every adapter.** ADR 0137
  rejected this, and the reason still holds: the adapter would take
  parameters it never mentions. This ADR keeps the "once" and drops the "for
  every adapter".
- **Settled by the type, as in Axum.** A route would take `me:
  accounts.Identity` and the type would know how to extract itself. That's the
  shortest form, but one type could then be settled only one way (an
  `Identity` from a cookie in one adapter and from an API key in another
  would be impossible), and a route's reader couldn't see that the route is
  guarded, or by what.
- **As a `rule`.** Settling a principal calls a workflow that reads the
  session store, so it isn't a pure decision, which is what a rule is. A
  principal is a declaration that wires a credential to a workflow and a
  refusal, like an adapter route, not computation.
- **A group of principals in one file** (`principals sessions { me ...
  admin ... }`, like a `formulas` group). `me` and `admin` are related, and a
  group would keep them together. But a group adds a second declaration shape
  for one feature, and the one-declaration-per-file rule already gives each
  a file Go to Declaration can open. See Open questions.

## Consequences

- Grammar: `principal-decl` allowed at the top level; `principal-ref` in an
  adapter body. `fiig fmt` prints named references before inline principals.
- Checker: U7-U9; U1, U2, U5 move to wherever the principal is declared.
- Runtime: none beyond resolution. A named principal resolves to the same
  `syntax.PrincipalDecl` the inline form produces, so `principalsFor` and
  `settlePrincipals` (ADR 0137 implementation notes) settle it unchanged.
- OpenAPI, registry, language server: §5.
- The TextMate grammar and the IntelliJ lexer read `principal` at the start
  of a top-level line as well as inside an adapter.
- Hearsay: two files in `sessions` (`Me.fiig`, `Admin.fiig`); seven adapters'
  `me` lines and two `admin` lines become `principal sessions.me` and
  `principal sessions.admin`; the federation adapter keeps its inline `peer`.

## Open questions

- **Should an inline principal that duplicates a named one be an error?** Two
  inline principals with the same source, binder and refusal, in one
  application, are exactly the drift this ADR exists to stop, and that is
  never what anyone wants. It might pass ADR 0051's test as "declare it once
  (ADR 0141)". Against it: it is a rule about copies, not about the program's
  meaning, and fiig has no other check of that kind.
- **A group for related principals** (Alternatives, last item): wait until an
  application has more than two related principals in one package.
- **The OpenAPI scheme name.** `sessionsMe` is mechanical. An application
  might want `sessionCookie`. That could come from the principal's name if
  applications name them that way, without new syntax.

## Implementation notes (2026-11-21)

- Syntax: `PrincipalDecl` is a declaration too (`DeclName`, `DeclKindName`
  say `principal`); `AdapterDecl.PrincipalRefs` holds `principal x.y [as
  z]`, with `HasPrincipals` and `PrincipalNames` over both forms. The
  parser reads a top-level `principal name:` as a declaration, and in an
  adapter a name followed by `:` as inline, anything else as a reference.
  `fiig fmt` prints inline principals, then named ones, then the routes.
- Checker (`compiler/checker/principal.go`): one `checkPrincipal` for U1
  and U2 in either form; `checkTopPrincipal` adds U9; an adapter's named
  principals resolve first (U7, U8), so an inline one may be settled from
  a named one (`principal again: T from me by ...`). A route's reference
  to a named principal points at the top-level declaration, so Go to
  Declaration and Find Usages reach it.
- Runtime (`compiler/adapter/principal.go`): `principalScope` resolves a
  reference through the adapter's package's imports; each plan step
  carries its declaring package, and the binder and refusal run under
  `Interp.EnterPackage` there, so `signedIn` written in `sessions` resolves
  in `sessions`. Settled values are kept by declaration, so a route that
  takes `admin` settles `me` once, even when the adapter names only
  `admin` (§3).
- OpenAPI: a named principal's scheme is `alias` + `Name` (`sessionsMe`),
  or the bare name in the adapter's own package; its refusal's statuses
  resolve in its own package. Inline principals keep the cookie or header
  name as the scheme, as before.
- Registry: a top-level principal has a `principal` card with its binder,
  refusal and `from` as dependencies; an adapter's card lists `principal
  x.y` for each named one, and its dependencies include them. `fiig
  config` follows a named principal's binder and refusal in its package.
- TextMate grammar: `principal` at the start of a line is a declaration
  word. **Not done:** an IntelliJ file icon for a principal file, which
  needs the plugin's Kotlin build.
- Tests: `TestANamedPrincipalChecks`, U7, U8 and U9 tests (checker);
  `TestFormatNamedPrincipalsAreStable`; `TestANamedRoleSettlesWhatItIsSettledFrom`
  (adapter, with OpenAPI); `TestANamedPrincipalIsSettledInItsOwnPackage`
  (servecmd, a three-package module through the loader).
- Dogfooded: a module where `sessions` declares `me` and `admin`, `books`
  names `sessions.me` and `admin` names only `sessions.admin`. Under `fiig
  serve`, no cookie gave 401 on both, `ann` 200 on books and 403 on admin,
  `boss` 200; `fiig openapi` had one `sessionsMe` scheme shared by both
  routes, each with 401 and 403.
- ADR 0104's one-file-per-name rule means a principal `admin` and a model
  `Admin` can't share a package (both want `Admin.fiig`). The ADR's Hearsay
  layout already keeps the model in `accounts`.
