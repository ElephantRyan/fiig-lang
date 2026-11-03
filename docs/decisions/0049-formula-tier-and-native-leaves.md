# ADR 0049: A `formula` Tier for Pure Computation, Grouped Into Namespaces

Status: Accepted (2026-10-27, after a one-decision-at-a-time review). §2.6,
§2.8, and K1 implemented 2026-11-02, then the `formulas` declaration (§2.1-
§2.3, §2.5, K3-K5, B11) the same day, and the prelude `math` group (§2.4,
§2.7) after ADR 0048's float, also 2026-11-02, and K2 on 2026-11-03 (see
Implementation notes). Fully implemented. ADR 0050's prerequisite parts are
done.
Date: 2026-10-12 (revised 2026-10-13: §2.4-§2.5 restructured around
`formulas` groups; naming and qualification split out into ADR 0050; §2.4
gained `closeTo`/`isZero` from ADR 0051; revised 2026-10-17: Context
rewritten around sharing, since ADR 0054's private helpers now cover splitting
a single rule into steps)

## Context

ADR 0048 admitted `float` and left one blocking open question: there is no home
for `sqrt`, `sin`, `cos`, `atan2`, or for §2.7's `floorToInt`-family crossing
rules. Without them `float` is a type you can add and compare but neither do
geometry with nor convert back to `int`, so it does not deliver the use case
that motivated admitting it ("geometry would fit inside of rules").

The same question is about to be asked twice more. `bytes` is a committed
Milestone 7 deliverable (`fiig-lang.md:466`) and ADR 0042's native floor
depends on it — parsing a length-prefixed Postgres or Kafka frame in fiig
needs indexing, slicing, and a length. Sequential iteration over `List<T>`
(since settled by ADR 0053) is the other M7 item.

**The real problem is sharing pure building blocks across files.** Geometry
is built from small named pieces (`dot`, `cross`, `magnitude`, `normalize`,
`lerp`), and dozens of rules in an application use the same ones. Those pieces
need a home that every rule can call.

No existing kind can be that home, because of rule isolation. `allowedCallees`
is `kindRule: {}` (`checker.go:543`): a rule may call **no declaration outside
its own file**, and B7 fires if it tries. ADR 0029 established this by
*reverting* ADR 0028's "rules may call rules," because a rule calling a rule is
chained judgment, which belongs in an `Activity`. So a math library made of
`rule` declarations would be uncallable from the one place it is needed. The
same is true of `conversion`: a conversion may call rules, but a rule may not
call a conversion.

ADR 0054 (2026-10-17) removes half of what originally motivated this ADR. A
rule may now keep **private helpers** in a `private { }` block in its own file,
so splitting one rule into named steps no longer needs anything new:

```fiig
rule validateMovement(v: Vector3) -> Result<None, MotionError> {
    if magnitude(v) > maxSpeed { ... }
    return Ok(None)
}

private {
    magnitude(v: Vector3) -> float {
        return math.sqrt(v.x * v.x + v.y * v.y + v.z * v.z)
    }
}
```

What private helpers can't do is be shared. Each one is visible only inside
its file, so a `magnitude` that fifty rules need is copied into fifty files,
and a fix to one copy leaves the other forty-nine unchanged. That is the gap
this ADR fills: a kind of declaration that is pure, carries no business
judgment, and can be called from any file, including from rules. Formulas are
a more basic building block than rules, not a workaround for rule isolation.

The first draft of this ADR put built-in operations behind a reserved prelude
identifier instead (`Math.sqrt(x)`), on the theory that anything callable from
a rule must not be a declaration at all. That was too narrow. There are two
ways to be callable from a rule: not being a declaration, *or* being a
declaration kind the composition matrix permits. A closed table of native
leaves supports one level of composition and then stops. Matrix
multiplication, quaternion composition, and transform chains all need
user-written shared pieces built on top of the natives, and a prelude table
can't hold those.

## Decision

### 2.1 `formula` is a new declaration kind: pure computation, callable from everywhere, calling only itself

```ebnf
formulas-decl ::= 'formulas' identifier '{' { formula-member } '}'
formula-member ::= 'formula' identifier '(' [ params ] ')' '->' type [ block ]
```

A `formula` is a named, pure, deterministic computation that may be called
from any other declaration kind — including from a `rule` — and may call only
other formulas.

**A formula is never declared alone.** The declaration is the *group*, which
§3.9 counts as the one declaration in its file; the members are addressable
individually as `group.member`. Group names are lowerCamelCase per ADR 0050
§2.6.

```fiig
formulas vector {
    formula magnitude(v: Vector3) -> float {
        return math.sqrt(v.x * v.x + v.y * v.y + v.z * v.z)
    }

    formula normalize(v: Vector3) -> Vector3 {
        return Vector3(
            x: v.x / magnitude(v),
            y: v.y / magnitude(v),
            z: v.z / magnitude(v),
        )
    }
}

rule validateMovement(v: Vector3) -> Result<None, MotionError> {
    if vector.magnitude(v) > maxSpeed {
        return Err(MotionError.TooFast(speed: vector.magnitude(v)))
    }
    return Ok(None)
}
```

