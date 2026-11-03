# ADR 0050: Names, Namespaces, and Qualification

Status: Accepted (2026-10-27, after review). Done: the `time.library` rename,
and the wildcard-import removal (§2.4, 2026-11-01; see Implementation notes).
Not yet implemented: the formula-group part of N1, which waits on ADR 0049.
Date: 2026-10-13 (revised the same day: §2.1 split into a checked half and a
conventional half by ADR 0051, which also closes two open questions)

## Context

Fiig has accumulated a naming system without ever deciding one. The pieces are
all in the corpus and none of them is written down:

- Models, values, and enums are capitalized (`Customer`, `CustomerId`,
  `CheckoutError`); rules, conversions, activities, gateways, and workflows are
  lowerCamelCase (`renameCustomer`, `addBusinessDays`).
- Capabilities are capitalized and called with a dot: `Database.queryOne(...)`,
  `PaymentGateway.charge(...)`, `EmailSender.send(...)`,
  `ShippingProvider.reserve(...)`.
- Package aliases are lowercase and called with a dot:
  `businessTime.addBusinessDays(from, 3)`
  (`time-library/activities/compute_follow_up_date.fiig:14`).
- Prelude identifiers are capitalized, and `grammar.md:71-73` says they are
  *"distinguished only by convention (capitalized)"* — an admission that the
  convention is load-bearing and unenforced.
- Package paths are dot-separated (`package-path ::= identifier { '.' identifier }`),
  which is the same separator used to select a member — and a package's path is
  independent of its directory name (`company.time` lives in
  `examples/company-time/`), which nothing had decided either way and which the
  corpus is already inconsistent about.

Every one of those is a real invariant that an agent reading fiig could rely on
and currently cannot, because none is stated and none is enforced.

The forcing question came from ADR 0049, which needs somewhere to put twenty
numeric operations and must choose a capitalization for the namespace that
holds them. Answering that in 0049 alone would have set a convention for the
whole language as a side effect of a math-library decision.

A first revision of this ADR also changed the path separator to `/`. §2.2
records why that was withdrawn; the section is kept because the reasoning
against it is more useful than the proposal was.

## Decision

### 2.1 Case is normative everywhere, and checked where it is load-bearing

| Form | Applies to |
|---|---|
| `UpperCamelCase` | `model`, `enum`, enum variants, `interface`, capability names and capability groups, prelude type and constructor identifiers, and a parameter or binding that holds a capability (ADR 0043 N3) |
| `lowerCamelCase` | `rule`, `conversion`, `activity`, `gateway`, `workflow`, `formula`, formula-group names, package path segments, package aliases, capability operations, field names, and every other parameter and binding |

(Table reconciled 2026-10-27 with ADR 0043 §6, which was accepted and
implemented after this ADR was drafted: `value` is gone (ADR 0055), and
`interface`, capability groups, and capability-holding bindings are added.)

The rule is **"a name that denotes a type is capitalized; a name that denotes
something you call or bind is not."** Capability names are the one entry that
needs explaining: a capability is a declared contract, not an operation, so it
sits on the type side. `Database` is a kind of thing; `Database.queryOne` is
the operation.

**One slice of this table is checked and the rest is convention.** ADR 0051
split it, on the grounds that only part of it is load-bearing. Most of the
checked slice was implemented by ADR 0043 as N1–N3 (see L1 below):

- **Checked (L1).** A name occupying a **dotted-prefix position**: a capability
  or enum name must be capitalized, a package alias or formula-group name must
  not be. This is exactly what §2.3's third tone rests on, so if it only
  usually held, the invariant would be worth nothing.
- **Revised 2026-10-27 (ADR 0051 §2.3):** declaration names are now checked
  too, as an extension of N1, because the first tone (`Customer(...)` builds a
  value, `validateEmail(...)` runs logic) depends on them, and bindings are
  checked by N3. What remains unchecked below is field-name case.
