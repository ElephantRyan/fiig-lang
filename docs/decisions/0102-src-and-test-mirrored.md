# ADR 0102: A Package Is `src/` and `test/`, and `test/` Mirrors `src/`

Status: Accepted (2026-11-20). Not yet implemented; built with ADRs 0104-0107 as one migration.
Date: 2026-11-20
Amends: ADR 0091 §2 (where tests live), fiig-lang.md §3.9 (one declaration
per file: test files are the exception), ADR 0054 (a test file's private
block serves all its tests).
Related: ADR 0096 (migrations), ADR 0103 (tests call workflows).

## Context

A package keeps its code folders (`rules/`, `models/`, `gateways/`, ...)
at its root, and its tests in `tests/` beside them, one test per file.
The owner finds a test folder in among the code folders confusing, and
prefers JUnit's layout: code and tests in two parallel trees.

### How others do it

| Language | Layout |
|---|---|
| Java, Kotlin (Maven, Gradle), Scala | `src/main/...` and `src/test/...`, the test tree mirroring the code's |
| Elixir, Ruby, Gleam, Dart | `lib/` or `src/`, and `test/` or `spec/` mirroring it |
| Swift | `Sources/` and `Tests/` |
| .NET | `src/Shop/` and `tests/Shop.Tests/`, separate projects |
| Go, most JS/TS | `rating_test.go` beside `rating.go` |
| Rust | unit tests in the file; `tests/` beside `src/` for integration |

Parallel trees are the most common layout. In JUnit a test class mirrors
the class it tests and holds many test methods; Go's `rating_test.go` and
Elixir's `rating_test.exs` hold many tests each too.

## Decision

### 1. The layout

```
backend/
  fiig.pkg.json
  fiig.lock.json
  caps.json
  migrations/0001_create_ratings.sql
  src/
    ratings/RankRecommendations.fiig
    ratings/FindRating.fiig
    ratings/Rate.fiig
  test/
    ratings/RankRecommendations.fiig
    ratings/Rate.fiig
    scenarios/PrivateRatingsStayPrivate.fiig
```

- **`src/`** holds the program: every `.fiig` file that isn't a test.
- **`test/`** holds the tests. They are never part of the program (ADR
  0091 §2's T1 and T2, with `test/` for `tests/`).
- **The package root** holds what configures the package: the manifest,
  the lockfile, a capabilities file, `migrations/`.
- A `.fiig` file anywhere else is an error that says where it goes.

### 2. A test file holds many tests

A file under `test/` holds any number of `test` declarations, and an
optional `private` block whose helpers every test in the file shares. It
holds nothing else. Test names are unique in the package, since `fiig
test -run` and the IDE select tests by name.

```
// test/ratings/RankRecommendations.fiig
test ordersByAverageScore { ... }
test breaksTiesByCount { ... }

private {
    rating(itemId: string, score: int) -> Rating { ... }
}
```

The one-declaration rule exists so a name has one home in the program and
the registry; tests are in neither.

### 3. `test/` mirrors `src/`, except `test/scenarios/`

A test file's path, with `test/` for `src/`, must name a file in `src/`:
`test/ratings/FindRating.fiig` tests `src/ratings/FindRating.fiig`. A
test file with no such file is an error naming the nearest one. So a
declaration's tests are always one path swap away, and renaming or moving
a file in `src/` without its tests is caught.

A test belongs in the file of what it calls; a test that sets things up
with other calls first still belongs with the one it checks.

**`test/scenarios/`** is the one folder outside the mirror, for a test of a
promise several entry points must all keep, which none of them owns:
abs-recommendations' `private_ratings_stay_private` rates once and checks
four separate reads. Its files may hold many tests, as any test file.

### 4. Moving a package

`fiig fmt -layout <dir>` moves a package to this layout: code into
`src/`, `tests/` into `test/`, and each test into the file mirroring what
it calls, merging tests that land in one file. A test that calls more than
one entry point, or none, is listed for the author to place, in its own
file under `test/scenarios/` until then. The corpus, the stdlib, and
abs-recommendations move with it.

## Rationale

- **The tree says what's program and what's test**, without knowing that
  one folder among the code folders is special.
- **A mirror is a guarantee, not a habit:** a test tree that's only
  conventionally parallel drifts as files move.
- **`scenarios/`** keeps the guarantee everywhere else while giving
  cross-cutting promises, often the important ones, a place readers look.

## Alternatives considered

- **Keep `tests/` among the code folders** (ADR 0091). The owner finds it
  confusing; few ecosystems do it.
- **Tests beside each file** (Go's `_test` files). Mixes program and test
  in every folder.
- **A separate test package** (.NET). A second manifest per package, and
  tests could then only see public declarations across a package boundary.
- **A mirror by convention only.** Drifts.
- **One test per file, mirrored by folder.** A rule with five tests
  becomes five files, and they can't share helpers.

## Consequences

- Every package moves once, by tool.
- The IDE can offer "go to test" and "go to tested file" by path.
- "Which files have no tests?" is a listing: `src/` files without a
  `test/` file.
- With ADR 0103, a workflow's tests sit in its mirrored file.

## Open questions

1. *(Settled: ADR 0104 names files in TitleCase; ADR 0105 makes the
   folders packages, which this ADR's example now shows.)*
