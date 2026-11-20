# Grammar sketch

Status: Milestone 0 complete — still a living sketch, extended as later milestones add syntax

Covers backlog items 4 and 5 (`fiig-lang.md` §6): the lexical grammar and the
grammar for models, enums, rules, conversions, activities, gateways,
workflows, and adapters — plus enough of the expression/statement grammar to make the
`examples/` corpus parseable, since Milestone 1's exit criteria requires every
Milestone 0 example to parse.

This is a sketch, not a spec: every production is backed by at least one
example file (cited inline), and nothing is here that the examples didn't
require, per backlog item 3's "resolve syntax only where the examples
require it." Where the examples left a question open, it's called out under
"Known gaps" instead of guessed at.

Notation: EBNF. `'...'` is a literal token, `[...]` is optional, `{...}` is
zero-or-more, `|` is alternation, `(...)` groups.

## 1. Lexical grammar

```ebnf
identifier   ::= letter (letter | digit | '_')*
letter       ::= 'a'..'z' | 'A'..'Z'
digit        ::= '0'..'9'

integer-literal ::= digit+ | '0' ( 'x' | 'X' ) hex-digit+
hex-digit       ::= digit | 'a'..'f' | 'A'..'F'
float-literal   ::= digit+ '.' digit+ [ exponent ] | digit+ exponent
exponent        ::= ( 'e' | 'E' ) [ '+' | '-' ] digit+

string-literal  ::= '"' string-char* '"'
string-char     ::= any-char-except('"', '\', '$')
                   | escape-sequence
                   | interpolation
escape-sequence ::= '\' ('"' | '\' | 'n' | 't' | 'r' | '$')
interpolation   ::= '${' expression '}'

line-comment ::= '//' any-char-except-newline* newline
```

- Only `//` line comments are attested (`examples/**/*.fiig`, every required
  file). No block-comment syntax has been needed yet.
- `\r` is CR and `\$` is a literal `$`, so `\${` writes `${` without
  starting an interpolation (ADR 0061). A raw line break inside a literal is
  always LF: CR LF in the source reads as LF, and a CR on its own is a lex
  error, so a string's value doesn't depend on the file's line endings.
- String interpolation is attested once
  (`customer-service/workflows/send_welcome_email.fiig`): `"...${customer.name}..."`.
- `true`/`false` are boolean literals (ADR 0041), resolved the same way
  `None` is: a bare `Ident` special-cased by name, not a dedicated token.

### Keywords

Structural keywords — reserved, part of the grammar below:

```
model  enum  rule  conversion  activity  gateway  workflow  adapter
interface  capability  formulas  formula  config
requires  use  as  return  if  not  match  transaction  concurrent  retry
in  limit  attempts  command  preserveOrder  private
```

`interface` and `capability` (ADR 0043) join the declaration-start keywords
below: reserved only where a declaration begins, ordinary names elsewhere.
So do `formulas` and `formula` (ADR 0049); `formula` opens a member inside a
`formulas` group, and is a name everywhere else. So does `config` (ADR
0071), so `config` stays usable as a parameter or binding name.
So do the adapter kinds `http` and `cli`, which are reserved only right after
`adapter` — a package alias needs them as names, since `use acme.http`
binds `http`. Any keyword is also accepted as one segment of a `use` path,
where a segment is never ambiguous.

The first eight (`model enum rule conversion activity gateway workflow
adapter`; `value` was removed by ADR 0055) are reserved only at declaration-start position — immediately after imports,
where the parser decides which kind of declaration is beginning
(`docs/decisions/0018-contextual-structural-keywords.md`, written when the
ninth keyword was still spelled `error` — ADR 0040 renamed it to `enum`
without changing this policy). Anywhere else a name is expected — field
names, parameter names, local bindings, patterns, bare identifier references
— the same spelling parses as an ordinary identifier, though none of these
eight currently has an attested case of that (the one that did, `error`, is
no longer one of them: it's an ordinary, non-reserved identifier now, used
as both a parameter and field name and as a bare pattern/identifier in
`checkout_error.fiig` and `customer_error_to_http_response.fiig`). The
remaining keywords above are reserved everywhere a parameter, local,
pattern, or declaration is named. `private` (ADR 0054) is one of these: it
opens a file's private block, and a helper can't be named `private`.

**Any keyword may name a model or enum payload field** (ADR 0073 §7a): a
field's name is read only in its declaration, after a `.`, and as a named
argument, where no keyword means anything else, so `limit: int`,
`page.limit`, and `Page(limit: 10)` all parse. In a `concurrent` header,
`limit:` is still the clause.

Not keywords — ordinary identifiers resolved against the standard prelude,
distinguished only by convention (capitalized) and by appearing as pattern
constructors:

```
Result  Ok  Err  BatchResult  List  Map  None  Connection  SocketError  Rounding
```

`Connection` is opaque: no fields, methods or constructor, and not
comparable. `SocketError` is an ordinary enum (`HostNotFound`, `Refused`,
`Unreachable`, `Reset`, `Closed`, `Tls(reason: string)`, and `TimedOut`, a
deadline the program passed, ADR 0074 §5) declared in the
prelude rather than in a package. Both are ADR 0060's. `Rounding` is ADR
0064's, an enum of the six rounding modes decimal's `math.round` and
`math.divide` take.

Since every file sees these names, along with the primitive type names and
the `math` group, a declaration may not take one: `model Result`, `enum
None`, or `formulas math` is K2. Without the check the declaration was
silently unreachable, or, for `SocketError`, silently replaced the type the
Socket primitive returns.

This split matters for backlog item 9 (registry contract cards): if `Result`
and friends are prelude identifiers rather than syntax, they need registry
entries like any other exported declaration, rather than being special-cased
in the parser. The prelude `math` group's members have them
(`registry.Prelude`, which `catalog-cli find` and `explain` search); the
prelude types above don't yet.

`copy` (lowercase, a call rather than a pattern constructor) is also a
prelude identifier, not syntax: `copy(base, field: value, ...)` produces a
value of `base`'s own model type, with every field not named among the
remaining arguments taken unchanged from `base`
(`docs/specification/ownership.md`). Attested:
`customer-service/rules/rename_customer.fiig`.

