# fiig in IntelliJ IDEA

The fiig plugin (ADR 0090) supports **IntelliJ IDEA 2026.2 and 2026.3**
(builds 262 and 263), from one zip. Each later release is added when it
ships; releases before 2026.2 aren't supported.

## Install

1. Have the `fiig` binary, from a release archive or `go build` in
   `cmd/fiig`. The plugin starts `fiig lsp` from it.
2. Get the plugin: `fiig-intellij-<version>.zip` from the release, or build
   it (below).
3. **Settings | Plugins | ⚙ | Install Plugin from Disk…**, choose the zip,
   and restart the IDE.
4. **Settings | Languages & Frameworks | fiig**: set the path to `fiig`, or
   leave it empty to use `fiig` from `PATH`. The page shows which binary it
   found and its version.

Open a project with `.fiig` files. A notification appears if no `fiig`
binary can be found.

## What it does

| | |
|---|---|
| As you type | errors from the compiler, the same as `fiig check` (each code is a [bug class](specification/bug-classes.md)) |
| Navigation | Go to Declaration (into other packages and `fiig.http`), Find Usages, Structure, Go to Symbol |
| Quick Documentation | a declaration's signature and the comment above it, or an expression's type |
| Formatting | Reformat Code runs `fiig fmt`; turn on **Settings \| Tools \| Actions on Save \| Reformat code** to format on save |
| Highlighting | colors under **Settings \| Editor \| Color Scheme \| fiig**; each call is coloured by what it calls (rule, conversion, activity, gateway, workflow, ...) under **Calls** (ADR 0106) |
| Editing | brace matching, `Ctrl+/` comments, folding of blocks and comment runs, TODO |
| Running | a ▶ in the gutter on a `workflow` (Run), an `adapter http` (Serve), or any other file's declaration (Check), and a **fiig** run configuration for `fiig run`, `serve`, and `check` |
| New files | **New \| fiig File**: pick a kind and a name; `get customer` makes `getCustomer` in `GetCustomer.fiig` (ADR 0104) |
| Manifests and capabilities files | completion and validation in `fiig.pkg.json`, `caps.json`, `caps.*.json`, and `capabilities.json` |

## Build it

From `ide/intellij`, with any JDK 17 or newer to run Gradle (the build
fetches the Java 25 toolchain IntelliJ 2026.2 and 2026.3 need):

```
./gradlew buildPlugin        # build/distributions/fiig-intellij-<version>.zip
./gradlew test               # the plugin's tests
./gradlew verifyPlugin       # JetBrains' Plugin Verifier against 2026.2 and 2026.3
./gradlew runIde             # a sandbox IDE with the plugin installed
```

By default the build compiles against IntelliJ IDEA 2026.2, the oldest
supported release, and downloads it. To build against an
installed copy instead, set `localIde` to its directory, for example in
`~/.gradle/gradle.properties`:

```
localIde = C:/Program Files/JetBrains/IntelliJ IDEA 2026.2.0.1
```

`-PfiigBinary=<path to fiig>` makes the tests also run `fiig check` on
every new-file template.

The plugin's keyword list is generated from the compiler's: after adding a
keyword, run `go run ./tooling/intellijgen` from the repository root
(`tooling/intellijgen`'s test fails until you do).
