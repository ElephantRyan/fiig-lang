# ADR 0046: An Enum Is the Dispatch Index for Owned Implementation Sets; Variant Constants Go on the Enum Header

Status: **Accepted** (2026-10-24), after review revisions to §2, §4b, §5 and §7;
**implemented** 2026-10-24 (see Implementation).
Date: 2026-10-11
Related: ADR 0040 (`enum` as a general sum type), ADR 0043 (capability
contracts — this record bounds where 0043 applies rather than changing it),
ADR 0041 (`bool`). Partially resolves `fiig-lang.md` §8 item 13.

## Context

ADR 0043 answered "how does a new provider get added" with: ship a package,
declare `provides`, select it from a pure rule returning the package value.
That model was designed against Stripe-versus-Square and it assumes two to
five providers.

The case that broke it is a `retrieveDeductions` gateway with **250 in-house
client integrations**. At that scale 0043 fails twice:

- The selection rule imports 250 packages, so every application that touches
  deductions links all 250 integrations.
- Adding client #251 means editing the selection rule — an open/closed
  violation at precisely the point 0043 exists to fix.

Fiig also weighs a constraint most languages do not: an LLM debugging one
client must not have to traverse 250 implementations to find the relevant
one. That constraint turns out to *conflict* with strict OCP, and this
record resolves the conflict deliberately.

## Decision

### 1. Ownership decides the mechanism

| | All implementations owned in-house | Third parties add implementations |
|---|---|---|
| Mechanism | `enum` + exhaustive `match` | ADR 0043 `provides` |
| Index | the enum declaration | the registry, derived |
| Completeness | compiler-enforced | manifest-declared |

Neither mechanism is a fallback for the other, and 0043 is unchanged for
the case it was written for. What changes is that 0043 is no longer the
answer to *every* extension question.

This is the same split Rust makes between `enum` + `match` and
`dyn Trait`, and Haskell makes between a sum type and a typeclass. Both
pick the closed form when the authoring code owns every case.

It also resolves what looked like a contradiction with fiig's own position
that a new enum variant is a deliberately *closed* axis, because
exhaustiveness forces every `match` to acknowledge it. That position
stands. Here the closed-ness **is** the feature: one exhaustive match is a
complete index that the compiler will not let you leave incomplete, whereas
strict OCP would scatter 250 self-registrations across 250 packages and
produce exactly the traversal problem this record is trying to avoid.

The "violation" of adding a line to a match is what buys the index. It is
also not the dangerous kind of modification — it appends a case under
compiler supervision rather than reinterpreting existing behavior.

### 2. Per-client facts live on the enum; a `match` is for logic

Revised 2026-10-24. The first draft put per-client facts in a match:

```fiig
rule deductionConfig(source: DeductionSource) -> DeductionConfig {
    match source {
        DeductionSource.Acme   -> DeductionConfig(layout: FieldLayout.FixedWidth)
        DeductionSource.Globex -> DeductionConfig(layout: FieldLayout.Csv)
    }
}
```

With header constants (§4), that is a second 250-row table that must agree
with the enum. A fact about a client that is a constant goes on the header
and is read as `source.layout` (§4b). One gateway takes the
`DeductionSource` and reads what it needs from it.

A `match` on the enum is for what a constant cannot express: a computation
or a decision that differs per client. Even then its arms return data, not
provider values. That is what keeps 250 package imports out of the
dispatch path.

Facts that differ by environment, such as a client's endpoint in test
versus production, are neither. They belong in capability configuration
(ADR 0027), not in source.

Separate packages stay reserved for the two or three clients whose protocol
is genuinely unlike the others, and those are reached through ADR 0043.

### 3. Parse and dispatch are separate declarations

Parse is partial — inbound strings are untrusted and it must return
`Result`. Dispatch over a closed enum is total. Keeping them apart makes
the enum the **trust boundary**: downstream of it no declaration can
encounter an unknown source, so no downstream signature carries an
"unknown source" error case.

Within parsing, two kinds of input must not be conflated:

- **Sentinels** (`"DEFAULT"`, `"OTHER"`) are business decisions —
  `"DEFAULT"` means consult this tenant's configured default. They belong
  in the rule.
- **Aliases** (`"ACMEINC"` for `Acme`) are pure lookup facts. They must
  *not* go in the rule, or it grows with every client times every spelling.

```fiig
rule resolveDeductionSource(raw: string, tenant: Tenant)
    -> Result<DeductionSource, DeductionError>
{
    if raw == "DEFAULT" { return Ok(tenant.defaultSource) }
    if raw == "OTHER"   { return Err(DeductionError.UnsupportedSource(raw)) }
    match DeductionSource(code: raw) {
        found -> Ok(found)
        None  -> Err(DeductionError.UnknownSource(raw))
    }
}
```