- **Convention, unchecked.** Everything else — a `model`, `value`, `rule`,
  `conversion`, `activity`, `gateway`, `workflow`, or `formula` name, a
  parameter, an `=` binding, a field. Nothing in resolution consults case for
  these; `inferFieldAccess` does not, and a lowercase model name breaks no
  invariant §2.3 relies on. ADR 0051 §2.4 names a formatter as the intended
  home and declines to commit to building one, so this half is unenforced for
  now and the table is a style guide for it.

The split is not a softening. The checked half is an error with no severity
caveat attached, which is a stronger position than the original rule could
hold — see ADR 0051's Rationale, where the warning this section originally
wanted turns out to have been a symptom of the rule being stated too wide.

### 2.2 A package's path is its identity, not its location — and the separator stays `.`

**A package's `path` is independent of the directory it lives in.** It is
declared in the manifest, it is the package's identity, and nothing requires it
to mirror a directory name, a directory nesting, or a workspace root.
Uniqueness is enforced on the identity rather than on the location:
`loader.go:67-75` records that the same path found under two roots at the same
major is already an ambiguous-install error from `validateInstalled`, and
*"which root it came from doesn't make two conflicting installs any less
conflicting."*

**The directory convention is hyphen-as-separator**, documented and not
enforced: a directory `company-time/` holds the package `company.time`. Two of
the three corpus manifests already follow it; the third
(`examples/time-library/` declaring `examples.timeLibrary`) reads the hyphen as
a word break instead and prepends a namespacing segment matching no directory.
That one is an exception, and the Consequences say what to do with it.

Requiring a match was considered and rejected — see the Rationale.

#### The separator stays `.`; a `/` form is rejected

```ebnf
import       ::= 'use' package-path [ 'as' identifier ]
package-path ::= identifier { '.' identifier }
```

Unchanged. A first revision of this ADR adopted Go-style `use company/time`,
on the reasoning that a path and a member selection are different operations
and deserve different operators. Both of that reasoning's premises turned out
to be false here, and recording why is the point of keeping this section.

**The ambiguity it was fixing cannot occur.** `company.time.addBusinessDays`
would indeed be unreadable — but it is unwritable. Multi-segment paths appear
only in `use` lines and in the manifest's `path` field, where the path is
delimited by `as`, end of line, or a JSON string boundary. Every reference site
goes through a single-identifier alias: `businessTime.addBusinessDays`,
`systemTime.Instant`. §3's `qualified-name` production permits more segments,
but §2.4 forbids them and §2.5 rejects path references outright. The separator
change was repairing something two sections later already made unreachable.

**A `/` would misinform, given the ruling above.** `loader.Load(root, roots)`
scans a workspace for manifests and matches declared `path` values; it never
walks a path as a directory, and no package's path mirrors its directory today.
A slash asserts filesystem nesting, so it would claim a relationship this
section just declined to require — worse than a dot rather than better.

**What a reader loses by keeping `.`, honestly stated.** In a multi-segment
import the boundary between package and formula group is invisible —
`use standard.geometry.vector` does not say which prefix is the package. Java
has the same shape (`java.lang.Math`) and escapes it by case, since its
namespaces are lowercase and its classes are not; fiig cannot, because §2.6
makes formula groups lowercase too. This is accepted rather than fixed: the
loader resolves the boundary by longest declared-prefix match (ADR 0049 §2.5),
and the boundary is not information a call site needs — `vector.normalize(v)`
reads the same either way.

### 2.3 Three call-site tones, and that is the whole vocabulary

Given §2.1, every call in fiig reads as exactly one of three things, with no
cross-referencing:

| Call | Means |
|---|---|
| `validateEmail(email)` | declared in **this package** |
| `businessTime.addBusinessDays(from, 3)` | declared in **another package**, or in a formula group |
| `Database.queryOne(...)` | a **capability** or an **enum constructor** |
| `email.contains("@")` | a **method on a local value** (added 2026-10-27) |

**The fourth row was missing from the first draft.** A lowercase dotted prefix
is either a namespace (a package alias or a formula group) or a local value
with a built-in method (`email.contains("@")`, `xs.at(0)`, ADR 0049 §2.6). The
two never share a name: G4 (ADR 0043 §3a) makes an alias colliding with a
parameter or binding an error. Telling them apart takes one lookup, and it is
always local. ADR 0049 §2.5 requires every formula group to be imported, so
this file's own `use` lines list every namespace prefix it can use, a few
lines above its one declaration (§3.9). A lowercase prefix not named there is
a local.

