# ADR 0121: An S3-Compatible Object Store Client in fiig

Status: Accepted (2026-11-20; the owner approved it in review). Implemented 2026-11-20.
Date: 2026-11-20
Related: ADR 0042 (the socket floor), ADR 0043 (capability groups), ADR 0071
(config), ADR 0074 (`fiig.http`, which this is built on), ADR 0075 (hashing and
HMAC), ADR 0101 (`http.pathSegment`), ADR 0109 (connections kept between
calls), ADR 0120 (`fiig.smtp`, the other package of this round).

## Context

Programs keep files somewhere: uploads, reports, exports, backups. The place
is almost always an object store that speaks the S3 API: AWS S3, MinIO,
Cloudflare R2, Backblaze B2, DigitalOcean Spaces, Wasabi, Ceph, and the
S3-compatible mode of Google Cloud Storage. fiig had nothing for it, and a
program that needed one would have called a Go provider, which Milestone 7
rules out.

The protocol is HTTP plus a signature. Each request carries an
`Authorization` header computed with AWS Signature Version 4, which is
SHA-256 and HMAC-SHA-256, both of which fiig has (ADR 0075). So the client
is a library over `fiig.http`, with no new runtime work.

How other languages shape it:

| Library | Surface | Signing |
| --- | --- | --- |
| boto3 | the whole AWS API, a client per service | SigV4, headers or presigned URL |
| aws-sdk-go-v2 | the same, with a streaming body | SigV4; unsigned payload over TLS |
| minio-go | the S3 object API only (about 60 calls) | SigV4, chunked for large bodies |

An SDK is large because AWS has hundreds of operations. A program that keeps
files uses four: put an object, get it, ask about it, delete it.

## Decision

### 1. Four operations: `S3.put`, `get`, `head`, `delete`

```fiig
capability S3 {
    gateway put(bucket: string, key: string, body: bytes, contentType: string) -> Result<None, S3Error>
    gateway get(bucket: string, key: string) -> Result<bytes, S3Error>
    gateway head(bucket: string, key: string) -> Result<ObjectInfo, S3Error>
    gateway delete(bucket: string, key: string) -> Result<None, S3Error>
}
```

Each `requires HttpClient`, so `fiig.http`'s connection handling (ADR 0109)
and its limits apply unchanged. `ObjectInfo` is `size`, `contentType`, and
`etag`. A key is any UTF-8 text up to 1024 bytes, with `/` as the folder
convention S3 clients use; each segment is percent-encoded once, so a `?`,
`#`, `%`, space or accent in a key can't change the URL, and a `.` or `..`
segment is refused, since a server would resolve it as "here" or "up one".

### 2. Settings: `S3_ENDPOINT` and the keys

```fiig
config S3Settings {
    endpoint: string from env "S3_ENDPOINT"
    region: string from env "S3_REGION" = "us-east-1"
    accessKeyId: string from env "S3_ACCESS_KEY_ID"
    secretAccessKey: Secret from env "S3_SECRET_ACCESS_KEY"
    sessionToken: Secret | None from env "S3_SESSION_TOKEN"
    pathStyle: bool from env "S3_PATH_STYLE" = true
    timeoutMillis: int from env "S3_TIMEOUT_MS" = 30000
}
```

- **The endpoint** is the service's address, `https://s3.eu-west-1.amazonaws.com`
  or `http://localhost:9000`: a scheme and a host with an optional port, and
  nothing else. A path would be signed and sent on every object, a bucket
  belongs in each call, and user information would put a secret in a URL the
  checker can't hide. Each is refused with a reason that never repeats the
  value.
- **Path style** (`host/bucket/key`) is the default because every
  S3-compatible server takes it and needs no DNS for the bucket.
  `S3_PATH_STYLE=false` is virtual-hosted style (`bucket.host/key`), which
  AWS's documentation recommends; with it a bucket name containing a dot can't
  be used over TLS, since the certificate won't cover it.
