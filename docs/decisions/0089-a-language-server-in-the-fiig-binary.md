# ADR 0089: A Language Server in the `fiig` Binary

Status: Accepted and implemented (2026-11-17; see Implementation notes)
Date: 2026-11-17
Related: fiig-lang.md Milestone 9 (IDE support), ADR 0051 (no severity
levels), ADR 0074 (packages built into the toolchain), ADR 0090 (the
IntelliJ plugin, to follow).

## Context

Milestone 9 asks for fiig in IntelliJ IDEA "with the same feedback the
compiler gives on the command line". The project owner chose a hybrid
plugin: editor basics (file type, highlighting, comments, braces, run
configurations) native to IntelliJ, and everything that needs the
compiler's understanding (errors, types, definitions, usages) from a
language server the plugin starts. IntelliJ IDEA 2026.2 bundles
JetBrains' LSP client (`com.intellij.platform.lsp`), so the plugin needs
no third-party client. This ADR is the server; ADR 0090 will be the
plugin.

What the compiler has today:

- **Speed.** `fiig check` of a whole example program, dependencies
  included, takes about 30 ms, so the server can recheck a package on
  every edit with no incremental compiler.
- **Positions.** Every AST node has a start and end (line, column, byte
  offset); columns count code points.
- **Recovery.** The parser reports several errors per file and keeps
  going, and one broken file doesn't stop the others being checked. A
  syntax error also produces a follow-on `undefined name "<error>"` for
  the placeholder node it leaves.
- **Gaps.** A diagnostic has a start but no end. The checker resolves
  every name and types every expression, but keeps none of it: nothing
  records which declaration a name refers to.

### How other languages built theirs

| | Where | Analysis | Lesson |
|---|---|---|---|
| Gleam `gleam lsp` | in the compiler binary | rechecks changed modules; keeps the last good result while a file is broken | closest to fiig: one binary, a small language |
| TypeScript tsserver | in the compiler | the compiler's own checker | the IDE and the command line agree |
| gopls | its own binary, over `go/types` | the checker records `Defs`, `Uses`, and `Types`, which the IDE queries | the record fiig's checker needs |
| rust-analyzer | its own frontend | an incremental query engine (salsa) | years of work; not needed at fiig's scale |
| zls, elm-language-server | separate projects | their own analysis | they disagree with the compiler and drift |

## Decision

### 1. `fiig lsp`

The `fiig` binary gains a subcommand, `fiig lsp`, that speaks the
Language Server Protocol 3.17 over standard input and output (JSON-RPC
2.0 with `Content-Length` framing). One install gives the command line
and the editor the same compiler, the same version, and the same
diagnostics. The server is `tooling/lsp` in the root module. Its
protocol layer, the JSON-RPC framing and the dozen message types it
uses, is written in the package rather than taken from a library: the
root module has one third-party dependency, and the subset is small and
stable.

### 2. Workspace and packages

The editor's project folder is the workspace. A package is a directory
with a `fiig.pkg.json`, or the folder of a file outside any package. A
dependency is found as `fiig check -workspace <project folder>` finds it,
plus the packages built into the toolchain. The server reads files from
disk, overlaid with the text of every file the editor has open, so it
checks what is on screen, saved or not.

### 3. Every change rechecks the package

On every change the server waits 150 ms for typing to pause, then checks
the package that changed with `fiig check`'s own code, and publishes
diagnostics for each of its files, an empty list for a file whose errors
are gone. Packages in the workspace that depend on it are checked too,
so a renamed declaration shows its broken callers.

While a file doesn't parse, hover, go to declaration, and find usages
answer from the last check in which it did, as Gleam does, so a
half-typed line doesn't blank the editor.

### 4. The checker records what it resolves

Checking produces an index alongside the diagnostics: for each name in
the source, the declaration, parameter, local, field, or variant it
refers to (file and span), and for each expression its type. It is
filled by the resolution the checker already does, so the editor can't
disagree with the compiler about what a name means. Hover, go to
declaration, and find usages are lookups in it.

### 5. Diagnostics

- **A range,** from the start to the end of the node the diagnostic is
  about. `checker.Diagnostic` gains `End`, so `fiig check -json` reports
  it too.
- **Every diagnostic is an error.** ADR 0051 rejected severity levels,
  and the protocol's severity is always Error.
- **The code is the bug class** (`A1`, `Q2`), linking to its row in
  `docs/specification/bug-classes.md`.
- **No follow-on noise.** A name the parser invented for a syntax error
  reports nothing further, in the editor and in `fiig check`.

### 6. Positions

The protocol counts columns in UTF-16 code units unless the client
accepts UTF-8 or UTF-32. The server offers UTF-32, which is fiig's own
code points, then UTF-8, and falls back to UTF-16; it converts using each
position's byte offset and the line's text.

