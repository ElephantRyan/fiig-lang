# ADR 0090: An IntelliJ Plugin for fiig

Status: Accepted and implemented (2026-11-18; see Implementation notes)
Date: 2026-11-17
Related: fiig-lang.md Milestone 9, ADR 0089 (`fiig lsp`), ADR 0027 and ADR
0071 (the capabilities file), ADR 0054 (one declaration per file).

## Context

The project owner wants fiig in IntelliJ IDEA specifically, built as a
hybrid: what needs the compiler comes from `fiig lsp` (ADR 0089) through
JetBrains' own LSP client, and what should never wait for a server is
native to the plugin. The owner's IDE is IntelliJ IDEA Ultimate 2026.2
(build 262), which bundles that client (`com.intellij.platform.lsp`,
with its structure-view support).

What the LSP client maps from `fiig lsp`: errors as you type, quick
documentation from hover, go to declaration, find usages, the structure
view from document symbols, Go to Symbol, and Reformat Code from
formatting. What it doesn't give: highlighting before the server answers,
brace matching, commenting, folding, run configurations, file templates,
or anything about the JSON files a fiig project also has.

### How others split it

| Plugin | Native | From the server |
|---|---|---|
| JetBrains' Prisma, Astro, Tailwind | file type, TextMate highlighting | everything else |
| Dart (analysis server) | lexer, PSI, run configurations | diagnostics, navigation, completion |
| ZigBrains | lexer, highlighting, run configurations | diagnostics, navigation (over LSP4IJ) |
| intellij-rust, intellij-elixir | everything | nothing: a second implementation |

## Decision

### 1. A Kotlin plugin in the repository

The plugin is a Gradle project at `ide/intellij/`, written in Kotlin with
the IntelliJ Platform Gradle Plugin 2.x and JDK 21, built against
IntelliJ IDEA 2026.2. Its plugin ID is `dev.fiig.lang` (JetBrains forbids "intellij" in one), and its
display name `fiig`. It depends on the platform's LSP module and JSON
module, and on nothing outside the IDE.

### 1a. One IDE version at a time

The plugin supports IntelliJ IDEA 2026.2 (build 262) and nothing earlier:
its `since-build` and `until-build` are both `262.*`, so the IDE won't
install it elsewhere, and the Plugin Verifier checks it against 262 only.
Each later IntelliJ release is supported when it ships, by moving the
range, building, and verifying against it; older versions aren't
back-supported. This is the project owner's decision (2026-11-18): the
plugin follows the IDE forward, as the toolchain does.

### 2. The file type and a native lexer

`.fiig` is a file type with its own icon. The plugin's lexer, a
`LexerBase` in Kotlin, produces keywords, identifiers, numbers, strings
(with `${...}` interpolation and escapes), binary segments, comments,
operators, and braces. It drives:

- **highlighting**, by token kind, with a color settings page so a
  theme or a user can change each one;
- **brace matching** for `{}`, `()`, `[]`, `<<>>`;
- **commenting** with `//`;
- **folding** of `{ }` blocks and runs of comment lines;
- **TODO** detection in comments.

The keyword list comes from the compiler's: `go generate` writes
`FiigKeywords.kt` from `compiler/lexer`'s table, and a Go test fails when
the two differ, so a new keyword reaches the plugin or the build breaks.
Contextual words (`serializable`, `repeatableRead`) are highlighted as
identifiers, as the compiler's lexer reads them.

A minimal flat PSI, one leaf per token under the file, is all the plugin
parses; it exists so run markers and templates can find a declaration's
keyword and name. Names are resolved only by the server.

### 3. The language server

The plugin starts `fiig lsp` for every project containing a `.fiig` file,
one server per project, with the project directory as its workspace.
The `fiig` binary is found from **Settings | Languages & Frameworks |
fiig** (a path field), then from `PATH`. When none is found the plugin
shows one notification with a link to that setting, rather than failing
silently. The settings page shows the found binary's `fiig version`.

Every feature ADR 0089 serves is used as the client maps it: errors in
the editor and the Problems view, Quick Documentation, Go to Declaration,
Find Usages, the Structure view, Go to Symbol, and Reformat Code (so
**Actions on Save → Reformat code** formats with `fiig fmt`).

### 4. Run configurations

A **fiig** run configuration type with three kinds, each a `fiig`
command with its flags as fields:

| Kind | Command | Fields |
|---|---|---|
| Run | `fiig run` | package directory, declaration, `-args` JSON, `-caps`, `-env-file`, `-workspace` |
| Serve | `fiig serve` | package directory, adapters, `-caps`, `-env-file`, `-addr`, `-workspace` |
| Check | `fiig check` | package directory, `-workspace` |

