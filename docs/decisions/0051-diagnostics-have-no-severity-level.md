# ADR 0051: Diagnostics Have No Severity Level

Status: Accepted (2026-10-27, after review). The float `==` restriction was
implemented 2026-11-02 with float itself (M1, ADR 0048), and `math.closeTo` and
`math.isZero` the same day with ADR 0049's `math` group. Not yet implemented:
the declaration-name case check and the `bug-classes.md` heading wording.
Date: 2026-10-13
Revises: ADR 0048 (§J2), ADR 0050 (§2.1, L1)
Settled by: ADR 0052, which closes this ADR's open question on the
structural-equality hole and carries §2.3's "move it down into typing" step
through to completion

## Context

Three Proposed ADRs have independently asked for a warning, and all three are
blocked on the same missing concept:

- **ADR 0050, L1** — a case-convention violation (`model customer`,
  `rule ValidateEmail`). Its open questions say outright: *"Either L1 is an
  error everywhere, or fiig needs a severity concept, and that is a larger
  decision than this ADR."*
- **ADR 0050, the directory convention** — a directory name and a declared
  package `path` disagreeing. §2.2 states the convention and declines to
  enforce it, then notes a warning *"would have caught the corpus drift
  without coupling anything."*
- **ADR 0048, J2** — exact `==` on two `float` operands, recorded as a
  *candidate* bug class specifically because *"fiig's checker reports errors
  rather than warnings, and this is a pattern that is usually wrong rather
  than always wrong."*

Today a `Diagnostic` is `{File, Msg, At, Rule}` (`compiler/checker/checker.go:78-83`)
and every one of them is fatal. `bug-classes.md` matches that shape: a thing is
either a numbered class the checker reports or it is listed under *"Explicitly
out of scope (not statically checkable)"* (:100). There is no middle.

Adding `Severity` to that struct is a one-line change, which is the trap this
ADR exists to avoid walking into.

## Decision

### 2.1 The checker reports errors only

There is no warning level, no `Severity` field, and no lint tier inside the
checker. A diagnostic means *this program is wrong*. The checker's output is a
binary gate.

### 2.2 The test a check must pass to be a checker diagnostic

A candidate becomes a numbered bug class only if it is all three of:

1. **Decidable** — a property of a finite syntactic object, per fiig's existing
   thesis.
2. **Wrong in every reading** — there is no program in which the pattern is the
   author's correct intent.
3. **Semantically consequential** — permitting it breaks a property something
   else in the language relies on.

A candidate failing (2) is not a check. A candidate failing (3) is not the
checker's business. In both cases the right response is to **fix the rule**,
not to lower its severity: see §2.3, where all three open cases turned out to
be mis-specified rather than merely un-enforceable.

### 2.3 The three blocked cases, resolved without severity

**L1 splits in two, and only one half is a bug class.**

ADR 0050 §2.1 checks case on every name. Only one slice of that is load-bearing
for §2.3's three call-site tones: **a name occupying a dotted-prefix position.**
A capability or enum name must be capitalized; a package alias or formula group
name must not be. That is what makes `Database.queryOne(...)` distinguishable
from `businessTime.addBusinessDays(...)` without reading the import list, and it
passes all three tests. **It stays L1 and stays an error.**

Everything else — `model customer`, `rule ValidateEmail`, parameter, binding,
and field casing — fails test 3. Nothing consults case when resolving those;
`inferFieldAccess` does not, and a lowercase model name breaks no invariant.
That half becomes **stated convention, enforced by a formatter if and when one
exists** (§2.4), not a diagnostic of any severity.

**Revised 2026-10-27: declaration names moved to the checked half.** Two
things now depend on the case that this section called style. ADR 0043's N3
checks binding case: a binding holding a capability is capitalized and any
other is not. And ADR 0050 §2.3's first tone depends on declaration case: a
capitalized call with no dot builds a value (`Customer(name: n)`), and a
lowercase one runs logic (`validateEmail(email)`). A `model customer` would make
`customer(name: n)` read as a rule call, and it would compete with lowercase
locals, the collision ADR 0054 reports. So declaration names now pass all three
tests in §2.2: `model`, `enum`, `interface`, and `capability` names are
capitalized, and `rule`, `conversion`, `activity`, `gateway`, `workflow`, and
`formula` names are not. This is an extension of N1. The corpus already
conforms; the only violation is the deliberate one in
`invalid-programs/capability_error.fiig`. **Field names stay convention**:
`customer.Name` is unambiguous, and ADR 0043 §6c already bans
capability-typed fields, so field case fails test 3.

