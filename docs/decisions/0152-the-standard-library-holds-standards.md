# ADR 0152: The Standard Library Holds Standards; Drivers Are First-Party Packages

Status: Proposed
Date: 2026-11-26
Amends: ADR 0074 §1 (what the `fiig.` prefix means).
Related: ADR 0032 (package versions), ADR 0037 (package sources; no fetch),
ADR 0042 (`nativeCapabilities`), ADR 0084 (drivers written in fiig), ADR
0085 (`fiig.postgres`), ADR 0110 (`fiig.redis`), ADR 0138 §2 (no hash for
a built-in package), ADR 0151 (the standard library move to `formulas`
groups).

## Context

The toolchain ships thirteen packages, all embedded in the `fiig` binary
(`compiler/stdlib`), all under the reserved `fiig.` prefix, and all at
version `1.0.0`:

| Kind | Packages |
| --- | --- |
| An open standard (an RFC or a W3C spec) | `fiig.http`, `fiig.smtp`, `fiig.time`, `fiig.jwt` |
| An interface other packages implement | `fiig.sql`, `fiig.session`, `fiig.stream`, `fiig.schedule` |
| An implementation over one of those interfaces, vendor-neutral | `fiig.sqllock` |
| One vendor's product protocol | `fiig.postgres`, `fiig.kafka`, `fiig.redis`, `fiig.s3` |

ADR 0074 §1 made `fiig.` mean "a package the toolchain ships", the way Java
reserves `java.*`. That was right while there was one package. It now
covers two different promises without saying so. A standard changes
slowly and an RFC is the same in ten years. A product protocol moves with
its vendor: Kafka adds API versions, Postgres adds authentication methods,
S3 adds headers. Today a fix to `fiig.kafka` needs a new toolchain, and a
breaking change to it would look like a breaking change to the language.