(Two files, shown together: §3.9 puts the group and the rule in separate ones.)

Within a group, members reference each other **unqualified** — `normalize`
calls `magnitude`, not `vector.magnitude`. The group is the only scope in fiig
where a bare formula name resolves, which is what makes a qualified call site
a reliable signal that the callee is elsewhere (ADR 0050 §2.3).

A member whose `block` is omitted is a **native leaf** (§2.4). The set of
bodiless formulas is closed and compiler-known, exactly as ADR 0042's native
capability set is; a user-written file declaring one is K5.

One group per name per package — no partial groups across files, which would
cost §3.9 its exactness for nothing.

**This narrows ADR 0029.** `kindRule` is no longer `{}` — it becomes
`{kindFormula: true}`, and B7's meaning narrows from "a rule may call nothing"
to "a rule may call nothing that carries judgment or effects." ADR 0029 is
amended, not superseded: rules still may not call rules, conversions,
activities, gateways, or workflows.

### 2.2 Three enforceable restrictions define the tier

"Contains no business judgment" is the actual intent and is not statically
checkable — the same reason C3 was dropped outright rather than deferred. The
enforceable surrogate is three restrictions, all decidable:

1. **No capabilities.** A `formula` has no `requires` clause. It therefore has
   no effects, and the Milestone 3 registry effect filters stay meaningful
   instead of filling with arithmetic.
2. **Calls only formulas.** Never a rule, conversion, activity, gateway, or
   workflow. A formula is the bottom of the composition lattice, so it cannot
   become a laundering channel for reaching something a rule is forbidden to
   reach.
3. **May not return `Result`.** A rule can fail *in domain terms*; a formula
   can only fault. This is the sharpest of the three and the one the category
   is really built on — see the Rationale.

**Format checks are formulas; policy checks are rules** (added 2026-10-27).
Restriction 3 bans `Result` but not `bool` or `T | None`, so a judgment can
still be written as a formula. No static check can tell arithmetic from
policy, the reason C3 was dropped, so the line is a convention, made visible
by the qualified call and the registry card:

- A **format check** has no business variation: two companies would not
  disagree about it. "Is this shaped like an email", "is this a valid ISO
  date". It may be a formula returning `bool`, and rules may call it.
- A **policy check** is judgment: "must be 18 or older", "under the tier's
  credit cap", "not on the blocklist". It is a `rule`, returns `Result` with a
  reason, and composes with other checks through an `activity` (ADRs 0030,
  0044), as `validateNewCustomer` composes `validateEmail` today.

The two combine: a rule calls the formula for the shared logic and supplies
the business reason itself.

```fiig
rule validateEmail(email: string) -> Result<string, CustomerError> {
    if not text.isEmailShaped(email) {
        return Err(CustomerError.Invalid("email", "must be a valid address"))
    }
    return Ok(email)
}
```

Restriction 3 is what keeps the second category out of the tier. If a formula
could return `Result<string, CustomerError>`, every validator would become a
formula and rules would call rules again by another name, which is what ADR
0029 reverted.

### 2.3 The composition matrix

```go
kindFormula:    {kindFormula: true},
kindRule:       {kindFormula: true},
kindConversion: {kindRule: true, kindConversion: true, kindFormula: true},
kindActivity:   {kindRule: true, kindConversion: true, kindFormula: true},
kindGateway:    {kindRule: true, kindConversion: true, kindFormula: true},
kindWorkflow:   {kindRule: true, kindConversion: true, kindActivity: true,
                 kindGateway: true, kindWorkflow: true, kindFormula: true},
```

Every kind may call a formula; a formula may call only a formula.

### 2.4 Native leaves are a prelude `formulas math` group with bodiless members

A formula written in fiig cannot be the home for operations fiig cannot
express. Rather than a second mechanism, those are members of a prelude group
whose bodies the compiler supplies:

```fiig
formulas math {
    formula sqrt(x: float) -> float      // native
    formula atan2(y: float, x: float) -> float
    ...
}
```

Native versus fiig-written becomes an implementation detail of the body,
invisible at the call site — the same arrangement ADR 0042 uses for
capabilities, where the contract is declared and the implementation is native.
The full member list:

| Member | Signature |
|---|---|
| `floorToInt` `ceilToInt` `truncToInt` `roundToInt` `roundHalfEvenToInt` | `float -> int` |
| `toFloat` | `int -> float` |
| `floor` `ceil` `round` `trunc` | `float -> float` |
| `abs` | `int -> int`, `float -> float` |
| `sqrt` `exp` `log` `log10` | `float -> float` |
| `sin` `cos` `tan` `asin` `acos` `atan` | `float -> float` |
| `min` `max` `floorMod` | `(int, int) -> int`, `(float, float) -> float` |
| `atan2` `pow` | `(float, float) -> float` |
| `addOrNone` `subOrNone` `mulOrNone` | `(int, int) -> int \| None`, `(float, float) -> float \| None` (ADR 0063) |
| `divOrNone` | `(float, float) -> float \| None` (ADR 0063) |
| `closeTo` | `(a: float, b: float, relative: float, absolute: float) -> bool` |
| `isZero` | `float -> bool` |
| `pi` `tau` `e` `minInt` `maxInt` `maxFloat` | untyped constants (§2.7; the last three from ADR 0063) |