**J2 becomes a type rule instead of a lint.**

`==` and `!=` with two `float` operands are a **type error**.

The shape of that change is worth stating precisely, because it is the reverse
of what it first looks like. `inferBinary` is int-only for arithmetic under ADR
0019 (`compiler/checker/expr.go:222-226`) — but its comparison branch
constrains nothing at all: it infers both operands for their own errors and
returns `bool` (`:233-241`). So float equality does not have to be *admitted*
when `float` lands; it arrives **for free** unless deliberately excluded, and
this is the first type-specific exclusion comparison has ever carried. That
makes it a real carve-out rather than a non-admission, and it should be read as
consistent with the carve-out arithmetic already has rather than as free.

ADR 0049 §2.4's prelude group gains the replacements:

| Member | Signature |
|---|---|
| `closeTo` | `(a: float, b: float, relative: float, absolute: float) -> bool` — Python's rule, both tolerances required (revised 2026-10-27; see Open questions) |
| `isZero` | `float -> bool` |

The undecidable question J2 was flagged for — *did the author mean an exact
comparison here?* — disappears, because the ambiguous spelling no longer
exists. `x == 0.0` was the legitimate case that made J2 unwarrantable as an
error; `math.isZero(x)` is that case, spelled unambiguously. J2 is therefore
**withdrawn as a bug class** and replaced by a typing restriction.

**The directory convention stays prose.**

It fails test 3 outright — ADR 0050 §2.2 already decided a package's path is
its identity and not its location, so a disagreement with a directory name
breaks nothing. Its entire demonstrated value is catching one instance of drift
in a three-manifest corpus, which is already on 0050's reconcile list. No
check, no lint, no severity.

### 2.4 A formatter is the home for mechanical conventions