The owner expects some of these to be maintained outside the project
eventually, as the Apache projects maintain most of Java's integrations.
Nobody is there to take them yet, and keeping them here has a real benefit:
a language change (ADR 0151's move to `formulas` groups is the latest) can
update every caller in one commit. So the question is not whether to hand
them off now, but whether to draw the line now, while fiig is an alpha with
no compatibility promise (`docs/known-limitations.md`) and renaming a
package is cheap.

### What a handoff would need

A package can only be handed off if it never needed to be in the toolchain.
Checked on 2026-11-26:

- **The loader:** the only difference between a built-in package and any
  other is where it is found (`stdlib.FS`, searched as one more root) and
  that its path may start with `fiig.` (`loader.reserved`).
- **The lockfile:** it records a built-in package without a hash, because
  the toolchain version pins its bytes (ADR 0138 §2).
- **Privileged capabilities:** no driver uses one. The one package with
  `nativeCapabilities` is `fiig.sqllock` (`Database`).
- **Go code:** none knows a driver by name. Three messages mention one as
  an example: the `ScheduleLock` errors in `compiler/adapter/schedule.go`
  and `startup.go`, and the parser's `groupHelperHint`.
- **The rest:** each driver is ordinary fiig over `Socket`, as ADR 0084
  intended.

So the drivers are already handoff-ready in what they do. What's missing is
in how they're named and versioned.

### Precedent

| Language | In the standard library | First-party, outside it | Third-party |
| --- | --- | --- | --- |
| Go | `net/http`, `net/smtp`, `crypto/*`, `time`, `database/sql` (interface only) | `golang.org/x/net`, `x/crypto`, `x/oauth2`: the Go team, versioned separately, no Go 1 promise | `pgx`, `lib/pq`, `go-redis`, `sarama`, the AWS SDK |
| Java | `java.net.http`, `java.time`, JDBC (interface only) | none | the Postgres JDBC driver, the Kafka client (Apache), Jedis, the AWS SDK |
| Python | `http`, `smtplib`, `datetime`, DB-API (a PEP, not code) | none | `psycopg`, `kafka-python`, `redis-py`, `boto3` |
| Rust | almost nothing past collections and I/O | `rust-lang/*` crates such as `regex` | `tokio-postgres`, `rdkafka`, `redis`, `aws-sdk-s3` |

All four put the standard in the library, put the interface in the library
when there's a family of implementations (JDBC, `database/sql`, DB-API),
and leave each vendor's protocol outside it. Go is the one that also has a
first-party tier: the project maintains a package but doesn't fold it into
the language's promise.

Java's `javax.*` → `jakarta.*` rename is the cautionary case. When Java EE
moved from Oracle to the Eclipse Foundation, the namespace couldn't go with
it, and every application had to rewrite its imports. A path that names who
maintains a package makes a handoff a breaking change.

## Decision

### 1. Two tiers, both shipped in the toolchain for now

- **The standard library, `fiig.*`,** holds open standards, the interfaces
  other packages implement, and vendor-neutral implementations of those
  interfaces. It changes with the language. Once fiig has a compatibility
  promise, these packages are inside it.
- **First-party packages, `fiig.x.*`,** hold one vendor's product protocol.
  The fiig project maintains them, in this repository, and they are
  versioned on their own. They are outside the language's compatibility
  promise. A breaking change to one is a major version of that package,
  not news about the language.

Both tiers stay embedded in the binary and both stay reserved: a workspace
package may not take a path starting with `fiig.`, so it can't take one
starting with `fiig.x.` either. Without a fetch mechanism (ADR 0037 §4),
embedding is still the only way a program gets a package without a
`-workspace` flag.

### 2. Where each package goes

| Stays in the standard library | Becomes first-party |
| --- | --- |
| `fiig.http`, `fiig.smtp`, `fiig.time`, `fiig.jwt` | `fiig.x.postgres` |
| `fiig.sql`, `fiig.session`, `fiig.stream`, `fiig.schedule` | `fiig.x.kafka` |
| `fiig.sqllock` | `fiig.x.redis` |
| | `fiig.x.s3` |

- **`fiig.jwt` stays.** Go and Java don't ship JWT. But it is RFC 7519 and
  RFC 7517, and ADR 0137's principal binding and ADR 0146's verification
  are built on it. A service's sign-in shouldn't depend on a package
  outside the language's promise.
- **`fiig.sqllock` stays.** It is a `ScheduleLock` over any `Database`,
  not over one vendor's product.
- **`fiig.s3` moves**, although many vendors speak the S3 API (MinIO, R2,
  Ceph, Backblaze). It is still a product's API, defined by AWS's
  documentation rather than a standards body, and AWS changes it at AWS's
  pace.

### 3. A first-party package uses nothing the standard library doesn't offer everyone

A `fiig.x` package may use only what a package outside the toolchain could
use: the public declarations of standard library packages, primitive
capabilities, and no `nativeCapabilities`. This is the property §Context
found already holds. Writing it down keeps it true, so handing a package to
outside maintainers (or moving it to a fetched source, once one exists)
never needs a change to what the code can do.

Go code still never names a `fiig.x` package except in a message's example.

### 4. Versions

A first-party package's version moves when that package changes, not when
the toolchain does. Each toolchain release embeds one version of each
package. A release note lists a `fiig.x` package's version only when it
changes, under its own heading.

This is weaker than Go's `x` packages, which are fetched, so they release
whenever they like. Until fiig can fetch a package, a fix to `fiig.x.kafka`
still reaches users only in a toolchain release. §1's split doesn't remove
that cost. It does make sure that the day fetching exists, nothing about
these packages has to change except where they're found.

### 5. Migration

Alpha programs make one change, in the manifest and in every `use`:

| Was | Is |
| --- | --- |
| `"fiig.postgres": "1.0.0"`, `use fiig.postgres;` | `"fiig.x.postgres": "1.0.0"`, `use fiig.x.postgres;` |
| `"fiig.kafka"`, `use fiig.kafka;` | `"fiig.x.kafka"`, `use fiig.x.kafka;` |
| `"fiig.redis"`, `use fiig.redis;`, and `"lock": "fiig.redis"` in a capabilities file | `"fiig.x.redis"`, `use fiig.x.redis;`, `"lock": "fiig.x.redis"` |
| `"fiig.s3"`, `use fiig.s3;` | `"fiig.x.s3"`, `use fiig.x.s3;` |

- **Call sites don't change.** A path's last segment is still `postgres`,
  `kafka`, `redis` or `s3`, so `postgres.Postgres` and `redis.Redis` read
  as before.
- **The rename ships in the same release as ADR 0151's phase 2** (each
  driver's protocol code moved to `formulas` groups), so a program that
  uses a driver takes one breaking change, not two.
- **Hearsay is unaffected.** It depends only on `fiig.http`.
- **Each package's own version stays `1.0.0`.** A path change is a new
  package as far as the loader is concerned, so nothing earlier is there
  to be compatible with.

## Alternatives considered

- **A. Leave everything in `fiig.*`.** This is the cheapest option today.
  It keeps one promise covering two kinds of package. The rename gets more
  expensive with every program written against it.
- **B. Move the drivers to their own repositories now.** That would be the
  real handoff, with nobody to hand them to. It would lose the one-commit
  update across all callers that a language change like ADR 0151 needs,
  and it would need a fetch mechanism first.
- **C. Neutral names with no prefix (`postgres`, `kafka`), so a handoff is
  never a rename.** This is the `javax` lesson taken all the way, and it's
  how Rust's crates work: a crate's owner can change and its name doesn't.
  Two things argue against it here:
  - Without a registry, nothing stops an organization's own `kafka`
    package from colliding with it.
  - A bare name says nothing about who answers for the code, and today
    that is the fiig project.

  If a handoff ever happens, a rename to the new maintainer's path is one
  more migration table. This alternative is worth revisiting when there
  is a registry (open question 1).
- **D. A prefix other than `fiig.x.`, such as `fiigx.`.** It reads less
  like a subpackage. But it is a second reserved prefix in the loader, and
  Go's `x` precedent is already familiar.
- **E. Build fetching first, then split.** This is the order Go had. It
  would delay the naming decision until after programs depend on the
  current names, which is the opposite of what an alpha is for.

## Consequences

- **The loader is unchanged.** `fiig.x.*` is already inside the reserved
  prefix, and `compiler/stdlib` embeds the four drivers as before. Their
  directories move under `compiler/stdlib/x/`, so the tier is visible in
  the tree.
- **Each driver's `fiig.pkg.json` path changes,** along with every `use`
  of it: the drivers' own source and tests, and the Go test fixtures in
  `tooling/runprogram` (twelve files), `tooling/intellijgen`,
  `tooling/cli/servecmd`, `compiler/runtime` and `compiler/checker`.
- **Examples and docs change:** `examples/relay-notes` (manifest,
  lockfile and capabilities file), `examples/README.md`,
  `docs/schemas/capabilities.schema.json`, `docs/known-limitations.md`,
  `docs/deploying.md` and `fiig-lang.md`.
- **The three messages that name a driver** say `fiig.x.redis` and
  `fiig.x.postgres`.
- **Accepted ADRs that name a driver** (about 25) get no edits to their
  text. Like every rename before it, the mapping is recorded here, and an
  ADR's status line gets an amendment pointer only where the ADR decided
  the package's path (ADR 0085, 0110, and the ADRs that introduced
  `fiig.kafka` and `fiig.s3`).
- **Release notes gain a "First-party packages" heading** listing each
  `fiig.x` package whose version changed.

## Open questions

1. **Fetching.** When fiig can fetch a package by its `Origin` (ADR 0037
   §3), first-party packages could leave the binary and release on their
   own. This ADR doesn't decide that. It only makes sure nothing in the
   packages has to change when it happens.
2. **Group names inside a package** (from ADR 0151's phase 2): whether a
   group repeats its package (`fiig.x.kafka.kafkaCodec`) or not
   (`fiig.x.kafka.codec`). The second avoids stutter. If it is chosen, it
   applies to the standard library too, which renames phase 1's
   `fiig.http.httpText` to `fiig.http.text`.
3. **Where a future driver goes.** A MySQL client or a gRPC transport
   (gRPC is an open spec, but one project's) would be decided by §2's
   test: does it implement a standard, or one product?
