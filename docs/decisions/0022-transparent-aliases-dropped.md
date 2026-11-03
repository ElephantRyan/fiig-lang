# ADR 0022: Transparent Type Aliases Dropped

Status: Accepted
Date: 2026-10-04

## Context

`fiig-lang.md` §3.3 stated "Transparent aliases are available when a new
type identity is unnecessary," and Milestone 2's deliverable list included
"Transparent aliases" as its own line item. `docs/terminology.md` already
had a glossary entry for the concept. None of this had a concrete syntax or
any implementation yet — this ADR is a decision made before writing any of
it, not a reversal of shipped behavior.

Before implementing, the question of *why* the feature was wanted at all was
worked through with a concrete example grounded in the real corpus:
`Result<Customer, CustomerError>` appears 13 separate times across
`examples/`, and an alias would let that be written once —

```
alias CustomerLookup: Result<Customer, CustomerError>

step getCustomer(id: CustomerId) -> CustomerLookup requires Database { ... }
workflow changeCustomerEmail(id: CustomerId, newEmail: EmailAddress) -> CustomerLookup { ... }
```

— fully transparent, so nothing inside either body changes (`?`, `Ok(...)`,
`match` all still work against the real underlying type).

A syntax survey was also done (Go's `type X = Y` vs. `type X Y`, differing
by one easily-missed character; Rust/Haskell's separate `type`/`newtype`
keywords; Kotlin/Swift's dedicated `typealias`), concluding a new keyword
(`alias Name: Type`, mirroring the existing `value Name: Type` shape) would
avoid Go's specific footgun. That syntax question turned out to be moot —
see Decision below.

## Decision

Transparent type aliases are dropped from the language entirely, not
deferred. The only aliasing mechanism Fiig has is import aliasing (§3.10,
`use company.time@2.4 as businessTime`), which already exists and is
implemented.

## Rationale

Fiig's central goal is making workflows easy for humans *and LLMs* to
discover, compose, and verify — which the language otherwise pursues
through explicitness: no implicit conversions, no `null`, no unchecked
casts, named-argument calls preferred over positional ones so a reader
matches by name rather than counting positions. A cross-file transparent
type alias cuts against this directly: reading `-> CustomerLookup` at a call
site gives no indication, without finding and reading wherever
`CustomerLookup` happens to be declared, that the real shape underneath is
`Result<Customer, CustomerError>`. That's exactly the kind of indirection
the project otherwise refuses to introduce — and it's worse for an LLM
composing from a compact registry contract than for a human skimming
source, since the contract card is supposed to be enough on its own
(`fiig-lang.md` §3.11), not a pointer to go look something up.

Import aliasing is different in kind, not degree: `use company.time@2.4 as
businessTime` is declared at the top of the very file that uses it, so the
real identity is one glance up, not a cross-file lookup — and the canonical
identity is preserved in the registry regardless of the local spelling
(`terminology.md`'s existing "Canonical identity" entry already makes this
distinction for imports). A cross-file type alias has no equivalent
locality: it could be declared in any file in the package, same as any
other named declaration, with no "look at the top of this file" convention
to anchor it.

A file-scoped version (declared at the top of the one file that uses it,
same locality argument as import aliasing) was considered as a middle
ground, but §3.9 already requires exactly one primary declaration per file.
That rule removes the actual motivating case: there is never a second
declaration in the same file to share the alias with, so a file-scoped
alias could only ever rename the one type already used by that file's one
declaration — moving a signature one line up within an already-small file,
not eliminating any real repetition. Nothing answers "does it reduce the
context required to understand or modify a workflow?" (`fiig-lang.md` §9's
own bar for a feature) once that's accounted for, so there is no remaining
case for transparent aliasing in any scope, not just the cross-file one.

## Consequences

- `fiig-lang.md` §3.3's "Transparent aliases are available..." line is
  removed.
- `fiig-lang.md` §5's Milestone 2 deliverable list no longer includes
  "Transparent aliases" — dropped outright, the same treatment bug class C3
  got in `bug-classes.md`'s "Explicitly out of scope" section, not left
  sitting as a perpetual "deferred."
- `docs/terminology.md`'s "Transparent alias" glossary entry is removed —
  the glossary documents current terms, and this one no longer is.
- No grammar, parser, or checker changes are needed: nothing was
  implemented yet.
- If a long repeated generic signature becomes a real readability problem
  later, it should be revisited with a mechanism that doesn't give up
  at-a-glance explicitness at the call site — not in scope now, and no
  specific alternative is proposed here.