This invariant is the point of the ADR. The first two tones are the
domain-code/library-code distinction, available without reading the import
list — which matters most for an agent generating code, since it is the
difference between "define this" and "call this."

The third tone is honestly imperfect: a capitalized prefix is already either a
capability (effects, must appear in `requires`) or an enum variant constructor
(pure, ADR 0046). The disambiguator is the enclosing declaration's `requires`
clause, which names every capability it may use, so a capitalized prefix absent
from `requires` is an enum. Two meanings is tolerable; three is not, which is
why §2.6 sends formula groups to the lowercase tone.

### 2.4 Cross-package references are always qualified

A reference to a declaration in another package is written `alias.name`. There
is no unqualified form. Omitting `as` binds the alias to the **last path
segment** — `use standard.time` binds `time`.

The wildcard form is **removed from the grammar** rather than left parseable
and rejected by the checker. §3.10 already rejects it and
`invalid-programs/wildcard_import_error.fiig` attests that; once qualification
is mandatory there is no form left for a wildcard to be the degenerate case of,
so it stops being a rule about imports and becomes an absence of syntax. The
fixture becomes a parse-error fixture.

A redundant alias is legal and sometimes deliberate: `add_business_days.fiig:1`
writes `use standard.time as time` specifically so the file's own alias is
independent of whatever a consuming file chooses.

### 2.5 Alias collisions are resolved by `as`, never by a full path

Two packages whose paths end in the same segment collide, and the collision is
already an error — bug class G3, which `checker_test.go:356-361` covers in both
its explicit and its implicit form. The resolution is to alias one of them.

A full-path reference form (`jones/math.add(x)`) is **rejected**:

- **It is lexically ambiguous.** `/` is division (ADR 0019), so
  `jones/math.add(x)` parses equally well as `jones / (math.add(x))` — divide
  the int `jones` by the result of the call. The lexer cannot separate them
  without already knowing `jones` names a package root. This is exactly why
  Rust spells paths `::` and not `.` or `/`.
- **An unambiguous spelling would still be the wrong trade.** `jones::math.add(x)`
  would lex, but a path's cost is paid at every call site while an alias's is
  paid once at the import. Go is the strongest precedent available here: it is
  the language most committed to mandatory qualification and it has **no**
  full-path reference form at all — two packages named `rand` are disambiguated
  with `import crand "crypto/rand"`.
- **§3.9 makes the alternative cheap.** One declaration per file means the
  import list is always a handful of lines directly above the only declaration
  that uses it. The cross-referencing cost that makes full paths attractive in
  a large file is near zero here.

### 2.6 Formula groups take the lowercase tone and share the alias namespace

ADR 0049's groups are named in lowerCamelCase — `math.sqrt(x)`,
`vector.magnitude(v)` — and resolve in the same namespace as package aliases.

Two consequences, both intended. A group name and an import alias can collide,
and that collision is G3 on the same terms as two aliases colliding. And
promoting a local group into a shared package does not change a single call
site, because `math.sqrt` reads identically whether `math` is a group in this
package or an alias for an imported one.

## Rationale

**Why case is checked in dotted-prefix position and nowhere else.** Enforcing
style is not normally a language's business — Go uses case for semantics but
never tells you to write `camelCase`. The exception here is §2.3's three-tone
rule, which *depends* on case: if a capitalized dotted prefix only usually
means a capability or an enum, a reader must verify rather than read, and the
invariant is worth nothing. Checking buys that property.

But it only buys it for prefixes. The first draft of this section checked the
whole table, and that overreached in a way worth recording: a lowercase `model`
name is purely cosmetic, so the rule covering it could not be justified as an
error and had no other severity available — which is how this ADR came to ask
for a warning level it could not have (ADR 0051). Splitting the rule resolved
it in the better direction. `charter.md:56` is the sharper form of the same
point: the project is to be judged on whether it provides *"leverage beyond
naming conventions,"* so spending the checker's credibility on enforcing one is
pointed the wrong way, except where an invariant genuinely rests on it.