### 7. Phase 1

| Request | What it gives |
|---|---|
| `textDocument/publishDiagnostics` | the checker's errors as you type |
| `textDocument/formatting` | `fiig fmt` of the whole file; no change while it doesn't parse |
| `textDocument/hover` | a declaration's signature and the comment above it; an expression's type |
| `textDocument/definition` | the declaration a name refers to, in this package, another, or a built-in one (`fiig.http`) |
| `textDocument/references` | every use of a declaration across the workspace |
| `textDocument/documentSymbol` | a file's declarations, for IntelliJ's structure view |
| `workspace/symbol` | every declaration by name, for Go to Symbol |

Completion, rename, quick fixes, and inlay hints are later ADRs; syntax
highlighting is the plugin's own lexer (ADR 0090), not the server's.

### 8. The server stays up

A malformed message is answered with a JSON-RPC error. A panic during a
check is recovered, logged with `window/logMessage`, and that check's
results dropped; the next change checks again. The server exits on
`exit`, or when its input closes.

## Consequences

- `tooling/lsp` (server, protocol, index queries); `fiig lsp` in
  `cmd/fiig`.
- `compiler/checker`: the resolution index, `Diagnostic.End`, and no
  diagnostics on parser placeholders.
- Tests drive the server over pipes with protocol messages, against the
  corpus: diagnostics appearing and clearing, a definition in another
  package, references across files, position encodings with non-ASCII
  text, and recovery from a half-typed file.

## Alternatives considered

- **A separate `fiig-lsp` binary.** Two things to install and to keep at
  the same version, for no gain.
- **An LSP library** (`go.lsp.dev/protocol`, or gopls's internal one,
  which can't be imported). A dependency for a subset small enough to
  write.
- **Analysis separate from the compiler** (tree-sitter, or a second
  resolver). zls and elm-language-server show where that goes.
- **An incremental engine** (salsa-style queries). A 30 ms full check
  doesn't need one; revisit if a real program makes checking slow.

## Open questions

1. **Completion, rename, and quick fixes**, which the index makes
   possible.
2. **Large workspaces.** Checking dependents on every change is cheap now;
   a workspace of hundreds of packages may need dependents checked only
   on save.
3. **Multiple project folders** in one editor window.

## Implementation notes (2026-11-17)

- **The index** (`compiler/checker/index.go`) is filled where the checker
  already resolves: written types (`resolve`), calls of a declaration, a
  group used as a value, qualified calls (an enum variant, a group's
  gateway, a package's declaration), field access on a model, bare
  variants, and constructor patterns; every inferred expression's type is
  kept for hover. `CheckSources` checks text in hand and returns each
  file's syntax, comments, index, and parse-error count; `CheckDir` now
  reads files and calls the same code.
- **Two answers don't come from the index.** A local or parameter isn't
  recorded (the checker's scopes are maps of types, with no positions), so
  go to declaration finds its binding lexically, the latest one above the
  use in the same declaration, which is fiig's scoping; and find usages
  covers declarations and their members, not locals. A name inside a
  string's `${...}` is found from the text, since no token covers it.
- **Diagnostic ends** are the end of the token at the diagnostic's start,
  computed after checking, rather than the whole node: the checker's 400
  report sites pass a start only. That highlights the name, keyword, or
  token the message is about.
- **Identity across checks:** each package's check parses its
  dependencies afresh, so declarations are matched by package path and
  name (`declID`), not by pointer. The last good check of a file is kept
  with its own snapshot, so navigation while a file doesn't parse still
  resolves.
- **Dependencies' unsaved edits:** a package's dependencies are read from
  disk by the loader, so an unsaved change in one package reaches its
  dependents' diagnostics on save, not while typing.
- **Built-in packages** are written once to the user cache directory
  (`fiig/stdlib/...`), so go to declaration into `fiig.http` opens a real
  file.
- **Dogfooded** with the built `fiig` binary over stdio against
  `examples/` (about 40 packages): initial analysis 0.18 s; go to
  declaration into `fiig.http`'s `HttpClient`, `Request`, and `Method`;
  hover on `Customer`; 53 usages of `Customer` in 22 files; workspace
  symbols in 1 ms; a typo's diagnostic 165 ms after the change, with its
  range. The manifest diagnostics it shows for `acme-http` and three other
  library packages (`Socket` not in their capabilities list) are the same
  ones `fiig check` reports.
- **Tests** (`tooling/lsp/lsp_test.go`) drive the server over pipes:
  encoding negotiation; diagnostics appearing and clearing; definition
  across packages, into a field, to a local and a parameter; references,
  hover, symbols, workspace symbols, and formatting; the three encodings
  against a line with `é`; navigation while a file doesn't parse.
