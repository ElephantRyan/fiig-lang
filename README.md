# fiig

fiig is a strongly typed language for application services: HTTP APIs, CLI
tools, and batch jobs. Models, business rules, external calls, and how they
compose are part of the language, so the compiler can reject whole classes
of bugs: unhandled errors, undeclared effects, unbounded concurrency, and
invalid transaction nesting.

```fiig
rule createValidRating(input: NewRating) -> Result<Rating, RatingError> {
    if input.score < 1 or input.score > 10 {
        return Err(RatingError.Invalid("score", "must be from 1 to 10"))
    }
    if input.item.isBlank() {
        return Err(RatingError.Invalid("item", "must not be blank"))
    }
    return Ok(Rating(item: input.item, score: input.score, note: input.note))
}
```

**Status: alpha.** It runs real programs against SQLite and serves HTTP,
but syntax and behavior can change without notice. See
[known limitations](docs/known-limitations.md).

## Install

macOS and Linux:

```
curl -fsSL https://raw.githubusercontent.com/ElephantRyan/fiig-lang/main/install.sh | sh
```

Windows (PowerShell):

```
irm https://raw.githubusercontent.com/ElephantRyan/fiig-lang/main/install.ps1 | iex
```

Each downloads the newest release for your platform, checks it against
`SHA256SUMS`, and puts `fiig` on your `PATH` (`~/.local/bin`, or
`%LOCALAPPDATA%\fiig`). Set `FIIG_VERSION` to pick a release and
`FIIG_INSTALL` to pick the directory.

Or download an archive from the releases page yourself, or run the
container image. To build from source with Go 1.26:

```
cd cmd/fiig
go build -o fiig .
```

## Use

```
fiig check <dir>                               type-check a program
fiig run -caps caps.json <dir> <name>           run one declaration
fiig serve -caps caps.json <dir> <adapter>...   serve http adapters
fiig openapi <dir> <adapter>...                 print their OpenAPI document
fiig test <dir>                                run the tests under tests/
fiig fmt -w <file.fiig>...                      format files
fiig lsp                                        the language server an editor starts
```

In IntelliJ IDEA 2026.2 or 2026.3, install the fiig plugin: see
[docs/intellij.md](docs/intellij.md).

```
fiig catalog explain <dir> <name>               a declaration's contract
```

Start with [getting started](docs/getting-started.md), then
[deploying](docs/deploying.md).

## Documentation

- [Language tour](docs/specification/tour.md)
- [Grammar](docs/specification/grammar.md)
- [Layers](docs/layers.md): adapter, workflow, activity, gateway, and
  where errors change meaning
- [Writing a client](docs/writing-a-client.md): a capability group over
  fiig.http, with its shared steps written once
- [A modular monolith](docs/modular-monolith.md): domains in one
  application that can be split out later
- [Bug classes](docs/specification/bug-classes.md): every diagnostic code
- [Decisions](docs/decisions/): one ADR per design decision
- [Charter](docs/charter.md) and [roadmap](fiig-lang.md)

## Repository

- `compiler/`: parser, checker, formatter, registry, runtime
- `cmd/fiig/`: the `fiig` binary
- `tooling/`: the command implementations, and the older single-purpose
  `*-cli` tools
- `libraries/sql/`: the SQL capability provider
- `examples/`: programs that exercise every feature, and invalid programs
  for every bug class

Tests: `go test ./...` at the root and in `libraries/sql`.

## License

BSD 3-Clause; see [LICENSE](LICENSE).
