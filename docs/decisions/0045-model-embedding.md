# ADR 0045: Model Embedding Is Go-Style — Explicit, Flattening, No Subtyping

Status: Accepted. Implemented 2026-10-14.
Date: 2026-10-11
Related: ADR 0042-0044 (Milestone 7). Independent of ADR 0043 — embedding
adds no assignability rule, so it stands or falls on its own.

## Context

Fiig models share structure constantly — a contact block across
`Customer` and `Merchant`, audit fields across every persisted record —
and the only way to reuse it today is to retype each field or to add a
named field and accept an extra level of access on every read.

ADR 0043 opened substitution for *effects*. This is the reuse axis for
*data*, and it is a separate problem: Go, Rust and Elixir all replaced
inheritance with the same split — composition for reuse, interface-likes
for substitution — and none of them conflated the two.

## Decision

A model may embed another model by naming its type with no field name:

```fiig
model ContactInfo {
    email: EmailAddress
    phone: string
}

model Customer {
    ContactInfo
    id: CustomerId
    name: string
}
```

Grammar delta: `field ::= identifier ':' type | type-name`.

### Flattening, not nesting

`customer.email` is legal. `customer.ContactInfo.email` is not — there is
no intermediate value. The embedded model's fields become fields of the
outer model for every purpose: field access, record literals,
exhaustiveness, registry cards, and JSON encoding.

This is the distinction worth being precise about, because it was
confused once already in discussion: **embedding flattens; a named field
nests.** Both are available and they do different jobs.

```fiig
model Customer {
    contact: ContactInfo    // a field  -> customer.contact.email, nested in JSON
}

model Customer {
    ContactInfo             // embedded -> customer.email,         flat in JSON
}
```

A nested JSON response is built from named fields, which have never had a
depth limit. Embedding is not the tool for shaping output.

### Unlimited depth

```fiig
model Address     { line1: string   city: string }
model ContactInfo { Address         email: EmailAddress }
model Customer    { ContactInfo     id: CustomerId }

// customer.city resolves through two levels of embedding
```

No cap. Real domain models nest arbitrarily, and a limit would be an
arbitrary cliff. The compiler flattens transitively at check time, so
depth is a source-level convenience with no runtime representation.

### No subtyping

`Customer` is **not** assignable to `ContactInfo`. `AssignableTo`
(`compiler/types/types.go:176`) is unchanged — still equality plus union
membership. A rule taking `ContactInfo` does not accept a `Customer`.

Embedding buys *declaration* reuse, not substitutability. This is exactly
Go, where an embedding struct is likewise not its embedded type, and Go
is not considered deficient for it because substitution comes from
interfaces instead.

### Collisions

- An explicitly declared field in the outer model **shadows** a promoted
  field of the same name. Explicit beats implicit, and the shadowing is
  visible in the declaration being read.
- Two embedded models promoting the same name at the same depth is a
  **declaration error**, reported where the embedding model is declared,
  whether or not the ambiguous name is ever used.
- An embedding cycle (`A` embeds `B` embeds `A`) is a declaration error.
  This is a structural cycle on types, distinct from the activity call
  cycles ADR 0044 introduced.

Go resolves same-depth ambiguity lazily — shallowest wins, and it errors
only if the ambiguous selector is actually used. Fiig reports it eagerly.
Lazy resolution means a model can be well-formed until someone reads a
field, which is the opposite of the property fiig trades for.

### Construction names the embedded type

```fiig
customer = Customer{ ContactInfo: contact, id: id, name: name }
```

Access is flat, construction is nested. The asymmetry is deliberate: it
is the only form that lets an existing `ContactInfo` value be reused,
which is most of the point of embedding. The alternative — flat
construction keys — would force every caller to destructure a value it
already holds.

This is the piece most likely to want revisiting once embedding is used
in anger. It is recorded as a decision rather than left open so that
"written down" means something, but it is the cheapest part of this ADR
to reverse: it affects literals only, not the type system.

## Rationale

Embedding was chosen over the two alternatives considered.

*Inheritance* is ruled out on precedent — Go, Rust and Elixir each
removed it deliberately, over the fragile-base-class and
Liskov-violation problems. Fiig having no subtyping at all makes it a
non-starter besides.

*A named field* is already available and is the right tool when the
structure should be visible to a reader and to JSON. It is the wrong tool
for audit fields, where `record.createdBy` reads correctly and
`record.audit.createdBy` is ceremony.

Keeping embedding free of any assignability rule is what makes it safe to
accept independently of ADR 0043. It adds a source-level convenience and
nothing to the type system.

## Implementation notes

Decisions made while implementing that the sections above left open:

- **Construction is call-style, not brace-style.** The examples above write
  `Customer{ ContactInfo: contact, ... }`, but fiig constructs models with a
  call (`Customer(id: id, ...)`), so embedded construction is
  `Customer(ContactInfo: contact, id: id, name: name)`. The embedded value
  is named by its type, as decided; only the bracket shape differs.
- **Embedding is same-package only.** An embedded type must be a plain model
  name declared in the same package; `alias.Model` is rejected. A promoted
  field's type then always resolves in the embedding model's own package,
  which is what lets the flattened set be computed once, in
  `types.BuildUniverse`, and stored on `ModelDecl.Flat`. Cross-package
  embedding is the first thing to relax if a real library needs it.
- **Depth resolution.** A shallower promoted field wins over a deeper one
  without error (as in Go); only a tie at the minimum depth is A10. The
  ADR's "same depth" wording implied this but did not say it.
- **`copy(base, field: v)` overrides a promoted field by its flat name**,
  matching field access. Overriding an entire embedded value
  (`copy(c, ContactInfo: x)`) is not supported.
- **Bug classes:** A10 (ambiguous promoted field, or the same model
  embedded twice) and A11 (embedding cycle, reported at every member).
- **The formatter does not reorder.** The Consequences below say embedded
  types sort before named fields; the formatter prints fields in source
  order, because reordering would detach each field from its leading
  comments, which are attached by line. Embedded-first stays a convention.
- **Registry:** `Param.PromotedFrom` carries provenance (`"ContactInfo.Address"`);
  `registry.Generate` now parses every file before building any card so
  flattening can see all models.

## Consequences

- `compiler/syntax`: a field production with no name; the parser must
  distinguish an embedded type from a malformed field.
- `compiler/checker`: transitive flattening; eager same-depth collision
  errors; shadowing resolution; embedding cycle detection. New bug
  classes — ambiguous promoted field, embedding cycle.
- `compiler/types`: `Model` carries its flattened field set.
  `AssignableTo` **unchanged** — worth asserting in a test, since the
  tempting bug is to let embedding imply assignability.
- `compiler/registry`: a model card lists flattened fields and records
  which were promoted and from where. Without the provenance, two models
  embedding the same block look like coincidence.
- `compiler/runtime`: flat representation; `json.go` encodes promoted
  fields at the top level.
- Formatter: embedded types sort before named fields, so the shared block
  reads first.
- No corpus migration. Embedding is additive, and no existing model is
  obliged to adopt it.