`closeTo(a, b, tolerance)` and `isZero(x)` were added 2026-10-13 by ADR 0051,
which made `==`/`!=` over two `float` operands a type error and needs somewhere
for the legitimate uses to go. They are the only members that return `bool`
rather than a number, and the only two that are **fiig-expressible** rather
than native — they are here because this is where a reader looks for them, not
because they need compiler support. `closeTo` uses Python's `math.isclose`
rule, decided in ADR 0051 (2026-10-27):
`|a − b| ≤ max(relative × max(|a|, |b|), absolute)`. Both tolerances are
required and named. `isZero` is an exact test.

The naming rule is mechanical and the only one a reader must learn: **a
`ToInt` suffix crosses the type boundary; no suffix keeps the argument's
type.** (`toFloat` is the one crossing the other way.)
`math.floor(x)` is the integral float; `math.floorToInt(x)` is the `int`. The
crossing members are ADR 0048 §2.7's set verbatim, and their out-of-range
behaviour is 0048's: a fault, not a `Result`.

Trigonometric members take and return **radians**.

**A native `math` member may take `int` or `float`, never both at once**
(revised 2026-10-27). `abs`, `min`, `max`, and `floorMod` (ADR 0048 §2.8) each
have an `int` form and a `float` form. In each form the arguments and the
result share one type, so nothing mixes: `math.max(count, offset)` with an
`int` and a `float` matches no form and is a type error. An untyped constant
argument with no typed partner takes its default type, so `math.abs(-3)` is the
`int` form. Decimal will add a third form to the same names. Java's `Math.abs`
and `Math.max` have this shape. Overloading is limited to the native members of
`math`: a user-written formula has exactly one signature, since fiig has no
user generics. `math.abs` of the most negative `int` overflows and faults
(ADR 0063).

### 2.5 Composition above the leaves ships as an ordinary package

Vector, matrix, and quaternion helpers are fiig-written formula groups in a
real package with a `fiig.pkg.json`, resolved by MVS like any other. It
inherits registry cards, semver, lockfile pinning, and `catalog-cli`
discoverability from the Milestone 6 machinery with no new mechanism, and a
user may write their own groups alongside — the tier is not reserved to the
standard library.

**A group is addressable as the tail of a package path, and qualification stays
one level deep.** A group is imported by name, not inherited from its package:

```fiig
use standard.geometry.vector

rule clampToUnit(v: Vector3) -> Vector3 { ... vector.normalize(v) ... }
```

`vector.normalize`, never `geometry.vector.normalize` and never a bare
`normalize`. This is Go's `math/rand` → `rand.Int()` arrangement, and it is
what keeps ADR 0050 §2.4 intact: the alias is bound explicitly by the `use`
line, so nothing enters the namespace unnamed. An un-aliased group arriving
with its package would have been a wildcard import in all but spelling, which
§2.4 removes.

**The package/group boundary is resolved by longest declared prefix.** In
`use standard.geometry.vector` nothing syntactically marks where the package
ends; the loader takes the longest prefix matching a declared manifest `path`
and treats the remainder as a group name. This is unambiguous given the
workspace's manifests but invisible to a reader, which ADR 0050 §2.2 accepts
rather than fixes — the boundary is not information a call site needs, since
`vector.normalize(v)` reads the same wherever `vector` came from. If two
manifests ever declare paths where one is a prefix of the other *and* the
longer one's extra segment names a group in the shorter one, that is a
workspace conflict the loader must report rather than resolve.

**Every group is imported, including one in the caller's own package**
(revised 2026-10-27). The first draft let a same-package group resolve without
a `use`, as Go does. That doesn't scale: a lowercase group name competes with
parameter and binding names, so adding `formulas total` in one file would break
`total = ...` in every other file of the package, including files that never
mentioned it. Requiring the import makes a group's visibility a property of the
file, stated at its top. A collision can then only come from a `use` line in
the same file, and it is G4 (ADR 0043 §3a), fixed with `as`. There is no
fully-qualified call form as a second fix; ADR 0050 §2.5 rejects one.

The prelude `math` group of §2.4 is the one group visible without a `use`, and
the prelude's group set is frozen at `math`. A new prelude group would reserve
its name as a local name in every file of every package, which is the scaling
problem above in its worst form.

So a group is a one-file, many-declaration unit addressable as a path tail. It
is not a package: a package is a directory with a manifest, and §3.9 still
holds inside it, one declaration — one group — per file.

### 2.6 Methods are a closed, typed table keyed on the receiver's type

A method call is legal only when the receiver's type is a primitive or a
prelude generic *and* the method is in that type's table. The table is closed:
no `model` or `value` may declare a method, which keeps this from becoming the
extension axis ADRs 0043 and 0045 govern.

- **`string`** — `isBlank()` and `contains(s)` are ratified as-is and become
  checker-modeled (§2.8). Further string methods are additive.
- **`bytes`** (M7) — ADR 0042's floor requires at minimum a length, indexed
  access, and slicing. Indexed access yields an **`int` in 0–255**, not a
  distinct byte type, following Python (`b"abc"[0]` is `97`). The full
  inventory belongs to `bytes`'s own ADR.