**Why a package path is not required to match its directory.** Go, Java, and
Python all require it, and all three require it for the same reason: the
compiler resolves an import path *to a directory*. `go build` maps a path onto
a relative directory under the module root, `javac` maps `com.example.foo` onto
`com/example/foo/`, and Python maps `a.b.c` onto nested packages. Coupling is
load-bearing there — without it there is nothing to look up.

Fiig does not resolve that way. The loader scans for manifests and indexes them
by declared `path`, the way `go.work` discovers a workspace's member modules,
so coupling would buy no resolution ability the loader does not already have.
Three further reasons it would cost something:

- **Paths and directory names use different alphabets.** A fiig path is a
  sequence of *identifiers*; a directory name is a *string*, and directory
  names conventionally contain hyphens that identifiers cannot. Go escapes this
  only because its import paths are strings too (`github.com/foo-bar/baz` is a
  valid path) and it separately declares the package *name* in source. Go
  couples path to directory precisely *because* it decoupled name from path.
  Fiig has no such split — the path's last segment is the name — so coupling
  would force either Java-style hyphen-free directories or a transform rule
  between the two alphabets, and a transform rule is a second mechanism needing
  its own specification.
- **ADR 0037's multi-root search makes "relative to which root?" a real
  question.** A package may be found under any of several roots, so a match
  could only ever be to a relative path.
- **It would forbid namespacing independent of location.** `examples.timeLibrary`
  prepends a segment corresponding to no directory, which is exactly what npm
  invented `@scope/` to permit. An author choosing an identity distinct from a
  checkout layout is a legitimate thing to want.

The honest cost of decoupling is one thing, and §2.2 names it: the package/group
boundary in `use standard.geometry.vector` cannot be settled by looking at the
filesystem, so ADR 0049 §2.5's longest-prefix rule is the only answer. With
coupling you could check whether `standard/geometry/` exists on disk.

The problem coupling *would* have prevented is real but does not need it: an
unenforced convention has already drifted in a three-manifest corpus, with two
manifests reading a hyphen as a path separator and one reading it as a word
break. That is fixed by stating the convention, not by promoting it to a rule.

**Which line a separator should draw, and why fiig's is the wrong one to cut.**
Three lines exist in practice. Go and npm separate **filesystem from language**
(`/` in specifiers, `.` in code). Rust and C++ separate **namespace navigation
from value access** (`::` compile-time, `.` runtime). Java, Python, Elixir, and
OCaml separate nothing and disambiguate by case or convention.

Fiig's paths are not filesystem paths (§2.2), so Go's line does not exist here
to be drawn. But Rust's line *does*: `.` currently serves both
`customer.name` — a runtime field read on a value — and `math.sqrt(x)`, a
compile-time lookup in a namespace. The checker already distinguishes them by
lookup rather than syntax, at `expr.go:172-177`:
`if _, isLocal := env[targetIdent.Name]; !isLocal`.

That is the separator question worth having, and this ADR deliberately does not
answer it, because the case for it is not a reading difficulty — it is that a
syntactic split would make ADR 0043 §3's expression-namespace collision
*impossible* rather than merely checked. An import alias could not compete with
a parameter name if the two never appeared in the same syntactic position. That
is a concrete problem already flagged as blocking in 0043, so the question
belongs there, weighed against a real cost: `Database::queryOne(...)` on every
capability call, in a language that deliberately banks on an LLM's dot-notation
priors.

**Why capability names sit on the type side of §2.1.** It is the one entry that
looks inconsistent — a capability is reached by calling things on it, so it
could plausibly be lowercase like a package alias. The deciding reason is ADR
0043: capabilities are becoming declared contracts with fiig-written providers,
which makes a capability name a type in the ordinary sense, satisfied by
implementations. `Database` denotes a kind of thing. A package alias does not
denote anything; it is a label for a namespace.

**What this does not decide.** It fixes the *shape* of names and the
qualification rules. It says nothing about visibility or export. (Noted
2026-10-27: ADR 0054 has since given fiig one visibility mechanism, a file's
`private { }` block; there is still no package-level export control.) When
drafted, fiig had no mechanism for it at all — "exported" currently appears only in
`checker.go:647`'s B6 message text and one registry comment. That remains an
open design question and is deliberately not bundled here, because case and
visibility are different axes and Go's decision to fuse them is the part of Go
least worth copying.

