# ADR 0105: A Folder Is a Package; a Module Has the Manifest

Status: Accepted (2026-11-20). Implemented 2026-11-20.
Date: 2026-11-20
Amends: ADR 0032-0037 (a manifest per package becomes a manifest per
module), ADR 0043 §2 (`provides` moves into source), ADR 0079 (a domain is
a folder), ADR 0096 (migrations are per module).
Related: ADR 0050 (one flat namespace per package), ADR 0102 (`src/` and
`test/`), ADR 0104 (file names).

## Context

A fiig package is a folder with a `fiig.pkg.json`, and its code sits in
folders by kind (`rules/`, `activities/`). Two problems, both seen in
abs-recommendations:

- **Folders by kind say nothing about the system.** A declaration's first
  word already says its kind.
- **Every boundary costs a manifest.** The app is five packages
  (`abs-client`, `signin`, `signin-password`, `signin-authentik`,
  `backend`), each repeating a version, a `kind`, dependencies on
  `fiig.http` and on each other, `capabilities`, and `workspaces`. The
  folder `abs-client` holds the package `abs.client`.

### How others do it

| Language | Has the manifest | Unit of code and names |
|---|---|---|
| Go | module (`go.mod`), one per project | package = a directory; path = module + folders |
| Rust | crate (`Cargo.toml`) | module = a file or folder in the crate |
| Java, Kotlin | Maven/Gradle project | package = folder path, by convention |
| Python | distribution (`pyproject.toml`) | package = a directory |
| C# | project (`.csproj`) | namespace, by convention the folders |
| Elixir | Mix project | modules, folders by convention |

Every one has two levels: one manifest for what is versioned and released
together, and folders inside it for names. fiig has one level. Go's split
is the model.

## Decision

### 1. A module is a folder with `fiig.pkg.json`

The manifest names the module's path, its version, its dependencies on
other modules, its capability allowlist, its workspaces, and whether it's
an application or a library. One per project, unless part of it is
released on its own.

### 2. A package is a folder under `src/`

```
abs-recommendations/
  fiig.pkg.json            module abs.recommendations
  migrations/
  src/
    Health.fiig            package abs.recommendations
    ratings/               package abs.recommendations.ratings
      Rate.fiig
      LoadUserAndBook.fiig
    signin/                package abs.recommendations.signin
      password/            package abs.recommendations.signin.password
    absclient/             package abs.recommendations.absclient
  test/
    ratings/Rate.fiig      tests of package abs.recommendations.ratings
```

- A package's path is its module's path plus its folders. It is imported
  as today: `use abs.recommendations.ratings`.
- A folder name is a path segment: letters and digits, starting with a
  letter, lowercase. No `-` or `_`.
- Each package has its own flat namespace (ADR 0050).
- Packages in a module may not import each other in a cycle.
- A test file under `test/<folders>/` is in the package `src/<folders>/`
  and sees what its code sees. It may also import any other package of the
  module (ADR 0102 §3a).

### 3. `provides` is written in the code

A capability group says which interface it fulfils:

```
capability AbsPasswordSignIn provides signin.SignIn {
    gateway authenticate(attempt: signin.SignInAttempt) -> Result<signin.Identity, signin.SignInError>
        requires AbsApi
    { ... }
}
```

The claim sits beside what fulfils it, and is checked as ADR 0043 checks
the manifest's today.

### 4. What moves to the module

- **Versions and dependencies**: only modules are versioned; the lockfile
  pins modules.
- **The capability allowlist** (Milestone 6's review): a module lists what
  its packages may require, once.
- **`kind`**: `application` or `library` describes the module.
- **Migrations** (ADR 0096): `migrations/` at the module root. The
  `fiig_migrations` table records the module's path.
- **Per-domain database bindings** (ADR 0079 §6a) stay keyed by package
  path, so a domain folder can still have its own database.

## Rationale

- **The tree is the system's shape.** `src/ratings/`, `src/signin/` say
  what the program does; the kind is in each file's first word.
- **A boundary costs a folder, not a manifest.**
- **A package's name can't disagree with its folder.**

## Alternatives considered

- **A manifest per package** (today). See Context.
- **Folders inside a package, names unchanged** (Elixir's convention).
  Folders would group files but not names, so they could drift from the
  packages they suggest.
- **Hiding declarations from sibling packages** (Go's `internal/`, Java's
  package-private). Not now; ADR 0079 prefers a clear shape, and nothing
  has leaked yet.

## Consequences

- abs-recommendations goes from five manifests to one.
- Every corpus example and the stdlib move once, with ADRs 0102 and 0104,
  by `fiig fmt -layout`.
- A library meant for other projects stays a module of its own.

## Open questions

1. **`internal/`**, if a package's declarations need hiding from siblings.

## Implementation notes

Built 2026-11-20.

- The loader builds a module's folder packages in import order
  (`compiler/loader/module.go`), refusing a cycle or an import of the
  top-level package from a folder. A dependency module exposes every
  package; the lockfile pins modules.
- `capability X provides alias.Interface` is parsed and merged with any
  manifest `provides` (`checker.CodeProvides`); the formatter prints it.
- `fiig check` and `fiig test` cover every folder package and its tests;
  `fiig run . folder.name` and `fiig serve . folder.adapter` reach them.
  The language server treats each folder as a package, navigates into it,
  and checks its test files with it.
- Checking each corpus module on its own found four library manifests
  that never declared the `Socket` they use; they do now.
- Dogfood: abs-recommendations' five packages became one module with
  `absclient`, `signin`, `signin/password`, and `signin/authentik` folders
  (`fiig fmt -layout -absorb ...`), after renaming five declarations that
  differed from another only by case. It checks clean and its 41 tests
  pass. fiig.postgres, with `provides` now in its code, ran against
  Postgres 17.