- **`List<T>`** — four methods, decided 2026-10-27 on `bytes`'s terms
  (ADR 0058):

  ```
  xs.length()        // int
  xs.at(i)           // T; faults if out of range
  xs.atOrNone(i)     // T | None
  xs.slice(a, b)     // List<T>, sharing storage; faults if out of range
  ```

  `atOrNone` has no `bytes` counterpart. A binary pattern is how code reads
  `bytes` of unknown length, and patterns fail softly. Lists have no pattern
  form, and "the first item, if there is one" is common in business code. The
  name follows ADR 0063's `OrNone` convention (Kotlin's `get`/`getOrNull`).
  `first`, `last`, `contains`, sorting, and `Map` belong to the collections
  ADR. Anything beyond that, libraries can add once fiig has generics.

### 2.7 `math`'s constants are untyped constants

`math.pi` is not a `float`. It is an untyped constant per ADR 0048 §2.4 —
exact at arbitrary precision at compile time, rounded once when it lands in a
type. One `pi` therefore serves `float` today and decimal when it arrives, and
`math.pi * 2` needs no conversion.

*Amended 2026-10-28 by ADR 0064 §7:* `pi`, `tau`, and `e` do **not** serve
decimal. An untyped constant becomes `decimal` only if its exact value fits in
34 digits, and an irrational one never does, so using one in a decimal context
is a compile error. They remain untyped constants, so `math.pi * 2` still needs
no conversion in a float context.

A group may therefore hold constant members as well as formulas, which the
§2.1 production does not yet cover; the member form for a constant is left to
the implementation of ADR 0048 §2.4, since it depends on how untyped constants
are represented.

### 2.8 One table, read by both the checker and the interpreter

Every member of §2.4 and §2.6 has its signature declared once, in a table the
checker consults for typing and the interpreter consults for dispatch.

This is a correction, not a convention. `compiler/runtime/callexpr.go:222-228`
records the current state in its own comment: *"Neither is modeled by the
checker at all … this is the one place their actual behavior is defined."* So
`42.isBlank()` type-checks today and faults at runtime. Adding twenty numeric
operations on the same footing would multiply an existing hole.

## Rationale

**Why a tier rather than a prelude-only answer.** Ask what rule isolation is
*for*. A rule is where business judgment lives, and if rule A calls rule B,
understanding A requires reading B — which breaks the registry's promise that
a declaration's behaviour is knowable without reading its source. Isolation
protects against **chained judgment**.

Arithmetic carries no judgment. `magnitude` encodes no policy and there is no
version of it a reviewer could disagree with. So isolation was never aimed at
it, and the first draft's framing — "don't be a declaration, because the
matrix is keyed on `declKind`" — described the enforcement mechanism without
naming what it was enforcing. Naming it shows the exemption is principled
rather than a carve-out: the tier is admitted precisely because the three
restrictions in §2.2 fence it out of the thing isolation exists to prevent.

**Why `Result` is the load-bearing restriction.** Of the three, the capability
ban and the call restriction are obvious. The `Result` ban is what actually
separates a formula from a rule, because business judgment almost always needs
to signal a typed failure: a validation that can't say *why* it failed is
rarely a business rule. It also keeps the tier usable in the position it
exists for — ADR 0048 §2.7 already rejected `Result` for out-of-range
crossings because F1 would turn every nested geometric expression into an
unwrap, and the same reasoning applies to every formula that appears inside
one.

The honest limit: a judgment-laden predicate returning `bool` can still be
written as a formula. The fence is a good proxy, not a proof, and this is
accepted rather than solved.

**Precedent: `const fn`, not `function`.** Rust's `const fn` can do strictly
less than a normal `fn` — and in exchange is usable where a normal `fn` is
not, in array sizes and const initializers. "Does less, therefore permitted in
more places" is exactly this trade. C++ `constexpr` is the same move, and
Postgres's `IMMUTABLE` volatility class is the database version: an immutable
function may be used in an index expression where a volatile one may not.

The name `function` was rejected because it implies first-class functions,
which fiig does not have. `formula` carries "fixed computation, not policy"
and matches the house style of full words (`conversion`, `activity`,
`gateway`); `calc` reads as an abbreviation.

**Why natives still need a separate home, stated precisely.** ADR 0042's
admission criterion is inexpressibility, and it is worth being exact rather
than overclaiming: once M7 lands `List<T>` iteration, `sqrt` *would* be
expressible in fiig via Newton's method. The reason it should still be native
is different and narrower — IEEE 754 specifies `sqrt` as **correctly rounded**,
and a hand-written iteration in fiig would not reproduce that, besides being
far slower than the single hardware instruction. So the criterion for native
membership is "not correctly implementable in fiig," which is a weaker claim
than inexpressibility and the one actually being relied on.

**Why grouped and qualified rather than bare `sqrt(x)`.** The members of §2.4
are roughly twenty **lowercase** identifiers, and fiig's `Universe` is flat
per-package, so each one would be reserved against every package author —
including `round`, `min`, `max`, and `abs`, all plausible business names.
`copy` was one deliberate reservation; twenty is different in kind. A group
costs exactly one name, needs no new call syntax (`inferQualifiedCall` already
resolves a FieldAccess on a bare `Ident`, the same shape ADR 0046 gave
`EnumName.Variant`), and leaves `rule round(...)` free for a business rounding
policy.

