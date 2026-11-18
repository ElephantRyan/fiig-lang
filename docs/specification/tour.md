# Fiig by example — everything decided, in one place

Every snippet below is legal under `docs/specification/grammar.md`, and most
are copied or adapted from `examples/`. This is a markdown file rather than a
`.fiig` file for a reason that is itself part of the language: **§3.9 allows
exactly one declaration per file**, so no single `.fiig` file could show more
than one of these.

Status markers used throughout:

| Marker | Meaning |
|---|---|
| `[built]` | Decided, implemented, and exercised by the corpus |
| `[decided]` | ADR accepted, not yet implemented |
| `[unattested]` | ADR accepted and implemented, but no example uses it yet |

Proposed syntax — nothing you can rely on — is quarantined in **Part 2** at
the bottom.

---

# Part 1 — Decided

## 1. A file is imports plus exactly one declaration

```fiig
// `use path [as alias]`. A package's version is never written here; it lives
// in that package's manifest. Both packages below are named `time`, so the
// aliases are the only way to reference both in one file.   [built]
use company.time as businessTime
use standard.time as systemTime

activity computeFollowUpDate(from: systemTime.Instant) -> systemTime.Instant {
    return businessTime.addBusinessDays(from, 3)
}
```

There is no wildcard import: `use standard.time.*` is a parse error. Every name
from another package is written through its alias, as in `systemTime.Instant`.

## 2. Types

```fiig
Customer                                     // named              [built]
string  int  float  bool  bytes              // primitives         [built]
systemTime.Instant                           // qualified          [built]
List<string>                                 // prelude generic    [built]
Result<Customer, CustomerError>              // prelude generic    [built]
BatchResult<string, Customer, CustomerError>
Customer | None                              // union              [unattested on a signature]
{ id: string, total: int }                // anonymous record — locals only
```

`List`, `Result`, `Ok`, `Err`, `BatchResult`, `None`, and `copy` are *prelude
identifiers*, not keywords. The only generic types are those three; you cannot
write your own (see Part 2).

## 3. Data declarations

```fiig
// A model is a record. Fields are newline-separated — no commas.   [built]
model Customer {
    id: string
    name: string
    email: string
}
```

IDs, emails, and amounts are plain primitives. There is no newtype
declaration: ADR 0055 removed `value`, and named-argument calls
(`getCustomer(id: customer.id)`) are the defense against swapped arguments.

```fiig
// An enum is a general sum type (ADR 0040). Variants may carry fields...
enum CustomerError {
    NotFound(id: string)
    EmailAlreadyInUse(email: string)
    Invalid(field: string, reason: string)
}
```

```fiig
// ...or carry nothing at all. A typed error is an ordinary enum, not a
// separate declaration kind.                                        [built]
enum Region {
    US
    EU
    APAC
}
```

```fiig
// Model embedding: name a type with no field name and its fields are
// promoted. `customer.email` is legal; `customer.ContactInfo.email` is not.
// Embedding is reuse only — `Customer` is NOT assignable to `ContactInfo`.
// Construction names the embedded type, access is flat:
//   Customer(ContactInfo: contact, id: id, name: name)
// (ADR 0045)                                                      [implemented]
model ContactInfo {
    email: string
    phone: string
}

model Customer {
    ContactInfo
    id: string
    name: string
}
```

## 4. The five behavioral declarations

All five share one shape: `kind name(params) -> type { ... }`. What differs is
what each may call and whether it may touch the outside world.

```fiig
// RULE — pure, and calls nothing but formulas (ADR 0029, narrowed by
// ADR 0049).                                                         [built]
rule regionDisplayName(region: Region) -> string {
    match region {
        Region.US -> "United States"
        Region.EU -> "Europe"
        Region.APAC -> "Asia-Pacific"
    }
}
```

```fiig
// CONVERSION — a rule specialized to relating two types. Pure; may call
// rules and other conversions.                                       [built]
conversion customerToSummary(customer: Customer) -> CustomerSummary {
    return CustomerSummary(id: customer.id, name: customer.name)
}
```