## Consequences

**No migration of existing source.** §2.2 keeps the separator, so every `use`
line, manifest `path`, and lockfile key in the corpus stays valid. That is the
main practical consequence of rejecting the `/` form, and it is why rejecting
it was cheap: the ADR's value was never in the separator.

**One corpus manifest should be reconciled with §2.2's convention.**
*Done 2026-10-27: renamed to `time.library`, option one below without moving
the directory, since `time-library/` already reads as `time.library` under the
convention. All tests pass.* Original text:
`examples/time-library/fiig.pkg.json` declares `examples.timeLibrary`, reading
its hyphen as a word break where the other two read theirs as a separator.
Either rename the directory to `examples/time/library/` and declare
`examples.time.library`, or keep the path and record it in the file as a
deliberate exception — the `examples.` prefix is doing real namespacing work
and is worth keeping on those terms. What should not persist is two unstated
conventions. The same choice applies to `fiig.lock.json` keys, which quote
these paths.

**Implementation:**

- `compiler/checker` — L1 as narrowed by ADR 0051 (dotted-prefix positions
  only), and §2.6's extension of G3 to formula-group names. This is the whole
  of the new enforcement.
- `compiler/syntax` — removal of the wildcard import production;
  `invalid-programs/wildcard_import_error.fiig:4` moves from a checker fixture
  to a parse-error fixture.
- `docs/specification/grammar.md` — drop the wildcard alternative from
  `import`, and rewrite the prelude-identifier paragraph at :71-73 so its
  "distinguished only by convention (capitalized)" caveat becomes a reference
  to §2.1 instead of an admission.
- Nothing in `compiler/lexer`, `compiler/manifest`, `compiler/loader`,
  `fiig.lock.json`, `compiler/registry`, or `catalog-cli` — all of which the
  first revision's separator change would have touched.

**It unblocks ADR 0049 §2.4.** The native math namespace can be named without
also silently legislating capitalization for the language.

**The three-tone invariant is a new thing to preserve.** Any future ADR that
adds a dotted call form has to pick a tone and justify it. ADR 0043 §3 is the
immediate case: when a capability provider becomes a value that can be passed
as a parameter, it acquires a lowercase binding name while its type keeps a
capitalized one, and §2.3's third tone needs to survive that.

## Proposed bug classes

- **L1 — a name in dotted-prefix position whose case contradicts §2.1.** A
  lowercase capability or enum name, or a capitalized package alias or formula
  group. Decidable from the name alone, and load-bearing for §2.3's third tone.
  **Folded into N1 (2026-10-27).** ADR 0043 implemented N1 and N2, which
  already cover capabilities, interfaces, enum variants, and package aliases.
  The one piece left, a capitalized formula-group name, joins N1 when formulas
  are implemented (ADR 0049), so `L` is never claimed.

Narrowed by ADR 0051 from "any name violating §2.1," which reached pure style.
A capitalized `rule` or lowercase `model` is now a convention violation with no
diagnostic, per §2.1.

The category letter is `L` because `bug-classes.md` runs A–H and ADRs 0047,
0048, and 0049 propose `I`, `J`, and `K` respectively. Four Proposed ADRs now
each hold an unused letter; they should be merged into `bug-classes.md` in one
pass when the first of them is implemented.

§2.5's collision case needs no new class. Two aliases colliding is G3, and an
alias colliding with a local is G4 (ADR 0043 §3a). §2.4's wildcard removal
is a parse error, not a checker diagnostic, which retires part of §3.10.

## Open questions

- ~~**Whether `.` should split into namespace-navigation and value-access**
  (`::` versus `.`).~~ **Closed 2026-10-27 as no.** It was routed to ADR 0043,
  which was accepted without ruling on it, so it is decided here. A `::` would
  let a reader tell a namespace call from a method call by its shape alone. The
  lookup it saves is now one glance at the file's own `use` lines (§2.3's
  fourth row), because every namespace prefix must be imported and G4 keeps
  alias and local names apart. That doesn't justify a second separator,
  changing every cross-package and formula call, and working against the
  dot-notation habits an LLM brings (only Rust and C++ split the two).