**Why the group is lowercase, and why that changed.** The first revision of
this ADR used a capitalized `Math`, on the precedent of `java.lang.Math` and
JavaScript's `Math` and of fiig's own capitalized prelude identifiers
(`Result`, `Ok`, `List`, `None`). Reading the corpus for ADR 0050 showed the
cost: a capitalized prefix at a *dotted call site* already means two things —
a capability (`Database.queryOne`, effects, declared in `requires`) and an
enum variant constructor (`CheckoutError.NotFound(id)`, ADR 0046). Two
meanings on one tone is tolerable because `requires` disambiguates them;
three is not. Lowercase puts formula groups on the package-alias tone instead,
where they belong anyway, since ADR 0050 §2.6 makes a local group and an
imported one indistinguishable at the call site.

**Why mandatory grouping rather than optional.** Permitting both a bare
top-level `formula` and a grouped one would give two spellings for one thing,
which fiig consistently declines (one declaration per file, one import form,
no transparent aliases per ADR 0022). Mandatory grouping also makes the
namespace win total: no formula is ever reachable by a bare name outside its
own group, so a bare call site always means "declared in this package, and not
a formula."

**Why groups do not generalize to the other kinds.** The obvious follow-up is
whether rules should group too, and they should not. Grouping pays when
declarations are small, numerous, and mutually composable — all three are true
of formulas and none is reliably true of rules. A rule is one business
operation, the registry's unit of discovery *is* the business operation, and
rule isolation means two rules in a group still could not call each other, so
a group would buy nothing but the class-shaped mental model the composition
rings replaced.

**Why math is a prefix call and collections are methods.** It tracks whether
the operation has a subject, which is the line every language draws.
Mathematical notation is prefix (`sin θ`) and every mainstream math library
follows it: C, Python, Java, Go, JS. Rust's `x.sqrt()` is the outlier.
`atan2(y, x)` and `pow(base, exp)` are the test case — two asymmetric operands
and no receiver, so Rust's `y.atan2(x)` must pick one arbitrarily.
Conversely `bytes` and `List<T>` operations have an obvious subject, and
`length(b)` would be the odd spelling there.

**Why same-type forms rather than float-only** (revised 2026-10-27). The
first draft kept `math` float-only, for two reasons. It said overloading by
argument type would reintroduce ADR 0019's objection. But 0019 objects to
mixing, an `int` silently widened to meet a `float`, and same-type forms never
mix. It also said integer `abs` couldn't be specified until `int` overflow was
settled. ADR 0063 has since settled it: `abs` of the most negative `int`
faults, like any other overflow. Separate names (`absInt`, `maxInt`) were the
other option, and decimal would have added a third set. ADR 0048 §2.8 had
already defined `floorMod` for both types.

The cost is that `math.abs(x)` doesn't state its result type at the call site.
That was weighed in review and accepted, for three reasons:

- **The operators already work this way.** ADR 0048 §2.2 makes `-x`, `x * 2`,
  and `x % 3` work on both types, so reading them needs `x`'s type too.
- **The type is always findable, and a wrong guess is a compile error.**
  Parameters are annotated and bindings take their type from what they're
  assigned.
- **Money moves from `int` to decimal later** (ADR 0048 §2.5).
  `math.abs(balance)` survives that move unchanged, where `absInt(balance)`
  would need renaming at every call.

This is not Java's overloading problem. There, implicit widening makes choosing
among overloads a puzzle. Fiig has no implicit conversions (ADR 0019), so the
argument's type picks the form and the result has that type.

**Why radians, and the hazard.** Every language's `sin` takes radians, so an
LLM's prior is correct by default — but `math.sin(90)` is well-typed, silently
wrong, and expresses a belief about degrees. The first draft proposed a
`value Radians` type as the later fix. ADR 0055 removed `value` declarations,
so that route is closed (noted 2026-10-27). The hazard stands, and a fix
would need units as types by some other route; nothing in the corpus needs
angles yet.

## Consequences

**A new declaration kind is the real cost of this ADR.** Fiig has no target
number of kinds; an earlier draft called "nine kinds" a design value, but that
was only the count on the day it was written (corrected 2026-10-27; there are
ten today, after ADR 0043 added `interface` and `capability` and ADR 0055
removed `value`). What fiig does hold to is that each kind earns its place:
ADR 0055 removed `value` because it didn't, and ADR 0022 rejected aliases for
the same reason. `formula` earns its place because it is a category rather
than a special case — several languages draw the same line between pure
computation and effectful or policy-bearing code — and because float, decimal
rounding, and ADR 0063's `OrNone` operations all need a home that rules can
call. It is still the first thing to re-examine if the tier turns out to be
used by only one package.

**Implementation:**

- `compiler/syntax` — a `FormulasDecl` node holding members, plus the parser
  production for both; `types.DeclName` and `DeclKindName` gain the kind
  (`"formula"`). The group is the file's declaration for §3.9 purposes, so
  G1's one-declaration-per-file check must count the group and not its members.
