# ADR 0080: An Enum's Text Form

Status: Accepted and implemented (2026-11-16; see Implementation notes)
Date: 2026-11-16 (revised the same day: variant spellings folded in)
Amends: ADR 0072 §1-§2 (an enum's JSON form is its variant's name), ADR
0071 §1 (config reads an enum by variant name), ADR 0069 §2 and ADR
0009/0021 (what a route argument read from text may be).
Related: ADR 0043 §6a (N2: variants are capitalized), ADR 0046 (headed
enums and `Enum(code: raw)`), ADR 0062 (play nice with foreign data),
ADR 0073 §7b (`from json "name"` on fields), charter principle 10. Closes
ADR 0072's open question 2.

## Context

Wherever a payload-free enum meets text, fiig spells it as its variant's
name: a JSON body sends and reads `"Express"` (ADR 0072 §1), and config
reads `SHIPPING_SPEED=Express` (ADR 0071 §1). Two gaps follow.

**Foreign producers don't spell variants that way.** N2 makes every
variant capitalized, and the JSON form is exactly the variant's name, so
the values real APIs send can't be decoded into an enum:

| Producer | Member | Values |
|---|---|---|
| Stripe | a subscription's `status` | `"active"`, `"past_due"`, `"incomplete_expired"` |
| GitHub | an issue's `state` | `"open"`, `"closed"` |
| AWS EC2 | an instance state's `Name` | `"running"`, `"stopped"` |
| OpenAI | `finish_reason` | `"stop"`, `"length"` |
| Kubernetes | a pod's `phase` | `"Running"`, `"Pending"` |

Only Kubernetes fits. A program reading Stripe's subscription status keeps
a `string` and compares it by hand, which loses the exhaustive `match` an
enum exists for. ADR 0073 §7b solved this for fields, `contentType:
string from json "content-type"`, but a field can usually be named for its
member (`created_at` is a legal field name) and a variant can't, so
variants need it more.

**Route arguments read from text bind only primitives.** A path, query,
header, cookie, or `cli` argument is `string`, `int`, `float`, `decimal`,
or `bool`, so an endpoint whose value is one of a closed set takes a
`string` and checks it by hand:

```fiig
GET "/rates/{speed}" -> rateFor(speed: string)   // then match on "Standard", "Express", else 400
```

And only header and cookie arguments have their types checked. A path or
query argument of any other type, a model for instance, passes the
checker, and then every request to the route is a 400: "cannot be decoded
from a path/query value".

### Precedent

Spelling a variant on the wire:

| | Per variant | Per enum |
|---|---|---|
| Java, Jackson | `@JsonProperty("past_due")` on the constant | a naming strategy |
| Rust, serde | `#[serde(rename = "past_due")]` | `#[serde(rename_all = "snake_case")]` |
| .NET 9 | `[JsonStringEnumMemberName("past_due")]` | a naming policy on the converter |
| Python, Pydantic | the member's value is its spelling: `PAST_DUE = "past_due"` | — |
| Go | no enums; a typed string constant is its spelling | — |

Binding an enum from a path or query:

| | Binds one | Match | An unknown value |
|---|---|---|---|
| Java, Spring MVC | yes (`StringToEnumConverterFactory`) | exact name, after trimming spaces | 400 |
| Java, JAX-RS | yes (`valueOf` or `fromString`) | exact name | 404 for a path, 400 for a query |
| .NET, ASP.NET Core | yes | name **ignoring case**, or the underlying number | 400 |
| Python, FastAPI | yes (`Enum` parameter) | exact value | 422 listing the permitted values |
| Rust, axum and serde | yes (`Path<T>`, `Query<T>`) | exact, after any rename | 400 naming the expected variants |
| OpenAPI | `schema: {type: string, enum: [...]}` | JSON Schema's `enum` is exact equality | — |

Every serializer lets a variant have a spelling of its own, and every
framework binds an enum from a URL; all but ASP.NET match exactly, and
ASP.NET's also accepting `2` for the third member says its matching
wasn't designed for the URL.

## Decision

### 1. A variant may declare its spelling: `from json "text"`

```fiig
enum SubscriptionStatus {
    Active from json "active"
    PastDue from json "past_due"
    IncompleteExpired from json "incomplete_expired"
}

enum ChargeOutcome {
    Paid(receipt: string) from json "paid"
    Refused(reason: string) from json "refused"
}
```

- It follows the variant, after any payload, as `from json` follows a
  field's type (ADR 0073 §7b), `from header` a route argument's (ADR
  0069), and `from env` a config field's (ADR 0071).