The style half of L1, acronym casing (0050's open question), and any future
"this is how fiig is written" rule belong to a `fiig fmt`-shaped tool that
rewrites rather than reports. Fiig is unusually well-positioned for one: the
checker already builds a whole-program `Universe`, and §3.9's one-declaration-
per-file rule makes a case rename a tractable mechanical rewrite.

**Corrected 2026-10-27:** a formatter does exist (`compiler/format`,
`tooling/fmt-cli`, Milestone 1's canonical formatter), but it formats layout and
does not rename identifiers. Enforcing case would mean renaming a declaration
and every reference to it across files, which `gofmt` doesn't do either. So the
gap described next still holds. Original text: **Stated plainly because it is
the weakest part of this ADR:** that tool is unbuilt and unscheduled. Until it exists, the style half of 0050 §2.1 is
*unchecked convention*, with the formatter named as its intended home rather
than assumed to be its home. This ADR does not commit to building it.

## Rationale

**A warning level is unusable without suppression, and suppression is a much
larger decision than any of the three cases deserves to force.** A corpus
cannot be driven to a clean state when some warnings are correct to ignore, so
every language with warnings also has a way to silence one: Rust's
`#[allow]`/`#[warn]`/`#[deny]`/`#[forbid]`, Java's `@SuppressWarnings`, Go's
`//lint:ignore`, ESLint's `eslint-disable`. Fiig has no attribute, pragma, or
annotation syntax at all. "Add a warning for L1" therefore means "add an
annotation system to fiig," which would be a larger grammar change than
anything ADRs 0047–0050 propose combined.

**C and C++ are the evidence that the three-way split collapses in practice.**
Warnings exist there, and every serious project builds with `-Wall -Werror`.
That is an admission of failure: teams want a binary gate badly enough to
reconstruct one on top of a severity system designed to avoid it. The severity
level ended up carrying configuration, not meaning.

**The languages that keep diagnostics trustworthy separate tools, not
severities.** Go's split is three binaries, each binary in its own right:
`go build` errors — and famously made unused variables and unused imports
*errors*, pushing them up rather than inventing a middle rung; `go vet` is a
separate executable with its own exit code for heuristics; `gofmt` is not a
diagnostic at all, it rewrites. Dialyzer is the same discipline in Erlang, with
an explicit design rule — never report something that might be fine — which is
why its output needs no triage. TypeScript is the same shape: `tsc` reports
errors only and the `strict` family decides *whether* something is an error,
with ESLint living outside. Rust is the counterexample, and its richness is
instructive: lint levels, per-item overrides, and Clippy categories
(correctness/style/pedantic/nursery) are a coherent system, but they are a
system, with configuration, documentation, and a learning cost. Fiig's current
diagnostic model is one sentence long.

**Fiig's consumer makes warnings worse here than elsewhere.** A human triages a
warning list by habit; an agent in a loop does one of two things. Either it
fixes everything until the output is clean — including rewriting a correct
`x == 0.0`, which is exactly the false positive J2 was flagged for — or, told
that some diagnostics are advisory, it learns that part of the checker's output
is ignorable. The second outcome is the expensive one, because the checker's
value proposition is that its output never needs judgment. Severity reintroduces
judgment at precisely the interface built to remove it.

**Deprecation, the hardest case for an errors-only checker, already fits**
(added 2026-10-27). Calling a deprecated API is the standard warning in Java
(`@Deprecated`) and Rust (`#[deprecated]`). Fiig has deprecation metadata
(ADR 0036), and it reaches people through the registry: `registry.Card.Deprecated`
and `catalog-cli find --deprecated`, not a checker diagnostic. That matches
§2.2: a call to a deprecated declaration that still works fails test 2, since
the call can be the author's correct intent for now. It is a derived fact about
the program, and derived facts belong in the registry (Open questions, last
item). Nothing about deprecation needs a severity level.

**And `charter.md:56` points the other way on the specific checks asking.** It
says the project should be recast as a framework or code generator *"if
contracts and the checker don't provide leverage beyond naming conventions."*
The charter names naming conventions as the bar to clear. Spending the
diagnostic model's credibility on enforcing one is aimed in the wrong
direction — which is a reason to split L1, not merely a reason to soften it.

**The generalization worth keeping: a request for a warning is usually a rule
that is not finished being designed.** Each of the three cases wanted a warning
for a different defect, and in each case the warning would have preserved the
defect:

- **L1 was stated too wide.** It bundled a load-bearing invariant with pure
  style, and it was the style half that could not carry an error. Splitting the
  rule gave the invariant a real error and cost nothing.
- **J2 was stated at the wrong level.** It was written as a usage heuristic when
  it is a type restriction. Moving it down into the type system removed the
  undecidability that made it a warning candidate. This is Go's unused-variable
  move: take what everyone else warns about and delete the ambiguity that made
  it unwarrantable.
- **The directory convention was stated as a check when ADR 0050 had already
  concluded it was a convention.** The warning request was an attempt to
  re-litigate 0050 §2.2 at a lower severity.

So the test in §2.2 is not only a gate; it is a diagnostic procedure. A check
that wants a warning should first be tried against: can it be *split*, can it
be *moved down* into typing, or should it be *demoted to prose*? If all three
fail, that is when fiig would have a real case for severity — and none of the
three current cases survives it.

## Consequences

**ADR 0050 is revised.** §2.1's rule is split into a checked dotted-prefix tone
rule and an unchecked style convention; L1 narrows to the former; two of its
four open questions close. Its three-tone invariant (§2.3) is unaffected and in
fact better supported, since the part of §2.1 that backs it is now an error with
no severity caveat attached.

**ADR 0048 is revised.** J2 is withdrawn and replaced by a typing restriction on
`==`/`!=` over `float`. J1 (`float` as a stream element key) is untouched — it
passes all three tests, since ADR 0047 §7's per-key ordering property is the
thing it protects.

**ADR 0049 §2.4 gains two members**, `closeTo` and `isZero`, which are the
replacement surface J2's withdrawal requires. Both are fiig-expressible rather
than native, and are in the prelude group because that is where a reader looks
for them, not because they need compiler support.

**`bug-classes.md` gains no new section, which is the point.** Its existing
binary structure — numbered classes, or "explicitly out of scope" — is ratified
rather than extended. The one editorial change is that "out of scope" currently
reads as *not statically checkable*; it now also covers *statically checkable
but not the checker's business*, and the heading should say so.

**Implementation:**

- `compiler/checker` — L1 narrowed to dotted-prefix positions; float `==`/`!=`
  rejected in `inferBinary`. No change to `Diagnostic`.
- `compiler/types` — `closeTo` and `isZero` in the prelude `math` group's
  member table.
- `docs/specification/bug-classes.md` — J2 removed from 0048's proposed set
  before the I/J/K/L merge pass; the out-of-scope heading reworded.
- Nothing anywhere else. There is no severity plumbing, no CLI flag, no second
  exit code, and no advisory tool.

**A lint or advisory tool is now a deliberate non-decision rather than an
assumed future.** An earlier form of this analysis proposed `check-cli --advise`
with its own exit code, on the Go-vet precedent. It was dropped because, after
§2.3, there is nothing left for it to report: one case became an error, one
became a type rule, one became prose. Building the channel first and finding
customers later is how a severity system arrives by accident.

## Proposed bug classes

None. This ADR removes one candidate (J2) and narrows one (L1); it proposes no
new class, and the absence is the decision.

## Open questions

- ~~**Whether `closeTo` should take an absolute tolerance, a relative one, or
  both.**~~ **Settled 2026-10-27: both, by Python's rule** (`math.isclose`,
  PEP 485): `a` and `b` are close when
  `|a − b| ≤ max(relative × max(|a|, |b|), absolute)`.
  - Absolute alone fails for large values: `1e10` and `1e10 + 1` agree to ten
    digits but aren't within `1e-6`. Relative alone fails near zero:
    `closeTo(1e-20, 0.0)` is false at any relative tolerance. Taking the larger
    of the two covers both cases.
  - The rule is symmetric, because it scales by `max(|a|, |b|)`. NumPy's
    `isclose` scales by `|b|` alone, so swapping the arguments can change its
    answer.
  - **Both tolerances are required and passed by name**:
    `math.closeTo(total, expected, relative: 0.0, absolute: 0.005)`. Python's
    defaults hide a choice: `abs_tol=0.0` makes `isclose(x, 0.0)` false for
    every nonzero `x`. With no defaults, every comparison states its tolerance
    at the call site, and naming them prevents swapping them (ADR 0055's
    named-argument defense).
  - `math.isZero(x)` stays an exact test. It spells the one legitimate exact
    comparison, `x == 0.0`; with `-0.0` normalized (ADR 0048 §2.3) there is
    exactly one zero.
- ~~**How far "two `float` operands" reaches.**~~ **Settled 2026-10-13 by ADR
  0052**, which makes comparability a recursive predicate over resolved types:
  a float anywhere in an operand's type graph rejects the comparison (M1), with
  the diagnostic naming the field path to the leaf. This ADR guessed the
  transitive form would be "a larger change" and "a wide restriction for a
  narrow hazard"; both guesses were wrong. It is one recursive function over
  seven type cases, the corpus migration is zero, and the pattern it blocks
  (`if newReading == oldReading` where `Reading` holds a float) is the hazard
  itself in its most dangerous form, since the float is invisible at the call
  site. 0052 also found two further undecided operand questions by applying
  §2.2's three tests to the operators rather than to the reported defect.
- ~~**Whether a formatter should exist.**~~ **Overtaken 2026-10-27**: one does
  (`tooling/fmt-cli`, layout only). Declaration-name case moved to the checker
  (§2.3, revised), so the only unenforced style left is field-name case and
  acronym casing (ADR 0050). Leaving those unenforced is acceptable, and it is
  not a plan to enforce them.
- **Whether the checker should ever report a non-fatal *informational* result**
  that is not a diagnostic at all — a count, a coverage figure, a derived fact.
  The registry and `catalog-cli` are the designed home for derived facts, so the
  presumption is no, but the question is distinct from severity and this ADR does
  not foreclose it.