- `compiler/types` — two-level resolution: the Universe gains group entries,
  and member lookup goes through the group. This is the one structurally new
  piece of name resolution, and it is where ADR 0050 §2.6's shared namespace
  between group names and import aliases is implemented.
- `compiler/checker` — `kindFormula` in `declKind`, the §2.3 matrix, and the
  §2.2 restriction checks. `bugClassForEffect` gains the formula rows.
- A builtin table package read by both `compiler/checker` and
  `compiler/runtime` (§2.8). This is the structural change; the member list is
  then data.
- `compiler/checker/expr.go` — `inferQualifiedCall` gains formula groups as
  recognized qualifiers, and method typing gains a receiver-keyed lookup. Per
  `callexpr.go`'s comment, `input.name.isBlank()` currently falls through to
  `Unknown` because its target is itself a `FieldAccess` rather than a bare
  `Ident`; teaching that path the receiver's type is the real work in making
  §2.6 checked.
- `compiler/runtime/callexpr.go:229-249` — `evalBuiltinMethod` stops being the
  definition of behaviour and becomes a consumer of the table.
- `compiler/registry` — a card kind for `formula`, **one card per member** with
  the group as qualifier, not one card for the group. Otherwise
  `catalog-cli find magnitude` cannot find it, and a math library an agent
  cannot discover is one it will not use. Group-level documentation belongs on
  the group header. Also cards for prelude identifiers, which is
  grammar.md:79-82's backlog item 9, still undone; `math` is its forcing case.
  §2.5's package needs nothing new.
- `docs/specification/grammar.md` — `formulas-decl` and `formula-member`, the
  declaration list, the §3.2 composition matrix, and `math` in the prelude
  identifier list.
- `docs/decisions/0029-*` — annotate as narrowed by this ADR.

**ADR 0050 is a prerequisite for this one.** It supplies the case convention
that §2.4's group name depends on, the shared namespace between group names and
import aliases, and the ruling that §2.5's package/group boundary is resolved
by the loader rather than marked in syntax.

**This closes ADR 0048's blocking open question** and supplies §2.7's crossing
rules, so 0048's `float` becomes implementable end to end. 0049 is a
prerequisite for 0048, not a follow-on.

**It also decides a Milestone 7 question ahead of its ADR**, deliberately:
`bytes` and `List<T>` accessors take the method shape of §2.6, and `bytes`
indexing yields `int`. Settling the shape now prevents three independent
answers to one question.

## Proposed bug classes

- **K1 — method not in the receiver type's builtin table.** `42.isBlank()`, or
  any method on a receiver whose type has no such member. Today a runtime
  fault (`callexpr.go:232`, `:247`) the checker never sees.
- **K2 — a declaration whose name collides with a reserved prelude
  identifier.** Found while writing this ADR and pre-existing: `types.resolve`
  matches `string`/`int`/`bool`, then `None`, then `preludeGenerics`, and only
  then falls through to `u.Lookup(name)` (`universe.go:241-257`). So a package
  declaring `model Result` today is **silently shadowed** — it parses, enters
  the Universe, and is unreachable as a type, with no diagnostic at either
  site. The mirror of the duplicate-declaration conflict `BuildUniverse` does
  report. `math` widens the hole by one name rather than creating it — and note
  that a *lowercase* group name widens it differently: the collision is now
  with ordinary declaration names rather than with capitalized prelude types.
- **K3 — a `formula` that returns `Result`.** §2.2 restriction 3.
- **K4 — a `formula` with a `requires` clause.** §2.2 restriction 1. Distinct
  from K3 because the diagnostic should name the tier's purpose, not just the
  grammar.
- **K5 — a bodiless `formula` member outside the closed native set.** §2.1
  makes an omitted block the marker for a native leaf, so a user-written file
  could otherwise declare one and get an unimplemented call that type-checks.
  The mirror of ADR 0042's closed capability set.

Restriction 2 needs no new class: a formula calling a non-formula is an
ordinary composition-matrix violation and belongs in the B series.

The category letter is `K` because ADR 0047 proposes `I1`–`I3`, ADR 0048
proposes `J1`–`J2`, and ADR 0050 proposes `L1`. Four Proposed ADRs each claim
an unused letter; if `bug-classes.md` would rather merge them, these are the
easiest to move.

## Resolved by the 2026-10-13 revision

- **One declaration per file (§3.9) applied to formulas.** Closed. The group is
  the declaration, so a thirty-formula vector package is one file and §3.9
  holds exactly as written — no exception, and the standard library is not its
  first violator. This was the open question this revision was written for.
- **Namespace cost of twenty lowercase prelude names.** Closed by §2.4's group.
- **Whether `Math` is the right name.** Closed differently than asked: the
  question was `Math` versus `Num`, given that the namespace also holds
  `int`/`float` crossing members that are representation changes rather than
  mathematics. It is now lowercase `math`, on precedent, and the crossing
  members stay — a reader looking for `floorToInt` will look under the
  arithmetic namespace regardless of what it is called.

## Open questions