## 2. Files and declarations

```ebnf
file ::= { import } declaration [ private-block ]

import ::= 'use' package-path ['as' identifier]
package-path ::= identifier { '.' identifier }
```

A package's version is never written at the import site — it lives in
that package's manifest (ADR 0032, `compiler/manifest`), not in source.
Attested: `use company.time as businessTime`, `use standard.time as systemTime`
(`time-library/rules/compute_follow_up_date.fiig`) — one aliased, one not,
confirming `as` is optional. There is no wildcard form (ADR 0050 §2.4):
`use standard.time.*` (`invalid-programs/wildcard_import_error.fiig`) is a
parse error, tagged G2, because every cross-package reference is qualified
and a wildcard would have nothing to be the degenerate case of.

Per §3.9, exactly one declaration follows the imports in a file. It is the
file's one public entry (ADR 0054). A second declaration is a parse error,
tagged G1, at the second declaration; the parser still parses it so the rest
of the file is checked (the `invalid-programs/` fixtures pack several into
one file on purpose).

```ebnf
declaration ::= model-decl | enum-decl
              | rule-decl | conversion-decl | activity-decl | gateway-decl
              | workflow-decl | adapter-decl
              | interface-decl | capability-decl | formulas-decl
              | config-decl

private-block  ::= 'private' '{' { private-member } '}'
private-member ::= helper-decl | model-decl
helper-decl    ::= identifier params '->' type block
```

A `private-block` (ADR 0054) holds members visible only inside their file. It
may follow only a declaration with code in it: a rule, conversion, activity,
gateway, or workflow. After a model, enum, or adapter it is a parse error. A
helper has no kind keyword; it takes its file's kind. Writing one anyway
(`rule helper(...)`) is reported and the keyword skipped. An enum inside the
block is a parse error until ADR 0054 §2's follow-up. Attested:
`order-processing/rules/shipping_cost.fiig` (one helper, one private model).

## 3. Types

```ebnf
type ::= named-type { '|' named-type }
named-type ::= qualified-name [type-args] | record-type
qualified-name ::= identifier { '.' identifier }
type-args ::= '<' type { ',' type } '>'
record-type ::= '{' field { ',' field } '}'
```

Attested forms across the corpus:

- Plain named type: `Customer`, `string`, `int`
- Qualified named type: `systemTime.Instant`
  (`time-library/rules/compute_follow_up_date.fiig`)
- Generic type: `List<string>`, `Result<Customer, CustomerError>`,
  `BatchResult<string, Customer, CustomerError>`
- Union with `None`: `fiig-lang.md` line 79, `Customer | None`. Every
  gateway reading from `Database.queryOne` writes one on a typed binding,
  which gives the query its row type (ADR 0082 §5: `row: Customer | None =
  Database.queryOne(...)` in `get_customer.fiig`) — and §3.5 ("Model fields are required unless their type includes `None`")
  already settles that this is a normal, directly-writable type, not just an
  internal artifact of certain capability calls. A model with an optional
  field, e.g. `middleName: string | None`, should parse under this same
  `type` production.
- Anonymous record type: `{ id: string, total: int }`
  (`invalid-programs/type_error.fiig`, `summarizeOrderShape`) — attested only
  as the *rejected* case (a structural type where §3.3 requires a named one
  in a public signature). `record-type` reuses `field` from §4 below; nothing
  in the corpus shows this type used legally (i.e. as a local anonymous
  record, which §3.3 does permit).

## 4. Declarations

```ebnf
model-decl ::= 'model' identifier '{' { model-field } '}'
model-field ::= field | type-name          // a bare type name embeds that model (ADR 0045)
field      ::= field-name ':' type [ 'from' 'json' string-literal ]   // ADR 0073 §7b; `from`, `json` contextual
field-name ::= identifier | keyword                                  // ADR 0073 §7a

enum-decl    ::= 'enum' identifier [ enum-header ] '{' { enum-variant } '}'
enum-header  ::= '(' enum-field { ',' enum-field } ')'      // ADR 0046 §4
enum-field   ::= [ 'key' ] identifier ':' type               // `key` is contextual
enum-variant ::= identifier [ '(' [ field { ',' field } ] ')' ] [ spelling ]   // no header: a payload
               | identifier [ arguments ] [ spelling ]                         // header: a row of constants
spelling     ::= 'from' 'json' string-literal                                   // ADR 0080 §1; contextual
```

A variant's `from json "text"` is its spelling, its text form wherever it
meets text (ADR 0080 §2): JSON both ways (a payload-free enum is the
string, an enum with payloads names its variant with it), a route
argument read from a path, query, header, cookie, or command line, a
config value, and OpenAPI's `enum`. A variant without one is spelled as
its name. Two variants of one enum with one spelling are J2. Attested:
`examples/event-inbox/models/subscription_status.fiig`.

On an enum with a header, a variant's parentheses hold `name: value`, one
constant per header field, in the same form as a call's named arguments; on
an enum without one they hold `name: Type`, a payload. Which applies is
decided by the header, so the two never mix (ADR 0046 §6). A headed enum's
type name is also callable as a key lookup, `DeductionSource(code: raw)`,
returning `DeductionSource | None` (§5), and its header fields are read as
`source.code` (§4b). Attested: `examples/deductions/models/deduction_source.fiig`.