- **The keys are Secrets.** `.reveal()` is legal only in a gateway, so each
  gateway reveals them and passes strings to the signing conversions, and
  what goes on the wire is the signature, never the key. A session token
  (temporary credentials from STS or an instance role) is sent as
  `x-amz-security-token` and signed.
- **A bucket** is 3 to 63 lowercase letters, digits, hyphens and dots, starting
  and ending with a letter or digit. The server enforces the finer rules.

### 3. Signing

AWS Signature Version 4, written in conversions so that each step is a pure
function:

- `canonicalRequest`: the method, the path as it is sent, no query, the
  headers `host`, `x-amz-content-sha256`, `x-amz-date` (and
  `x-amz-security-token`), the names of the signed headers, and the body's
  SHA-256 in hex.
- `stringToSign`: the algorithm, the timestamp, the credential scope
  (`20261120/us-east-1/s3/aws4_request`), and the SHA-256 of the canonical
  request.
- `signingKey`: the secret, prefixed `AWS4`, keys an HMAC of the day, whose
  result keys one of the region, then the service, then `aws4_request`.
- `signatureOf`, `signedRequest`, which assembles the `http.Request`.

**The body's hash is always signed** (`x-amz-content-sha256` carries it, not
`UNSIGNED-PAYLOAD`), so a body altered on the way fails the check. The cost
is hashing the body, which is Go code in `crypto.sha256` and fast.

**The timestamp is `fiig.time`'s `formatBasic`** (ADR 0123), which turns
`Clock.now()` into `20261120T135958Z`. It began as a hand-written `amzDate`
here, beside the same code in `fiig.smtp`, and both moved to `fiig.time`.

### 4. Errors

```fiig
enum S3Error {
    NotFound
    Refused(status: int, code: string, message: string)
    Http(failure: http.HttpError)
    InvalidRequest(reason: string)
    InvalidConfig(reason: string)
}
```

- **A missing key is `NotFound`**, a branch a program expects, apart from a
  failure. **A missing bucket is `Refused(404, "NoSuchBucket", ...)`**, since
  it is a mistake in the call or the settings. A HEAD has no body to tell
  them apart by, so it reports `NotFound` for both.
- **Any other status is `Refused`** with the server's `<Code>` and
  `<Message>`, read by looking for the two tags and not as XML, since the
  error document is the server's to shape (`SignatureDoesNotMatch`,
  `AccessDenied`). A reply with no such document has empty ones.
- **A delete of a key that isn't there is `Ok`**: S3 answers 204, so a
  delete is safe to repeat. Redirects (a bucket in another region) are not
  followed: they come back as `Refused` with the server's code.
- `InvalidRequest` and `InvalidConfig` are refused before any connection is
  made.

### 5. Scope

In: put, get, head and delete of one object, over HTTP or HTTPS, in path or
virtual-hosted style, with permanent or temporary credentials.

Out: listing, multipart upload, presigned URLs, ranges, copy, bucket
operations, custom metadata, server-side encryption, and streaming bodies
(`fiig.http` reads and writes a whole body in memory).