```fiig
// FORMULA — pure computation, always in a `formulas` group (one group per
// file). Every kind may call one; it calls only formulas. Never returns a
// Result, never `requires` (ADR 0049).                               [built]
formulas text {
    formula isEmailShaped(s: string) -> bool {
        return s.contains("@") and not hasSpace(s)   // siblings: unqualified
    }

    formula hasSpace(s: string) -> bool {
        return s.contains(" ")
    }
}

// Callers import the group by path tail, even in its own package, and
// qualify every call.
use customer.service.text

rule validateEmail(email: string) -> Result<string, CustomerError> {
    if not text.isEmailShaped(email) {
        return Err(CustomerError.Invalid("email", "must be a valid address"))
    }
    return Ok(email)
}
```

```fiig
// ACTIVITY — reusable coordination. Identical in shape to a rule; it has no
// `requires` clause, ever.                                           [built]
activity validateNewCustomer(input: NewCustomer) -> Result<NewCustomer, CustomerError> {
    if input.name.isBlank() {
        return Err(CustomerError.Invalid("name", "must not be blank"))
    }
    validateEmail(input.email)?
    return Ok(input)
}
```

```fiig
// GATEWAY — the only declaration that touches a capability, and exactly one
// (ADR 0010). `idempotent` is optional and follows the capability name.
// A gateway may not call another gateway (ADR 0031). A query's row type
// comes from context, and its columns fill the model by name; the SQL
// text is a literal, values are bound after it (ADR 0082).          [built]
gateway getCustomer(id: string) -> Result<Customer, CustomerError>
    requires Database
    idempotent
{
    row: Customer | None = Database.queryOne("SELECT id, name, email FROM customers WHERE id = ?", id)
    match row {
        found -> Ok(found)
        None -> Err(CustomerError.NotFound(id))
    }
}
```

```fiig
// WORKFLOW — the adapter boundary. `?` propagates Err out of the
// enclosing declaration.                                             [built]
workflow createCustomer(input: NewCustomer) -> Result<Customer, CustomerError> {
    validated = validateNewCustomer(input)?
    return insertCustomer(validated)
}
```

```fiig
// A Database call opts in to the conflicts a program decides on by the
// type its context expects; every other call still faults on them
// (ADR 0087).                                                        [built]
inserted: Result<int, DatabaseConflict> = Database.execute("INSERT INTO customers ...", id, name, email)
match inserted {
    Ok(_) -> Ok(Customer(id: id, name: name, email: email))
    Err(DatabaseConflict.Unique(constraint)) -> Err(CustomerError.EmailAlreadyInUse(email))
    Err(other) -> Err(CustomerError.Invalid("customer", "${other}"))
}
// DatabaseConflict: Unique, ForeignKey, NotNull, Check, Retryable
```

### Composition matrix

Who may call what. **ADR 0044 is accepted but not yet implemented**, so the
two columns currently differ — this is the single most important thing to know
when reading the corpus.

| Caller | Previously (ADR 0030) | Now (ADR 0044) |
|---|---|---|
| `rule` | nothing | nothing |
| `conversion` | rules, conversions | rules, conversions |
| `activity` | rules, conversions | rules, conversions, **gateways**, **activities**, `transaction` blocks |
| `gateway` | rules, conversions | rules, conversions |
| `workflow` | rules, conversions, activities, gateways, workflows | rules, conversions, activities, gateways — **not workflows** |
| `adapter` | exported workflows | workflows only, and an activity may **not** be adapter-exposed |

Since ADR 0049, every kind in the table may also call a formula, a rule
included, and a formula may call only formulas.

Under 0044 a workflow becomes uncallable from fiig and is purely the line
between adapters and interior logic. That is what makes its error contract
final.

## 5. Statements

```fiig
// Binding, if/else, return, match, and bare expressions.
activity example(input: NewCustomer) -> Result<None, CustomerError> {
    trimmed = input.name                      // binding              [built]

    if input.name.isBlank() {                 // single-sided guard    [built]
        return Err(CustomerError.Invalid("name", "blank"))
    } else if trimmed == "test" {             // else / else if  [unattested]
        return Err(CustomerError.Invalid("name", "reserved"))
    } else {
        return Ok(None)
    }
}
```

A block's final statement, if it is a bare expression or a `match`, implicitly
becomes the return value. `return expr` as the last statement is exactly
equivalent sugar. This is why some corpus files end in a bare
`concurrent(...)` with no `return`, and others write `return` explicitly.
`if`/`else` in final position is *not* covered by this rule.

