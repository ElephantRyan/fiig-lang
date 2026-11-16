# ADR 0081: A Union Whose Members Read the Same JSON

Status: Accepted and implemented (2026-11-16; see Implementation notes)
Date: 2026-11-16
Related: ADR 0072 (the JSON shape of enums and `Result`; this is its open
question 1), ADR 0073 §8 (J1, J2), ADR 0080 (an enum's text form, and its
open question 1), charter principle 10.

## Context

A union in a JSON position is decoded by trying its members in the order
written, and the first that fits wins. When two members can read the same
JSON, that order silently decides what the value is:

```fiig
enum OrderError {
    NotFound(id: string)
    OutOfStock(sku: string)
}

enum CustomerError {
    NotFound(id: string)
    Suspended
}

workflow lookup(...) -> Result<Order, OrderError | CustomerError>
```

`{"variant": "NotFound", "id": "c9"}` is a customer's `NotFound` to the
sender and an `OrderError` to anyone decoding it, because `OrderError`
comes first. Swapping the two members in the signature changes what every
client reads, and nothing says so. The OpenAPI document is wrong too: it
writes the union as `oneOf`, which means the value matches exactly one
member, and this one matches both.

The encoder isn't confused (a value knows its enum), so the problem is
the reader: a fiig client (`json.decode`, `fiig.http`), a request body the
adapter decodes, or any client built from the OpenAPI document.

### Precedent

| | Two members that read the same tag | What happens |
|---|---|---|
| serde, `#[serde(untagged)]` | allowed | the first that fits wins; serde's docs warn about it |
| Pydantic, discriminated union | refused | an error when the model is defined: the tag maps to more than one choice |
| Jackson, `@JsonSubTypes` | refused | duplicate type ids are a configuration error |
| Kotlin serialization, sealed classes | refused | duplicate serial names fail when the serializer is built |
| OpenAPI `oneOf` | invalid data | a value matching two members fails validation |
| TypeScript, discriminated unions | can't narrow | a shared tag value doesn't narrow to either member |

Order-dependent decoding is the one behavior every tagged-union library
moved away from.

## Decision

### 1. J3: a union whose members read the same JSON is refused

Wherever a type meets JSON, the checker refuses a union in which two
members can read the same JSON value. That is J1's reach (ADR 0073 §8):
a request or response body, `json.decode`'s target, and `json.encode`'s
argument, at any depth (a field, a list element, a map value, a variant's
payload). The overlaps are:

- **Two payload-free enums with a text form in common** (`"NotFound"` in
  both, or a spelling that equals the other's variant).
- **Two enums with payloads sharing a variant's text form**, since both
  are objects told apart only by `variant`.
- **An enum with payloads and a `Result`** where a variant is spelled `Ok`
  or `Err`, `Result`'s own tags (ADR 0072 §4).

```
J3: OrderError | CustomerError: both read {"variant": "NotFound", ...},
so a reader can't tell which it is. Spell one (NotFound from json
"order_not_found"), or wrap them in an enum of your own
(enum LookupError { Order(error: OrderError)  Customer(error: CustomerError) }).
```

Both fixes already exist: ADR 0080's spelling, and an enum with payloads
whose variants say which side failed. The wrapper is the better one when
both enums belong to other packages.

### 2. Overlap is about text forms, so spellings count

Two variants named `NotFound` don't collide when one is spelled
`"order_not_found"`; two variants with different names do when one is
spelled as the other. J3 compares what a reader sees, which is the text
form (ADR 0080 §2), not the fiig name.

### 3. A declared spelling is more specific than `string`

A union of a payload-free enum and `string` also reads the same JSON: any
of the enum's spellings is a string too. That one is useful rather than
ambiguous, so it isn't J3, and it decodes by one rule, not by order:

- a JSON string that is one of the enum's spellings is the enum's
  variant, wherever `string` is written in the union;
- any other string is the `string`.

```fiig
model SubscriptionUpdated {
    id: string
    status: SubscriptionStatus | string
}
```

`"past_due"` is `SubscriptionStatus.PastDue`, and a status Stripe adds
next year arrives as its text instead of failing the whole delivery. That
answers ADR 0080's open question 1, a value a producer adds later, without
new syntax: a program says it accepts statuses it can't name by writing
`| string`, and a `match` handles the known ones and the text.

The OpenAPI document writes this union as `anyOf`, which allows a value to
match both members, rather than `oneOf`.

### 4. The corpus

- `invalid-programs/json_error.fiig` attests J3 with `OrderError |
  CustomerError`.
- `examples/event-inbox`'s `SubscriptionUpdated.status` becomes
  `SubscriptionStatus | string`, and its test sends a status the enum
  doesn't list (`"stalled"`), which arrives as text.

## Alternatives considered

**A. Keep first-match decoding, and document it** (serde's untagged). The
order of a union's members would decide what a client reads, invisibly.
Rejected: every tagged-union library above that lets you choose refuses
this.

**B. Name the enum in the JSON** (`{"enum": "CustomerError", "variant":
"NotFound"}`) when it's in a union. It makes one enum's JSON depend on
where it's used, the same error sent two ways, which ADR 0072 rejected
for a related reason. Rejected.

**C. Refuse `Enum | string` too.** Consistent with §1, but it takes away
the one way to receive a value a producer adds, and a declared spelling
being more specific than any string isn't a guess. Rejected; §3.

**D. Decode `Enum | string` by member order.** That's today's behavior,
and `string | Speed` would then never produce a `Speed`. Rejected for the
same reason as A.

## Consequences

- What a union's JSON decodes to no longer depends on the order its
  members are written in.
- A program can accept values a producer adds to an enum later, by
  writing `| string`.
- `docs/known-limitations.md` drops "two enums with a variant name in
  common" and "an enum value a producer adds later is refused".

## Open questions

1. **Two models in a union.** A model ignores members it doesn't declare
   (ADR 0073), so `{"id": "x"}` can fit two models that both have an
   `id`, and decoding still takes the first. Telling models apart needs
   either a declared tag or a structural rule, and no corpus program has
   such a union yet.
2. **Numbers.** `int | decimal` reads `3` either way. It's decided by
   member order today, as models are.

## Implementation notes (2026-11-16)

- **J3** is `checkJsonOverlap` (`compiler/checker/overlap.go`), a walk
  like J1's that compares each union's tagged members (payload-free enums
  as strings, enums with payloads and `Result` as `variant` tags) by text
  form. It runs where J1 does, `json.decode` and `json.encode` and a
  request body, and also on what a JSON route sends: the workflow's
  result under ADR 0026's default mapping, and each response arm's body.
  `JsonValue` reads any JSON and is never a tagged member.
- **Decoding** tries a union's members as written but `string` last
  (`stringLast`), which is all §3 needs, since J3 leaves no other overlap
  among enums. OpenAPI writes a union with `string` in it as `anyOf`.
- **Two gaps §3 depended on, found by trying the corpus's `match`.** A
  `match` over a union without `None` wasn't checked for exhaustiveness
  at all, and a bare name bound the whole union, so `unknown.length()` on
  the text was refused. Now, for a union with enum members, every
  variant, `None` if present, and the rest are required (A9 names the
  rest as `string (a bare name binds it)`), and a bare name binds the
  rest. The checker records the enums it doesn't catch on the pattern
  (`NamePattern.Excludes`), and the runtime holds to it whatever the
  arms' order, as it already did for `None`; a test writes the text arm
  first.
- **Corpus.** event-inbox's status is `SubscriptionStatus | string`, and
  the event carries `needsAttention`, a private helper of `readEvent`
  (a rule may not call another rule, B7) that matches every status and
  the text. `"stalled"` arrives as text and needs attention; a number is
  still a `Mismatch`. `invalid-programs/json_error.fiig` attests J3.