- **Acronyms in UpperCamelCase.** `CustomerId` is attested, `CustomerID` is not;
  Go's own style guide says `ID`. This now sits on the *unchecked* side of
  §2.1 — acronym casing never appears in a dotted prefix — so it is a style
  guide entry awaiting a formatter (ADR 0051 §2.4) rather than a second check,
  and L1's implementation no longer waits on it.
- ~~**Whether a parameter may shadow a package alias or formula group.**~~
  **Resolved**: it may not. ADR 0043 §3a made a collision between an import
  alias and a parameter or binding an error (G4, implemented 2026-10-23), and
  ADR 0049 §2.5 (revised 2026-10-27) requires every formula group to be
  imported, so a group name is always an import alias and G4 covers it.
- **Whether same-package declarations should require an import too** (raised
  2026-10-27 during the ADR 0049 review). Today models, enums, rules, and the
  other kinds are visible across the files of their package without a `use`.
  ADR 0049 §2.5 now requires an import for formula groups even within a
  package, for explicitness and because lowercase group names would otherwise
  compete with locals in files that never mention them. The question is whether
  every same-package reference should be explicit too.
  - For: each file's top would list everything it depends on, which is context
    an LLM reading one file can use. Python (`from .models import Customer`)
    and Rust (`use crate::models::Customer`) both require this. Go and Java do
    not.
  - Against: fiig has no import form for a single model or rule. `use` binds a
    package alias or, by path tail, a group or capability group, and fiig has
    kept one import form. The alternative, importing one's own package as an
    alias, would make every same-package call qualified
    (`checkout.validateEmail(...)`). Collisions are also less of a concern:
    capitalized kinds don't collide with lowercase locals, and ADR 0054 already
    reports a declaration colliding with a local. The registry already computes
    each declaration's dependencies.
  - Decide it on its own, not as part of formulas.
- **Whether user-written declarations may be overloaded** (raised 2026-10-27).
  ADR 0019 bans operator overloading. Function overloading (two formulas or
  rules sharing a name, with different parameter types) has never been
  decided. Today it is impossible only because a package has one namespace and
  duplicate names are an error. ADR 0049 §2.4 gives the native `math` members
  same-type `int`/`float` forms, which is a closed built-in set. The demand
  for user overloading mostly comes from wanting one operation over many
  types, and generics serve that need with one signature. Go and Rust both
  have generics and no overloading. Lean: no user overloading, with generics as
  the answer. Decide it alongside generics.

### Closed by ADR 0051 (2026-10-13)

- **Whether L1 should be an error or a warning.** Closed differently than
  asked, by splitting the rule: the dotted-prefix half is an error, the style
  half is not a diagnostic. Fiig gains no severity level.
- **Whether the directory convention should become a lint.** Closed as **no**.
  It fails ADR 0051 §2.2's third test outright — §2.2 of this ADR already ruled
  that a package's path is its identity rather than its location, so a
  disagreement with a directory name breaks nothing. The warning request was an
  attempt to re-litigate that ruling at a lower severity.

## Implementation notes (2026-11-01)

- **§2.4.** `syntax.Import` has no `Wildcard` field. `parseImport` reports
  `.*` as a parse error tagged G2, the way G1 is a tagged parse error, and
  recovers by keeping the path before it, so the rest of the file is still
  checked. Every `imp.Wildcard` branch in the checker, loader, and formatter
  is gone. The tag keeps G2 in the bug-class catalog and keeps
  `wildcard_import_error.fiig` attesting it through `CheckDir`, which already
  surfaces tagged parse errors.
- **The formatter refuses G2, unlike G1.** For G1 the tree is complete. For
  G2 the recovered tree has dropped the `.*`, so printing it would silently
  rewrite the import into a legal one. The format corpus tests skip that one
  fixture and say why.
- The error message names the fix: "there is no wildcard import; write
  `use standard.time` and qualify each name, as in `time.Name`".