```fiig
// MATCH. Patterns are `_`, a qualified name with optional sub-patterns, or a
// bare identifier that binds the whole value. Matches over enums must be
// exhaustive (bug class A9).                                          [built]
conversion customerErrorToHttpResponse(error: CustomerError) -> HttpResponse {
    match error {
        CustomerError.NotFound(_) -> HttpResponse(status: 404, body: None)
        CustomerError.EmailAlreadyInUse(_) -> HttpResponse(status: 409, body: None)
        error -> HttpResponse(status: 500, body: None)
    }
}
```

There are **no literal patterns** — you cannot match on `"ACME"` or `3`. Use
`if` with `==` instead.

## 6. Expressions

```fiig
// Operators, in precedence order: postfix access/call, unary (`not`, `-`),
// `* / %`, `+ -`, comparison, `and`, `or`.
// Arithmetic is on int or on float, never one of each, and never
// overloadable (ADR 0019, ADR 0048).
rule orderTotal(subtotal: int, shipping: int) -> int {
    return subtotal + shipping
}
```

```fiig
// FLOAT (ADR 0048) is binary64 with no NaN and no Infinity: `x / 0.0` and
// overflow fault, as `7 / 0` does. Number literals are untyped constants,
// as in Go: one takes its type from where it lands, so `/ 5000` divides a
// float by a float here, with no conversion written.
rule billableWeightKg(parcel: Parcel) -> float {
    dimensional = parcel.lengthCm * parcel.widthCm * parcel.heightCm / 5000
    if dimensional > parcel.weightKg {
        return dimensional
    }
    return parcel.weightKg
}

// A constant is evaluated exactly and rounded once, where it lands, so
// `0.1 + 0.2` is the float nearest 0.3. The wart, inherited from Go: `/` on
// two integer constants is integer division even in a float context.
whole: float = 7 / 2      // 3.0
exact: float = 7.0 / 2    // 3.5

// No float ==: two floats that should agree can differ in the last bit
// (M1, at any depth: a model holding a float has no == either). < <= > >=
// are fine. int and float never mix: `count * ratio` is A1.
```

```fiig
// MATH (ADR 0049 §2.4) is the prelude's one formulas group, callable from
// every file with no `use`. Crossing between int and float is a call that
// says how it rounds; a result with no int, or no finite float, faults.
rule billedKilograms(parcel: Parcel) -> int {
    return math.ceilToInt(parcel.weightKg)   // 19.2 -> 20
}

average = total / math.toFloat(count)
near = math.closeTo(average, expected, relative: 1e-9, absolute: 0.005)
math.abs(-3)          // int form: a constant with no typed partner is an int
math.max(x, 0)        // float form, when x is a float; math.max(n, x) is A1
math.addOrNone(a, b)  // int | None: None where `a + b` would fault (ADR 0063)
math.sqrt(-1.0)       // fault: not a real number

// Its constants are untyped, like any literal: math.pi * 2 folds exactly,
// and math.maxInt + 1 is a compile error. pi tau e minInt maxInt maxFloat.
area = math.pi * r * r
```

```fiig
// DECIMAL (ADR 0064) is exact: at most 34 digits written in full, and an
// operation whose exact result needs more faults rather than rounding. A
// decimal keeps its scale for display (1.50) and compares by value
// (1.50 == 1.5). A constant becomes a decimal only where one is wanted, and
// only if it is exact: 1.0 / 3.0 or math.pi as a decimal is A1.
rule declaredWeightKg(pounds: decimal) -> decimal {
    return math.round(shipping.poundsToKilograms(pounds), places: 1, rounding: Rounding.HalfUp)
}

// No `/`: most quotients have no exact decimal value, so the places and the
// rounding are written down. Splitting a total uses allocate, whose shares
// add back to it exactly.
math.divide(10.00, 3, places: 2, rounding: Rounding.HalfEven)   // 3.33
math.allocate(10.00, parts: 3, places: 2)                        // [3.34, 3.33, 3.33]
math.toDecimal(2.675)        // 2.675: the shortest decimal that is that float
math.roundToInt(price)       // the *ToInt crossings take decimals too
"19.99".toDecimalOrNone()    // decimal | None, JSON's number grammar
```

```fiig
// Calls take positional or named arguments; a trailing comma is allowed.
Customer(id: id, name: input.name, email: input.email)        // named
CustomerError.NotFound(id)                                     // positional
s.replaceAll(old: "-", new: "/")      // builtins take names too (ADR 0068 §5)
xs = append(xs, element: x)          // positional first, then named
Database.queryOne("SELECT ...", id)   // capability call = ordinary postfix

// `copy` produces a new value of the same model, changing only what's named.
rule renameCustomer(customer: Customer, newName: string) -> Customer {
    return copy(customer, name: newName)
}

// String interpolation.
"Thanks for joining, ${customer.name}."
```

