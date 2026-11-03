# ADR 0018: Structural Declaration Keywords Are Contextual, Not Globally Reserved

Status: Accepted
Date: 2026-10-01

## Context

The parser (Milestone 1) reserves `model`, `value`, `error`, `rule`,
`conversion`, `step`, `workflow`, and `adapter` unconditionally at the lexer
level. The real example corpus already uses `error` as an ordinary lowercase
name in three different positions:

- a parameter name: `conversion customerErrorToHttpResponse(error: CustomerError)`
- a field name: `Customer(error: CustomerError)` (`order-processing/errors/checkout_error.fiig`)
- a bare identifier reference and catch-all pattern:
  `match error { ... error -> HttpResponse(status: 500, body: None) }`

Because the lexer tags `error` as `KwError` with no regard for position, the
parser's `expect(Ident)` calls fail to match it, and since `Parser.expect`
doesn't consume a token on mismatch, the failure doesn't just produce a
diagnostic — it leaves the token stream stuck, and the enclosing loop never
terminates. `TestParseErrorDeclWithZeroFieldVariant` hangs rather than fails,
and `TestCorpusParsesWithoutErrors` would hang on the real corpus files for
the same reason.

This is exactly the problem grammar.md §1 already solved once for `Result`,
`Ok`, `Err`, `BatchResult`, `List`, and `None`: those are deliberately *not*
keywords, specifically so they don't need special-casing in the parser and
can get ordinary registry entries like any other declaration. The eight
structural keywords above need the same treatment, with one difference: they
really do have to be reserved somewhere, because they're what the parser
uses to decide a declaration is starting.

## Decision

The eight structural keywords are reserved only at the position where a
declaration may legally start (the top level of a file, immediately after
imports). Everywhere else — field names, parameter names, local bindings,
match patterns, and bare identifier expressions — a token spelled `model`,
`value`, `error`, `rule`, `conversion`, `step`, `workflow`, or `adapter`
parses as an ordinary identifier, using its text.

The lexer is unchanged: it still tags these eight spellings as keyword
tokens unconditionally, since it has no syntactic context. The parser
absorbs the contextuality instead, through a single `expectName` /
`atName` helper pair that treats "identifier or one of the eight structural
keyword kinds" as a name, used at every point the grammar calls for a name
rather than a specific reserved spelling. The declaration-start position is
unaffected — `parseDecl`'s dispatch still switches on the keyword kind
directly, so `error CheckoutError { ... }` still opens an `error`
declaration, not a reference to a variable named `error`.

Other reserved words (`if`, `return`, `match`, `atomic`, `concurrent`,
`retry`, `use`, `as`, `in`, `requires`, `and`, `or`, `not`, `limit`,
`attempts`, `command`, `idempotent`, `exit`, `http`, `cli`) are out of scope
for this decision. They appear inside expressions and statements, where
making them contextual would create real grammatical ambiguity (e.g. `if`
at the start of a statement) rather than just a name collision, and nothing
in the corpus has ever needed one of them as a name.

## Rationale

The corpus already treats `error` as ordinary business vocabulary — an error
value, a conversion's input — not as syntax. Renaming around the collision
in just the two affected files would dodge today's symptom but leave the
same trap for `model`, `rule`, `step`, or any other structural keyword the
next example happens to need as a field name. Scoping the keyword to
declaration-start position, instead, is a direct generalization of the
reasoning that already kept `Result`/`Ok`/`Err`/`None` out of the keyword
table, and fixes the class of bug rather than one instance of it.

Independently of which fix was chosen here, the loops that walk a
brace-delimited list of declarations (error variants, model fields, adapter
routes, response-mapping arms, match arms) had no forward-progress guard —
only `parseFile` and `parseBlock` did. Any parse failure inside one of those
that fails to consume a token hangs the parser permanently rather than
reporting an error. That's a correctness bug independent of the keyword
question, and is fixed alongside it by reusing the same before/after
position check already used at the file and block level.

## Consequences

- `compiler/parser`: new `atName` / `expectName` helpers, used at every
  former `expect(Ident)` call site that parses a name rather than a fixed
  spelling; `parsePrimary`, `parseArgument`'s named-argument lookahead,
  `parseStatement`'s binding lookahead, and `looksLikeArmStart`'s pattern
  lookahead all needed to recognize a structural keyword as a valid name
  token, not just `Ident`.
- `compiler/parser`: the five "list until closing brace" loops (error
  variants, model fields, adapter routes, response-mapping arms, match arms)
  now use the same forward-progress guard as `parseFile`/`parseBlock`.
- `docs/specification/grammar.md`'s keyword list needs a note that the eight
  structural keywords are reserved only at declaration-start position, not
  globally.
- If a future structural keyword is added, this ADR's scope — declaration-
  start-only — is the default to extend to it, not a one-off exception.
