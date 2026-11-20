# ADR 0095: `mapErr(e in result): newError`

Status: Proposed (built 2026-11-19 under the owner's goal to work through
the abs-recommendations gaps; awaiting review)
Date: 2026-11-19
Amends: `grammar.md` (a list-form-shaped expression over a `Result`).
Related: ADR 0006 (`?` never converts an error), ADR 0073 §2
(`json.decode` takes its type from context), ADR 0093 (list forms), docs
`writing-a-client.md`, abs-recommendations GAPS G8, G19, G23.

## Context

ADR 0006 decided that `?` propagates an error only when the enclosing
declaration already returns that error type: reinterpreting an error is
written out. abs-recommendations has now written it out in three places,
and each is boilerplate:

- **G8:** every `AbsApi` gateway ends in four lines that decode a reply
  and turn a `JsonError` into `AbsError.Unreadable`:
  ```
  parsed: Result<AbsUser, JsonError> = json.decode(reply.body)
  match parsed {
      Err(failure) -> Err(AbsError.Unreadable(failure: failure))
      Ok(user) -> Ok(user)
  }
  ```
- **G19:** four conversions in their own files (`readDiscovery`,
  `readTokenReply`, `readUserInfo`, `readUsers`) that differ only in the
  type they decode.
- **G23:** `signIn(...).mapErr(apiErrorOfSignIn)?` doesn't exist, so each
  pair of error types gets a conversion from `Result<T, E1>` to
  `Result<T, E2>`.

A shared conversion would need to work for any `T`, which needs generics.
A method `r.mapErr(f)` would need a function value, which ADR 0056 turned
down. ADR 0093 gave the same problem for lists a form whose body is
written in place, and that answer fits here too.

### How others do it

| Language | Change a result's error |
|---|---|
| Rust | `r.map_err(\|e\| AbsError::Unreadable(e))?`, or `?` through a `From` impl |
| Swift | `r.mapError { .unreadable($0) }` |
| Kotlin (`Result`, Arrow `Either`) | `mapLeft { ... }` |
| Scala | `either.left.map(...)` |
| Go | `if err != nil { return fmt.Errorf("...: %w", err) }` |

Rust's `From` is the implicit route ADR 0006 refused. The others write
the mapping at the call, as a function value.

## Decision

### 1. The form

```
mapErr(e in result): newError
```

`result` is a `Result<T, E1>`. When it is `Ok(v)`, the form is `Ok(v)`.
When it is `Err(x)`, the form is `Err(body)`, with `e` bound to `x`. Its
type is `Result<T, E2>`, where `E2` is the body's type.

**A `?` at the end applies to the whole form.** The body is an error
value, so a `?` on it would never mean anything; `mapErr(e in r):
Wrap(e)?` is `(mapErr(e in r): Wrap(e))?`. With `?`:

```
reply = absReply(HttpClient.send(absGet(Abs.url, "/api/me", accessToken)))?
user: AbsUser = mapErr(e in json.decode(reply.body)): AbsError.Unreadable(failure: e)?
return Ok(user)
```

G8's four lines become one, G19's four conversion files go away, and
G23 reads `mapErr(e in signIn(attempt)): apiErrorOfSignIn(e)?`.

### 2. Context reaches the result inside

The form passes the type it is expected to have to the result inside it,
so `json.decode` still takes its type from context (ADR 0073 §2):

- `user: AbsUser = mapErr(e in json.decode(b)): ...?` decodes into
  `AbsUser`.
- `return mapErr(e in json.decode(b)): ...` in a function returning
  `Result<AbsUser, AbsError>` decodes into `AbsUser`, and the body must be
  an `AbsError` (A1).

### 3. What the body may call

The body maps one value, so it may call what the enclosing declaration
may, **except anything with an effect**: formulas, methods, and the rules
and conversions the enclosing kind may call. A `mapErr` in a gateway may
call `apiErrorOfSignIn`, a conversion; a `mapErr` in a rule may call only
formulas, as the rule may. A gateway, an activity, or a capability is
never callable from a body, so the form can't hide an effect in an error
path.

This is wider than ADR 0093's list forms, whose bodies may call only what
a rule may. The difference is deliberate: an error mapping is the
conversion's job, and G23's mapper is a conversion. Whether the list
forms should widen the same way is open question 1.

### 4. The rest follows the list forms

- `mapErr` is contextual: the form starts only at `mapErr ( name in`.
- A `mapErr` whose result is dropped is A13. A `mapErr` left unhandled is
  F1, as any `Result` is.
- A `?` inside the body, before its end (`Wrap(check(e)?)`), returns from
  the enclosing declaration, as in the list forms.

## Rationale

- **The mapping is still written out.** ADR 0006's point stands: nothing
  converts an error by itself. The form only removes the `match` that
  says "and `Ok` stays `Ok`".
- **No generics, no function values.** The body is checked where it's
  written, like ADR 0093's forms, so `T` is whatever the result's is.
- **`mapErr` is Rust's and Swift's name** (`map_err`, `mapError`), in
  fiig's camelCase.

## Alternatives considered

- **`?` through a declared conversion**, Rust's `From`: `user: AbsUser =
  json.decode(b)?` finding a conversion from `JsonError` to `AbsError`.
  Shortest, but it's the implicit conversion ADR 0006 refused, and which
  conversion applies would depend on what's in scope.
- **Generic conversions.** Solves this and more, but generics are a much
  larger decision, and this is the only attested need.
- **A method, `r.mapErr(f)`.** Needs function values (ADR 0056).
- **`r.mapErr(e -> ...)`.** An inline lambda; ADR 0093 kept the `in`
  spelling for forms, so this would be a second spelling.

## Consequences

- GAPS G19 and G23 close, and G8's last four lines become one.
- `writing-a-client.md`'s "what still repeats" section is replaced by
  this form.
- Generics lose their only attested use. They stay open, for when
  something other than error mapping needs them.

## Open questions

1. **Should ADR 0093's list forms take §3's wider rule?** A `map(r in
   rows): toApi(r)` calling a conversion is the obvious next case.
2. **`map(v in result): ...` for the `Ok` side.** Not needed by any
   program yet.

## Implementation notes

Built 2026-11-19.

- `mapErr` is an ADR 0093 `ListForm` whose iterable is a `Result`. The
  parser moves a `?` that ends a mapErr body onto the form (§1).
- `checkExpectedMapErr` (`compiler/checker/listform.go`) passes the
  context to the result inside: a `json.decode` there decodes into the
  binding's type with `?`, or the return type's `Ok` type without.
- §3: the body is checked with the caller kind of a conversion (a rule's
  or formula's stays its own), which is the pure part of every wider
  kind's table. A gateway called from a body is B5.
- **Dogfood** on a copy of abs-recommendations: `signin-authentik`'s four
  `read*` conversion files are gone, and its five decodes are one line
  each; `abs-client`'s four gateways are two or three lines each. All
  tests pass (abs-client 5, signin-authentik 10, signin-password 2,
  backend 13).
