# ADR 0097: Workspaces in the Manifest

Status: Accepted (2026-11-20). Implemented 2026-11-19; see Implementation
notes.
Date: 2026-11-19
Related: ADR 0033 (the workspace-scanned loader), ADR 0037 (more than one
root), ADR 0079 (a modular monolith's sibling packages),
abs-recommendations GAPS G16.

## Context

An application whose packages sit next to it, as ADR 0079's modular
monolith and abs-recommendations do, has to say where they are on every
command:

```
fiig check -workspace .. .
fiig test -workspace .. .
fiig serve -workspace .. -caps caps.json . api
```

Forgetting it fails with "nothing at that major was found", far from the
cause. The location is a fact about the repository, not about one run.

### How others do it

- **Go:** `go.work` at the repository root lists the modules.
- **Cargo:** `[workspace] members = [...]` in the root `Cargo.toml`.
- **npm / pnpm:** `"workspaces"` in `package.json`, or
  `pnpm-workspace.yaml`.

## Decision

A manifest may list `workspaces`, directories relative to the package,
searched for its dependencies as `-workspace` would be:

```json
{
  "path": "abs.recommendations",
  "kind": "application",
  "version": "0.1.0",
  "dependencies": { "abs.client": "0.1.0" },
  "workspaces": [".."]
}
```

- Only the root package's are read. A dependency's dependencies are found
  from the root's workspaces, as they are today.
- `-workspace` still works, and adds to the manifest's. A directory named
  both ways is searched once.
- An empty entry is a manifest error.

## Rationale

- **In the manifest, not a new file.** npm does the same; a separate
  `fiig.work.json` (Go's shape) would be a second file to find and keep
  beside the one every package already has.
- **Relative to the package**, so the repository can be cloned anywhere.

## Alternatives considered

- **A `fiig.work.json` at the repository root**, like `go.work`. Useful
  when many applications share one set of packages, but it would have to
  be searched for upwards from the working directory, and nothing needs
  that yet.
- **Searching the parent directory by default.** Silent, and would find
  packages a program didn't mean to use.

## Consequences

- abs-recommendations' backend drops `-workspace ..` from its scripts.
- `examples/` can name its workspace in each example's manifest; it
  doesn't yet, because the corpus tests pass it explicitly.