- The spelling is any string. A variant without one is spelled as its
  name, as today, so no existing program changes.
- In fiig code the variant is still `SubscriptionStatus.PastDue`.
- **J2** widens: two variants of one enum with the same spelling, such as
  `A from json "B"` beside a variant `B`, is J2 at the second, as two
  fields reading one member are.

**Why `from json` and not a neutral keyword.** The spelling is used
outside JSON too (§2), but every one of those places reads an enum the way
its JSON does, and §2 says so. One syntax for "the name on the wire,"
fields and variants alike, is easier to learn than two. `PastDue =
"past_due"` was considered and rejected: it reads like a value the
variant holds, which is what an ADR 0046 header is, and the two would be
confused.

**Why per variant, not per enum.** ADR 0073 §7b rejected a naming policy
for a whole model because a reader looking for `"created_at"` would find
nothing in the source; the same holds for a variant. Each spelling is
written beside its variant, where a search for `past_due` finds it.

### 2. The spelling is the enum's one text form, everywhere

A variant's spelling (its name, when it declares none) is what it is in
every place it meets text:

- **JSON**, both ways: a payload-free enum is the string, and an enum with
  payloads names its variant with the spelling, `{"variant": "paid",
  "receipt": "r_1"}` (ADR 0072). This covers `json.decode`, `json.encode`,
  request and response bodies, and `fiig run -args`.
- **Route arguments read from text** (§3).
- **Config** (ADR 0071 §1): `SUBSCRIPTION_STATUS=past_due`.
- **OpenAPI**: the schema's `enum` lists the spellings (§5).
- **Error messages** that list accepted values.

`fiig run -json` keeps its tagged form, with fiig's own names, since it
shows fiig values and isn't a wire contract (ADR 0072 §7).

### 3. A payload-free enum binds from text

A path, query, header, cookie, or `cli` argument may be an enum whose
variants carry no payload, optionally `| None`, read by its text form:

```fiig
adapter http shippingApi {
    GET "/rates/{speed}?weightGrams={weightGrams}" -> rateFor(speed: Speed, weightGrams: int)
        produces "application/json"
}
```

`GET /rates/Express?weightGrams=500` calls `rateFor(speed: Speed.Express,
weightGrams: 500)`; with `Express from json "express"`, the URL is
`/rates/express`.

- An enum with a header (ADR 0046) binds by its text form too, as its JSON
  does. A route that wants a header code takes a `string` and calls
  `Region(code: raw)`, which answers `None` for an unknown code.
- An optional enum is `None` when the query value, header, or cookie is
  missing, like any other optional argument.

### 4. The match is exact, and an unknown value names the accepted ones

`express` doesn't match `Express`. A program that wants lowercase URLs or
snake_case JSON declares the spelling (§1), and then that spelling is
the one that matches. Reasons not to ignore case as well:

- **It wouldn't do the job.** It turns `open` into `Open` but not
  `past_due` into `PastDue`, so it covers one-word values and nothing
  else. §1 covers all of them.
- **One resource, one URL.** `/rates/express` and `/rates/Express` naming
  one thing gives a cache, a log, and a link two keys for it.
- **One rule per enum.** A body, a path, and config all match the same
  spelling the same way.

Principle 10's "lenient about what it receives" is met by §1: the program
says what the producer sends, rather than the runtime guessing at it.

An unknown value is a 400 naming the accepted spellings:

```json
{"error": "speed: \"Overnight\" isn't a Speed; expected Standard or Express"}
```

A path segment is no exception: the route matched, so the resource
exists, and its argument is the problem, the 400 an `int` segment that
isn't a number already gets. JAX-RS's 404 tells the client the URL is
wrong without saying why. In a JSON body an unknown value stays
`JsonError.Mismatch`, naming the accepted spellings.

### 5. OpenAPI lists the spellings

A parameter or member of a payload-free enum is `{"type": "string",
"enum": ["active", "past_due", "incomplete_expired"]}`, and an enum with
payloads names each variant's spelling in its `variant` member's `enum`.
A generated client gets an enum type, not a string.

### 6. W4: an argument read from text must be readable from text

The checker refuses, at the argument, a path, query, header, cookie, or
`cli` argument whose type isn't `string`, `int`, `float`, `decimal`,
`bool`, or a payload-free enum, optionally `| None`. This is **W4**:

- **An enum with payloads** has no single-text form. The message suggests
  binding its fields as separate arguments, or sending it in a body.