*(Amended 2026-11-21: metadata, checksums, ranges and copy are in, as four
more gateways beside the first four, whose signatures don't change:*

```fiig
gateway putWithMetadata(bucket: string, key: string, body: bytes, contentType: string, metadata: Map<string, string>) -> Result<None, S3Error>
gateway getObject(bucket: string, key: string) -> Result<S3Object, S3Error>
gateway getRange(bucket: string, key: string, first: int, last: int | None) -> Result<bytes, S3Error>
gateway copy(fromBucket: string, fromKey: string, toBucket: string, toKey: string) -> Result<None, S3Error>
```

*- **Every PUT sends `x-amz-checksum-sha256`**, the body's SHA-256 in
  base64, so S3 refuses an upload whose bytes differ and stores the checksum
  with the object. `x-amz-content-sha256` already lets the signature catch a
  changed body; the checksum is what the object keeps. `Content-MD5` isn't
  sent: SHA-256 covers what it would.
- **Metadata** is a `Map<string, string>`; each key is sent as
  `x-amz-meta-<key>` in lower case, which is how S3 keeps it, so two keys
  that differ only in case are refused instead of one replacing the other. A
  key must be a header name and a value printable ASCII: S3 hands a value
  back byte for byte, and a line break would add a header of the caller's
  choosing. `ObjectInfo` gains `metadata`, read from a reply's
  `x-amz-meta-*` headers in any case, so `head` returns it; `get` keeps
  returning bytes and `getObject` returns `S3Object(body, info)`. A gateway
  can't have an optional parameter (only builtins can, ADR 0074), which is
  why these are new gateways and not a parameter on `put` and `get`.
- **A range** is bytes `first` to `last`, both included, or to the end when
  `last` is `None`. A negative start or a backward range is
  `InvalidRequest`; a 206's body is the part; a 416, a start past the end,
  is the new variant `S3Error.OutOfRange`. A server that ignores `Range` and
  answers 200 with the whole object has the part cut from it, the same bytes
  a 206 would have held.
- **Copy** is a PUT of the destination with `x-amz-copy-source`, the
  source's path-style path (`/bucket/key`, each key segment percent-encoded
  once), within a bucket or across two; the source is checked like any
  object. S3 can answer a copy 200 and fail it in the body, so a 200 whose
  body holds `<Error>` is `Refused(200, code, message)`, found by a substring
  check since fiig has no XML reader. Metadata and content type are copied
  with the object (S3's default `COPY` directive).
- **Signing is over every header an operation adds**, `Range` included,
  which needed the header list to stop being fixed: `canonicalHeaders`
  lower-cases names, trims values and makes their runs of spaces one (as
  SigV4 canonicalises them), sorts, and refuses two of one name;
  `canonicalRequestOf` and `signedHeaderList` build the canonical request
  and `SignedHeaders` from it. `S3Call` gains `headers`. `canonicalRequest`
  and `signedHeaderNames` stay, for the four headers every request signs.)*

*(Amended 2026-11-21: presigned URLs are in, as a ninth gateway:*

```fiig
gateway presign(method: string, bucket: string, key: string, expiresSeconds: int) -> Result<string, S3Error>
    requires HttpClient
```

*- **SigV4 in the query string**: `X-Amz-Algorithm`, `X-Amz-Credential`,
  `X-Amz-Date`, `X-Amz-Expires`, `X-Amz-Security-Token` with temporary
  credentials, and `X-Amz-SignedHeaders=host`, then `X-Amz-Signature`. Only
  `host` is signed and the payload is `UNSIGNED-PAYLOAD`, since the body
  isn't known when the link is made; that is the one place this package
  doesn't sign a body (§3), and it is how every SDK presigns. The token is
  signed with the rest, as botocore and the Go SDK do it. The query has a
  fixed set of names, written in SigV4's sorted order, so the general query
  canonicaliser the "presigned URLs first" alternative assumed isn't needed;
  listing still needs one.
- **The method is GET, PUT, HEAD or DELETE**, as `signedRequest` takes, and
  the expiry is 1 to 604800 seconds (seven days), S3's own bounds; anything
  else is `InvalidRequest`, as is a key or bucket `objectTarget` refuses.
  The URL is path or virtual-hosted style as `S3_PATH_STYLE` says.
- **The signing is the conversion `presignedUrl`**, which takes the
  `S3Account` and the time like `signedRequest`; the gateway reads the
  settings and `Clock.now()` and calls it.
- **It requires `HttpClient` and never calls it.** A gateway outside a fake
  must name what it requires (ADR 0010; the parser makes the clause
  mandatory), and only a gateway or an activity may read config or reveal a
  Secret (ADR 0071), so `presign` can't be a conversion. `HttpClient` is
  the client the link's request goes through, made by whoever holds the
  URL, and the package's gateways already require it, so this adds no
  binding: a program that uses `fiig.s3` binds `Socket` either way. Calling
  `presign` opens no connection (a Go test runs it against a socket script
  with no peers). Allowing a `requires`-less gateway for "reads settings,
  touches nothing" would be a language change for one operation, and the
  honest clause is close enough.)*

