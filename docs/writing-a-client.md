# Writing a client over fiig.http

A client for someone else's API is a capability group: one gateway per
call, each requiring `HttpClient` (ADR 0043, ADR 0074). Its gateways do
the same few things every time: send, turn a failure or an unwanted status
into the client's error, and decode the body. This page shows how to write
those once.

## The shape

```fiig
use fiig.http;
use fiig.http.HttpClient;

capability ShelfApi {
    gateway book(token: string, id: string) -> Result<Book, ShelfError> requires HttpClient {
        reply = shelfReply(HttpClient.send(shelfGet(Shelf.url, "/api/books/${id}", token)))?;
        return mapErr(e in json.decode(reply.body)): ShelfError.Unreadable(failure: e);
    }
}
```

The shared steps are conversions, each in its own file:

```fiig
use fiig.http;

// A sent request as the server answers it: unreachable, refused by
// status, or a 200 reply to decode.
conversion shelfReply(sent: Result<http.Reply, http.HttpError>) -> Result<http.Reply, ShelfError> {
    return match (sent) {
        Err(failure) -> Err(ShelfError.Unreachable(failure: failure)),
        Ok(reply) -> {
            if (reply.status != 200) {
                return Err(shelfFailure(reply.status));
            }

            return Ok(reply);
        }
    }
}
```

`Shelf` is the client's config (`config Shelf { url: string from env
"SHELF_URL" }`, ADR 0071), so it can't share the group's name. The rule
`shelfGet` builds the request (URL, `Authorization` header, timeout), and
the conversion `shelfFailure` says what a status means (401 is
`Unauthorized`, 404 is `NotFound`).

## Why it's shaped this way

- **The send stays in the gateway.** Only a gateway may call `HttpClient`,
  and a gateway can't call another gateway, so the effect is always in the
  gateway the caller named. What's shared is everything around the effect,
  which is pure: building the request and judging the reply.
- **A conversion turns the result into the client's error, and `?` passes
  it on.** `?` never converts an error itself (ADR 0006), so the
  conversion is where `HttpError` becomes `ShelfError`, written once. The
  Postgres driver does the same over `Socket`:
  `connection = connectionOr(Socket.connect(host, port))?`.
- **A capability group has no `private` block.** Its shared steps are
  ordinary rules and conversions, which also means each one can be tested
  by passing it values.

## Putting an argument into a path

Text a caller supplied goes into a path through `http.pathSegment` (ADR
0101), never straight into the interpolation: an id like
`../../api/users` would otherwise reach another endpoint with the
client's credentials. It percent-encodes what could change the URL's
shape, and is `None` for empty, `.` and `..`:

```fiig
return match (http.pathSegment(id)) {
    None -> Err(ShelfError.NotFound)
    safe -> shelfBook(token, safe)
}
```

## Testing it

Fake `HttpClient`, not the client, so the real gateway bodies run (ADR
0091). Give the fake replies shaped like the live server's, and fake the
client's config:

```fiig
use fiig.http;
use fiig.http.HttpClient;

test bookSendsTheToken {
    fake Shelf {
        url: "https://shelf.test";
    }

    fake HttpClient {
        gateway send(request: http.Request) -> Result<http.Reply, http.HttpError> {
            expect http.headerValue(request.headers, "authorization") == "Bearer tok-1";
            return Ok(
                http.Reply(
                    status: 200,
                    headers: [],
                    body: "{\"id\":\"b-1\",\"title\":\"Dune\"}".utf8()
                )
            );
        }
    }

    expect ShelfApi.book("tok-1", "b-1") is Ok(found);
    expect found.title == "Dune";
}
```

## Decoding the reply

`json.decode` returns a `Result<T, JsonError>`, and `mapErr` (ADR 0095)
turns its error into the client's, so the decode is one line. To keep the
value and do more with it, add `?`:

```fiig
found: ShelfItem = mapErr(e in json.decode(reply.body)): ShelfError.Unreadable(failure: e)?;
return Ok(bookOfItem(found));
```

`json.decode` still takes its type from the line it's on (`found:
ShelfItem`, or the gateway's return type).