Output goes to the Run tool window, where `file:line:column` locations in
`fiig check`'s output and in faults are links. A gutter ▶ icon on a
`workflow` declaration offers Run and on an `adapter http` offers Serve,
each creating a configuration from context: the package is the nearest
directory with `fiig.pkg.json` (or the file's directory), `-workspace` is
the project directory, and `-caps` is `caps.json` beside the manifest
when there is one. A gutter Check action on any file runs `fiig check`
on its package.

### 5. New-file templates

**New → fiig File** asks for a kind (model, enum, rule, conversion,
activity, gateway, workflow, adapter, config, capability, interface,
formulas) and a name, and creates one declaration in a file named from
it, as the corpus names them: `getCustomer` in `get_customer.fiig`
(ADR 0054's one declaration per file). Each template is the smallest
declaration that checks, with a gateway's `requires` and a workflow's
return type left as placeholders to fill.

### 6. JSON schemas for the files around the code

The plugin registers JSON Schemas, so IntelliJ completes and validates:

- **`fiig.pkg.json`**: every manifest field, the package kinds, a
  dependency's version form, `provides`, `capabilities`, and
  `nativeCapabilities`;
- **capabilities files**: `caps.json`, `caps.*.json`, and
  `capabilities.json` — each capability's `type` (`sql`, `driver`,
  `socket`, `socket-script`, `scripted`, `random-seeded`) and that type's
  fields, the `packages` section, and `${NAME}` strings.

The schemas are files in the repository (`docs/schemas/`), and a Go test
validates every manifest and capabilities file in `examples/` against
them, so they can't fall behind what the toolchain accepts.

### 7. Testing and distribution

- **Tests** run with Gradle against the IDE's test framework: the lexer
  over every `.fiig` file in `examples/` (no bad-character tokens, every
  token's text reassembling the file), highlighting, brace matching,
  folding, templates producing files `fiig check` accepts, run
  configurations built from a gutter marker, and the schemas' mapping.
  The JetBrains Plugin Verifier checks the plugin against 2026.2.
- **CI** builds and tests the plugin in its own job.
- **Releases** attach `fiig-intellij-<version>.zip` to the GitHub release,
  installed with **Settings | Plugins | ⚙ | Install Plugin from Disk**.
  JetBrains Marketplace is later, and needs a public listing.
- The look and the end-to-end behavior in a real IDE are checked by the
  owner, from a build installed from disk or `./gradlew runIde`.

## Alternatives considered

- **TextMate highlighting** (Prisma, Astro). No brace matching or folding
  driven by the language, and a grammar of regular expressions drifts
  from the compiler's lexer as easily as a Kotlin one, without a test
  holding it.
- **A full PSI parser and resolver in Kotlin** (intellij-rust). A second
  fiig, which ADR 0089 rejected for the same reason.
- **LSP4IJ** (Red Hat's LSP client plugin) instead of the platform's.
  Works in more editions, but is a second plugin to install, and the
  owner's IDE has the platform client.

## Open questions

1. **Completion, rename, and quick fixes** arrive with the server ADRs
   that add them, with no plugin change beyond what the client maps.
2. **Debugging** fiig code, which needs a debug adapter the toolchain
   doesn't have.

## Implementation notes (2026-11-18)

- **Build:** Gradle 9.9.1 (wrapper), IntelliJ Platform Gradle Plugin
  2.20.1, Kotlin 2.4.21. IntelliJ 2026.2's classes are Java 25 (class file
  69), so the build uses a Java 25 toolchain, fetched by the foojay
  resolver when none is installed; the plugin's bytecode targets 21. The
  build downloads IntelliJ IDEA 2026.2.5, or uses an installed IDE when
  `localIde` is set.
- **The LSP API was renamed in 2026.2:** `LspIntegrationProvider`,
  `ProjectWideLspClientDescriptor`, `LspClientManager`, and the
  `platform.lsp.integrationProvider` extension point replace the
  `...ServerSupportProvider` names, which still work but are deprecated.
  The plugin uses the new ones.
- **Plugin ID** is `dev.fiig.lang`: the Plugin Verifier refuses an ID
  containing "intellij".
- **Brace matching** pairs `{}`, `()`, `[]`, and `${ }`, not `<< >>`: the
  closing `>>` is two `>` tokens in fiig's lexer (ADR 0058), which the
  platform's matcher can't pair as one.
- **The gutter** offers Check on any file's first declaration that isn't a
  workflow or an `adapter http` (an `adapter cli` included).
- **Keywords** are generated by `go run ./tooling/intellijgen`, which
  `TestKeywordsAreCurrent` keeps current; `lexer.Keywords()` is new.
- **Schemas** are tested by `tooling/intellijgen`'s `schemas_test.go`
  against all 17 manifests and 7 capabilities files in the repository,
  with a small validator for the JSON Schema subset they use, and against
  ten documents the toolchain refuses.
- **Tests:** 16 in the plugin (the lexer over every example file;
  highlighting, braces, commenting, folding, gutter targets, command
  lines, naming, every template run through `fiig check`, schema
  registration, and the server's command line). The Plugin Verifier
  reports **Compatible** against IU-262.8665.337 with no deprecated or
  internal API use.
- **Not checked by automation:** how it looks and behaves in a running
  IDE, which the project owner checks from the zip or `./gradlew runIde`
  (§7).
- `.gitignore`'s bare `fiig` pattern, meant for the built binary, would
  have ignored the plugin's `dev/fiig/` sources; it now names the binary's
  two paths.