## Alternatives considered

- **The whole S3 API.** Hundreds of operations, most of which no program
  here uses, each with its own request and reply shape. The four are the
  ones a program that keeps files needs, and the rest can be added one at a
  time.
- **`UNSIGNED-PAYLOAD` over TLS**, as the AWS SDKs do. It saves hashing the
  body but gives up detecting a changed body, and doesn't work over plain
  HTTP, which MinIO and a local server use.
- **Presigned URLs first.** Useful for handing a browser an upload link,
  and the same signing with the signature in the query string. It needs the
  query to be canonicalised, which listing needs too, so they go together
  (open question 1).
- **A generic `S3.send` and request builders**, as `fiig.redis` does. S3's
  operations have distinct results (bytes, info, nothing), which four typed
  gateways give a program without a second step.

## Consequences

- New built-in package `fiig.s3`, depending on `fiig.http`, embedded beside
  it.
- A program that stores files `requires S3`, binds `Socket` for `fiig.http`,
  and sets the five variables. `Socket`'s `trust` (ADR 0086) is how a private
  CA is trusted.

## Open questions

1. **Listing** (`ListObjectsV2`). It needs the query string canonicalised
   and signed, and the reply, which is XML, parsed. fiig has no XML parser,
   so a list of keys means one, or a reader of this one document.
