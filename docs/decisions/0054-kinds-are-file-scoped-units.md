# ADR 0054: A Kind Is a File-Scoped Unit With One Public Entry

Status: **Accepted** (2026-10-17), **implemented** 2026-10-17 (see
Implementation notes). Revised 2026-10-17: recursion is
allowed, bounded by a runtime call depth limit (§6); escape is transitive
(§3); the 1,000-frame default is confirmed; open questions now cover how
public and private members are marked, name collisions, and formula groups.
Revised again 2026-10-17: private members live in one `private { }` block
(§1), private names may not collide with package-level declarations (§3a), and
formula groups are kept and may have a `private` block (§1, §7).
Revised 2026-10-19: a call to a file's own public entry is judged by the
composition table like a call to another declaration of its kind, so a rule
never calls itself and recursion in a rule lives in its helpers (§4, §6).
Date: 2026-10-16
Amends: ADR 0029 (rule isolation), ADR 0030 and ADR 0044 (activity effects),
`fiig-lang.md` §3.2 (composition rules) and §3.9 (files).
Related: ADR 0049 (formula groups already put several members in one file),
ADR 0046 (closed dispatch inside an owned set), ADR 0053 (`for` loops).
Planning item: `fiig-lang.md` §8 item 14.

## Context

Each top-level kind (`rule`, `conversion`, `activity`, `gateway`, `workflow`)
currently holds exactly one thing: its own signature and body. Helper logic
therefore has nowhere to live except as another top-level declaration. That
declaration is then visible to the whole application, appears in the registry,
and can be called by anything the composition rules allow. A rule with a long
decision path must either be one long body or be split into several public
rules.

Java solves the same problem with a class: one public surface, and private
members that the rest of the program cannot see. Fiig's §3.9 already gives each
kind its own file. This ADR uses the file as the boundary, so that a kind
behaves like a class whose private members sit in a `private` block in the same
file.

## Decision

### 1. A kind's file is its class

A file holds exactly one **public** declaration, the kind itself. That
declaration is its entry function, and it is the only thing exported,
registered, and callable from outside the file. After it, the file may hold
one **`private` block**. Everything inside that block is a private member,
visible only inside the file. §3.9's "one primary semantic declaration per
file" still holds, because private members are not primary declarations.

```
rule fooBar(number: int, number2: int) -> int {
    someResult: bool = interiorFunctionCall(number)
    if someResult {
        return 3
    }
    return 4
}

private {
    interiorFunctionCall(number: int) -> bool {
        return number > 0
    }

    model Threshold {
        minimum: int
    }
}
```

`fooBar` is the one public entry. `interiorFunctionCall` and `Threshold` are
private.

The rules are structural, so the grammar enforces them and the checker doesn't
have to:

- A file's top level is, in order: its `use` lines, the public declaration,
  and at most one `private` block. Nothing else may appear at the top level.
  The `use` lines are file-scoped, so private members can use the same
  imports as the public entry.
- A second public declaration is a **parse error**. The diagnostic points at
  it and offers the fix: move it into the `private` block. Reordering a file
  therefore can never change which member is public.
- Inside the block, helper functions have no kind keyword. Models keep
  their `model` keyword. Nothing inside the block needs a
  per-member `private` modifier, because the block already says it.
- A `private` block may follow only a declaration with code in it: `rule`,
  `conversion`, `activity`, `gateway`, `workflow`, or a `formulas` group (ADR
  0049). A file whose public declaration is a `model`, `enum`, or
  `capability` may not have one, because those hold no code that a helper
  could serve.
- In a `formulas` group, the group is the public declaration, and every
  `formula` inside it stays public as `group.member`. The `private` block
  holds helpers that every member of the group can call but no other file
  can. By §5, those helpers carry the formula restrictions: no capabilities,
  calls only to formulas, no `Result` (the last lifted by ADR 0151).

**Why a labeled block.** Three other shapes were considered:

- **First declaration is implicitly public.** Rejected. Reordering a file would
  silently change its API, and a member's visibility couldn't be read without
  scrolling to the top.
- **A `private` modifier on each member, checked.** Each member labels itself,
  but a second public declaration is only caught by a checker error, not the
  grammar.
- **An unlabeled second block** (`rule fooBar { } { helpers }`). Structural, but
  a bare `{` after a body reads as a second body or a trailing closure, which
  is what that shape means in Swift, Kotlin, and Ruby. It also can't be
  searched for or named in a diagnostic.

The labeled block keeps the structural guarantee of the unlabeled one and adds
the word that explains it. Precedent: C++ `private:` sections, Ruby's
`private` line, Kotlin's `companion object { }`. The cost is that helpers sit
one indent deeper, and a reader deep inside a long block has to remember
they're in it.

### 2. What may be a private member

- **Helper functions**, with the same signature rules as a public function.
- **Models** used only inside the file. They are file-local types.
- **`for` loops** (ADR 0053) are already statements and need no change.

Not in this ADR: lambdas, which are planned separately because they need
function types and a capture rule, and enums, which interact with ADR 0046's
dispatch and are a follow-up.

### 3. Escape is leaving the file

A private member **escapes** if it is referenced from outside its file. Three
rules follow:

- A private member may not appear in the public declaration's signature,
  whether as a parameter or as the return type. A caller therefore never
  receives a value of a private type.
- A private member is never placed in the registry, the catalog, or any
  exported surface. The registry card describes the public entry only.
- No other file may reference a private member, by name or by import.

Escape is **transitive**. A private type may not reach the public signature
directly, or indirectly as the type of a field in a type that does. A private
model returned from a private helper to the public entry is fine. The public
entry's signature still may not mention that model, or any type that holds it.

### 3a. Private names may not collide with package-level declarations

Private members never need qualifying. Nothing outside the file can reach
them, so the file itself plays the role of the class name, and a bare call
keeps ADR 0050 §2.3's first tone: declared in this package (more narrowly, in
this file). Everything visible from outside is already qualified under ADR
0050 (`alias.name` for other packages and formula groups,
`Capability.method` for capabilities).

That leaves one collision. If a file has a private `foo` and the same package
has a top-level `foo`, a bare `foo(...)` in that file is ambiguous. This is an
**error**, reported at both sites with downstream diagnostics suppressed, as
with duplicate declarations today. The file-local name does not win.

- Two private members with the same name in **different files** never
  collide. Neither can see the other.
- Adding a top-level `foo` can break a file whose private helper is also named
  `foo`. That's action at a distance, but it's caught at compile time and the
  fix is a rename.
- Qualifying private calls (`fooBar.helper(...)`) was rejected. It would put a
  lowercase prefix on a call within the same file, and in ADR 0050 that prefix
  means "another package or a formula group."

A private name that matches an **import alias** or a formula-group name is not
settled here. See open question 1.

### 4. Calls follow the kind's rule, applied to its whole file

Private members are part of the kind and inherit its composition rules. A call
from a private member is judged by the same table as a call from the public
entry.

- **Within the file**, any member may call any private member of the same
  file, itself included. These calls are allowed for every kind.
- **A call to the file's own public entry**, from the entry itself or from one
  of its helpers, is judged by the table below as a call to another
  declaration of that kind. See §6.
- **Across files**, the existing table in §3.2 decides. A `rule` calls no
  declaration outside its file. A `conversion` may call rules and conversions.
  An `activity` may call rules, conversions, and gateways, never a capability.
  These are the same rules as today, now applied to the whole file.

So ADR 0029's sentence "rules call nothing" becomes "rules call nothing
**but their own private helpers**." A rule never calls itself, directly or
through a helper.

### 5. Effects inherit the file's kind