```fiig
// Binary patterns and expressions (ADR 0058): Erlang's model with fiig's
// `name: encoding` segments. Byte order is always written; a short buffer
// falls through to the next arm; a built value that doesn't fit faults.
conversion readFrame(buffer: bytes) -> Frame | None {
    match buffer {
        <<kind: u8, length: i32be, body: bytes(length - 4), rest: bytes>> -> Frame(kind: kind, body: body, rest: rest)
        _ -> None
    }
}
<<body.length() + 4: i32be, body: bytes>>     // building bytes
0x00030000                                   // hex integer literal
```

```fiig
// List literals (ADR 0057). The element type is inferred; `[]` needs a
// declared type. `append` returns a new list and must be assigned.
// `==` on lists compares contents, in order.
codes = ["ACME", "GLBX"]
rule largeOrderIds(orders: List<Order>, threshold: int) -> List<string> {
    ids: List<string> = []
    for order in orders {
        if order.total >= threshold {
            ids = append(ids, order.id)
        }
    }
    return ids
}

// List queries (ADR 0065). Every method returns a value and changes
// nothing, so a bare `xs.sorted()` statement is an error (A13).
best = scores.sortedDescending()   // stable; sorted() ascends
top = best.firstOrNone()           // int | None; first() faults when empty
seen = best.contains(10)           // ==, so floats use math.containsClose
// Sorting records needs a key: evaluated once per element, stable, and
// limited to what a rule may call. Ties keep their order.
ranked = sorted(book in books, descending): book.score

// Map (ADR 0065): immutable, in insertion order, keys compared with ==.
limits = ["small": 10, "large": 500]       // Map<string, int>; [:] is empty
limits = limits.put("medium", 50)           // a new map; assign it
for size, limit in limits {                 // two names; _ discards one
    total = total + limit
}

// Strings (ADR 0067): every count is named.
"Zoë 👍🏽".length()                  // 5, what a reader sees
"Zoë 👍🏽".codePointCount()          // 6, what a VARCHAR column counts
"Zoë 👍🏽".utf8().length()           // 13 bytes
"hi".utf8().toHex()                 // "6869"; also toBase64(), toBase64Url()
"Njg2OQ".fromBase64()               // bytes | None (ADR 0069): also fromHex(), fromBase64Url()
email = input.email.trim().lowercase()
token = header.removePrefix("Bearer ")
tags = "a,b,,c".split(",")          // ["a", "b", "", "c"]
line = tags.join(", ")
page = query.toIntOrNone()          // int | None
```

```fiig
// Clock and Random are local effects (ADR 0068): an activity or a gateway
// reads them directly, and a rule takes the value as a parameter.
activity newSessionToken() -> string {
    return Random.bytes(count: 32).toBase64Url()   // 256 bits, cookie-safe
}

activity nextRetryDelay(attempt: int) -> int {
    return backoffDelay(attempt: attempt, jitter: Random.int(min: 0, max: 99))
}
// Random.int includes both ends; Random.float() is in [0, 1);
// Random.uuid() is a version 4 UUID; Clock.now() is Unix milliseconds.
```

## 7. Concurrency, retry, transactions

```fiig
// `concurrent` is the ONLY concurrency construct (ADR 0016) and the only
// thing that walks a List. Modifiers are comma-separated, each at most once,
// in any order: `limit: N | unbounded`, `preserveOrder`.              [built]
workflow retrieveCustomers(customerIds: List<string>) -> BatchResult<string, Customer, CustomerError> {
    concurrent(customerId in customerIds, limit: 5, preserveOrder):
        getCustomer(customerId)
}
```

```fiig
// `retry` is explicit, and only legal around an idempotent gateway (ADR 0014).
workflow readCustomerWithRetry(id: string) -> Result<Customer, CustomerError> {
    return retry(attempts: 3): getCustomer(id)
}
```

```fiig
// `transaction Cap { ... }`. Binding is implicit (ADR 0015): a gateway called
// inside the block joins the transaction if its own `requires` names that
// capability. No transaction handle appears anywhere.                [built]
workflow changeCustomerEmail(id: string, newEmail: string) -> Result<Customer, CustomerError> {
    transaction Database {
        updateCustomerEmail(id, newEmail)?
        return getCustomer(id)
    }
}
```

