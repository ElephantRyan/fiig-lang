# Milestone 0 examples

The ten required examples from `fiig-lang.md` §5 (Milestone 0), plus the
candidate 11th compensation example flagged in
`docs/decisions/0003-project-name.md`. No grammar exists yet — see "Syntax
introduced here" below — so treat every `.fiig` file as a sketch, not a
parseable program.

| # | Requirement | File(s) |
|---|---|---|
| 1 | Read one customer | `customer-service/workflows/read_customer.fiig` |
| 2 | Concurrently retrieve many customers with partial failures | `customer-service/workflows/retrieve_customers.fiig` |
| 3 | Convert scalar models and concurrently convert a collection | `customer-service/activities/summarize_customer.fiig`, `workflows/summarize_customers.fiig` |
| 4 | Create a customer using pure validation and an external uniqueness check | `customer-service/activities/register_customer.fiig` (behind the `workflows/create_customer.fiig` boundary) |
| 5 | Update and reread data inside one transaction block | `customer-service/workflows/change_customer_email.fiig` |
| 6 | Two sequential transaction blocks around a network gateway | `order-processing/activities/place_order.fiig` |
| 7 | Compose two child activities into a parent workflow | `customer-service/workflows/onboard_customer.fiig` (composes `activities/register_customer.fiig` + `activities/send_welcome_email.fiig`) |
| 8 | Call one workflow from both an HTTP adapter and a CLI adapter | `customer-service/adapters/http_customer_api.fiig`, `cli_customer.fiig` |
| 9 | Import two aliased time libraries | `time-library/rules/compute_follow_up_date.fiig` |
| 10 | Deliberately invalid program: type, effect, retry, concurrency, transaction errors | `invalid-programs/*.fiig` (grown into the full bug-class catalog — see `docs/specification/bug-classes.md`) |
| 11 (candidate) | Failure after an earlier commit requires explicit compensation | `order-processing/activities/place_and_ship_order.fiig` |
| 12 (supplementary) | Cross-domain workflow composition requires explicit error interpretation (ADR 0006) | `order-processing/workflows/checkout.fiig` |
| 13 (supplementary) | A valid `retry` around an idempotent gateway (ADR 0014) | `customer-service/workflows/read_customer_with_retry.fiig` |
| 14 (supplementary) | Explicit adapter response mapping, with a reusable error interpreter (ADR 0017) | `customer-service/adapters/http_customer_api_with_mapping.fiig` + `conversions/customer_error_to_http_response.fiig` |
| 15 (supplementary) | Path, query, and request-body parameter binding across POST/PATCH/QUERY routes (ADR 0021) | `customer-service/adapters/http_customer_create_api.fiig`, `http_customer_email_api.fiig`, `http_customer_query_api.fiig` |
| 16 (supplementary) | Sequential `for` over `List<T>`: a field sum through a reassignable local, and a validate-all with early `return` (ADR 0053) | `order-processing/rules/total_of_orders.fiig`, `all_orders_have_positive_totals.fiig` |
| 17 (supplementary) | `float` on measured values, with an untyped constant landing as a float (ADR 0048) | `order-processing/rules/billable_weight.fiig` + `models/parcel.fiig` |
| 27 (supplementary) | JSON decoded in fiig code: a webhook envelope whose payload is kept as a `JsonValue` and decoded by its type, the sender's `"@type"` read through `from json`, a field named `limit`, the same envelope received as raw bytes and decoded with `json.decode`, and `JsonError` as the 422 body, runnable with `fiig serve . inboxApi` (ADR 0073) | `event-inbox/` (`models/envelope.fiig`, `rules/read_event.fiig`, `workflows/receive_raw_event.fiig`); `invalid-programs/json_error.fiig` for J1/J2 |
| 28 (supplementary) | An HTTP client written in fiig (ADR 0074): an application that depends on `fiig.http`, built into the toolchain, sends a typed event with `json.encode`, and decodes the reply with `json.decode`; a status other than 200 and a failure to connect are its own error variants. Runnable against `fiig serve examples/event-inbox inboxApi` with `fiig run -caps capabilities.json` | `inbox-sender/` (`gateways/send_invoice_paid.fiig`); the package itself is `compiler/stdlib/http/` |
| 26 (supplementary) | Enums in HTTP bodies: a payload-free enum sent and received as a string, an enum with payloads as an object naming its variant, and an error enum sent as the 422 body itself, runnable with `fiig serve . shippingApi` (ADR 0072) | `shipping-quotes/` (`models/destination.fiig`, `errors/quote_error.fiig`, `adapters/shipping_api.fiig`); `invalid-programs/wire_error.fiig` for W3 |
| 25 (supplementary) | Config from the environment: a session lifetime with a default, an export token checked as a `Secret` in constant time, a payment provider's key revealed only in its gateway, and two applications over one shared package, one of which can't contain Square and renames its Stripe key (ADR 0071) | `accounts/config/sessions.fiig`, `activities/export_allowed.fiig`; `acme-stripe/config/`; `checkout-core/`, `checkout-clientvertical/` (`tooling/runprogram`'s `TestAVerticalProvesWhatItDoesNotContain`) |
| 24 (supplementary) | A login that sets a session cookie with the secure defaults, a route that reads it, a logout that deletes it, a 401 that sends a challenge header, a CSV export behind a bearer token, and a Basic auth rule, runnable against SQLite with `fiig serve` (ADR 0069) | `accounts/` (`adapters/accounts_api.fiig`, `conversions/logged_in.fiig`, `rules/basic_credentials.fiig`, `rules/accounts_csv.fiig`); `invalid-programs/wire_error.fiig` for W1/W2 |
| 23 (supplementary) | A session token from random bytes, a retry delay whose jitter an activity draws for a pure rule, and a gateway stamping a new row with a UUID beside its one capability (ADR 0068) | `customer-service/activities/new_session_token.fiig`, `activities/next_retry_delay.fiig`, `rules/backoff_delay.fiig`, `gateways/insert_customer.fiig` |
| 22 (supplementary) | A name limited in characters a reader sees, an email trimmed and lowercased, and an address limit in bytes (ADR 0067) | `customer-service/activities/validate_new_customer.fiig`, `rules/validate_email.fiig`, `formulas/text.fiig` |
| 21 (supplementary) | A count per category and an index by id, each a `Map` built with `put`, and a stable keyed sort of records (ADR 0065) | `order-processing/rules/order_count_by_customer.fiig`, `orders_by_id.fiig`, `largest_orders_first.fiig` |
| 20 (supplementary) | The getting-started guide's program: a rule, SQLite gateways, and an HTTP adapter with error mapping, runnable from a release archive | `getting-started/` (`docs/getting-started.md`) |
| 19 (supplementary) | Exact `decimal` arithmetic with one named rounding, and a split whose shares add back to the total (ADR 0064 §3a, §4) | `order-processing/rules/declared_weight.fiig` + `formulas/shipping.fiig` (`poundsToKilograms`), `rules/package_weights.fiig` |
| 18 (supplementary) | The prelude `math` group with no `use`, and a crossing from float to int that names its rounding (ADR 0049 §2.4, ADR 0048 §2.7); a fiig-written `formulas` group shared by two rules | `order-processing/rules/billed_kilograms.fiig`, `billable_weight.fiig` + `formulas/shipping.fiig` |

Shared domain types live under each folder's `models/` and `errors/`, one
declaration per file per §3.9. `customer-service/` is reused across examples
1–5, 7, and 8 so the same concepts behave consistently, per the Milestone 0
exit criteria; `order-processing/` exists only because example 6 needed a
workflow with a real network gateway between two transaction blocks, and `time-library/`
only because example 9 needed a second package to alias against.

## Syntax introduced here

None of this is decided grammar — it's the minimum needed to make these
programs readable, exactly as backlog item 3 calls for ("resolve syntax only
where the examples require it"). Backlog items 4–5 (lexical grammar,
declaration grammar) should treat this list as their starting input, not as a
settled spec:

- `enum Name { Variant(field: Type, ...) ... }` — a closed sum type; every
  attested error type in the corpus is one (ADR 0040).
- `enum Name(key code: string, ...) { Variant(code: "X", ...) ... }` — a
  headed enum whose rows are constants, with a derived lookup
  `Name(code: raw)` and field access `value.code` (ADR 0046); attested by
  `deductions/models/deduction_source.fiig`.
- `gateway name(...) -> Result<T, E> requires Capability { ... }` (renamed
  from `step`, ADR 0031) — the `requires` clause names the capability a
  gateway needs.
- `?` after a call — propagate a `Result`'s `Err` out of the current
  declaration; used in every workflow with more than one fallible call.
- `transaction Capability { ... }` — names which capability the transaction block is
  transactional over.
- `concurrent(x in xs): expr` and `concurrent(x in xs, limit: N): expr` — the
  bounded form and its explicit-limit variant; `limit: unbounded` is used only
  in `invalid-programs/concurrency_error.fiig` to show what must be rejected.
- `retry(attempts: N): expr` — retry wrapper; only valid around a gateway marked
  `idempotent`. `getCustomer` is the one idempotent gateway in the corpus
  (`docs/decisions/0014-idempotency-and-explicit-retry.md`), retried validly
  in `read_customer_with_retry.fiig`.
- `idempotent` — gateway-declaration modifier (after `requires`) marking a gateway
  safe to retry; attested on `get_customer.fiig` only — every other gateway,
  including `chargePayment`, is deliberately left non-idempotent.
- `use pkg as alias` — already specified in `fiig-lang.md` §3.10, reused
  as-is. A package's version lives in its own manifest (ADR 0032), never
  at the `use` site.
- `//` line comments.
- String interpolation: `"...${expr}..."`, used once in
  `send_welcome_email.fiig`.
- `use pkg.*` — the wildcard-import form itself, used only in
  `invalid-programs/wildcard_import_error.fiig` to show what §3.10 already
  says must be rejected; no valid example uses it.
- `{ field: Type, ... }` — an anonymous structural record type, used only in
  `invalid-programs/type_error.fiig` (`summarizeOrderShape`) to show a
  structural type where a named one is required in a public signature.
- `idempotent` — gateway-declaration modifier marking a gateway safe to retry
  (ADR 0014); attested only on `get_customer.fiig`.
- A trailing `{ Ok(x) -> ...; Err(e) -> ... }` block on an adapter route
  (ADR 0017), mapping the workflow's `Result` to a protocol response —
  `200 customer` is bare status-code-plus-body shorthand, not an ordinary
  call; `customerErrorToHttpResponse(error)` in the same block is an
  ordinary call to a reusable `conversion`, shown side by side in
  `http_customer_api_with_mapping.fiig`.
- `+ - * / %` and unary `-` (ADR 0019) — fixed, int-only, never
  overloadable; attested by `order-processing/rules/total_of_orders.fiig`.
- `POST`/`PUT`/`PATCH`/`DELETE`/`QUERY` as `http-method`s (ADR 0021),
  alongside `GET`; a `?name={value}` query-placeholder region in a route's
  path string, binding by name exactly like a path placeholder
  (`http_customer_email_api.fiig`); and body binding — the one call
  argument a `POST`/`PUT`/`PATCH`/`QUERY` route leaves unclaimed by any
  path/query placeholder binds from the decoded request body
  (`http_customer_create_api.fiig`, `http_customer_query_api.fiig`).

## Exit criteria check (`fiig-lang.md` §5)

- Readable without prose: each workflow is a handful of lines; comments are
  limited to one line stating which requirement a file satisfies.
- Same concepts behave consistently: one `Customer`/`CustomerError` domain
  spans examples 1–5, 7, and 8.
- At least five bug classes rejected: comfortably exceeded — `invalid-programs/`
  now attests 26 of the 28 bug classes in `docs/specification/bug-classes.md`,
  across all eight categories (type, effect, transaction, concurrency, retry,
  error-handling, file/import). The Milestone 2 "programs that must fail"
  list (`fiig-lang.md` §Milestone 2) is a subset of this catalog, so the two
  milestones stay aligned.
- Advantage over a Go/Java framework: see below — four concrete comparisons,
  not just the one.

### Advantage over a Go/Java framework, in detail

`charter.md`'s own success criteria ask for "bug classes that are compile
errors here and runtime incidents in the comparison language," not just "a
language that could theoretically express this." The comparisons below are
chosen to hold up under that stricter bar — each names what a Go/Java team
would have to get right by discipline, convention, or a bolted-on linter,
where Fiig's checker makes the same mistake impossible to compile.

- *(Withdrawn: nominal IDs.)* This list used to claim `value string:
  string` made an `string`-for-`string` swap a compile error. ADR 0055
  removed `value`, because a wrapper in a library rule's signature forces
  ceremony on every caller. IDs are plain `string`s, and named-argument calls
  are the defense against swaps. That makes swaps visible, not impossible,
  so this is no longer an advantage claimed over Go/Java.
- **Transactions holding capabilities they shouldn't (bug classes C1/C4,
  `transaction_error.fiig`'s `reserveAndCharge`).** Holding a DB transaction
  open across a slow network call is a well-known production hazard (it
  blocks the connection pool for everyone else) — but nothing in Go's or
  Java's type system stops a function from doing it; catching it depends on
  code review or an opt-in static-analysis rule. Fiig's checker rejects it
  unconditionally, every time, with no opt-in required.
- **Unhandled errors (bug class F1, `error_handling_error.fiig`).** `result,
  _ := doSomething()` silently discarding an error is common enough in Go
  that a whole separate tool (`errcheck`) exists just to bolt this check on
  after the fact, and it still has to be remembered per-project. Fiig makes
  an unhandled `Result` a core compiler error with no escape hatch — not a
  linter a team has to remember to run.
- **Registry search is real today, not aspirational.** `catalog-cli find
  --output "Result<Customer, CustomerError>" examples/customer-service`
  returns `getCustomer` and `insertCustomer` — their full contracts (params,
  return type, required capability, idempotency) — with no source file
  read. The Go/Java equivalent of "what existing code already produces a
  Customer" is grepping the codebase or asking a teammate.
