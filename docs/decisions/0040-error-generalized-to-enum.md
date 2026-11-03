# ADR 0040: `error` Generalized to a General `enum` Declaration

Status: Accepted
Date: 2026-10-10

## Context

`error` was a dedicated declaration kind for exactly one shape: a tagged
union of variants, each with its own field list, used as the `E` in
`Result<T, E>`. That shape — a closed set of named variants, each
optionally carrying fields, matched exhaustively — is not inherently
error-specific. A settlement region (`US`/`EU`/`APAC`), a card-payment
state, or any other fixed closed set needs the identical mechanism and had
nowhere to live: `model` only expresses a single product-of-fields shape,
not a sum of alternatives.

Rust takes exactly this position: one `enum` construct, and a typed error
is just an `enum` used in a particular position (as `Result`'s error
type), not a distinct declaration kind. Java's alternative — a dedicated
checked-exception hierarchy separate from its `enum` — was considered and
rejected, since fiig has no exception hierarchy to begin with and already
models typed errors as plain tagged unions; keeping `error` as a
syntactically separate kind from a future general sum type would mean
maintaining two parsers/checkers for one underlying concept.

## Decision

`error` is removed as a declaration kind. A new, general `enum` kind
replaces it everywhere, including every existing typed-error use:

```
enum Region {
    US
    EU
    APAC
}

enum CheckoutError {
    OutOfStock
    InvalidQuantity(requested: int, available: int)
}
```

Grammar:

```
enum-decl    ::= 'enum' identifier '{' { enum-variant } '}'
enum-variant ::= identifier [ '(' [ field { ',' field } ] ')' ]
```

The parenthesized field list is optional per-variant — a zero-field
variant (`US`, `OutOfStock`) is constructed and matched bare
(`Region.US`, `CheckoutError.OutOfStock`), with no empty `()`. A variant
with fields is constructed and matched with named-argument call syntax
(`InvalidQuantity(requested: 3, available: 1)`), exactly as before under
`error`.

A typed error (`Result<T, E>`'s `E`) is now just an ordinary `enum` —
there is no separate error-declaration grammar, no `ErrorDecl` AST node,
and no error-specific checker/runtime code path. Exhaustive `match`
checking (bug class A9) applies uniformly to any `enum`, not only to
typed errors.

## Rationale

This is the same move ADR 0020 (`atomic`→`transaction`) and ADR 0031
(`step`→`gateway`) already made: when a feature gets used by a second,
unrelated concrete case, a name/shape that was previously scoped to the
first case either needs to be proven inherently specific to it, or needs
to generalize. "Fixed set of named, independently-shaped variants" is not
specific to errors — `Region` is the proof. Collapsing into one `enum`
keeps the language with a single sum-type mechanism rather than two
syntactically distinct ones that would inevitably drift apart.

## Consequences

- Grammar: `error`/`error-decl`/`error-variant` keyword and productions
  renamed to `enum`/`enum-decl`/`enum-variant`. No change to the optional
  parenthesized field list, which was already correctly optional in the
  parser (the grammar doc previously — incorrectly — showed it as
  mandatory).
- `compiler/lexer`: `KwError`→`KwEnum`; keyword text `"error"`→`"enum"`.
- `compiler/syntax`: `ErrorDecl`/`ErrorVariant`→`EnumDecl`/`EnumVariant`.
- `compiler/parser`: `parseErrorDecl`/`parseErrorVariant`→
  `parseEnumDecl`/`parseEnumVariant`; `structuralKeywords` and `parseDecl`
  updated.
- `compiler/checker`, `compiler/types/universe.go`: every `*syntax.ErrorDecl`
  type-switch case renamed to `*syntax.EnumDecl` — no behavioral branching
  was error-specific, so this is a pure rename. Bug class A9 (exhaustive
  match) wording generalized from "a missing error variant" to "a missing
  enum variant (a typed error's or any other enum's)".
- `compiler/registry`: `Card.Kind` emits `"enum"` instead of `"error"`;
  `registry/diff.go`'s variant-change case key updated to match.
- `compiler/runtime`:
  - `callexpr.go`: enum-variant construction renamed throughout
    (`constructEnum`, `enumDecl`).
  - `value.go`: `EnumValue{TypeName, Variant, Fields}` (was the
    error-specific value type).
  - `json.go`: wire key renamed `$error`→`$enum`.
  - `match.go`: **real bug fix, not just a rename** — `MatchPattern`'s
    `NamePattern` case previously rejected any path longer than 1 segment
    outright, so a zero-field enum-variant pattern (`Region.US`) would
    never match at runtime even though the checker's `patternCoverage`
    already treated it as covering that tag. No existing example had ever
    matched a zero-field variant bare, so this was a dormant bug predating
    this change, surfaced only by adding `Region`. Fixed by adding a
    2-segment branch that compares `EnumValue.TypeName`/`Variant`.
- `compiler/format`: **real bug fix, not just a rename** — `printDecl`
  was still literally emitting the string `"error "+d.Name` as the printed
  header for an `*syntax.EnumDecl`, which would have made canonical
  formatting silently keep printing the old keyword. Fixed to
  `"enum "+d.Name`.
- `compiler/adapter/openapi.go`: enum variants still collapse to a generic
  object in the generated schema, now described as "a tagged enum
  variant" rather than error-specific wording.
- Every Go test exercising inline `error E { ... }` source or an
  `ErrorDecl`/`"error"`-kind assertion updated to `enum`/`"enum"`
  (`parser_test.go`, `checker_test.go`, `registry_test.go`,
  `diff_test.go`, `syntax_snapshot_test.go`, `format_test.go`,
  `fault_test.go`, `transaction_test.go`).
- Real corpus: `examples/customer-service/errors/customer_error.fiig`,
  `examples/order-processing/errors/order_error.fiig`, and
  `examples/order-processing/errors/checkout_error.fiig` renamed from
  `error X { ... }` to `enum X { ... }`.
- New example attesting `enum` outside the error-handling role:
  `examples/order-processing/models/region.fiig` (`Region`, three
  zero-field variants) and `examples/order-processing/rules/region_display_name.fiig`
  (exhaustive match over all three, each matched bare with no call
  parens). A corresponding invalid-program fixture was added to
  `examples/invalid-programs/type_error.fiig` (`describeRegion`, missing
  the `APAC` arm) generalizing bug class A9's attestation beyond typed
  errors; `checker_test.go`'s `wantRuleCount["A9"]` bumped from 1 to 2.
- New permanent regression test:
  `TestRegionDisplayNameMatchesZeroFieldEnumVariant` in
  `compiler/runtime/interp_test.go`, covering the `match.go` fix above
  against the real `Region`/`regionDisplayName` corpus files.
- Documentation: `fiig-lang.md`, `docs/terminology.md`,
  `docs/specification/grammar.md`, `docs/specification/bug-classes.md`,
  and `examples/README.md` updated to describe `enum` as the general
  mechanism and typed errors as one use of it. Historical ADRs
  (0006, 0018) are left using the old `error` terminology, consistent
  with this project's convention that past decisions are a frozen record,
  not retroactively edited.