Nesting a transaction, or calling a network gateway inside one, parses but is
rejected by the checker.

## 8. Adapters

```fiig
// HTTP. Methods: GET POST PUT PATCH DELETE QUERY. Every argument of the
// target call binds from a path placeholder, a query placeholder, a header
// or cookie (`from`), or (POST/PUT/PATCH/QUERY only) the request body —
// checker-enforced. Every route says what it sends (`produces`), and a body
// route what it reads (`consumes`); another Content-Type is a 415.   [built]
adapter http customerHttpApi {
    GET "/customers/{id}" -> readCustomer(id: string)
        produces "application/json"
    POST "/customers" -> createCustomer(input: NewCustomer)
        consumes "application/json"
        produces "application/json"
    GET "/me" -> currentAccount(session: string | None from cookie "sid")
        produces "application/json"
    GET "/accounts.csv" -> exportAccounts(authorization: string from header "Authorization")
        produces "text/csv"           // a non-JSON type sends a string or bytes
}
```

```fiig
// Optional per-route response mapping (ADR 0017). `200 customer` is
// shorthand for HttpResponse(status: 200, body: customer); any other arm
// produces the prelude HttpResponse<B> (ADR 0069 §3).                [built]
adapter http customerHttpApiWithMapping {
    GET "/customers/{id}" -> readCustomer(id: string)
        produces "application/json"
    {
        Ok(customer) -> 200 customer
        Err(error) -> customerErrorToHttpResponse(error)
    }
}

// Headers and cookies a response sets. A Cookie's left-out fields are the
// secure answer: HttpOnly, Secure, SameSite=Lax, Path=/, this host only.
conversion loggedIn(session: Session) -> HttpResponse<Account> {
    return HttpResponse(
        status: 200,
        body: session.account,
        headers: ["Cache-Control": "no-store"],
        cookies: [Cookie(name: "sid", value: session.token, maxAge: 86400)],
    )
}
```

```fiig
// CLI. Same placeholder notation, `exit N` instead of a status code.  [built]
adapter cli customerCli {
    command "customer get {id}" -> readCustomer(id: string)
}
```

## 9. Packages

Not fiig source — `fiig.json` beside the package.

```json
{
  "path": "acme/customers",
  "kind": "library",
  "version": "1.2.0",
  "dependencies": { "acme/time": "1.0.0" },
  "capabilities": ["Database"],
  "deprecations": {
    "oldGetCustomer": { "since": "1.1.0", "message": "use getCustomer" }
  }
}
```

`capabilities` is a compiler-enforced allowlist of what the package may use
(ADR 0035). Version resolution is MVS, recorded in `fiig.lock.json`.

An application's manifest may also rename a dependency's config variables
for its deployment, `"environment": {"acme.stripe": {"STRIPE_API_KEY":
"CLIENTVERTICAL_STRIPE_API_KEY"}}` (ADR 0071 §5).

### Configuration  [built]

```fiig
// A deployment's settings, read once from the environment before the
// program starts (ADR 0071). Capitalized, read as Sessions.maxAge.
config Sessions {
    maxAge: int from env "SESSION_MAX_AGE" = 24 * 60 * 60
    exportToken: Secret from env "ACCOUNTS_EXPORT_TOKEN"
}

// Only an activity or a gateway reads config; a rule takes the value as a
// parameter (B10). A Secret is matched in constant time, never shown.
activity exportAllowed(token: string) -> bool {
    return Sessions.exportToken.matches(token)
}

// .reveal() only in a gateway, where the value leaves (S2).
gateway charge(request: ChargeRequest) -> Result<string, string>
    requires Client
{
    outbound = http.WebRequest(host: "api.stripe.com", path: "/v1/charges", body: "...",
        authorization: "Bearer ${StripeAccount.apiKey.reveal()}")
    ...
}
```

## 10. Names: one flat namespace per package

```fiig
// There is ONE namespace per package, shared by all ten declaration kinds.
// A name resolves to exactly one declaration, so there is no kind-qualified
// call syntax and no overloading — by kind or by signature.          [built]

workflow createCustomer(input: NewCustomer) -> Result<Customer, CustomerError> { ... }
activity createCustomer(input: NewCustomer) -> Result<Customer, CustomerError> { ... }
//       ^ error: duplicate declaration name "createCustomer"
```

This is what lets every registry card have one canonical identity, and it
pairs with §3.9's one-declaration-per-file rule: the filename and the name
are effectively the same fact.

