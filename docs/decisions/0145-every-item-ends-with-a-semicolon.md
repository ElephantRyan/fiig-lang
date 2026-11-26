# ADR 0145: Every Item Ends with a Semicolon, Every Alternative with a Comma

Status: Accepted (2026-11-22). Implemented 2026-11-22 (62ccdbd, with the
relayout fix c7fad67). Amends ADR 0142 §1.
Amended by ADR 0147 (2026-11-23): a comma after the last alternative, or
before any closing bracket, is now a syntax error, and `fiig fmt` removes it.

## Context

ADR 0142 §1 made `;` end a statement, to stop two lines from silently
merging (`x = a` then `(b)` parsing as `x = a(b)`). It then exempted
everything that isn't a statement: imports, model fields, enum variants,
adapter routes and wires, and match and response-mapping arms. That
exemption was never weighed. It isn't among 0142's alternatives. It was
written into §1 as if it followed from the problem, and it came in with the
ADR when the ADR was accepted.

It doesn't follow from the problem. It holds only because of how the grammar
happens to look today, and 0142's own open questions say so:

> `match` arms and route arms stay newline-separated. The merge can't happen
> there today, because no pattern starts with `(` or `-`. If literal
> patterns ever allow negative numbers, arms need a separator too.

The same is true of a wire field's default (`= expression`), the one
expression a declaration body holds today. Its safety depends on the next
line starting with a name. Each time the grammar grows, the question comes
back. 0142 also says its rule is "one rule with no list to remember", and
then gives a list of six exempt kinds.

## Decision

Nothing in a body is separated by a newline alone. A body holds either
*items*, which end with `;`, or *alternatives*, which are separated by `,`.

### 1. An item ends with `;` unless it ends with `}`

An item is a thing a body holds, each one standing for itself:

- a `use` import
- a statement (as in ADR 0142 §1)
- a model field or embedded model
- a wire field, a single wire, and an adapter's `wire` reference
- an interface member
- an adapter route
- a `serve` block's adapter reference (replacing its optional `,`)
- a faked config's field in a test

Every item ends with `;`, except one whose last token is a `}` it opened.
That covers a declaration (`model X { ... }`), a block statement, a route
with a response mapping, a test's `fake` group, and a `private { }` block.

The last item before a closing `}` takes its `;` too, as Java's last field
does. A rule with an optional case is a rule with two forms.

### 2. Alternatives are separated by `,`

An alternative is one of a set, of which exactly one applies:

- an enum variant, with or without a payload or a row of constants
- a `match` arm
- a response-mapping arm, and a stream, schedule, or startup disposition arm

These are separated by `,`, as Java writes enum constants and Rust writes
variants and arms. An arm whose value is a `{ ... }` block takes its `,`
like any other, so the rule has no exception for it. A trailing `,` after
the last alternative is allowed, as in every comma list (§3), and `fiig
fmt` writes one when the alternatives are one per line.

The split follows what the body means. Items are a sequence: each field,
import, or statement is there in addition to the others. Alternatives are a
choice: a value is one variant, and a match takes one arm. Java and Rust
draw the same line.

### 3. Delimited lists keep commas

A list between `(` `)`, `[` `]`, or `<` `>` (parameters, arguments, type
arguments, a list literal, an enum header, a variant's payload or
constants) keeps `,`, with an optional trailing comma, as today. Its closing
bracket already ends it, so a newline inside it can't merge anything. An
inline record type, `{ id: string, total: int }`, is a type written in one
place, not a body of items, and keeps `,` too.

### 4. A header is not an item

A declaration's head runs up to its `{`. That includes a gateway's
`requires` and `idempotent`, a route's `consumes` and `produces` clauses,
and a wire group's `when (...)`. The head takes no `;`, wherever its
clauses break across lines. A route with no response mapping ends with `;`
after its last clause.

### 5. Migration

As in ADR 0142 §5, `fiig fmt -w` writes the `;` and `,` into old files, and
the parser accepts both forms until the second step. After that, a missing
separator is a syntax error that names the fix: "an item ends with `;`" or
"alternatives are separated by `,`".

## Examples

```fiig
use acme.payments;
use acme.stripe.Stripe;

model Customer {
    id: string;
    email: string from json "email_address";
    middleName: string | None;
    Audit;
}

enum Region { US, EU, APAC }

enum DeductionSource(key code: string, layout: FieldLayout) {
    Acme(code: "ACME", layout: FieldLayout.FixedWidth),
    Globex(code: "GLBX", layout: FieldLayout.Csv),
}

interface PaymentProvider {
    charge(request: ChargeRequest) -> Result<string, string>;
}

wire Sessions {
    ttlSeconds: int from env "SESSION_TTL" = 24 * 60 * 60;
    signingKey: Secret from env "SESSION_KEY";
}

adapter http libraryApi {
    wire sessions.me;

    GET "/books/{id}" -> readBook(id: string) produces "application/json";

    POST "/books" -> addBook(input: NewBook, me)
      consumes "application/json"
      produces "application/json" {
        Ok(book) -> 201 book,
        Err(BookError.Duplicate) -> 409,
    }
}

application library {
    serve api { libraryApi; }
}

gateway charge(orderId: string, amount: int) -> Result<string, string>
  requires PaymentGateway {
    result = PaymentGateway.charge(orderId, amount);
    return match (result) {
        Ok(reference) -> Ok(reference),
        Err(e) -> {
            log(e);
            return Err(e.message);
        },
    }
}

test signsIn {
    fake SignInConfig { authMode: AuthMode.Abs; }
}
```

## Alternatives

- **Keep ADR 0142's exemptions.** Nothing mis-parses today. Rejected:
  that is true only for the current grammar, 0142 already names the change
  that breaks it, and the exemption list is the thing 0142 said it avoided.
- **`;` everywhere, alternatives included.** One separator. Rejected on the
  owner's review of the examples: `enum Region { US; EU; APAC; }` and
  `Ok(book) -> 201 book;` read as a sequence of statements, which a set of
  alternatives isn't.
- **`,` everywhere, as Rust does for fields.** Rejected: an import or a
  field isn't one of a set, and Rust itself ends items with `;`.
- **`;` optional before a closing `}`.** Saves a keystroke on one-line
  bodies. Rejected: it gives the last item two spellings. Alternatives take
  an optional trailing `,` because every comma list in fiig does.

## Consequences

- Every source file in the corpus, both Go modules' embedded fiig, the
  stdlib packages, the IntelliJ plugin's samples, and the docs change.
  `fiig fmt -w` does it. The other lane's open branches need the same
  rewrite before they merge, as with 0142.
- ADR 0142's open question on arm separators is closed.
- The grammar's repetitions in §2, §4, and §5 become `{ item ';' }` (apart
  from items ending in `}`) and `alternative { ',' alternative } [ ',' ]`.

## Settled in review

- **A `match` used as a statement**, and the stream, schedule, and startup
  arms, take `,` too, as §2 says: each is still a choice of one arm. A
  block arm takes its `,` like any other (`},`). The owner confirmed both.
- **Editors** colour `;` as plain text. The IntelliJ plugin had given it
  the platform's semicolon colour, which the Dark scheme shares with
  keywords, so every `;` looked like an error.