- **Anything else,** such as a model or a list, which until now passed the
  checker and failed every request. Header and cookie arguments were
  already checked, without a code; they now report W4 too.

### 7. The corpus

- `examples/event-inbox` gains a Stripe-style `subscription.updated`
  event whose `status` decodes into a `SubscriptionStatus` with
  snake_case spellings, and the inbox's tests send `"past_due"` and an
  unknown status.
- `examples/shipping-quotes` gains `GET "/rates/{speed}?weightGrams=..."`,
  reading `Speed` from the path.
- `invalid-programs/` attests W4 (a `Destination` in a path) and the
  widened J2 (two variants with one spelling).

## Alternatives considered

**A. Ignore case when matching** (ASP.NET). It covers single-word values
only, and gives one resource two URLs. §4. Rejected.

**B. A naming policy per enum** (`rename_all`, naming strategies). One
line for a whole enum, but the spelling of each variant is then nowhere
in the source, which ADR 0073 §7b rejected for fields. Rejected.

**C. Spellings for URLs only.** A route would read `/rates/express` while
the body sends `"Express"`, two forms of one enum in one API. Rejected:
§2.

**D. Allow lowercase variant names**, so `past_due` is the variant. It
breaks N2, which lets `Upper.Upper` always read as a variant (ADR 0043
§6a), and still can't spell `"in-progress"` or `"2fa"`. Rejected.

**E. Match a headed enum by its code** (ADR 0046), so `/regions/us-east`
binds `Region.UsEast`. An enum can have several header fields, and its
text form is its spelling, so a choice of header field would be a second
form only routes use. `Region(code: raw)` already does it in one line.
Rejected.

**F. 404 for an unknown path value** (JAX-RS). §4. Rejected.

## Consequences

- A program can decode the enums foreign APIs send, and match on them
  exhaustively, instead of comparing strings.
- A route reads a closed set as the enum it is, with the 400 and the
  OpenAPI enum included.
- A route argument of a type that can't come from text is a compile error
  (W4), not a 400 on every request.
- No existing program changes: an enum without spellings reads and writes
  as it does today.

## Open questions

1. **A value a producer adds later.** Stripe documents that it may add
   subscription statuses; one this enum doesn't list is a `Mismatch`, so
   the delivery is refused rather than handled. A catch-all variant, such
   as `Other(text: string) from json *`, would let a program receive what
   it can't name yet, which is charter principle 10's lenient receiving;
   it needs its own decision (does `match` still have to name it, and
   what does encoding it send?). Found while dogfooding. Resolved by ADR
   0081 §3: `SubscriptionStatus | string`.
2. **A list from a repeated query value** (`?speed=Standard&speed=Express`
   into `List<Speed>`). No route argument takes a list today, of any type.

## Implementation notes (2026-11-16)

- **Syntax.** `syntax.EnumVariant` gained `JSON` and `Text()`, and
  `EnumDecl` gained `VariantByText` and `Texts`. The parser reads the
  clause after a payload or a headed row through one `parseJSONName`,
  shared with fields; the formatter prints it after the variant.
- **One place each.** `decodeEnum` and `writeEnumJSON` (JSON both ways,
  so `json.decode`, `json.encode`, bodies, and `fiig run -args`), config's
  enum reader, `enumSchema` in OpenAPI, and the adapter's `decodeString`
  all go through the text form. `fiig run -json` still prints fiig's
  names (§2).
- **Messages.** A route's 400 keeps the adapter's existing prefix:
  `path value "speed": "express" isn't a Speed; expected Standard or
  Express`. A JSON `Mismatch` lists the spellings in parentheses, as it
  listed the names.
- **W4** is `checkReadFromText`, run for path and query placeholders,
  `cli` words, and header and cookie arguments, whose old uncoded check
  it replaces. J2 for variants is `checkVariantSpellings`.
- **OpenAPI.** A parameter of a payload-free enum is inlined as `{"type":
  "string", "enum": [...]}`. An optional query value had been listed as
  required, with `None` in its schema; it now isn't, as headers and
  cookies already weren't.
- **Registry.** An enum card's variant carries its spelling (`text`), and
  `catalog diff` calls a changed spelling major: clients send and read it.
- **Dogfooded** with `fiig serve`: `GET /rates/Express` answers, and
  `/rates/express` is the 400 naming `Standard or Express`; event-inbox
  reads Stripe's `"past_due"` and `"trialing"`, sends them back spelled
  the same way, and refuses `"PastDue"` with the eight spellings listed.
  That last case raised open question 1.