The rule is eight lines whether there are 250 clients or 2,500. That
size-independence is the deliverable: an LLM reads this rule and one enum
row, never 250 implementations.

### 4. Variant constants: the schema goes on the enum header

```fiig
enum DeductionSource(key code: string, layout: FieldLayout) {
    Acme(code: "ACME", layout: FieldLayout.FixedWidth)
    Globex(code: "GLBX", layout: FieldLayout.Csv)
}
```

Grammar delta:

```ebnf
enum-decl   ::= 'enum' identifier [ '(' enum-field { ',' enum-field } ')' ]
                '{' { enum-variant } '}'
enum-field  ::= [ 'key' ] identifier ':' type
```

A variant on an enum *with* a header supplies values, using the existing
`identifier ':' expression` argument form.

**No new operator is introduced.** Position disambiguates: the header is
`name: Type` (a schema), a row is `name: value` — identical to fiig's
existing construction syntax, `HttpResponse(status: 404, body: None)`.
Payload variants keep their meaning unchanged on enums with no header, so
`NotFound(id: CustomerId)` is never ambiguous.

#### 4a. Header values are constants

Added 2026-10-20. A value a variant supplies for a header field must be a
**constant**:

- a string, integer, or boolean literal, or `None`;
- a variant of an enum (`layout: FieldLayout.FixedWidth`);
- a list literal whose elements are all constants (ADR 0057 §7);
- a model constructed with named arguments that are all constants
  (`limits: Limits(min: 1, max: 5)`).

A call to any declaration (rule, conversion, formula, or anything else) is
not a constant, and neither is arithmetic (`5 * 60`). Both are errors in a
header.

Why constants only:

1. **Duplicate `key` values are checked at compile time** (§5). That needs
   every value known to the compiler. A computed alias could collide with
   another variant's, and the clash would surface only at runtime, or never.
2. **The registry card shows the real values** (Consequences), so the
   catalog can answer "which source has code `ACME`" without running
   anything. A computed value would show as unknown.
3. **An enum has no setup step that can fail.** In Java, an enum whose
   constructor throws gives `ExceptionInInitializerError`, and every later
   use of the enum fails with `NoClassDefFoundError`, which does not name
   the cause.
4. **No effects question.** An enum is not a kind in §3.2's call table, so a
   header that could call something would need its own rule for what.
5. **Loosening later is safe; tightening later breaks code.**

The cost: a value derived from another (`displayName` from `code`) is written
out on every row, or computed at the use site by a named rule
(`displayNameOf(source)`), which already works.

**How this may loosen later.** Formulas (ADR 0049) are pure and fixed, and
0049 compares them to Rust `const fn` and C++ `constexpr`. If derived header
values become a burden, the next step is to allow formula calls in headers,
evaluated by the compiler, so §5's duplicate check and the registry card
keep working. Arithmetic on constants would come with that decision, or
earlier if a case is attested. Calls to rules and other kinds stay out.

#### 4b. Header fields are readable on any value of the enum

Added 2026-10-24. Every header field is readable with ordinary field
access, on any value of the enum, with no `match`:

```fiig
source = DeductionSource.Acme
source.code      // "ACME"
source.layout    // FieldLayout.FixedWidth
```

The type is the header field's declared type. This is the enum→value
direction, and it needs saying because without it the only way to send a
client's code back out is a 250-arm `match` that repeats the header,
which is the two-edit-sites problem §5 removes in the other direction.
Together, §4b and §5 make the header the one place each fact is written:
`source.code` reads it, `DeductionSource(code: raw)` finds by it.

`source.code` is `lower.lower`, the pure tone under ADR 0043 §6, so this
adds no call-site meaning. Payload variants are unaffected: an enum
without a header has no fields shared by every variant, and its payload
is still reached by `match`.

`=` was considered and rejected twice over: it is already assignment
(`existing = Database.queryOne(...)`), and it would be the only place in
fiig where `=` means what `:` means everywhere else.

Declaring field names and types once on the header rather than per variant
also matters at this scale — 250 variants with five attributes would
otherwise repeat each field name 250 times. The result reads as a table:
the header is the column list, each row is one client.

### 5. `key` fields get a compiler-derived lookup

**Any number of fields may be marked `key`.** For each one the compiler
derives a lookup, written as a call to the enum type with exactly one
named argument, which must be a `key` field:

```fiig
enum DeductionSource(key code: string, key legacyId: string, layout: FieldLayout) {
    Acme(code: "ACME", legacyId: "A-001", layout: FieldLayout.FixedWidth)
}

DeductionSource(code: raw)        // -> DeductionSource | None
DeductionSource(legacyId: raw)    // -> DeductionSource | None
DeductionSource(layout: x)        // error: layout is not a key field
```

`| None` rather than `Result` matches the existing `Row | None` idiom that
every `Database.queryOne` caller already destructures.

**Why this form and not `DeductionSource.byCode(raw)`** (revised
2026-10-24). The first draft derived a member named `by` + the field name.
ADR 0043 §6a, accepted after this record was drafted, makes
`Upper.lower(...)` mean exactly "a capability call, may do I/O", checked
as N2. A pure lookup in that form would break the rule §6a exists to
guarantee. `Upper.Upper(...)` would read as a variant and could collide
with a real one. A generated lowercase function would put a name in the
package that no file declares. The `Upper(...)` construction tone is
already pure, and the named argument picks the key, so multiple keys need
no generated names. Python's `Color("red")` is the same form.

The cost: on a model, `Name(field: value)` builds a value and always
succeeds; on a headed enum it finds an existing variant and may return
`None`. The declaration named in the call tells a reader which. A headed
enum is never constructed any other way, so the form is unambiguous.

**Duplicate values within a `key` field are a declaration error**, which is
what makes each lookup well defined. Non-`key` fields carry no uniqueness
requirement — `layout` is *expected* to repeat across clients.

The marker is not merely a way to nominate a column, and this is the part
worth being precise about because the keyword looks redundant at first.
Uniqueness is computable: the compiler can inspect all 250 rows and see
which columns happen to be distinct. But deriving the lookup from observed
uniqueness would make its **return type depend on the data** — adding client
#251 with an already-used layout would silently flip a derived signature
from `DeductionSource | None` to a collection and break every caller.
`key` pins the return type and converts uniqueness from an accident of the
current rows into a checked invariant. That is the same class of guarantee
exhaustiveness provides on the other direction.

**Non-goal: one-to-many lookup.** "Which sources use `FieldLayout.Csv`" is a filter over
a non-unique column, not a lookup, and it is out of scope here. It needs
iteration over an enum's variants, which fiig does not have and which this
record does not add (see the end of this section). Note that
the common reason to ask it — understanding the corpus rather than branching
at runtime — is already served without any language feature, because an enum
card records every variant's constants and the catalog can query them.

This derivation is the correctness core of the record, not a convenience.
**Exhaustiveness is one-directional.** It protects the enum→config
direction: add a variant and every `match` stops compiling
(`compiler/checker/stmt.go:265`, bug class A9). It does nothing for the
string→enum direction, because the compiler cannot know that a new variant
requires a new string branch. Without a derived lookup, the sequence is:
add client #251, fix every broken match, and ship a parse rule that
silently never returns the new variant — discovered in production. Moving
the code onto the variant collapses both directions to one edit site.

Deriving this in fiig *source* is not possible and should not be attempted:
it needs iteration over an enum's variants, and runtime reflection is on
§9's do-not-build list.

### 6. A variant has header constants or a payload, never both

The mixed case worth wanting — an error variant carrying a payload *and* an
HTTP status — already has a home chosen on purpose.
`examples/customer-service/conversions/customer_error_to_http_response.fiig`
does that mapping as a conversion, and its own comment records the choice:
"An ordinary conversion, not a language feature." Status mapping is an
adapter concern, not enum identity. Leave it there.

### 7. Aliases are deferred, not rejected

Multiple spellings per variant were deferred when this record was drafted,
because fiig had no list literals. ADR 0057 (2026-10-20) supplies them, and
§4a admits them as header constants, so aliases need no further feature.
They are an ordinary `key` field of type `List<string>`:

```fiig
enum DeductionSource(key code: string, key aliases: List<string>) {
    Acme(code: "ACME", aliases: ["ACMEINC", "ACME-INC"])
}

DeductionSource(aliases: raw)    // matches if raw equals any element
```

For a list-typed `key` field the lookup matches any element, and the
duplicate check covers every element of every variant: no spelling may name
two variants. An empty list is allowed and matches nothing.

## Rationale

The enum-header form is Java's design (`ACME("Acme")`) and Kotlin's
(`enum class Color(val rgb: Int) { RED(0xFF0000) }`), differing only in
using named rather than positional arguments — 250 rows of five positional
values would be unreadable. The derived lookup is `Enum.valueOf` in Java,
`strum::EnumString` in Rust, and `Enum(value)` in Python; all three
generate it from declared constants for exactly this reason.

Three alternatives were considered and rejected:

*A declared string-keyed config table* was the first proposal and is worse
than the enum on safety: a missing row can only fail at runtime, where a
missing variant fails at compile time. It did, however, force a useful
correction — the objection to string-keyed dispatch is not that keys are
strings. It is that the mapping must be **a single statically enumerable
artifact**. A Spring-style runtime scan fails that test; a declared table
passes it; an exhaustive match passes it and is checked besides.

*Attributes* (`@code("ACME")`), as Rust's `strum` and Go's struct tags do
it, would require inventing an attribute syntax, which invites a general
metadata language for one feature.

*A companion model plus a mapping rule* needs no new syntax at all, and is
the status quo. It cannot support a derived lookup, since inverting it would
mean symbolically executing a rule body — so it loses the property that
motivated the record.

## Consequences

- `compiler/syntax`: optional parenthesized header on `enum-decl`;
  contextual keyword `key`; variant arguments parsed as expressions rather
  than fields. The parser must reject a header-less enum whose variants
  supply values, and a headed enum whose variants declare types.
- `compiler/checker`: each variant's arguments checked against the header
  schema; every header field supplied by every variant; duplicate `key`
  values a declaration error; key-lookup resolution; field access on a
  headed enum's values (§4b). New bug classes —
  variant missing a constant, duplicate key value, mixed
  constant-and-payload variant.
- `compiler/types`: `Enum` carries a constant schema and per-variant
  constant values. No change to `AssignableTo` — a constant does not make
  a variant assignable to its field's type.
- `compiler/runtime`: key-lookup evaluation; constants are compile-time
  values with no construction cost.
- `compiler/registry`: an enum card lists the header schema and every
  variant's constants. This is what lets the catalog answer "which source
  has code `ACME`" across all 250 without reading source, and it is the
  tooling half of this record's navigability goal.
- Formatter: header fields on one line; variant rows aligned by column, so
  the table reads as a table.
- No corpus migration. The feature is additive and no existing enum must
  adopt it. `models/region.fiig` is the natural first adopter if `US`/`EU`/
  `APAC` ever need wire codes, and
  `order-processing/rules/region_display_name.fiig` would then collapse into
  a constant.
- Unresolved beyond this record: whether a `key` lookup should also be
  derivable over an *interface*'s implementations. `interface` itself landed
  in ADR 0043; this question is the pure-dispatch half of §8 item 13, where
  a third party supplies the implementation, and needs its own ADR.

## Implementation (2026-10-24)

- **Syntax.** `EnumDecl.Header` (`EnumField`, with `Key`) and
  `EnumVariant.Values`. A row is parsed with the ordinary call-argument
  parser, so a header decides whether `name: x` is a type or a value. `key`
  is contextual: it is the modifier only when another name follows it.
- **Bug classes.** A15 (a row is not exactly the header's fields as
  constants, including a payload on a headed enum and a key whose type is
  not `string`, `int`, or a list of one), A16 (duplicate key value, or
  duplicate element of a list key), A17 (a lookup that is not one named key
  argument, or a lookup on an enum with no header). Key types were narrowed
  to those four while implementing: they are what the corpus case needs, and
  loosening later is safe (§4a, reason 5).
- **Constants** are compared for A16 in the formatter's canonical spelling
  (`format.Expr`), which is also what the registry card shows.
- **Runtime.** A headed variant carries its constants (`EnumValue.Consts`),
  evaluated when the value is built, in the enum's own package. A lookup
  scans the variants in order; nothing is cached yet.
- **Registry.** Enum cards carry `header` and per-variant `constants`.
  `catalog diff`: a changed key value or a dropped `key` marker is Major (a
  lookup that found the variant now returns None); any other changed
  constant, an added header field, or a new key is Minor.
- **Formatter.** Header on one line; rows aligned by column when every row
  names its values in the same order.
- **Found while dogfooding.** A bare-name match arm against `T | None`
  bound the whole union, though exhaustiveness already treated it as
  covering only `T` (grammar.md §5). §3's example could not type-check
  `found -> Ok(found)`. The arm now binds `T`.
- **Catalog search** (added 2026-10-25). `catalog find --constant
  code=ACME` answers "which source has code ACME" with the variant
  (`DeductionSource.Acme`), not just the enum. It matches exactly, like the
  lookup: a string with or without its quotes, a list field by element.
  It works on any header field, not only keys, so `--constant
  layout=FieldLayout.Csv` is the one-to-many question §5 sends to the
  catalog. `--text` stays name-only; widening it to values was rejected
  as too loose (`--text A` would match nearly every row).
- **Corpus:** `examples/deductions`, run by `compiler/runtime`'s
  `TestCorpusDeductions`.
