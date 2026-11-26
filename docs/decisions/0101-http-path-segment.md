# ADR 0101: `http.pathSegment`, Building a URL Path From Untrusted Text

Status: Accepted (2026-11-20). Implemented 2026-11-20. Amended by ADR 0151
(2026-11-25): `pathSegment` is a formula in the `httpText` group
(`httpText.pathSegment(...)`).
Date: 2026-11-20
Amends: ADR 0074 (fiig.http gains a conversion).
Related: ADR 0082 §3 (SQL text is a literal, values are bound), docs
`writing-a-client.md`, abs-recommendations GAPS G34 (security).

## Context

A client builds a URL with interpolation:

```
absGet(Abs.url, "/api/items/${itemId}", token)
```

If `itemId` came from a request, it's pasted into the path as is.
abs-recommendations found in review that an id like `../../api/users`
would have sent a request to a different ABS endpoint, with the server's
own token. It now checks every id by hand (`validId`: letters, digits,
`-`, `_`). fiig.http has `formEncode` for a query string, and nothing for
a path.

### How others do it

| Language | Encode one path segment |
|---|---|
| Go | `url.PathEscape(s)` |
| JavaScript | `encodeURIComponent(s)` |
| Python | `urllib.parse.quote(s, safe="")` |
| Java (Spring) | `UriComponentsBuilder...buildAndExpand(id).encode()` |
| Kotlin (Ktor) | `URLBuilder().appendPathSegments(id)` |

All of them percent-encode `/`. None of them stop `.` or `..`, which are
unreserved characters but mean "this directory" and "up one" as a whole
segment, and a server may decode `%2E%2E` to `..` before resolving
(RFC 3986 §6.2.2.2).

## Decision

```
conversion pathSegment(text: string) -> string | None
```

- Percent-encodes every byte of the UTF-8 text except RFC 3986's
  unreserved characters (`A-Z a-z 0-9 - . _ ~`), so `/`, `?`, `#`, `%`
  and spaces can't change the URL's shape.
- Returns `None` for a text that can't be one segment: empty, `.`, or
  `..`. The caller decides what that means, usually "not found":

```
match http.pathSegment(itemId) {
    None -> Err(AbsError.NotFound)
    safe -> absGet(Abs.url, "/api/items/${safe}", token)
}
```

`writing-a-client.md` uses it wherever an argument goes into a path.

## Rationale

- **The safe way should be the short way.** Hand-written id checks are
  the kind of thing a reviewer finds missing.
- **`None` for `.` and `..`**, which encoding can't make safe, so the
  caller has to decide, rather than a fault at run time.

## Alternatives considered

- **A lint on interpolation inside a URL string.** A string doesn't know
  it's a URL; the check would guess.
- **A URL builder** (`http.url(base, ["api", "items", id])`). Larger;
  `pathSegment` covers the attested case and composes with
  interpolation.
- **Faulting on `..`.** A fault for input a client controls is the wrong
  tool (ADR 0083's line between faults and values).

## Consequences

- G34 closes. The app can replace `validId` with `http.pathSegment`, or
  keep `validId` as a stricter shape check on top.
- `fiig.http`'s hash changes, so lockfiles that pin it refresh.
