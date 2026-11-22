# ADR 0144: A Header Wire Names Its Auth Scheme

Status: Accepted
Date: 2026-11-21
Related: ADR 0137 (principals), ADR 0141 (named principals, OpenAPI
schemes), ADR 0143 (wires), ADR 0142 (syntax used here), Hearsay GAPS G65.

## Context

Hearsay's federation adapter settles the calling server from a bearer key:

```fiig
wire peer: LinkedServer from header "Authorization" by callingServer else peerRefused
```

The adapter hands `callingServer` the whole header, and the binder parses it:

```fiig
workflow callingServer(authorization: string | None) -> Result<LinkedServer, PeerRefusal> {
    return match (authorization) {
        None -> { return Err(PeerRefusal.NoKey); }
        given -> {
            if (!given.startsWith("Bearer ")) {
                return Err(PeerRefusal.NoKey);
            }
            return match (linkWithKey(given.removePrefix("Bearer ").trim())) { ... }
        }
    }
}
```

Three things follow from the prefix living in the binder:

1. **OpenAPI can't see it.** `fiig openapi` describes the wire as
   `{"type": "apiKey", "in": "header", "name": "Authorization"}`. A reader,
   or a generated client, gets no hint that the value starts with `Bearer `
   (G65). Bearer auth has its own OpenAPI form, `{"type": "http",
   "scheme": "bearer"}`, which clients know how to fill.
2. **Every binder repeats the same parsing**, and it's easy to get slightly
   wrong. The scheme name is case-insensitive (RFC 9110 §11.1: `bearer
   abc` is valid), and Hearsay's `startsWith("Bearer ")` refuses it.
3. **The binder does two jobs.** It parses the credential's wire format,
   then decides who is calling. Only the second is the application's
   decision. The cookie form already works this way: the runtime reads
   the cookie, and the binder gets the value.

## Decision

### 1. `scheme` after a header source

A single wire read from a header may name the auth scheme its value uses:

```fiig
wire peer: LinkedServer from header "Authorization" scheme "Bearer" by callingServer else peerRefused
```

`scheme` is contextual, like `from` and `by`. The grammar's `wire-single`
becomes:

```
wire-single ::= 'wire' identifier ':' type 'from'
                ( 'header' string-literal [ 'scheme' string-literal ]
                | 'cookie' string-literal
                | identifier )
                'by' qualified-name 'else' qualified-name
```

The same holds for an inline wire in an adapter.

### 2. The runtime strips the prefix

With `scheme "Bearer"`, the binder's input is the credential alone.

| Header | Binder gets |
| --- | --- |
| `Authorization: Bearer abc123` | `"abc123"` |
| `Authorization: bearer abc123` | `"abc123"` (the scheme name is case-insensitive) |
| `Authorization: Bearer   abc123 ` | `"abc123"` (surrounding spaces trimmed) |
| `Authorization: Basic dXNlcjpwdw==` | `None` (another scheme) |
| `Authorization: Bearer` or `Bearer ` | `None` (no credential) |
| no header | `None` |

The binder's parameter type doesn't change (`string | None`), so the binder
decides what a missing credential means, as it already does for a missing
cookie. Hearsay's binder loses its prefix check:

```fiig
workflow callingServer(key: string | None) -> Result<LinkedServer, PeerRefusal> {
    return match (key) {
        None -> { return Err(PeerRefusal.NoKey); }
        given -> {
            return match (linkWithKey(given)) {
                None -> { return Err(PeerRefusal.UnknownKey); }
                link -> { return Ok(link); }
            }
        }
    }
}
```

### 3. OpenAPI says bearer

A wire with `scheme "Bearer"` is described as
`{"type": "http", "scheme": "bearer"}`. It keeps the scheme name ADR 0141
gives it (`linksPeer` for a named wire, the header name for an inline one).
A header wire without `scheme` is still an `apiKey` in that header, as
today.

### 4. Only `"Bearer"`, for now

`"Bearer"` is the one scheme accepted. `"Basic"` would need decoding
base64 into a user and password, which means a second value shape, not a
string. Other schemes (`Digest`, `Negotiate`) are challenge-response
protocols that a header read can't settle. Each can be added when an
application needs it, as a new accepted string.

### 5. Bug class

| # | Bug class |
| --- | --- |
| U11 | `scheme` on a wire that isn't read from a header, or a scheme other than `"Bearer"` |

The parser reports U11, as it reports U10. `scheme` after `cookie` or after
another wire doesn't parse as part of the wire; the message names U11.

## Rationale

- **The declaration says what the credential is.** Where the credential is
  (header, cookie) is already declared, and the runtime reads it. Its
  format belongs in the same place, so the reader of the wire line sees the
  whole contract and OpenAPI can publish it.
- **It can't drift.** If `scheme` only fed OpenAPI, the document could say
  bearer while the binder checked something else. Because the runtime
  strips the prefix, the declaration is what's enforced.
- **Binders get simpler and more correct.** Case-insensitive matching and
  trimming are written once, in the runtime, instead of in every
  application's binder.

## Alternatives considered

- **Document that the binder owns the format** (G65's other suggestion).
  No language change, but OpenAPI stays wrong and every binder keeps
  parsing the prefix.
- **A source of its own, `from bearer`**, implying the `Authorization`
  header. Shorter, but a proxy credential lives in `Proxy-Authorization`,
  and adding Basic later would mean another source word. `scheme` keeps one
  header source with one option.
- **Infer bearer from the header name `Authorization`.** That header also
  carries Basic and others, so the guess would be wrong for some
  applications and invisible to the reader.

## Consequences

- Parser: `scheme` after a header source, U11. `fiig fmt` prints it between
  the source and `by`.
- Syntax: `PrincipalDecl` gains a `Scheme` string.
- Runtime (`compiler/adapter/principal.go`): `settlePrincipals` strips the
  prefix before binding the input.
- OpenAPI (`describePrincipals`): the `http`/`bearer` form for a wire with
  a scheme.
- Registry card: the wire's source includes the scheme.
- Editors: no grammar change. Neither TextMate nor IntelliJ colours the
  wire line's other contextual words (`from`, `header`, `by`), so `scheme`
  reads like them. The LSP hover shows it.
- Docs: grammar.md's `wire-single`, bug-classes.md's U11 row.
- Hearsay: `FederationApi.fiig`'s `peer` line gains `scheme "Bearer"`, and
  `callingServer` drops its prefix check. Regenerate `openapi.json` and
  `schema.d.ts`.

## Open questions

- **A refusal's `WWW-Authenticate` header.** Hearsay's `peerRefused` writes
  `WWW-Authenticate: Bearer` by hand. The runtime could add it to a 401
  from a bearer wire's refusal, but that's a change to what a refusal
  returns, and it can wait until a second application repeats the line.