2. **Multipart upload**, for objects over 5 GiB (S3's limit for one PUT) and
   to retry a part, and **streaming**, which needs `fiig.http` to send and
   read a body in pieces (ADR 0074 §9). The whole object is in memory now.
3. ~~**Presigned URLs**, to give a browser a time-limited link.~~ Done
   2026-11-21 as `S3.presign` (§5's second amendment).
4. ~~**Ranges** (`Range: bytes=0-99`), for reading part of an object.~~
   Done 2026-11-21 as `S3.getRange` (§5's amendment).
5. ~~**Custom metadata and checksums** (`x-amz-meta-*`, `Content-MD5`,
   `x-amz-checksum-sha256`)~~ Done 2026-11-21: `putWithMetadata`,
   `getObject`, `ObjectInfo.metadata`, and a SHA-256 checksum on every PUT
   (§5's amendment). Server-side encryption headers are still open.
6. ~~**A time type**, to retire this package's `amzDate` and `fiig.smtp`'s
   `rfc5322Date`.~~ Done by ADR 0123, as the library `fiig.time`.
7. **A wrong region on AWS.** AWS answers a request signed for the wrong
   region with a 301 or 400 (`AuthorizationHeaderMalformed`) that names the
   right one. It comes back as `Refused` with that code, and the program can
   read the region out of the message. Not tried against AWS.

## Implementation notes (2026-11-20)

- `compiler/stdlib/s3/src`: `S3` (the capability), `S3Settings`, `S3Error`,
  `ObjectInfo`, `S3Account`, `S3Call`, `S3Endpoint`, `S3Target`,
  `ParseS3Endpoint`, `ObjectTarget`, `AmzDate`, `SigningKey`,
  `CanonicalRequest`, `SignedHeaderNames`, `StringToSign`, `SignatureOf`,
  `SignedRequest`, `ErrorOf`, `PutResult`, `GetResult`, `HeadResult`, and
  `DeleteResult`.
- **The expected values come from a reference that shares no code with
  this package**: a SigV4 implementation in Python (`hmac` and `hashlib`),
  which generated the fiig tests and the Go tests. It reproduces AWS's own
  published signing-key example (`f4780e2d...404d`, for IAM), and a real
  server accepted a request it signed. That is the point of it: a mistake in
  the fiig code can't also be in the expectation.
- **Tests.** 57 fiig tests (`compiler/stdlib/s3/test`): the timestamp
  against known instants, the signing key against AWS's example, the canonical
  request, string to sign and signature for a GET, a PUT with an encoded key,
  a session token and a port, every refused key, bucket and endpoint, and each
  reply's handling. Eight Go tests (`tooling/runprogram/s3_test.go`) run the
  gateways against scripted servers byte for byte, signatures included: a
  PUT, a GET, a missing key, a 403 with the server's code (and the secret absent
  from the error), a HEAD, a DELETE, a session token, and a bad key or endpoint
  that never connects. A package whose path starts `fiig.` can't run `fiig
  test` in place, so the fiig tests run on a copy under another path.
- **Dogfooded against Scality CloudServer** (an S3-compatible server that
  verifies SigV4; MinIO's image couldn't be pulled, from Docker Hub or
quay.io): a PUT of a key
  with spaces and accents, `reports/2026/Grüße café.txt`, and a body with an
  emoji and a newline, accepted by the server's signature check; a HEAD that
  read back its size (36), content type and ETag; a GET that returned the body
  byte for byte; a DELETE, then a GET and a HEAD that were `NotFound`, then a
  second DELETE that was `Ok`; a wrong secret, `Refused(403,
  SignatureDoesNotMatch, ...)`; a missing bucket, `Refused(404, NoSuchBucket,
  ...)` (which first came back as plain `NotFound`, which is why §4 separates
  them); and a 1 MiB object, put and read back identical in about 70 ms each
  way. **Not tried:** AWS itself, virtual-hosted style against a real server,
  a session token against one, and an object larger than a few MiB.
  CloudServer didn't enforce the region, so a wrong region is untested.
- **Language friction.** (1) **A capability group can't have a private
  block**, and only a gateway may read config or reveal a Secret, so the four
  gateways each read the seven settings the same way: about 12 repeated
  lines. A conversion can't do it, and a gateway can't call a gateway. (2)
  `match` isn't an expression and a pattern can't hold a string literal, so
  mapping `"GET"` to `http.Method.Get` is a chain of `if`s. (3) A test's name
  must be unique across a package's files, not just its own file, which
  makes `test getExample` in two files an error. (4) `amzDate` duplicated
  `fiig.smtp`'s calendar code (§3); both moved to `fiig.time` (ADR 0123).
- *(Amended 2026-11-21: metadata, checksums, ranges and copy.)* New files
  `CanonicalHeaders`, `CanonicalRequestOf`, `SignedHeaderList`,
  `ChecksumHeader`, `MetadataHeaders`, `MetadataOf`, `ObjectInfoOf`,
  `S3Object`, `ObjectResult`, `RangeHeader`, `RangeResult`, `CopySource` and
  `CopyResult`. The same Python reference signed the new requests, and it
  still reproduces the old vectors. 84 fiig tests now (AWS's example
  credentials over a PUT with metadata and a checksum among them) and 16 Go
  tests: the checksum on a plain PUT, a PUT with metadata, metadata with a
  CR LF that never connects, `getObject` and `head` reading metadata back, a
  206 and a 416, and a copy that succeeds and one that fails after 200.
  **Not dogfooded against a real server**: no S3-compatible container was
  running and none is kept, so the new signatures are checked only against
  the reference. The four gateways' repeated settings block (friction 1)
  is now eight.
- *(Amended 2026-11-21: presigned URLs.)* New file `PresignedUrl`; `S3`
  gains `presign`. The Python reference gained query-string signing and
  reproduces AWS's published presigned-URL example (GET
  `examplebucket/test.txt` at `20130524T000000Z` for 86400 seconds,
  signature `aeeed9bb...d404`); note that example's secret is
  `wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY`, with a `/` where the
  header-signing examples have a `+`. 92 fiig tests now: AWS's example, a
  PUT with an encoded key, a session token (`/`, `+` and `=` encoded), HEAD
  for seven days, DELETE for one second, an expiry of 0 and of 604801, an
  unknown or lower-case method, and a bad endpoint or key. One Go test
  (17 now) runs the gateway against a socket script with no peers. **Not
  tried against a real server.** The friction-1 settings block is now
  nine.
