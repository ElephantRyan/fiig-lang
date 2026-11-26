# ADR 0132: The `application` Declaration

Status: Accepted (2026-11-25, by the owner). Implemented 2026-11-21, except
the IntelliJ icon for `application` files (see Implementation notes).
Date: 2026-11-21
Related: ADR 0079 §6 (several adapters on one listener; one adapter per
domain), ADR 0105 (a folder is a package), ADR 0108 (an application declares
nothing at the top level), ADR 0124 (startup adapters), ADR 0112 and 0111
(schedule and stream adapters).

## Context

A program has no main. `fiig serve <root> <adapter>...` took the adapters as
arguments, so what launching the program means lived outside it:
abs-recommendations keeps its thirteen adapters, written out by their dotted
names, in `serve-dev.sh`, and each deployment keeps another copy. Add an adapter
and every copy must change, and nothing notices when one is forgotten.

ADR 0108 left the top level of an application empty on purpose: what shares
code belongs in a folder, since folders can't import the top level, and an
adapter belongs with its domain (ADR 0079 §6 rejected a top-level adapter that
composes them).

### How others handle it

| | The entry point | What runs |
| --- | --- | --- |
| Spring Boot | the `@SpringBootApplication` class | scanning its package: everything found |
| Quarkus, Micronaut | none | scanning |
| Dropwizard | an `Application<Config>` subclass | `run()` registers each resource explicitly |
| ASP.NET Core | `Program.cs` | explicit: `MapControllers()`, `AddHostedService<>()` |
| Go | `cmd/<name>/main.go`, one folder per process | explicit |
| Rails | `routes.rb`, and a Procfile of `web:` and `worker:` | explicit, as named process types |
| Phoenix, OTP | an `Application` module whose `start/2` lists its children | an explicit list the VM supervises |

Two camps: scanning and an explicit list. A program that is also a worker needs
named processes (`web`, `worker`). Scanning hides what runs, which is what
fiig is built to avoid; Phoenix's is the closest shape, a declaration that
lists what runs and is checked.

## Decision

### 1. An `application` declaration

```fiig
use abs.recommendations.sessions
use abs.recommendations.ratings
use abs.recommendations.sync

application AbsRecommendations {
    serve web {
        sessions.sessionsApi
        ratings.ratingsApi
    }
    serve worker {
        sync.syncSchedule
    }
}
```

```ebnf
application-decl ::= 'application' identifier '{' { serve-block } '}'
serve-block      ::= 'serve' [ identifier ] '{' { adapter-ref [ ',' ] } '}'
adapter-ref      ::= identifier [ '.' identifier ]
```

- `application` and `serve` are contextual: `application` starts a declaration
  only before a name and a brace, and is an ordinary name everywhere else.
- Each **`serve` block is one process**. A block names a **role** (`web`,
  `worker`), or is the program's only block and names none (`serve { ... }`).
- An adapter is named `alias.adapter`, the folder package imported with `use`
  and the adapter in it, or by its bare name when it is declared in the same
  package. The names are **checked**: they resolve, they are adapters, Go to
  Declaration reaches them, and Find Usages on an adapter shows which process
  runs it.
- It lives at the **top of `src/`** (`src/AbsRecommendations.fiig`, named for
  the declaration as ADR 0104 says). It is the **one declaration an
  application's top level may hold**: ADR 0108's reason for an empty top level,
  that folders can't import it, doesn't apply to a declaration that only names
  things and that nothing imports.
- A module has **one**. A **library has none**: it doesn't run.

### 2. `fiig serve`

| Command | Runs |
| --- | --- |
| `fiig serve .` | the application's only block |
| `fiig serve . worker` | the block with that role |
| `fiig serve .` with several blocks | an error that names the roles |
| `fiig serve . sessions.sessionsApi` | the adapters named, as before |
| `fiig serve .` with **no application** | an error that shows how to declare one |

Adapters named on the command line beat the declaration, so one adapter can be
run while developing. A single name that is a role runs the role. A program
that declares no application can't be launched without naming its adapters:
**a program has to say what it runs.**

`fiig openapi .` describes the HTTP adapters of the same selection.

### 3. Bug classes, in O

| # | |
| --- | --- |
| O1 | a name in a serve block that is no adapter (not imported, no such adapter, a workflow or model, a path longer than `alias.adapter`), or a block or an application that serves nothing |
| O2 | an adapter of the application's own packages that no serve block names, so nothing runs it |
| O3 | an application in a library, or a second application in one module |
| O4 | a role declared twice, an adapter named twice in a block, or a block with no role beside others |

O2 is what the old shell script could not do: an adapter added and not served
is found by `fiig check`.

## Alternatives considered

- **A `serve` list in `fiig.pkg.json`** (built first, and removed). It is a
  list of strings, so a typo is found at launch, nothing follows a name, and the
  editor can't navigate it. The manifest remains for what a package *is*; what
  a program *runs* is code.
- **Scanning**: serve every adapter found. It would be one line shorter, a
  worker and a web process would run the same adapters, and a schedule would
  fire in every replica.
- **`-role` as a flag.** A role is a word the program defines, and `fiig serve
  . worker` reads as a command. A role named like an adapter wins; the adapter
  can be named by its full path.
- **A declaration inside a folder.** It would keep the top level empty, and
  hide the one file a reader looks for first.

## Consequences

- `application` in the syntax, parser, formatter and checker (O1-O4);
  `fiig serve` and `fiig openapi` read it; the language server records each
  name as a reference; `fiig fmt -layout` leaves the file at the top; the
  IntelliJ plugin gives it an icon.
- ADR 0108's H6 has one exception, said in its message.
- Existing programs have no application and `fiig serve .` now stops. Naming the
  adapters still works, and one `application` file replaces the script.

## Open questions

1. **Per-process settings**, such as a worker that needs no `FRONTEND_URL`, or
   CORS and request limits that are flags and environment today (ADR 0070,
   0076). They could be written in the serve block. Config is already read
   only for what the served adapters reach (ADR 0071 §3, ADR 0122), so a role
   may already need less.
2. **More than one application in a module**, Go `cmd/`-style.
3. **A role including another**, for development.

## Implementation notes (2026-11-21)

- `syntax.ApplicationDecl`, `ServeBlock`, `AdapterRef`; `parseApplicationDecl`;
  `compiler/checker/application.go`; `tooling/cli/servecmd/application.go`.
- Tests: the parser and formatter (`application_format_test.go`), the checker
  (every O rule, `application_test.go`), the module rules (H6's exception, O3,
  `compiler/loader`), `fiig serve` through the loader (one block, each role, the
  command line winning, no application, an unknown adapter), the language server
  (Go to Declaration to an adapter of the same package and of an imported one,
  Find Usages from the adapter), and `fiig fmt -layout`.
- The IntelliJ plugin's change (an icon for `application` files) is not built
  or tested here.