A private helper has the effect ceiling of its file's kind. A helper in a
`rule` is pure, a helper in an `activity` cannot touch a capability, and a
helper in a `gateway` may do what a gateway may do. The effect checker's
existing per-kind table stays the single source of truth, so helpers need no
parallel rule.

### 6. Recursion and termination

Recursion is allowed among private helpers, for every kind. A public entry
may call itself, directly or through a helper, exactly when its kind may call
its own kind (§4's table):

| Kind | May call itself? |
|---|---|
| rule | no (B7): rules call nothing but their helpers |
| conversion | yes |
| activity | yes |
| gateway | no (B3): gateways don't coordinate gateways |
| workflow | no (B8): nothing calls a workflow |

So a rule that needs recursion puts it in a helper, and an activity that walks
a tree may call itself directly and call a rule at each node. Recursive tree
walks and recursive structural conversions over nested models are both
legitimate uses, and ADR 0029 already relies on the latter.

A cycle between activities in different files stays B9 (ADR 0044). Only
recursion that stays inside one file is allowed.

Termination is not checked statically. Instead, the runtime enforces a **call
depth limit**:

- Each call increments a depth counter for its call chain, and returning
  decrements it.
- When the depth would exceed the limit, the call produces an ordinary `Fault`
  (the runtime's existing fault type, §3.6) instead of running. The fault names
  the limit and carries the call chain, the sequence of declarations from the
  entry point to the call that overflowed, so the failure is reportable.
- A depth fault is an ordinary fault. It follows the same propagation as any
  other fault and is never a process crash. Go's stack-overflow crash is the
  behavior this rules out.
- The limit is a runtime setting, not part of the language. Its default is
  1,000 frames. It may be configured per
  application in the same declarative style as ADR 0027's capability
  configuration, and the checker does not read it.

The depth limit is a backstop, not a proof. A recursion that terminates after
many frames passes if the limit is high enough, and a non-terminating one
faults at the limit. That is the trade the language accepts for allowing
recursion.

Option 2 from the review (structural termination checking) is not part of this
ADR. It may be added later as a checker for conversions over recursive models,
where the argument is always a sub-part of the input. Those cases would then
never need the runtime limit.

### 7. Formula groups stay

ADR 0049 introduced `formulas` partly because rules could call nothing, so
splitting a rule into named steps was impossible. This ADR now covers that
case: a rule can keep its own private helpers. What private helpers can't do
is be **shared**. A `magnitude` used by fifty geometry rules would be copied
into fifty files. Formula groups remain the home for pure building blocks that
many files reuse. They are more basic than rules, not a workaround for them.
ADR 0049's Context was rewritten around sharing rather than isolation
(2026-10-17).

## Rationale

Keeping helpers inside the file gives a reader, human or LLM, the context it
needs without widening the graph it must navigate. ADR 0046 identified that
constraint: understanding one decision should not require traversing the whole
application. The registry shows one entry per kind, and helpers stay with the
code that uses them.

The file boundary is also one the compiler can check cheaply. Escape becomes a
per-file reference check, not a whole-program analysis.

Rules are the case most affected, and the change is deliberate. ADR 0029
restored "rules call nothing" because cross-rule calls were really
coordination, which belongs to an `Activity`. Helpers inside one rule are not
coordination. They split one decision into named steps, and no other rule can
see them.

## Consequences

- **Grammar.** A file is one public declaration, optionally followed by one
  `private { ... }` block. `private` becomes a reserved word. Helper functions
  inside the block have no kind keyword; models keep theirs.
- **Checker.** Name resolution gains a file scope for private members. The
  composition check (B6, which today rejects a rule calling another rule) now
  rejects a call to a declaration outside the file, and allows a call within
  it. A new escape check, proposed as bug class H4, rejects a private member in
  a public signature, transitively, or referenced from another file. The
  collision rule in §3a reuses the existing duplicate-declaration reporting.
- **Registry.** Private members are excluded from registry cards, the catalog,
  and search. The public entry's card does not list them.
- **Formatter.** The `private` block prints after the public declaration,
  members in source order, indented one level.
- **Corpus.** Every existing file has one declaration and stays valid, so no
  corpus change is needed for this ADR to be accepted.

## Open questions

1. **Private names vs. import aliases and formula-group names.** A private
   `geometry` in a file that also imports `geometry` is a different question
   from §3a, because the alias is not a declaration. It's the
   parameter-shadowing question ADRs 0043, 0049, and 0050 all defer, and it
   should get one answer there rather than a fourth one here.

## Implementation notes

Implemented 2026-10-17. Calls made beyond the text above:

- **G1 keeps its ID.** A second public declaration is a parse error, as §1
  says, but the parser tags it G1 (`parser.Error` gained a `Rule` field) and
  still parses the declaration. The `invalid-programs/` fixtures pack many
  declarations into one file, so without recovery they would stop being
  checked at all. G1 now reports once per extra declaration, not once per
  file. The formatter still formats a file whose only errors are G1, and the
  corpus tests allow G1 in `invalid-programs/` and nowhere else.
- **B9 now means a cycle that leaves the file.** §6 allows recursion for any
  member, so an activity calling itself, directly or through its own
  helpers, is no longer B9. A cycle through another file's activity still
  is: §6 is about members of one file, and nothing here retracts ADR 0044's
  rule for cycles between activities. The walk treats each file as one unit,
  helpers' calls included. (As first implemented, any call to the file's
  own public entry skipped the table, so `rule fact(n) { ... fact(n - 1) }`
  was legal. Revised 2026-10-19 per §6: the table now judges it, and it is
  B7.)
- **Calls to private members skip the composition matrix only.** Other checks on
  the callee still apply: calling the file's own activity inside a
  `transaction` is still C5.
- **Scoping, not a reference check, enforces "no other file may reference
  a private member."** Each file with a private block gets a scope
  (`types.Universe` with a `Parent`) that the checker and the runtime both
  resolve in. A reference from another file would just be undefined; the
  checker reports it as H4 instead, naming the owning file, using a
  package-wide index of private names.
- **The transitive escape rule is the signature walk.** A public model can't
  see a private type, so only the file's own types can hold one, and the H4
  check walks the whole signature type (generic arguments, union members,
  record fields). There is no separate walk through models.
- **§3a collisions** reuse the duplicate-declaration diagnostic at both
  sites. The name is ambiguous only inside the private member's file;
  every other file keeps using the package-level declaration. Two private
  members with the same name in one file are a duplicate the same way.
- **Registry.** The public entry's card lists what its helpers call, minus
  the private names, so ADR 0044's derived purity still sees a gateway
  reached only through a helper.
- **Depth limit.** `runtime.Interp.MaxCallDepth`, default
  `DefaultMaxCallDepth` (1,000). A call that would exceed it faults
  before running; the fault's `Stack` is the chain from the entry point
  to the call that overflowed. Not yet configurable from a file: ADR
  0027's declarative config covers capabilities only, and there is no
  application-level runtime settings file to add it to.
- **Not implemented:** private blocks on `formulas` groups, because ADR
  0049's formula tier itself isn't built yet. The parser accepts a private
  block only after a rule, conversion, activity, gateway, or workflow.
  *Built 2026-11-25 with ADR 0151 §6: the parser accepts one after a
  `formulas` group, members resolve in the file's scope, and a helper is
  checked as a formula.*
- **Found while dogfooding:** the parser built `BinaryExpr` and `TryExpr`
  nodes with no span, so a trailing comment on `return n > 0` was moved to
  the end of the file by the formatter, and any diagnostic on one landed at
  0:0. Fixed.
- **Corpus:** `order-processing/rules/shipping_cost.fiig` (a helper
  returning a private model), run by `TestCorpusShippingCostUsesPrivateHelpers`.