Calling the *wrong kind* is caught separately. An adapter may only attach to
a workflow:

```fiig
adapter http badTarget {
    POST "/validate" -> validateNewCustomer(input: NewCustomer)
    //                  ^ [B6] adapter route must call an exported workflow
}
```

Cross-package names don't collide — each package has its own namespace, and
an import alias is how you reach into another one (`businessTime.addBusinessDays`).

## 11. Capabilities today

```fiig
// A gateway names one capability. Today those are domain concepts supplied by
// the host: Database, EmailSender, PaymentGateway, ShippingProvider.  [built]
requires Database

// ADR 0042 closes that set to mechanical primitives — everything else,
// including Postgres wire, HTTP, and JSON, gets written in fiig. ADR 0060
// folded TlsSocket into Socket, leaving four:                       [decided]
//     Socket  Clock  FileSystem  Random

// Socket is built (ADR 0060). A Connection is opaque; only Socket makes or
// uses one, and only in a gateway that requires Socket.             [built]
gateway openSecure(host: string, port: int) -> Result<Connection, SocketError>
    requires Socket
{
    connection = Socket.connect(host, port)?
    Socket.write(connection, <<8: i32be, 80877103: i32be>>)?
    answer = Socket.read(connection)?
    match answer {
        <<83: u8>> -> Socket.startTls(connection, host)
        _ -> {
            Socket.close(connection)
            return Err(SocketError.Refused)
        }
    }
}
```

---

# Part 2 — Proposed, not decided

**Do not treat anything below as settled.** None of it parses today.

## ADR 0043 — interfaces `[proposed]`

A declaration listing gateway signatures that a whole *package* fulfills.
Fulfillment is declared in the manifest's `provides` and verified
structurally — there is no `implements` on any declaration.

```fiig
// The keyword is `interface` (settled 2026-10-22). `capability` stays with
// ADR 0042's five primitives above.
interface PaymentProvider {
    charge(orderId: string, amount: int) -> Result<string, PaymentError>
    refund(reference: string) -> Result<None, PaymentError>
}
```

```json
{ "path": "acme/stripe", "capabilities": ["HttpClient"], "provides": ["PaymentProvider"] }
```

```fiig
// The import name IS the value. A rule may select and return a provider but
// may never invoke one; the call happens in an activity or workflow.
use acme/stripe
use acme/square

rule choosePaymentProvider(merchant: Merchant) -> PaymentProvider {
    if merchant.region == Region.EU { return square }
    return stripe
}
```

Open within 0043: whether interfaces may also hold **rule** signatures (pure
members), which would be the extension point for a third-party discount
policy. Also open: whether a default-implementation body is allowed — argued
against, since it would put executable code inside a signature list and leave
it absent from the registry.

## ADR 0046 — enum dispatch and variant constants `[implemented]`

For an implementation set you own entirely (250 in-house client integrations),
the enum is the dispatch index and exhaustiveness is the completeness check.

```fiig
// Constant schema on the enum HEADER; each row supplies values with `:`,
// exactly like ordinary construction. `key` marks a lookup column.
enum DeductionSource(key code: string, key aliases: List<string>, layout: FieldLayout) {
    Acme(   code: "ACME", aliases: ["ACMEINC", "ACME-INC"], layout: FieldLayout.FixedWidth)
    Globex( code: "GLBX", aliases: [],                      layout: FieldLayout.Csv)
    Initech(code: "INIT", aliases: ["INITECH"],             layout: FieldLayout.Csv)
}

DeductionSource(code: raw)       // lookup by a key: DeductionSource | None
DeductionSource(aliases: raw)    // a list key matches any element
source.layout                    // header fields read off any value
```

Header values are constants: literals, enum variants, and lists or models of
constants. Duplicate key values are a declaration error (A16). A variant has
header constants **or** a payload, never both. Per-client facts go on the
header; a `match` on the enum is for per-client logic. Corpus:
`examples/deductions`.

## Known gaps — no design at all

```fiig
// USER-DEFINED GENERICS. No declaration takes a type parameter. Entangled
// with iteration: a reusable "apply this to each item" needs a rule value.

// TWO-WAY CONVERSION. ADR 0004 settled that both directions live in one
// declaration; how they're written inside it is undesigned.

// LITERAL PATTERNS in `match` (`"ACME" -> ...`). Not decided; `if` covers it.
```
