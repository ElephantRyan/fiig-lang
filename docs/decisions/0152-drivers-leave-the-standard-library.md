# ADR 0152: Drivers Leave the Standard Library

Status: Proposed
Date: 2026-11-26
Amends: ADR 0074 §1 (what the toolchain ships), ADR 0085 (`fiig.postgres`'s
path), ADR 0110 (`fiig.redis`'s path).
Related: ADR 0032 (package versions), ADR 0037 (package sources, `Origin`,
no fetch), ADR 0042 (`nativeCapabilities`), ADR 0084 (drivers written in
fiig), ADR 0138 §2 (no hash for a built-in package), ADR 0151 (the standard
library's move to `formulas` groups).

## Context

The toolchain ships thirteen packages. All are embedded in the `fiig`
binary (`compiler/stdlib`), all sit under the reserved `fiig.` prefix, and
all are at version `1.0.0`:

| Kind | Packages |
| --- | --- |
| An open standard (an RFC or a W3C spec) | `fiig.http`, `fiig.smtp`, `fiig.time`, `fiig.jwt` |
| An interface other packages implement | `fiig.sql`, `fiig.session`, `fiig.stream`, `fiig.schedule` |
| A vendor-neutral implementation of one of those | `fiig.sqllock` |
| One vendor's product protocol | `fiig.postgres`, `fiig.kafka`, `fiig.redis`, `fiig.s3` |

ADR 0074 §1 made `fiig.` mean "a package the toolchain ships", the way Java
reserves `java.*`. That was right while there was one package, but the
prefix now makes one promise about two kinds of package. A standard changes
slowly: an RFC is the same in ten years. A product protocol moves with its
vendor. Kafka adds API versions, Postgres adds authentication methods, and
S3 adds headers. Today, a fix to `fiig.kafka` needs a new toolchain, and a
breaking change to it looks like a breaking change to the language.

The owner intends to keep maintaining the drivers, but not as part of the
language. If fiig grows, someone else may take one over. That is an
ordinary event in every ecosystem: the new maintainer publishes under their
own name, and users update a manifest line and their imports.

### What leaving needs

A package can only leave the standard library if it never needed to be in
it. Checked on 2026-11-26:

- **The loader:** the only differences between a built-in package and any
  other are where it is found (`stdlib.FS`, searched as one more root) and
  that its path may start with `fiig.` (`loader.reserved`).
- **The lockfile:** it records a built-in package without a hash, because
  the toolchain version pins its bytes (ADR 0138 §2).
- **No privileged capabilities:** no driver uses one. The only package with
  `nativeCapabilities` is `fiig.sqllock` (`Database`).
- **No Go code depends on a driver.** Three messages name one as an
  example: the `ScheduleLock` errors in `compiler/adapter/schedule.go` and
  `startup.go`, and the parser's `groupHelperHint`.
- **The dependencies point one way.** Every driver depends on a standard
  library interface (`fiig.sql`, `fiig.stream`, `fiig.session`,
  `fiig.schedule`) or a standard (`fiig.http`, `fiig.time`). No standard
  library package depends on a driver. The only mention runs the other way,
  in a comment in `fiig.http`'s `HttpClientSettings.fiig`.

Each driver is ordinary fiig over `Socket`, as ADR 0084 intended. What
keeps them in the standard library is their names.

### Precedent

How other languages got their first drivers, and where those drivers live
(from memory; verify a detail before citing it):

| Language | Interface in the standard library | First drivers |
| --- | --- | --- |
| Elixir | none (Ecto is a package) | Postgrex and Ecto, from core team members and Plataformatec, the language creator's company, under its own name. Later maintained by the community. |
| Go | `database/sql` (Go 1, interface only; the Go team wrote no driver) | `lib/pq`, `go-sql-driver/mysql`, `pgx`: individuals |
| Java | JDBC | the Postgres JDBC driver, `org.postgresql`; Jedis; the Kafka client from Apache |
| Node | none | `pg`, one person's project since 2010 |
| Rust | none | the `postgres` crate, one person's project since before 1.0 |
| Crystal | `crystal-db` | `crystal-pg`, an individual's package |
| Nim | `db_postgres`, `db_mysql`, `db_sqlite`, in the standard library | moved out to a separate package, `db_connector`, in Nim 2.0 (2023) because maintaining them there was a burden |

The pattern: the standard library owns the interface, and a named person or
company owns each driver. Elixir's is the closest to fiig's case. Nim's is
the case for doing this now rather than after programs depend on the
current names.

Most of these also took a shortcut fiig didn't: wrapping a C client
(`libpq`, `librdkafka`). fiig's drivers are written in fiig, as Postgrex,
`pgx` and Go's MySQL driver are.

Every case that moved changed its name when it did. Jackson went from
`org.codehaus.jackson` to `com.fasterxml.jackson`, and Java EE from `javax.*`
to `jakarta.*`. Users updated their imports. A rename on handoff is a
routine, occasional cost, not one to design a namespace around.

## Decision

### 1. The standard library holds standards and interfaces

`fiig.*` keeps:

- the open standards: `fiig.http`, `fiig.smtp`, `fiig.time` and `fiig.jwt`;
- the interfaces other packages implement: `fiig.sql`, `fiig.session`,
  `fiig.stream` and `fiig.schedule`;
- `fiig.sqllock`, a `ScheduleLock` over any `Database`, not over one
  vendor's product.

They change with the language. Once fiig has a compatibility promise,
these packages are inside it.

`fiig.jwt` stays although Go and Java don't ship JWT. It is RFC 7519 and
RFC 7517, and ADR 0137's principal binding and ADR 0146's verification are
built on it, so a service's sign-in doesn't depend on a package outside the
language.

### 2. The drivers are the owner's packages: `com.degeneratedevelopment.*`

| Was | Is |
| --- | --- |
| `fiig.postgres` | `com.degeneratedevelopment.postgres` |
| `fiig.kafka` | `com.degeneratedevelopment.kafka` |
| `fiig.redis` | `com.degeneratedevelopment.redis` |
| `fiig.s3` | `com.degeneratedevelopment.s3` |

- **A reverse domain name** is Maven's convention, because owning the
  domain shows who owns the prefix, and a future registry can check it the
  way Maven Central does. The path depends on the owner keeping the domain.
- **The prefix names the maintainer.** Each manifest's `Origin` (ADR 0037
  §3) says where the source lives.
- **Each driver is versioned on its own** (ADR 0032), and is outside the
  language's compatibility promise. A breaking change to one is a major
  version of that package, not news about the language.
- **`s3` moves** although many vendors speak the S3 API (MinIO, R2, Ceph,
  Backblaze). It is still a product's API: AWS defines it in its own
  documentation, not a standards body, and changes it at its own pace.

A future driver follows the same test: does it implement a standard, or
one product?

### 3. Bundled, until fiig can fetch a package

The drivers stay in this repository and in the `fiig` binary. A program
names one in its manifest and the loader finds it with no `-workspace`
flag, as now. Python bundles pip and Node bundles npm the same way: shipped
with the tool, but not part of the language.

- **Why bundle:** without a fetch mechanism (ADR 0037 §4), moving the
  drivers to their own repository would make every user clone it and pass
  `-workspace`. Keeping them here also lets a language change update every
  caller in one commit, as ADR 0151 just did.
- **They are labeled bundled, not standard:** in `compiler/stdlib/bundled/`
  (the tree shows the tier), in the release notes, and in
  `docs/known-limitations.md`.
- **When fiig can fetch a package,** the drivers leave the binary, move to
  their own repository, and release when they like. Nothing in them changes
  except where they're found.

### 4. A bundled package has no privileges

A bundled package may use only what a package outside the toolchain could
use: the public declarations of standard library packages, primitive
capabilities, and no `nativeCapabilities`. §Context found this already
holds. Writing it down keeps it true, so leaving the binary, or changing
hands, never needs a change to what the code can do.

The standard library never depends on a bundled package, and Go code never
names one except in a message's example.

### 5. Versions

A bundled package's version moves when that package changes, not when the
toolchain does. Each toolchain release embeds one version of each package,
and the release notes list a bundled package under its own heading only
when its version changes.

Until fiig can fetch, a fix to a driver still reaches users only in a
toolchain release, and two programs on one toolchain can't use different
majors of one driver. §3 removes this cost when fetching exists. This ADR
doesn't.

### 6. Migration

Alpha programs make one change, in the manifest and in every `use`:

| Was | Is |
| --- | --- |
| `"fiig.postgres": "1.0.0"`, `use fiig.postgres;` | `"com.degeneratedevelopment.postgres": "1.0.0"`, `use com.degeneratedevelopment.postgres;` |
| `"fiig.kafka"`, `use fiig.kafka;` | `"com.degeneratedevelopment.kafka"`, `use com.degeneratedevelopment.kafka;` |
| `"fiig.redis"`, `use fiig.redis;`, and `"lock": "fiig.redis"` in a capabilities file | `"com.degeneratedevelopment.redis"`, `use com.degeneratedevelopment.redis;`, `"lock": "com.degeneratedevelopment.redis"` |
| `"fiig.s3"`, `use fiig.s3;` | `"com.degeneratedevelopment.s3"`, `use com.degeneratedevelopment.s3;` |

- **Call sites don't change.** A path's last segment is still `postgres`,
  `kafka`, `redis` or `s3`, so `postgres.Postgres` and `redis.Redis` read as
  before.
- **The rename ships in the same release as ADR 0151's phase 2** (each
  driver's protocol code moved to `formulas` groups), so a program that uses
  a driver takes one breaking change, not two.
- **Hearsay is unaffected:** it depends only on `fiig.http`.
- **Each package's own version stays `1.0.0`.** A new path is a new
  package to the loader, so nothing earlier exists to be compatible with.

## Alternatives considered

- **A. Leave everything in `fiig.*`.** This is the cheapest option today.
  It keeps one promise covering two kinds of package, and the rename gets
  more expensive with every program written against it. Nim made this
  choice first and reversed it later.
- **B. A first-party tier under fiig (`fiig.x.*`, `fiig.drivers.*`),** as
  Go's `golang.org/x` is. It keeps the drivers under the language's name,
  so a handoff still changes `fiig.` paths, and it implies the language
  project answers for them. The owner does, not the language.
- **C. Neutral names (`postgres`, `kafka`)** that never change on a
  handoff, as Rust's crates and npm work. Without a registry, the loader
  would have to reserve the bare names. It would also hide who maintains
  the package, to avoid a rename that is routine anyway.
- **D. Move the drivers to their own repository now.** This is the clean
  end state. Without fetching, it costs every user a clone and a
  `-workspace` flag, and costs the language the one-commit update across
  all callers. §3 does this later, when fetching exists.
- **E. Build fetching first, then rename.** That would decide the names
  after programs depend on the current ones.

## Consequences

- **`compiler/stdlib`:** the four driver directories move to
  `compiler/stdlib/bundled/`. Each `fiig.pkg.json` gets its new path and an
  `Origin`, and the `//go:embed` line lists `bundled`. Each driver's own
  `use` lines change.
- **Loader:**
  - `loader.reserved` is unchanged. `com.degeneratedevelopment.` isn't
    reserved, and a bundled package is found the way a built-in one is now.
  - A workspace package at a bundled path and the same major is ADR 0037
    §2's existing "ambiguous dependency" error. That is the right answer,
    and the message says which copy is bundled.
- **Tests:**
  - `TestTheStandardLibraryPassesItsTests` (`tooling/cli/testcmd`) also
    runs the bundled packages' tests. Its copy step renames each package's
    path as it does today, so the copy doesn't collide with the embedded
    one.
  - `fmtcmd`'s shipped-package check (`checkShipped`) covers bundled
    packages too.
- **Go test fixtures that name a driver change:** `tooling/runprogram` (12
  files), `tooling/intellijgen`, `tooling/cli/servecmd`,
  `compiler/runtime` and `compiler/checker`.
- **Examples and docs:**
  - `examples/relay-notes`'s manifest, lockfile and capabilities file.
  - `examples/README.md`, `docs/schemas/capabilities.schema.json`,
    `docs/known-limitations.md`, `docs/deploying.md` and `fiig-lang.md`.
- **The three messages that name a driver** say
  `com.degeneratedevelopment.redis` and `com.degeneratedevelopment.postgres`.
- **Accepted ADRs that name a driver** (about 25) keep their text. This
  ADR records the mapping. ADRs 0085 and 0110, and the ADRs that introduced
  `fiig.kafka` and `fiig.s3`, get an amendment pointer on their status
  line.
- **Release notes** gain a "Bundled packages" heading.

## Open questions

1. **Fetching.** What fiig fetches from, and whether `Origin` (ADR 0037 §3)
   becomes the address. When it exists, §3 says the drivers leave the
   binary.
2. **Group names inside a package** (from ADR 0151's phase 2): whether a
   group repeats its package (`com.degeneratedevelopment.kafka.kafkaCodec`)
   or not (`…kafka.codec`). The second avoids stutter. If it is chosen,
   it applies to the standard library too, which renames phase 1's
   `fiig.http.httpText` to `fiig.http.text`.
