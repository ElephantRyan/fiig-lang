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

**The timestamp is written by hand.** fiig has no time type, so `amzDate`
turns `Clock.now()` into `20261120T135958Z` with the civil-from-days
algorithm. `fiig.smtp` has the same code for its `Date` header (ADR 0120 open
question 5).

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
3. **Presigned URLs**, to give a browser a time-limited link.
4. **Ranges** (`Range: bytes=0-99`), for reading part of an object.
5. **Custom metadata and checksums** (`x-amz-meta-*`, `Content-MD5`,
   `x-amz-checksum-sha256`), and server-side encryption headers.
6. **A time type**, to retire this package's `amzDate` and `fiig.smtp`'s
   `rfc5322Date`.
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
  makes `test getExample` in two files an error. (4) `amzDate` duplicates
  `fiig.smtp`'s calendar code (§3).