Attested: `examples/customer-service/models/customer.fiig` (multi-field
model), `errors/customer_error.fiig`
(three variants, each carrying at least one field). A zero-field variant
(no parens at all, parsing as a bare name rather than a call-shaped form) is
attested too — `errors/checkout_error.fiig`'s `OutOfStock`, constructed (not
matched) in `workflows/checkout.fiig`, and `order-processing/models/region.fiig`'s
`US`/`EU`/`APAC` (ADR 0040's first enum with no error-handling role at all),
both constructed and matched exhaustively in
`order-processing/rules/region_display_name.fiig`. A typed error
(`Result<T, E>`'s `E`) is an ordinary `enum-decl`, not a separate production
— ADR 0040 folded the previous `error-decl` into this one.

```ebnf
rule-decl       ::= 'rule' identifier params '->' type block
conversion-decl ::= 'conversion' identifier params '->' type block
activity-decl   ::= 'activity' identifier params '->' type block
gateway-decl    ::= 'gateway' identifier params '->' type 'requires' identifier [ 'idempotent' ] block
workflow-decl   ::= 'workflow' identifier params '->' type block

params ::= '(' [ param { ',' param } ] ')'
param  ::= identifier ':' type

interface-decl   ::= 'interface' identifier '{' { interface-member } '}'
interface-member ::= identifier params '->' type
capability-decl  ::= 'capability' identifier '{' { gateway-decl } '}'

formulas-decl  ::= 'formulas' identifier '{' { formula-member } '}'
formula-member ::= 'formula' identifier params '->' type [ block ]

config-decl  ::= 'config' identifier '{' { config-field } '}'
config-field ::= identifier ':' type 'from' 'env' string [ '=' expression ]   // `from` and `env` are contextual
```

`config-decl` is ADR 0071's: values a deployment supplies through the
environment, each read and checked once, before the program starts, and
fixed for the life of the process. A config is capitalized and read as
`Name.field`, only in an activity or a gateway; a rule, conversion,
formula, or workflow takes the value as a parameter (B10). A field is
`string`, `int`, `float`, `decimal`, `bool`, an enum (read by variant
name), or `Secret`, optionally `| None` (V1). The variable's name is a
string with no interpolation. A default is a constant, constant arithmetic
included (`= 24 * 60 * 60`), and used when the variable is unset or empty;
a `Secret` has none, since it would put the secret in the source. A field
with neither a default nor `| None` is required. Another package's config
is imported by path tail, like a capability group: `use
acme.postgres.Postgres`. Attested: `accounts/config/sessions.fiig`,
`acme-stripe/config/stripe_account.fiig`.

`Secret` (ADR 0071 §4) is a prelude type only config produces. It can't be
interpolated (S1), compared with `==` (M1), or reach a workflow's
signature or a response body (S1). It prints as `Secret(NAME)`, naming its
variable. `.matches(candidate: string) -> bool` compares in constant time,
anywhere; `.reveal() -> string` gives the text, only in a gateway (S2).

`formulas-decl` is ADR 0049's: a group of pure formulas, which §3.9 counts as
its file's one declaration. Every kind may call a formula, a rule included,
and a formula calls only formulas (`fiig-lang.md` §3.2's composition matrix). Outside the group a
member is always called through the group, `text.isEmailShaped(email)`, and
only in a file that imports the group by path tail, even when it is in the
same package (ADR 0049 §2.5): `use customer.service.text`. Inside the group,
members call each other unqualified; that is the one place a formula's bare
name resolves. Group and member names are lowercase. A formula may not
return a `Result` (K3) or have a `requires` clause (K4; the parser accepts
one only so the checker can say why). Every member written in source has a
block; one without is K5. Attested: `customer-service/formulas/text.fiig`,
called from `customer-service/rules/validate_email.fiig`, and
`order-processing/formulas/shipping.fiig`.

The prelude `math` group (ADR 0049 §2.4), like `json` (ADR 0073) and
`crypto` (below), is a group every file sees without a `use`, so no import
alias, parameter, or binding may be named `math` (G4). It has no source. Its formulas are native and listed in
`compiler/types/math.go`: the crossings `toFloat`, `toDecimal`, and
`floorToInt`, `ceilToInt`, `truncToInt`, `roundToInt`, `roundHalfEvenToInt`;
`floor`, `ceil`, `round`, `trunc`, `sqrt`, `exp`, `log`, `log10`, the
trigonometric six and `atan2`, `pow`; `abs`, `min`, `max`, `floorMod`;
`addOrNone`, `subOrNone`, `mulOrNone`, `divOrNone`; `closeTo` and `isZero`;
and for decimals (ADR 0064) `divide` and `allocate`. Many have a form per
numeric type, and a call picks the form its arguments bind to and whose
types they fit, never mixing types (A1). Decimal's `round` and `divide`
take `places:` and `rounding:` (a prelude `Rounding`: `HalfUp`,
`HalfEven`, `Up`, `Down`, `Ceiling`, `Floor`), which float's `round` does
not. Named-only parameters must be passed by name: `closeTo`'s `relative`
and `absolute`, `places` and `rounding`, and `allocate`'s `parts`. Its
constants `pi`, `tau`, `e`, `minInt`, `maxInt`, and
`maxFloat` are written `math.pi`, without parentheses. They are untyped
constants (§2.7), so `math.pi * 2` folds exactly and `math.maxInt + 1` is a
compile error.

The prelude `crypto` group (ADR 0075) is listed in
`compiler/types/crypto.go`. It takes and returns `bytes`, never `string`:
`sha256(data)`; `hmacSha256(key: Secret | bytes, data)`, whose `Secret` key
is its value's UTF-8; `pbkdf2Sha256(password: Secret | bytes, salt,
iterations)`, 32 bytes of RFC 8018 PBKDF2 for protocols that specify it,
such as SCRAM (ADR 0085 §7), faulting outside 1 to 10,000,000 iterations;
`equal(a, b)`, which compares in time that depends
only on the lengths, for a signature (`==` stops at the first differing
byte); `hashPassword(password: string) -> string`, Argon2id at 19 MiB, 2
passes, 1 lane, written as a PHC string (`$argon2id$v=19$m=19456,t=2,p=1$...`);
and `passwordMatches(password, stored) -> bool`, which reads the parameters
from `stored` and faults on one that isn't such a string or asks for more
than 1 GiB, 10 passes, or 16 lanes. Every member is pure but
`hashPassword`, whose salt comes from the `Random` provider: like `Random`,
only an activity or a gateway may call it (B10). Attested:
`accounts/gateways/` (`add_account.fiig`, `find_account_by_key.fiig`) and
`accounts/conversions/token_hash.fiig`.

`interface-decl` and `capability-decl` are ADR 0043's. An interface lists the
operations a capability group must have to fulfill it; its name is a type,
and a value of it is a group chosen at runtime. A capability group is named
gateways that other gateways reach through `requires Name` and that
activities and workflows call as `Name.op(...)`; used as a bare value
(`return Stripe`), it is a provider. A group is imported by naming it as the
tail of its package's path — `use acme.http.Client` binds `Client` — and a
`requires` clause names a primitive capability or a capability group, local
or imported. Fulfillment is declared only in a package manifest's
`provides` map, never in source. Attested: the `examples/acme-*` packages
and `examples/checkout-app`.

`activity-decl` (ADR 0030, widened by ADR 0044) is identical in shape to
`rule-decl`/`conversion-decl` — no `requires` clause exists in its grammar at
all, since only a gateway names the capability it consumes (an activity
reaches capabilities through the gateways it calls). Attested:
`customer-service/activities/validate_new_customer.fiig`.

`gateway-decl` (ADR 0031, renamed from `step-decl`) is attested by every
file in `rules/`, `conversions/`, `gateways/`, `workflows/`. The `requires`
clause takes exactly one capability name in every attested gateway, and
`docs/decisions/0010-steps-require-one-capability.md` confirms that's by
design, not just an unexercised gap — a gateway is scoped to one
capability, full stop. ADR 0079 adds one more thing a `requires` may name,
still one: an imported package, by its lowercase alias (`requires
payments`), whose workflows the gateway may then call. That is how one
domain of a modular monolith asks another to do something
(`docs/modular-monolith.md`); attested by
`shop-orders/gateways/charge_order.fiig`. The optional `idempotent` modifier
(`docs/decisions/0014-idempotency-and-explicit-retry.md`) is attested by
`get_customer.fiig`; every other gateway omits it, including
`chargePayment`, which is deliberately non-idempotent
(`invalid-programs/retry_error.fiig` depends on that).

```ebnf
adapter-decl  ::= 'adapter' adapter-kind identifier '{' { route } '}'
adapter-kind  ::= 'http' | 'cli'
route         ::= http-route | cli-route
http-route    ::= http-method string-literal '->' route-call { media-clause } [ response-mapping ]
http-method   ::= 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE' | 'QUERY'
cli-route     ::= 'command' string-literal '->' route-call [ response-mapping ]
route-call    ::= identifier '(' [ route-arg { ',' route-arg } [ ',' ] ] ')'
route-arg     ::= identifier ':' type [ 'from' ( 'header' | 'cookie' ) string-literal ]   -- ADR 0069 §1; read from text, so string, int, float, decimal, bool, or a payload-free enum, optionally | None (W4, ADR 0080)
media-clause  ::= ( 'consumes' | 'produces' ) string-literal                              -- ADR 0069 §5

response-mapping ::= '{' response-arm { response-arm } '}'
response-arm     ::= pattern '->' response-value
response-value   ::= integer-literal [ expression ]           -- HTTP: status code [ body ]
                    | 'exit' integer-literal [ expression ]    -- CLI: exit code [ body ]
                    | expression                               -- e.g. a call to a shared conversion
```

Attested: `adapters/http_customer_api.fiig` (`GET "/customers/{id}" -> readCustomer(id: string)`),
`adapters/cli_customer.fiig` (`command "customer get {id}" -> readCustomer(id: string)`),
`adapters/http_customer_create_api.fiig` (`POST "/customers" -> createCustomer(input: NewCustomer)`),
`adapters/http_customer_email_api.fiig` (`PATCH "/customers/{id}?newEmail={newEmail}" -> changeCustomerEmail(id: string, newEmail: string)`),
`adapters/http_customer_query_api.fiig` (`QUERY "/customers/lookup" -> lookupCustomer(criteria: CustomerLookup)`).

Per `docs/decisions/0009-adapter-path-parameter-binding.md` and
`docs/decisions/0021-adapter-request-binding.md`, every call-argument
parameter in a route's target binds from exactly one of three sources:

- A path placeholder (`{id}` in `"/customers/{id}"`) or, for HTTP routes
  only, a query placeholder (`{newEmail}` in `"?newEmail={newEmail}"`) —
  both bind by name match against the call's argument list, the only
  difference being which side of the `?` the placeholder falls on. The
  placeholder notation (`{id}`) is unified across both regions and both
  adapter kinds — chosen to match OpenAPI's own path-templating syntax
  ahead of Milestone 5's endpoint generation, rather than each context
  picking its own idiom.
- The request body, for `POST`/`PUT`/`PATCH`/`QUERY` HTTP routes only:
  whichever single call argument isn't claimed by a path or query
  placeholder binds from the decoded body. `GET`/`DELETE` routes and every
  CLI route have no body — every argument must be claimed by a placeholder.

This is a checker-enforced rule (`checker.checkRouteBinding`), not just
example convention: an unmatched placeholder, an unclaimed argument on a
route with no body, or more than one unclaimed argument on a body-carrying
route are all compile errors. A body-bound argument must also be typed as a
`model` — binding it to a primitive type is bug class A7
(`docs/specification/bug-classes.md`), since a primitive has no fields
to structurally decode against; see `invalid-programs/type_error.fiig`
(`customerEmailApiBroken`).

A route argument may instead name the header or cookie it is read from
(ADR 0069 §1): `token: string from header "Authorization"`, `session:
string | None from cookie "sid"`. Such an argument is never the body. It
has a path or query value's types (`string`, `int`, `float`, `decimal`,
`bool`), optionally `| None`, which receives `None` when the header or
cookie is absent; a required one that is absent is a 400. A repeated
header is read as its values joined by `", "`, a repeated cookie as the
first. Header names match case-insensitively, cookie names exactly. `from`,
`header`, and `cookie` are contextual here only. Each argument's written
type must be its workflow parameter's type.

Every HTTP route declares the content type it sends with `produces`, and a
route that binds a body declares the type it reads with `consumes` (ADR
0069 §5); a request with another `Content-Type` is a 415. A JSON type
(`application/json`, or any `+json`) encodes any body as JSON. Any other
type sends a `string` (as UTF-8, with `; charset=utf-8` for `text/*`) or
`bytes` body, and a route that consumes one binds the body raw as a
`string` or `bytes` argument, so A7 applies only to JSON bodies. A JSON
route binds its body to a model, decoded, or to `bytes`, as it arrived and
unparsed, for a signature checked before `json.decode` (ADR 0077). The
formatter puts each clause on its own line under the route, consumes
first, and the response mapping's `{` on its own line after them. CLI
routes have neither clause.

`response-mapping` (`docs/decisions/0017-adapter-response-mapping.md`) is
attested by `adapters/http_customer_api_with_mapping.fiig`. It reuses
`pattern` from §5's `match` rather than inventing a second pattern grammar,
but `response-value`'s first two alternatives (`200 customer`,
`exit 0 customer`) are bespoke shorthand, not ordinary `expression`s —
juxtaposing a literal and an expression with no operator between them
doesn't fit the `expression` grammar below. ADR 0069 §3 kept the
shorthand: `200 customer` means `HttpResponse(status: 200, body:
customer)`, where `HttpResponse<B>` is a prelude generic (`status: int`,
`body: B | None`, `headers: Map<string, string> | None`, `cookies:
List<Cookie> | None`), constructed like a model with every field but
`status` optional, and `B` inferred from `body` or taken from the expected
type. An `expression` arm must produce an `HttpResponse`, checked as a
type. `Cookie` (with secure defaults for every left-out field) and `enum
SameSite { Strict, Lax, AnySite }` are prelude declarations too, so a
program can't declare any of the three names (K2).

## 5. Statements and expressions

```ebnf
block ::= '{' { statement } '}'

statement ::= binding
            | if-statement
            | for-statement
            | while-statement
            | break-statement
            | continue-statement
            | return-statement
            | match-statement
            | transaction-block
            | expression

binding         ::= identifier [ ':' type ] '=' expression    -- ADR 0053: typed form declares a reassignable local
if-statement    ::= 'if' expression block { 'else' 'if' expression block } [ 'else' block ]
for-statement   ::= [ identifier ':' ] 'for' loop-name [ ',' loop-name ] 'in' expression block   -- ADR 0053; two names over a Map, ADR 0065 §7
loop-name       ::= identifier | '_'
while-statement ::= [ identifier ':' ] 'while' expression block                -- ADR 0059
break-statement ::= 'break' [ identifier ]                                   -- label on the same line
continue-statement ::= 'continue' [ identifier ]
return-statement::= 'return' [ expression ]
```

A plain `name = expression` names a reassignable local only when `name` was
declared with a type; a name declared without one is immutable, and
reassigning it is an error (ADR 0053). Iteration in a `for` is over a
`List<T>`, sequential, in list order. A `while` evaluates its `bool`
condition before every iteration, including the first, and is legal in
every declaration kind; `while true` is the infinite form (ADR 0059).
`break`, `continue` and labels apply to either loop. They may not cross a
`concurrent` or `transaction` body.

Attested: bindings (`row: Customer | None = Database.queryOne(...)`), bare `return` (`Ok(...)`
value), and `return` of a bare call (`return getCustomer(id)`). Every `if`
in the corpus (`validate_new_customer.fiig`) is a single-sided guard with no
`else` — `else`/`else if` are decided
(`docs/decisions/0013-if-else-chains.md`) but unattested, the same status as
the comparison/boolean operators above.

A block's final statement, if it's a bare `expression` or a
`match-statement` rather than a `return-statement`, implicitly produces a
value as the enclosing declaration's return value — `return expr` as the
last statement is always equivalent, explicit sugar for the same thing,
not a different mechanism. For a final `match-statement` specifically,
each arm's own body is subject to this same rule recursively: an arm whose
body is a bare expression produces that expression's value, and an arm
whose body is a block applies the rule again to *that* block's own final
statement — this is how `get_customer.fiig` and nearly every other gateway
actually return their result, matching on a capability call and letting
each arm produce the value directly, and is at least as common in the
corpus as a bare final expression (narrowly missed in an earlier pass of
this note, which only covered the latter). This reconciles two otherwise-
inconsistent-looking corpus conventions: `read_customer.fiig` and
`read_customer_with_retry.fiig` both write `return getCustomer(id)` /
`return retry(...): getCustomer(id)` explicitly, while
`retrieve_customers.fiig` and `summarize_customers.fiig` both end their
block with a bare `concurrent(...): ...` and no `return` at all — both
styles are legal under this rule, and this is a documentation
clarification of already-attested behavior, not a new decision (no ADR
needed). `if`/`else` as a final implicit-return position is *not* covered
by this rule — unattested anywhere in the corpus, left unguessed rather
than assumed symmetric with `match`. Whether a non-final bare expression statement (one that isn't the
block's last statement) is ever meaningful — as opposed to being flagged by
bug class F1 when its value is an unhandled `Result` — is a checker
question, not addressed here.

### Match

```ebnf
match-statement ::= 'match' expression '{' match-arm { match-arm } '}'
match-arm       ::= pattern '->' ( expression | block )
pattern         ::= '_' | qualified-name [ '(' [ pattern { ',' pattern } ] ')' ]
                  | binary-pattern
binary-pattern  ::= '<<' [ pattern-segment { ',' pattern-segment } [ ',' ] ] '>' '>'
pattern-segment ::= ( identifier | '_' | integer-literal | string-literal ) ':' encoding
encoding        ::= identifier [ '(' expression ')' ]
```

`None` is just an unqualified `qualified-name` here — it's a prelude name,
not special pattern syntax, matching the "not a keyword" call in §1. A bare
identifier pattern (e.g. `found`) binds the whole matched value, which is how
the corpus destructures a `T | None` without an `Option`/`Some` wrapper
(§3.4) — `get_customer.fiig` and `check_email_unique.fiig` were both
corrected to this form after an earlier draft mistakenly used `Some(r)`,
which would have implied an `Option<T>` type the language doesn't have.
`Err(OrderError.InventoryUnavailable(_))` (ADR 0006's `checkout` example)
qualifies a constructor by its error type, reusing the same qualified-name
shape already used in construction (`CustomerError.NotFound(id)`) — this is
why `pattern` uses `qualified-name` rather than a bare `identifier`.

Attested arm bodies: single expression (`found -> Ok(Customer(...))`) and
block (`Err(_) -> { refundPayment(...); releaseInventory(...); return Err(...) }`
in `place_and_ship_order.fiig`).

A zero-field variant's pattern (e.g. `Region.US`) is the parenless form of
`qualified-name [ '(' ... ')' ]` above — a two-segment `qualified-name` with
no trailing parens, attested by `order-processing/rules/region_display_name.fiig`
matching every `Region` variant exhaustively (ADR 0040).

Against a union of enums and something else, such as `SubscriptionStatus |
string` (ADR 0081 §3), a bare name takes only the something else and is
typed as it: the enums' variants need arms of their own (A9 names any
missing, and the text), and a variant never reaches the bare name
whichever order the arms are in, as `None` never does. Attested:
`examples/event-inbox/rules/read_event.fiig` (`needsAttention`).

### Calls, field access, error propagation

```ebnf
expression      ::= or-expression
or-expression   ::= and-expression { 'or' and-expression }
and-expression  ::= comparison { 'and' comparison }
comparison      ::= additive [ comparison-op additive ] [ '?' ]
comparison-op   ::= '==' | '!=' | '<' | '<=' | '>' | '>='
additive        ::= multiplicative { ( '+' | '-' ) multiplicative }
multiplicative  ::= unary { ( '*' | '/' | '%' ) unary }
unary           ::= '-' unary | postfix-expression
postfix-expression ::= primary { '.' field-name | '(' [ arguments ] ')' }
arguments  ::= argument { ',' argument } [ ',' ]
argument   ::= [ field-name ':' ] expression
primary    ::= identifier | literal | list-literal | map-literal | sorted-expr | list-form | '(' expression ')' | 'not' primary
literal    ::= string-literal | integer-literal | float-literal
list-literal ::= '[' [ expression { ',' expression } [ ',' ] ] ']'
map-literal  ::= '[' ':' ']' | '[' expression ':' expression { ',' expression ':' expression } [ ',' ] ']'
binary-expr  ::= '<<' [ expression ':' encoding { ',' expression ':' encoding } [ ',' ] ] '>' '>'
```

- `binary-pattern` and `binary-expr` (ADR 0058) share segments: `name:
  encoding` binds in a pattern, `value: encoding` encodes in an expression.
  `<<` is one token; the closing `>>` is two `>` tokens, so
  `List<List<int>>` is unaffected. An `encoding` is an integer format (`u8`,
  `i8`, `u16be`...`i64le` with mandatory byte order for whole-byte widths
  above 8, `u1`...`u63` bit fields), `bytes` / `bytes(n)`, or `utf8` /
  `utf8(n)`; which names are legal is the checker's job (A14), not the
  grammar's. Attested: `postgres-wire/conversions/`.

- `list-literal` (ADR 0057) is the only use of `[` and `]`. Its element type
  is inferred from the elements, which must all have the same type, or taken
  from the expected type; `[]` is legal only where an expected type exists
  (a typed declaration, a parameter, a return, an `append` operand). Lists
  grow with the prelude builtin `append(list, element)`, which returns a new
  list. Attested: `order-processing/rules/large_order_ids.fiig`.

- Postfix `?` propagates `Err` out of the enclosing declaration — attested
  throughout every multi-gateway workflow (e.g. `create_customer.fiig`:
  `validated = validateNewCustomer(input)?`).
- A trailing comma before the closing `)` is allowed — attested in
  `order-processing/gateways/record_order.fiig`'s multi-line `Database.execute`
  call. `arguments` and `list-literal` allow one; no other comma-separated
  list (params, fields, type args, pattern args) has needed one yet.
- `arguments` supports both positional (`CustomerError.NotFound(id)`) and
  named (`Customer(id: id, name: input.name, email: input.email)`, `insert_customer.fiig`)
  forms, and may mix them: positional arguments come first, then named
  ones, and no parameter is given twice (ADR 0068 §5). Builtins take named
  arguments too: a method (`s.replaceAll(old: "-", new: "/")`), a typed
  primitive operation (`Socket.connect(host: h, port: 5432)`), `append`
  (`list`, `element`), and `math` (ADR 0049). Their parameter names are in
  `compiler/types`'s tables. A builtin's trailing parameter may be optional,
  and a call that leaves it out passes `None`: every `Socket` operation
  that waits takes `deadline: int | None` last (ADR 0074 §5), so
  `Socket.read(connection)` and `Socket.read(connection, deadline: until)`
  are both calls.
- Another package's enum variant is written with the package's alias:
  `http.Method.Get`, `http.HttpError.Socket(failure: f)`, and the same
  three segments as a pattern (`http.Method.Get -> ...`). Nothing needed it
  before `fiig.http` (ADR 0074). A qualified type whose alias no `use`
  line of the file binds (`reply: http.Reply` without `use fiig.http`) is
  an unresolved type.
- `not` and unary `-` are the only unary operators. `not` wraps a bare
  `primary` only, not a full `postfix-expression` (`validate_new_customer.fiig`,
  `reserve_inventory.fiig`); unary `-` wraps a full `unary` so it binds
  looser than postfix access/calls but tighter than `*`/`/`/`%`
  (`docs/decisions/0019-arithmetic-operators.md`) — `-a.b` is `-(a.b)`, not
  `(-a).b`.
- Arithmetic (`+ - * / %`, ADR 0019) applies to `int`, to `float` (ADR
  0048 §2.2), and to `decimal` (ADR 0064), never to two different ones, and
  is never overloadable — attested by
  `order-processing/rules/total_of_orders.fiig` and, for `float`,
  `order-processing/rules/billable_weight.fiig`. `%` truncates on both,
  so its result takes the dividend's sign (§2.8). An `int` result outside
  int64 is a fault, never a wrapped value (ADR 0063); a `float` result
  that IEEE 754 would make NaN or infinite is a fault, `x / 0.0` included,
  and `-0.0` never appears (ADR 0048 §2.3). Underflow rounds toward `0.0`.
  A `decimal` holds at most 34 digits written in full and is exact: `+ - *
  %` give the exact result, keeping a scale (`1.50 * 2` is `3.00`), and a
  result over 34 digits is a fault, never a rounded value. `/` on decimals
  is a compile error naming `math.divide`, since most quotients have no
  exact decimal value (ADR 0064 §3).
- Number literals are untyped constants (ADR 0048 §2.4), as in Go. A
  constant expression — literals under `-`, parentheses, and `+ - * / %` —
  is evaluated exactly at compile time and takes its type where it lands:
  a parameter, field, return, typed binding, or the other operand of an
  arithmetic or comparison operator. With nothing to land in it takes a
  default type: `int` when every literal in it is an integer, `float`
  otherwise. `x * 2` therefore multiplies two floats when `x` is one, and
  `0.1 + 0.2` rounds once, to the float nearest 0.3. A value that does not
  fit where it lands is a compile error: `2.5` as an `int` (A1),
  `9223372036854775807 + 1`, `1e400` as a `float`, or a constant division
  by zero. As in Go, `/` on two integer constants is integer division even
  in a float context: `7 / 2` is `3.0` there, and `7.0 / 2` is `3.5`. A
  constant becomes `decimal` only where one is wanted and only if its exact
  value fits: `rate: decimal = 0.0725` and `10.0 / 4.0` are legal, `1.0 /
  3.0` and `math.pi` as decimals are A1. A literal keeps its written scale
  (`1.50`); a computed constant takes the smallest one (`2.5`).
  Precedence is the universal convention (`*`/`/`/`%` tighter than `+`/`-`,
  both tighter than comparison), deliberately unoriginal rather than a
  guess. Comparison and boolean operators
  (`docs/decisions/0011-comparison-and-boolean-operators.md`) are decided but
  still otherwise unattested beyond `if` conditions — the precedence below
  `not` (comparison > `and` > `or`) remains a first-cut guess, not validated
  against a real example.
- Comparison operands (ADR 0052): `==` and `!=` take two operands of one
  type (M2), where one side being assignable to the other counts, so
  `x == None` is legal for `x: T | None`; an `int` and a `float` are two
  types (ADR 0048 §2.9). `==` and `!=` never take a `float` at any depth
  (M1, ADR 0051 §2.3). `decimal` compares by value, so `1.50 == 1.5`
  although they print differently (ADR 0064 §5). `<`, `<=`, `>`, `>=` need
  `int`, `float`, `decimal`, or `string` on both sides (M3); nothing
  structural is ordered.
- A method call (`name.isBlank()`, `xs.at(0)`) is legal only when the
  receiver's type is in the closed builtin table of ADR 0049 §2.6: `string`
  (`isBlank`, `contains`, `utf8`, `toDecimalOrNone`, and ADR 0067's
  `length`, `codePointCount`, `slice`, `startsWith`, `endsWith`,
  `removePrefix`, `removeSuffix`, `trim`, `trimStart`, `trimEnd`,
  `lowercase`, `uppercase`, `equalsIgnoringCase`, `split`, `replaceAll`,
  `toIntOrNone`, `toFloatOrNone`; ADR 0069 §7's `fromHex`, `fromBase64`,
  `fromBase64Url`, each `bytes | None`), `bytes` (`length`,
  `at`, `slice`, `utf8`; ADR 0058; ADR 0068's `toHex`, `toBase64`,
  `toBase64Url`; and ADR 0085's `xor`, which faults on two lengths), and `List<T>` (`length`, `at`,
  `atOrNone`, `slice`, and ADR 0065's `first`, `firstOrNone`, `last`,
  `lastOrNone`, `contains`, `sorted`, `sortedDescending`, and `join` on a
  `List<string>`). Anything else is K1. A string's `length` and `slice`
  count grapheme clusters, `codePointCount` code points, and its bytes are
  `utf8().length()`; searching matches code points (ADR 0067 §1, §5). Models and enums have no methods. `contains` needs a comparable
  element (M1; floats use `math.containsClose`) and the sorts an ordered one
  (M3); both sorts are stable and return a new list. Every method is pure,
  so a bare method-call statement is A13. `Map<K, V>` (ADR 0065 §4-§9)
  has `at`, `atOrNone`, `containsKey`, `length`, `keys`, `values`, `put`,
  `putIfAbsent`, and `remove`; the last three return a new map. Its key must
  be comparable (M1; a float key is told to round to a decimal), it
  iterates in insertion order with `for key, value in m`, and `==` ignores
  order. A map literal with two equal constant keys is A18; a repeated key
  the compiler can't see faults.
- Qualified calls like `Database.queryOne(...)` and `PaymentGateway.charge(...)`
  parse as ordinary postfix field-access-then-call — the grammar doesn't need
  to distinguish "capability method call" from any other call. The checker
  does: a capitalized receiver that names nothing declared is a capability
  with no declaration in fiig, held to its gateway's `requires` (B10, P7),
  and `Database`'s are typed (ADR 0082): the SQL text is a string literal
  with no `${...}` (Q1), the values after it are bound by position (Q2),
  and a query's row type comes from context (A12 without one, Q3 for a
  type SQL can't fill). A context expecting `Result<X, DatabaseConflict>`
  opts the call in to its integrity and retryable conflicts (ADR 0087),
  which it then returns as `Err` instead of faulting.

### Concurrency and retry

```ebnf
concurrent-expr ::= 'concurrent' '(' identifier 'in' expression { ',' concurrent-modifier } ')' ':' ( expression | block )
concurrent-modifier ::= 'limit' ':' limit-value | 'preserveOrder'
limit-value     ::= integer-literal | 'unbounded'

retry-expr ::= 'retry' '(' 'attempts' ':' integer-literal ')' ':' ( expression | block )
```

### Keyed sort

```ebnf
sorted-expr ::= 'sorted' '(' identifier 'in' expression ')' ':' sort-key { 'then' sort-key }
sort-key    ::= expression [ 'descending' ]
```

ADR 0065 §3 and ADR 0093 §4: a new list of the iterable's elements, stably
ordered by the first key, ties by the next, and so on. `then` and
`descending` count only on the line their key ends on. The old header
modifier, `sorted(x in xs, descending): key`, parses, is A1, and `fiig fmt`
rewrites it as `key descending`. Each key is evaluated once per element and must be `int`, `float`,
`decimal`, or `string` (M3; a `T | None` key is M3 with a pointer to
`match`). `concurrent`'s header shape, with an expression for a body. The
key may call only what a rule may (formulas and methods) in any kind of
declaration, so a sort never reaches a gateway; a bare `sorted(...)`
statement is A13. `sorted`, `then` and `descending` are contextual, not reserved:
the form starts only at `sorted ( identifier in`, so `xs.sorted()`, a local
named `sorted`, and a call to a declaration named `sorted` keep their
meaning.

### List forms

```ebnf
list-form ::= list-form-name '(' identifier 'in' expression ')' ':' expression
list-form-name ::= 'filter' | 'map' | 'groupBy' | 'count' | 'any' | 'all'
                 | 'sum' | 'max' | 'min' | 'maxOrNone' | 'minOrNone'
```

ADR 0093: the body is evaluated once per element, in list order, with the
element bound to the name, under the keyed sort's rules: it may call only
what a rule may, in any kind of declaration, and a bare form as a statement
is A13.

| Form | Body | Result |
|---|---|---|
| `filter(x in xs): c` | `bool` | `List<T>`, the elements where `c` holds |
| `map(x in xs): e` | any `U` | `List<U>` |
| `groupBy(x in xs): k` | comparable `K` (M1) | `Map<K, List<T>>`, keys in order of first appearance |
| `count(x in xs): c` | `bool` | `int` |
| `any(x in xs): c` / `all(x in xs): c` | `bool` | `bool`; `false` / `true` on `[]` |
| `sum(x in xs): e` | `int`, `float`, `decimal` | the body's type; `0` on `[]`, `int` for a constant body |
| `max(x in xs): k` / `min(x in xs): k` | ordered (M3) | the key; faults on `[]` |
| `maxOrNone(x in xs): k` / `minOrNone(x in xs): k` | ordered (M3) | the key, or `None` on `[]` |

Each name is contextual, like `sorted`: the form starts only at
`name ( identifier in`, so `math.max(a, b)`, a local named `count`, and a
call to a declaration named `filter` keep their meaning.

Attested: the bare form `concurrent(customerId in customerIds): getCustomer(customerId)`
(`retrieve_customers.fiig`, matching the snippet already in `fiig-lang.md`
line 124); the `limit:` form only in `invalid-programs/concurrency_error.fiig`,
where `limit: unbounded` is exactly the case Milestone 2 says must be
rejected — the grammar accepts it syntactically, the checker rejects it
semantically, matching the parse/check split in `fiig-lang.md` §Milestone 1
vs §Milestone 2. `retry` is attested both ways now: the rejected case in
`invalid-programs/retry_error.fiig` (retrying non-idempotent `chargePayment`),
and a valid case in `read_customer_with_retry.fiig`, retrying `getCustomer`
now that it's marked `idempotent`
(`docs/decisions/0014-idempotency-and-explicit-retry.md`).

`preserveOrder` (`docs/decisions/0024-preserve-order-modifier.md`) is a second,
independent modifier — zero or more `concurrent-modifier`s, comma-separated,
in either order (`limit: 5, preserveOrder` and `preserveOrder, limit: 5` are
both legal and equivalent), each at most once. It's attested in
`retrieve_customers_in_order.fiig`. There's no static check for it (Category D
is entirely about numeric limits); it only changes Milestone 4's runtime
result ordering, not anything the type/effect checker reasons about.

Per `docs/decisions/0016-unified-concurrency-syntax.md`, `concurrent-expr` is
the only concurrency construct — there is no separate `parallel` form, for
CPU-bound rule/conversion work or otherwise. Every attested use wraps a gateway
call; nothing in this grammar restricts the body to gateways, so wrapping a
rule call instead is already legal syntax, just unattested so far.

### Transaction blocks

```ebnf
transaction-block ::= 'transaction' identifier [ isolation ] [ 'retry' '(' 'attempts' ':' integer-literal ')' ] block
isolation         ::= 'serializable' | 'repeatableRead'
```

ADR 0088: the isolation words are contextual, keywords only here. With
`retry`, a block that ends in a `DatabaseConflict.Retryable` the program
didn't handle, or whose `COMMIT` fails with one, is rolled back and run
again from the locals it started with, up to `attempts` runs; a domain
`Err` or any other fault ends it at once. No `idempotent` is needed: C1,
C4, and C5 already keep a block's effects inside the database.

The identifier names a capability (`transaction Database { ... }` — every
attested use). Nesting (`transaction_error.fiig`'s `doubleReserve`) and a
network gateway inside the block (`reserveAndCharge`) both parse fine under this
grammar; both are checker-rejected per §3.8, again matching the parse/check
split.

Binding is implicit, not part of the grammar at all
(`docs/decisions/0015-implicit-transaction-binding.md`): a gateway called
lexically inside `transaction Database { ... }` automatically joins that
transaction if its own `requires` names `Database` — no transaction handle
appears anywhere in `gateway-decl` or in call syntax. `getCustomer(id)`
parses identically in `change_customer_email.fiig` (inside the block) and
`read_customer.fiig` (outside it); which one applies is a semantic/runtime
fact about enclosing scope, not a syntactic difference.

## Known gaps

Things the examples didn't force a decision on, left open rather than
guessed:

- Syntax for a two-way `conversion` declaration (`docs/decisions/0004-conversion-directionality.md`
  settled that both directions live in one declaration; how the two directed
  contracts are written inside it is still undesigned — every attested
  `conversion` example is one-directional).
- The full shape of a *valid* `retry`, since both attested uses are
  checker-rejected — overlaps `fiig-lang.md` §8 item 9 (whether retries are
  explicit-only or capability-configured).
- User-defined generics. `List<T>`, `Result<T, E>`, and `BatchResult`
  are the only generic types, and they're all built into the prelude —
  nothing lets a library author write their own `rule` or `model`
  parameterized over a type. Likely entangled with the gap above: a
  reusable "apply this rule to every item in a list" primitive needs a
  function-or-rule value as a parameter, which is most of what a real
  generics design would need to answer anyway.
