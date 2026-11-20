# ADR 0108: An Application's Code Lives in Folders

Status: Accepted (2026-11-20). Implemented 2026-11-20.
Date: 2026-11-20
Amends: ADR 0105 (a module's top-level package, and the example tree).
Amends also: ADR 0102 §3a (a test may call a workflow of its own module).
Related: ADR 0079 (a domain is a package; each domain has its own
adapter), ADR 0102 (`src/` and `test/`), ADR 0050 (one flat namespace per
package), ADR 0051 (no severity level).

## Context

abs-recommendations kept 120 files in one package, `backend`, until it was
split by hand into domain folders on 2026-11-20. The language didn't force
that, but it allowed it and gave no sign it was happening:

- **Nothing pushed back as the package grew.** One declaration per file and
  one flat namespace make the 121st file free. ADR 0079 says "one package
  per domain", but a package that is no domain looks the same as one that is.
- **The flat package hid coupling ADR 0079 §6a forbids.** Splitting it
  found two domains reading another's table (P4) and three tests calling
  another domain's workflow (B8). Neither was visible inside one package.
- **ADR 0105 made the top level look normal.** Its example tree puts
  `Health.fiig` at `src/`, and its dogfood migration (`-absorb`) folded the
  five packages that already existed and left the loose files where they
  were.
- **The top-level package is already a dead end.** Its folders can't import
  it (G40), so anything shared lands in a folder anyway. Splitting late costs
  more than splitting early, because every cross-package friction G40-G48
  met is paid in one go.

### How others do it

| Language | Code with no package |
|---|---|
| Java | the unnamed package: a named package can't import from it (the same as G40), and style guides and tools say not to use it past a scratch file |
| Go | `package main` at the root, but a project of any size moves its code under `internal/` and `pkg/`; `cmd/` holds only entry points |
| Rust | code in the crate root is normal, and `mod` moves it out as it grows |

Java's unnamed package is the closest case. It's allowed, it works for ten
lines, and it stops working the moment a second package wants to use it.

## Decision

### 1. An application module's top-level package declares nothing

For a module whose manifest says `"kind": "application"`, every
declaration lives in a folder under `src/`. A `.fiig` file directly in
`src/` is an error (H6, loader):

```
src/Rate.fiig: an application's declarations live in a folder, one per domain
(ADR 0108); move it to src/<domain>/Rate.fiig
```

The same goes for `test/`: a test sits in the folder of the package it
tests (ADR 0102 already mirrors them, so no new rule is needed there).

A **library** module is unchanged. Its top-level package is the library's
public face, and it may hold declarations.

### 2. What this changes in practice

- `fiig serve . ratings.ratingsApi` and `fiig run . ratings.rate`: the
  adapter and the workflow are named by their folder, as ADR 0079 §6 already
  does for a dependency's adapters.
- Each domain keeps its own adapter (ADR 0079 §6). The top level isn't where
  the routes are composed.
- ADR 0105's example tree becomes `src/health/Health.fiig`, and its text
  says the top level is empty in an application.

### 2a. Tests of an application with no top level

`test/scenarios/` is the top-level package's. In an application that package
is empty, so a scenario calls the domains by import
(`use shop.orders.orders`), and its whole purpose, checking a promise several
entry points keep, is to call their workflows. B8 forbade a test calling
another package's workflow (abs-recommendations G46), which would have left
every scenario unable to start. ADR 0102 §3a now lets a test call a workflow
of any package of its own module. Another module's workflow is still reached
through a gateway.

### 3. Why a structural rule and not a size limit

- A warning on a large package is a rule that isn't finished being designed
  (ADR 0051), and there's no level for it to sit at.
- A hard limit on the number of files in a package is an arbitrary number.
- "An application has no top level" is a structural fact the language
  already half enforces. A name that has to be chosen for the first file
  ("what domain is this?") is the nudge. No count is involved.

The rule doesn't prevent one folder with 120 files in it. It makes the
author name that folder first, and a name like `backend/` or `app/` on its
own is a visible sign of a missing domain split. What it can't do is say
what a domain is. ADR 0079 §5 gives that guidance, and ADR 0105 should link
to it.

## Rationale

- **The first file is when naming is cheapest.** Later, the split pays for
  every call and test that crossed an invisible boundary.
- **It matches what G40 already forces.** The top package can't hold shared
  code, and ADR 0079 §6 already puts adapters in domains. What's left for
  the top level is nothing.
- **The corpus already agrees.** abs-recommendations ended up at exactly
  this shape after the split: eleven folders, nothing at `src/`.

## Alternatives considered

- **Guidance only** (a paragraph in ADR 0105). What we had. The example
  tree itself taught the opposite.
- **Top level for adapters and config only**, composing the domains in one
  `Api.fiig`. It needs routes that name another package's workflow (G41),
  which ADR 0079 §6 chose against so a domain's adapter moves with the
  domain. Rejected.
- **A package-size warning.** ADR 0051.
- **A hard cap on files per package.** An arbitrary number, and it moves the
  problem to whatever the cap is.
- **Applying it to libraries too.** A library's top level is its API, the one
  place the name `acme.payments` should resolve to something.

## Consequences

- Seven corpus applications have declarations at the top, and nothing else
  does: `getting-started` (14 files), `relay-notes` (14), `shop-orders-split`
  (10), `shop-orders` (9), `inbox-sender` (6), `checkout-app` (2),
  `checkout-clientvertical` (2). That's 57 files. `fiig fmt -layout` moves
  them mechanically; each needs one folder name, which only a person can
  choose.
- `examples/getting-started` gets one folder. That's slightly more ceremony
  for a hello-world, and it shows the layout from the first example.
- Every reference to `src/` files in docs, `docs/intellij.md` and the tour
  moves with them.
- The IntelliJ plugin's New | fiig File dialog should refuse the project's
  `src/` root in an application and ask for a folder.

## Open questions


1. *(Settled: `fiig fmt -layout -into <folder>` names it. The author names
   the domain, since the module path's last segment can collide with a real
   one, so an application with loose files and no `-into` is refused.)*
1. **A module that is both** (an application others import from). ADR 0105
   says a library meant for other projects stays a module of its own, so
   this shouldn't arise. If it does, this ADR says it's a library.

## Implementation notes (2026-11-20)

- **H6** is reported by `checker.CheckDir` for a module whose manifest says
  `application` (a file directly in `src/`, naming the folder to move it to),
  and by the language server as you type. `layout.TopLevelProblem` is the one
  rule both call.
- **The corpus moved.** getting-started (`ratings`), relay-notes (`notes`),
  shop-orders and shop-orders-split (`orders`), inbox-sender (`invoices`),
  and checkout-app and checkout-clientvertical (`checkout`): 57 files, with
  `fiig serve . orders.ordersApi` and `fiig run . ratings.rate` for the
  commands. Two Go fixtures that tested the loader and the language server
  with files at the top level are libraries now, since that isn't what they
  test.
- **A scenario test in the empty top level imports a sibling and calls its
  workflows** (§2a), which needed the B8 change in ADR 0102 §3a.
- **`fiig fmt -layout -into <folder>`** puts the root module's loose files
  in that folder, with their tests beside them, and an application with
  loose files and no `-into` is refused naming the flag. A scenario test, which
  stays in the top-level package, is listed as "by hand": it needs a `use` of
  the folder and qualified calls, which the tool doesn't rewrite. A library
  keeps its top-level files. `compiler/layout.ValidFolder` checks the name.