- ~~**Recursion and termination.**~~ **Answered by ADR 0054** (accepted
  2026-10-17): a declaration may call itself exactly when its kind may call its
  own kind, so a formula may recurse. Unbounded recursion hits 0054's
  depth-limit fault. A formula with a non-terminating `while true` loop is not
  caught, as for every other kind; fiig makes no totality promise for any kind.
- ~~**Integer `abs`/`min`/`max`.**~~ **Resolved 2026-10-27** by §2.4's
  same-type forms, after ADR 0063 settled `int` overflow.
- ~~**Whether a parameter or binding may shadow a group name.**~~ **Resolved
  2026-10-27**: it may not. Since every group is now imported (§2.5), a group
  name is an import alias, and a collision with a parameter or binding is G4
  (ADR 0043 §3a), fixed with `as`. `math` is the one prelude group, so it is
  the one name reserved everywhere.
- **Whether a group may be nested or re-exported.** §2.5 makes a group a path
  tail, which invites `standard.geometry.vector.quat`. Nothing here needs it and
  it should stay disallowed until something does.
- **The rest of the `List<T>` inventory, and `Map`**, are deferred to a
  collections ADR. `bytes`'s inventory was settled by ADR 0058; `List`'s four
  core methods by §2.6.

## Implementation notes

**Order (agreed 2026-11-02).** `math` is mostly float, and float needs
`math` to cross to and from `int`, so the ADR is built in halves around ADR
0048: (1) the method table and K1; (2) the `formulas` declaration, with no
`math` members; (3) float, including 0048 §2.4's constant evaluator; (4)
`math` complete, int and float forms and untyped constants together; (5) K2.
Float isn't called done until (4) lands.

**Step 1, 2026-11-02: §2.6, §2.8, K1.**

- The table is `compiler/types/methods.go` (`types.MethodsOf`), in the one
  package both the checker and the runtime already import. `List<T>`'s
  entries are built for the receiver's element type, so `xs.at(0)` on a
  `List<string>` is a `string`.
- The runtime's bodies are in `compiler/runtime/methods.go`, and
  `evalBuiltinMethod` looks up the signature in the shared table before
  dispatching. `TestEveryBuiltinMethodIsImplemented` fails if either side
  has a method the other doesn't.
- The checker already typed a receiver of any shape by the time this landed
  (`callexpr.go`'s comment that `input.name.isBlank()` went untyped was
  stale); what was missing was the table for `string` and `List`, and a
  diagnostic for a method outside it. K1 fires for a method missing from a
  receiver's table and for any method on a type with no table (a model, an
  enum, `string | None`). An `Unknown` receiver gets no second diagnostic.
- `List<T>` gained §2.6's `length`, `at`, `atOrNone`, and `slice`. A slice
  shares storage but claims no slot, so an `append` to it copies rather than
  writing into the original (ADR 0057).
- The corpus needed no migration.

**Step 2, 2026-11-02: the `formulas` declaration (§2.1-§2.3, §2.5), K3-K5.**

- `formulas`/`formula` are declaration-start keywords (ADR 0018), names
  everywhere else. `syntax.FormulasDecl` holds `[]*FormulaDecl`; a member
  with no block has a nil `Body`. A `requires` on a member is parsed and
  kept only so the checker can report K4 with §2.2's reason.
- An import of a group reuses ADR 0043's path-tail machinery:
  `types.GroupRef` gained a `Formulas` field beside `Decl`, so
  `ResolveFileGroups`, `Package.Groups`, and the runtime's `scope.groups`
  carry formulas groups unchanged. Every consumer that meant "capability
  group" now checks `Decl != nil`.
- §2.5's own-package import is the one new resolution step. A package is
  never among its own imports, so `checker.bindOwnFormulas` binds `use
  <own path>.group` to the local group, comparing against the manifest
  `path` when there is one. Without one (the `examples/` corpus checked as
  one universe) any unresolved prefix binds, as ADR 0043's G4 already does
  for capability groups. Calling an unimported group, even one in the same
  package, is an untagged error naming the import to add. Importing the
  group in its own package is not G4 against the group itself.
- A sibling call resolves through the group being checked (`checker.group`)
  and, at runtime, the group carried in `scope.group`. Members shadow
  package declarations of the same name inside the group.
- The matrix row for a formula caller is `{kindFormula}`, and every other
  row gained `kindFormula`. A formula calling anything else is **B11**, a
  new B-series class as the ADR said restriction 2 would be. A formula
  calling a primitive capability is B10, like any non-gateway; invoking a
  provider is P3, like a rule.
- K3 fires for a `Result` return or a union holding one. K5's closed set is
  `types.IsNativeFormula`, empty until step 4.
- The registry gives each member a card named `group.member`, `Kind:
  "formula"`, `Group`, and `Pure: true`; a member's dependencies are the
  siblings it calls. A rule's card does **not** list the formulas it calls:
  the registry's dependency scan has never recorded qualified calls, for
  cross-package calls either. Not fixed here.
- Corpus: `customer-service/formulas/text.fiig` (§2.2's `isEmailShaped`,
  with a sibling call), called from `rules/validate_email.fiig`. Six runtime
  and adapter tests that load customer-service directory by directory
  gained `customer-service/formulas`. `invalid-programs/formula_error.fiig`
  attests K3, K4, K5 and B11 in one group, so it has no incidental G1.
