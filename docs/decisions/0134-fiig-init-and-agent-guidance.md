# ADR 0134: `fiig init` Writes the Design Contract Next to the Code

Status: Proposed
Date: 2026-11-21
Related: ADR 0133 (`fiig review`), ADR 0105 (a folder is a package), ADR 0108
(an application has no top-level package), ADR 0132 (`application`),
`docs/layers.md`, `docs/getting-started.md`.

## Context

A new fiig program starts from `getting-started.md` and whatever the author
copies from the corpus. Nothing ships with the program that says what each
layer is for. The rules a checker can enforce (B12: a workflow does not call
a gateway) are in the program; the rules it cannot (a workflow decides what a
step's error means for its feature) live in `docs/layers.md`, in the fiig
repository, which the application's author, human or agent, may never open.

Hearsay shows the cost. It has a `PLAN.md` and a `GAPS.md`, and no statement
of the layer contract, and an agent building in it chose one HTTP-shaped
error type for every layer (ADR 0133, Context). The fiig docs already say
the right thing; it was in the wrong repository for the work.

### How others handle it

| | How a new project gets its conventions |
| --- | --- |
| `rails new`, `mix phx.new` | a scaffold whose layout *is* the convention, plus a README that names it |
| `cargo new` | a layout and nothing else; conventions live in the Book |
| `go mod init` | nothing |
| `npm create vite`, `django-admin startproject` | a scaffold and a README |
| agent tooling (`CLAUDE.md`, `AGENTS.md`) | a file the agent reads at the start of every session |

The two that give design guidance (Rails, Phoenix) do it by making the
scaffold a worked example. Fiig's checker already makes the layout partly
mandatory (H6, one folder per domain), so the scaffold can be small.

## Decision

### 2.1 `fiig init [<dir>]`

Creates, in an empty or new directory:

- `fiig.pkg.json` for an `application` (`kind: application`, capabilities
  empty);
- `src/<domain>/` for one domain named by `-domain` (default `app`), holding
  one working vertical slice that passes `fiig check` and `fiig test`: an
  adapter route, a workflow, an activity, a gateway, a model, a domain error
  enum, the conversion from that error to an HTTP response in the adapter's
  package, and one test through the workflow;
- `src/<domain>/<Name>.fiig` with the `application` declaration (ADR 0132);
- `AGENTS.md` (below).

The slice follows `docs/layers.md` exactly, so it is a worked example of the
error flow: the gateway returns the outside world's failure mapped into a
domain error, the activity passes it up unchanged, the workflow decides what
it means for this feature, and only the adapter chooses a status.

### 2.2 `AGENTS.md`

Generated from a template in the toolchain, not copied from `layers.md` at
run time, so it is the same text offline. It states, in under a page:

1. The four layers and one sentence of job each (the table in `layers.md`).
2. The error rule: errors below the adapter are domain errors and carry no
   transport meaning; only an adapter turns one into a status.
3. What a workflow is for: it decides something about its feature. A feature
   that decides nothing still gets a workflow, because B12 requires it; it
   returns its activity's result, and says so in its comment.
4. Where a declaration goes: one declaration per file, one folder per domain,
   shared code is a folder and not the top level (H6).
5. The commands: `fiig check`, `fiig test`, `fiig fmt`, `fiig catalog`,
   `fiig review` (ADR 0133).
6. Where to record friction: a `GAPS.md` entry per the format fiig's author
   uses (reproducible, with the exact diagnostic).

Item 3 deliberately says what `layers.md` leaves open (ADR 0133, Open
question 1); this ADR's text must follow whichever answer that gets.

### 2.3 Idempotent

`fiig init` refuses to overwrite: if any file it would create exists, it
changes nothing and names the file.

`fiig init -agents-only` writes only `AGENTS.md`, into an existing program,
which is how Hearsay and the corpus applications would adopt it.

## Rationale

- **The agent reads the repo it is in.** A contract that lives with the
  program is read at the start of every session. One that lives in a
  toolchain repository is not.
- **A worked example beats prose for a layering rule.** The slice is also the
  smallest test that the rules agree with each other: it must check, test and
  pass `fiig review` with nothing worth reporting.
- **It costs the author almost nothing.** The scaffold is what they would
  have written, and `-agents-only` adds one file to an existing program.

## Alternatives considered

- **A longer `getting-started.md`.** It exists, and it is not in the
  program's repository.
- **A template repository.** Same content, but it cannot be versioned with
  the toolchain, so the text drifts from the checker. `fiig init` is built
  by the same release, and a test keeps them together.
- **Make `fiig check` print the guidance.** Rejected by ADR 0051: the checker
  says what is wrong, not how to design.

## Consequences

- A test runs `fiig init` into a temporary directory, then `check`, `test`
  and `review` over it, so the scaffold cannot rot.
- The template text is a second copy of part of `layers.md`; a test fails if
  `layers.md`'s table and the template disagree.
- Existing applications gain a file when they choose to.

## Open questions

1. **How many domains in the scaffold?** One keeps it small, but a layering
   rule that crosses domains (ADR 0079 §6a) is not shown.
2. **Should `AGENTS.md` be named for a specific tool?** The name is a
   convention several agents read; the content is plain markdown.
