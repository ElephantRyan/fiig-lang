# ADR 0135: `fiig explain <code>`: Why a Rule Exists and What to Do

Status: Proposed
Date: 2026-11-21
Related: ADR 0051 (diagnostics have no severity), ADR 0107 (B12),
`docs/specification/bug-classes.md`, ADR 0131 (checking queries against the
schema), abs-recommendations GAPS G18.

## Context

A diagnostic says what the checker found, e.g.:

```
workflow rate may not call gateway findBook; call it from an activity, and the activity from the workflow (ADR 0107)
```

The reason it exists is in `bug-classes.md` and in the ADR it cites, in the
fiig repository. A reader of the diagnostic alone has the instruction and
none of the reason, and an agent following the instruction literally can
satisfy it in the way the rule wants least. Hearsay's `rememberBook` is one
line, `return saveBook(book, now)`, the exact wrapper B12 asks for. It is
correct, and ADR 0107 is right that "the seam is free". But nothing in the
message says when the wrapper should instead hold a decision, or that the
workflow is where the decision about an error goes.

GAPS G18 is the same problem from the other side: `B7 rule may not call
conversion "http.formEncode"` does not say to make the caller a conversion.
Each is a message that is correct and does not teach.

### How others handle it

| | |
| --- | --- |
| `rustc --explain E0308` | a page per error code: what it means, a failing example, a fix |
| Elm | the message carries a hint; the compiler is known for it |
| `go vet`, `staticcheck` | a URL per check |
| TypeScript | a code (`TS2322`) and the docs search for it |

Rust's model fits best: the diagnostic is short, the explanation is on demand,
and the code is the key. Fiig already prints a code in brackets (`[B12]`,
`[T5]`, `[G4]`).

## Decision

### 2.1 The command

`fiig explain <code>` prints, for one code from `bug-classes.md`:

1. **What it is**: the row's text.
2. **Why it exists**: one or two sentences, with the ADR numbers.
3. **A failing example** and **the fix**, as short `fiig` code.
4. **Not the fix**: the tempting change that satisfies the checker and misses
   the point, where there is one. For B12: adding an activity that only
   forwards a decision the workflow should make.

`fiig explain` with no code lists every code and its one-line summary.

### 2.2 The source is `bug-classes.md`, not a second file

`bug-classes.md` already has one row per class, and `compiler/checker` tests
already cite rows by code. Add the example, the fix and the not-fix to each
row's entry in the file's detail section (the table row stays short), and
have `fiig explain` read the toolchain's embedded copy of it. There is one
place to edit, and a test checks that every code the checker can report has
an entry.

### 2.3 Diagnostics point at it

Each checker message ends with `(fiig explain B12)` when its class has a
detail entry. It replaces the ADR reference in the text only where that
reference is the only pointer; messages that cite an ADR keep it, since the
ADR is where the argument is.

## Rationale

- **A rule is only as good as its reading.** Fiig's checker is how most
  authors learn the language; the ADRs are written for the language's
  maintainers.
- **No new severity or tier.** This changes no diagnostic's meaning (ADR
  0051). It adds an explanation after the fact.
- **It is the cheapest item in this group.** The text already exists in
  scattered form.

## Alternatives considered

- **Longer messages.** Messages are read on every run, and a paragraph per
  error buries the next one.
- **A URL.** The toolchain is offline-capable and versioned; a link is
  neither.
- **Only edit `bug-classes.md`.** Done anyway; but an agent working in an
  application repository does not have it.

## Consequences

- `bug-classes.md` grows a detail section; the table stays as is.
- A test asserts every code in `compiler/checker` has an entry, so a new
  class cannot be added without one.
- `tooling/check-cli` messages gain a trailing `(fiig explain CODE)`;
  anything matching them verbatim (tests, the IntelliJ plugin's problem
  parser) needs to be checked.

## Open questions

1. **Explanations for the "not the fix" line** need judgement per class. Start
   with the classes that have produced a documented misuse (B12, B7, G4, T5)
   and add others as they do.
2. **Should the language server surface it?** `fiig lsp` could offer the text
   on hover over a diagnostic. Not required for this ADR.