- A formulas file may not have a `private` block (the parser's existing
  rule for non-code kinds). §2.1 lets members call each other, which covers
  what a helper would do, and nothing yet needs a member hidden from callers.

**Step 4, 2026-11-02: the prelude `math` group (§2.4, §2.7).**

- `math` is not a `formulas` declaration. Its formulas are a table,
  `compiler/types/math.go` (`types.MathFormula`), because a member may have
  two forms and a declaration has exactly one signature. The runtime's
  bodies are `compiler/runtime/math.go`, and
  `TestEveryMathFormulaIsImplemented` keeps the two in step, as step 1's
  test does for methods. Since no source declares a native member, K5 now
  fires for every bodiless formula, and `types.IsNativeFormula` is gone.
- The checker (`checker/math.go`) resolves `math.member(...)` before any
  imported group, with no `use`. It types each non-constant argument once
  and picks the form those types fit. With only constant arguments, a
  member with two forms takes the float form if any constant is fractional
  and the int form otherwise, so `math.abs(-3)` is the int form and
  `math.min(1, 2.5)` the float one. Constants then land in the chosen form's
  parameter types, so `math.sqrt(2)` needs no `2.0`. No fitting form is
  A1, and the message lists the forms. `closeTo`'s `relative` and
  `absolute` must be passed by name (ADR 0051), and an argument by
  position there is an untagged error. A missing argument is reported,
  unlike an ordinary call, because there is no fiig body to fault in.
- §2.7's open member form: a constant is an entry in
  `compiler/constant/math.go`, and `constant.Eval` treats `math.pi` as a
  constant expression, so `math.pi * 2` folds exactly and lands once, and
  `math.maxInt + 1` is a compile error. `syntax.FieldAccess` gained the
  `ConstSlot` the runtime reads. `pi` and `e` are 100-digit decimals;
  `tau` is exactly twice `pi`'s value.
- The 2026-10-27 resolution that `math` is reserved everywhere is G4: a
  parameter, binding, loop or pattern variable, or import alias named
  `math` is reported, with `as` as the fix for an alias. That is also what
  lets `constant.Eval` read `math.pi` without knowing the scope. A
  declaration named `math` is K2 (step 5).
- Every IEEE result that is NaN or infinite faults, naming the call:
  `math.sqrt(-1.0) at 2:12 is not a real number`, `math.log(0.0) at 2:12
  has no finite result`. Underflow returns `0.0`. Integer `min`/`max`
  compare as ints, since two values near `maxInt` round to one float.
  `floorMod` takes the divisor's sign and faults on a zero divisor.
  `closeTo` with a negative tolerance faults, as Python's raises; a
  difference beyond float's range is simply not close.
- Registry: `registry.Prelude()` gives every member a card, `Source:
  "prelude"`, with `Kind: "formula"` or `"constant"`. A member with two
  forms lists both in a new `Card.Forms`, and `find --input/--output`
  matches either. `catalog-cli find` and `explain` include these cards;
  `generate` and `diff` leave them out, since they are no package's
  contract. A constant has no same-signature siblings. This is the first
  part of grammar.md's backlog item 9; `Result`, `List`, and the other
  prelude types still have no cards.
- Corpus: `order-processing/formulas/shipping.fiig` (a `dimensionalWeightKg`
  formula), used by `billable_weight.fiig` with `math.max` and by the new
  `billed_kilograms.fiig` with `math.ceilToInt`.
  `invalid-programs/math_error.fiig` attests the A1 form mismatch and the
  positional tolerance.
- Tests: `compiler/checker/math_test.go`, `compiler/runtime/math_test.go`
  (every case also checks clean, since the runtime test helper ignores
  diagnostics), and `compiler/registry/prelude_test.go`.

**ADR 0064's decimal forms (2026-11-03)** made forms differ in their
parameters, since decimal's `round` takes `places:` and `rounding:`. A call
now binds its arguments to each form (`types.BindMathArgs`), shared by the
checker and the runtime; see ADR 0064's step 2 notes.

**Step 5, 2026-11-03: K2.**

- `types.PreludeName` lists what the prelude binds in every file: the
  primitive type names, `None`, `Connection`, `List`, `Result`,
  `BatchResult`, the prelude's declared types (today `SocketError`), `Ok`
  and `Err`, and `math`. The checker reports K2 on any declaration with one
  of those names, public entry or private member, once, in its own file
  (`checker/math.go` `preludeNameTaken`). A dependency with one is refused
  by the loader, like any other dependency diagnostic.
- Two shapes of the hole: `model Result` or `enum None` was unreachable,
  since resolution matches the prelude first; `enum SocketError` was the
  reverse, since `Universe.Lookup` tries the package before the prelude, so
  the package's enum replaced the one the Socket primitive returns. K2
  closes both without touching resolution order.
- `Map` (ADR 0065) is not on the list yet; it joins when that ADR is built.
- Corpus: a private `model Result` in `invalid-programs/math_error.fiig`.
  No real corpus declaration had a prelude name.
