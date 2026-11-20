# ADR 0079: Domains in One Application Call Each Other Through Gateways

Status: Accepted and implemented (2026-11-16; see Implementation notes)
Date: 2026-11-16
Amends: ADR 0031 and ADR 0044 (B4: a gateway may not call a workflow).
Related: ADR 0010 (a gateway requires exactly one capability), ADR 0032
and ADR 0033 (packages, manifests, the bump check), ADR 0035 (the
capability allowlist), ADR 0043 (capability groups and providers), ADR
0049 (`formulas`), ADR 0050 (qualification and case), ADR 0054 (`private`
blocks), ADR 0070 (CORS), ADR 0071 (configuration), ADR 0072 (the JSON
shape of enums), ADR 0074 (`fiig.http`), ADR 0076 (request limits),
charter principle 10.

## Context

An application that will grow usually starts as one service and splits
later. While it's one service, its domains (Payments, Customers, Returns)
should stay separate enough that pulling one out is a small change rather
than an untangling: a **modular monolith**.

The question is what a domain may use from another, and how. Today fiig
gives a domain one unit, a package with its own `fiig.pkg.json`, and two
of the three things a boundary needs:

- **Declared dependencies.** A package uses only packages its manifest
  lists, and the loader refuses a cycle between them.
- **A seam that looks remote.** A gateway is fiig's one place for an
  effect: it returns a `Result`, so its callers already handle failure,
  and a transaction controls one capability (C4), so a gateway to some
  other capability can't run inside it.

What it lacks is a way for one domain to **ask another to do something**.
A domain's behaviour is its workflows, and nothing may call a workflow
but an adapter (B4 for a gateway, B8 for an activity or workflow). So
today Orders can only reach Payments by importing Payments' activities and
gateways, which ties Orders to how Payments works, or by calling Payments
over HTTP while both run in one process.

### Precedent

| | The boundary | How another domain calls in |
|---|---|---|
| Java packages | package-private | a domain spread over several packages must make its internals `public` to share them across its own packages, which opens them to everyone |
| Java, Spring Modulith | a module's base package is its API; tests verify the rest isn't reached | through API beans, or application events |
| Ruby, Shopify's Packwerk | a `public/` folder, and declared dependencies | constants under `public/`; both rules checked by a tool, not the language |
| .NET modular monoliths | one assembly per module, `internal` types | a module's public contract assembly, often a mediator |
| Erlang/OTP | a process | a message, which reads the same whether the process is local or remote |

Two lessons. A boundary the language can't express ends up checked by a
tool beside it (Spring Modulith, Packwerk, ArchUnit). And making a remote
call look local, as Erlang does, surprises teams when the call becomes
remote: it gains timeouts and partial failure, and loses the shared
transaction. The common modular-monolith advice is the reverse: talk to
another module as if over the network, before it is.

fiig's gateway already looks remote. This ADR lets it be the seam.

## Decision

### 1. A gateway may call another package's workflow

A gateway may call a workflow declared in **another package**, when its
`requires` clause names that package's import alias:

```fiig
use shop.payments

gateway chargeOrder(order: string, amount: decimal) -> Result<Charge, ChargeError>
    requires payments
{
    match payments.takePayment(reference: order, amount: amount) {
        Ok(receipt) -> Ok(Charge(id: receipt.id, takenAt: receipt.at))
        Err(failure) -> Err(chargeErrorFrom(failure))
    }
}
```

- **One package per gateway.** ADR 0010 holds: a gateway requires exactly
  one thing, so it reaches one package's workflows and nothing else
  effectful.
- **The name is the alias.** `requires payments` names what `use
  shop.payments` binds, the same name that qualifies the call. A `use ...
  as pay` makes it `requires pay`. It is lowercase because it is a
  package, not a capability (ADR 0050: capabilities are capitalized,
  aliases lowercase). Seeing a lowercase name in `requires` tells the reader
  this gateway calls into a domain, not into the outside world.
- **Only workflows.** Another package's activities and gateways stay out
  of reach of a gateway, as they are today (B3, B4's remaining case).
  Its rules, conversions, formulas, models and enums are importable as
  before.
- **In-process.** The call runs in the same process and returns what the
  workflow returns. A fault in the workflow is a fault in the caller,
  as for any call.

### 2. B4 stands inside a package

A gateway calling a workflow of its **own** package is still B4: within
one domain, that's coordination, which is an activity's job (ADR 0044).
A package can't import itself, so `requires` can't name it. B4's message
gains the rule above: a workflow is called only from outside its package,
by an adapter or by another package's gateway.

### 3. Splitting a domain out changes one gateway

When Payments becomes its own service, `chargeOrder` keeps its name and
signature, and changes only its `requires` line and body:

```fiig
use fiig.http
use fiig.http.HttpClient

gateway chargeOrder(order: string, amount: decimal) -> Result<Charge, ChargeError>
    requires HttpClient
{
    // POST to Payments' route, decode the receipt or the error (ADR 0072)
}
```

Every caller of `chargeOrder` is unchanged, because the gateway's
signature never mentioned Payments. Two practices keep it that way, and
§5 documents them; neither is checked:

- **The gateway returns its own domain's types.** `Charge` and
  `ChargeError` are Orders' models, converted from Payments' types inside
  the gateway. A gateway that returns `payments.Receipt` passes Payments'
  vocabulary to every caller, and the split then reaches all of them.
  DDD calls this conversion an anti-corruption layer.
- **The workflow's errors survive HTTP.** After the split, the gateway
  reads Payments' answer back into `ChargeError`. ADR 0072's enum JSON
  shape carries an error variant and its payload whole, so an adapter
  that answers with the error enum loses nothing.

### 4. What the compiler records

- The registry card of a gateway that requires a package records the
  package's **path** (`shop.payments`), not its alias, so the catalog's
  dependency graph shows which domains call which.
- The manifest needs nothing new: the package is already a listed
  dependency, and ADR 0035's allowlist is about what code may reach
  outside the program, which a call to a workflow doesn't.

### 5. What is shared between domains: guidance, not a check

Not every use of another domain's code should become a gateway. fiig's
kinds already sort it, and `docs/modular-monolith.md` will say so:

| Kind | What it is | Across a domain boundary |
|---|---|---|
| `model`, `enum` | vocabulary | shared: a small shared-kernel package every domain depends on (`Money`, `CustomerId`) |
| `formulas` group | pure building blocks | shared, the same way |
| `rule` | a business decision | owned by one domain; another asks it through a gateway, or gets the answer as data |
| `conversion` | translating between shapes | each side owns its own |
| `workflow` | what the domain does | called through a gateway (§1) |

A decision imported by a second domain is the smell. `isRefundable` in a
shared library still gives two answers once the services deploy
separately, since each pins its own version; a copy drifts the same way.
One domain owns it and the others ask. The exception is a look-alike: two
rules with the same body that answer different questions (Customers'
"can this be shown as returnable?" against Payments' refund check) are
better kept as two, since they'll diverge.

The checker can't tell a decision from a look-alike, which takes business
judgment (the reason C3 was dropped). So this is documentation, and a
future refactoring tool's prompt, not a diagnostic.

### 6. One process serves every domain's adapters

A monolith serves Orders' adapter and Payments' on one listener. `fiig
serve` takes one adapter today, and only from the root package's own
files. It takes several, from the root or from its dependencies:

```
fiig serve examples/shop-orders orders.ordersApi shop.payments.paymentsApi
```

- **Naming.** A bare name is an adapter in the root package. A dotted one
  is a dependency's adapter: the package path, then the adapter's name.
  The command line has no `use` lines, so it names the path, not an alias.
  The package must be one the root depends on, directly or not.
- **Conflicting routes are refused at startup.** Two adapters with a
  route on the same method and path (or two patterns neither of which is
  more specific, by Go's `ServeMux` rules) are an error naming both, before
  the listener opens. `docs/modular-monolith.md` advises each domain to
  put its routes under its own prefix (`/payments/...`), so the routes
  don't change when the domain moves.
- **One set of limits.** ADR 0076's limits are the process's: the
  `MAX_REQUESTS` slots, the body limit, and the deadline are shared by
  every route of every served adapter, because they protect one process's
  memory and CPU. Limits per adapter would be ADR 0076's open question 1
  (limits per route).
- **One CORS policy.** `-allow-origin` applies to every served adapter,
  and a preflight answers with the methods of all of them. ADR 0070's
  refusal of `*` with a cookie-reading route is checked per adapter.
- **One configuration report.** ADR 0071's report covers the config every
  served adapter's workflows reach, so a missing variable for Payments
  stops the monolith at startup, as it would stop Payments alone.

After the split, the same Payments adapter is served alone with `fiig
serve examples/shop-payments paymentsApi`; nothing in it changes.

### 6a. Each domain can have its own database in one process

A shared table outlives every boundary in the code, so the modular
monolith's standard advice is one database, or one schema, per module,
with no module reading another's tables (Spring Modulith, and the .NET
modular-monolith writing). Several schemas on one server is fine; what
matters is that each module has its own connection.

The `-caps` file (ADR 0027) binds a capability once per process, and
`Database` is a primitive (ADR 0042), so a domain can't declare its own
name for one. The caps file gains bindings per package:

```json
{
  "Database": {"type": "sql", "driver": "postgres", "dsn": "${ORDERS_DB}"},
  "packages": {
    "shop.payments": {"Database": {"type": "sql", "driver": "postgres", "dsn": "${PAYMENTS_DB}"}}
  }
}
```

- **A package without an entry uses the process-wide binding**, so
  existing programs are unchanged.
- **The binding follows the workflow that is running, not the gateway's
  own package.** If Payments uses a migration library whose gateway
  `requires Database`, binding by the gateway's package would run
  Payments' migrations on the process-wide (Orders') database, and a
  library both domains use couldn't be bound to either. So a gateway uses
  the binding of the package whose workflow it is running for. That
  changes only where the code names it: an adapter route entering a
  workflow, and a §1 gateway calling another package's workflow. Spring's
  routing data source chooses a connection from the calling context the
  same way.
- **It lives in the caps file, not the manifest**, because a DSN is a
  deployment detail, which is the caps file's job (ADR 0071 §6). Keys
  under `packages` are package paths; a path the root doesn't depend on
  is a startup error, as a stale rename is (ADR 0071 §5).
- **Sharing is shown, not refused.** `fiig serve` at startup and `fiig
  config` list each served package's bindings and mark one that falls back
  to the process-wide binding: `shop.payments: Database (shared with the
  application)`.
- **After the split**, Payments' entry moves to its own caps file as a
  plain `"Database"`, and its code doesn't change.

A check that each table belongs to one package needs declared tables
(ADR 0066, alpha item 9) and is left to them.

### 7. The corpus

Four packages in `examples/`:

- `shop-kernel`: shared vocabulary (`Money`).
- `shop-payments`: a domain with a workflow, an `isRefundable` rule it
  owns, and an http adapter serving the workflow under `/payments`.
- `shop-orders`: an application whose `chargeOrder` gateway `requires
  payments`, served together with Payments' adapter in one process (§6).
- `shop-orders-split`: the same application after Payments is split out,
  `chargeOrder` calling Payments' route over `fiig.http`.

A test checks that `shop-orders` and `shop-orders-split` differ only in
the gateway file and the manifest. Both run against the same Payments:
`shop-orders` in one process serving both adapters, and
`shop-orders-split` against Payments served on its own.

## Alternatives considered

**A. Enforce boundaries: package-internal visibility, or refusing
another package's rules.** Kotlin's `internal`, Rust's `pub(crate)`, or a
check that a domain imports no other domain's `rule`. A boundary the
language enforces can't be crossed by accident, but fiig's packages don't
know which are domains and which are libraries, and §5's line between a
decision and a look-alike is a judgment. Not now: this ADR gives the
obvious path. Package-internal visibility stays an open question, and
still matters for libraries such as `fiig.http` (`docs/known-limitations.md`).

**B. A generic capability for cross-domain calls** (`requires Domains`).
Every such gateway would name the same word, so it says nothing the body
doesn't. Naming the package says which domain. Rejected.

**C. Expose a domain as a capability group or an interface** (ADR 0043),
with an in-process provider and an HTTP one, so the split is a manifest
change and no code changes. A capability group's members are gateways,
and a gateway can't coordinate: it can't call the activities and rules a
domain's behaviour is made of. Making it coordinate removes the line
between gateways and activities. Rejected; the one-file change of §3 is
small enough.

**D. Let an activity call another package's workflow.** Shorter, but the
call then reads as local, without the `Result` a gateway's caller expects
or C4's exclusion from transactions, which is the Erlang lesson. Rejected:
the seam should look remote before it is.

**E. Call another domain over HTTP from the start.** `fiig.http` to
`localhost` works today. It pays for serialization and a network hop
while the domains share a process, and needs every domain served before
any can be tested. Rejected as the default; it's §3's end state.

## Consequences

- A modular monolith in fiig is a workspace of packages: a shared kernel,
  one package per domain, and an application that depends on them.
- A domain's behaviour is reached through gateways that name it, so
  `requires payments` finds every place that depends on Payments.
- Splitting a domain out changes the gateways that require it, and
  nothing that calls them.
- B4's message changes; its rule narrows to one package.
- `fiig serve` serves several adapters, from the root and its
  dependencies, on one listener under one set of limits.
- A capability can be bound per package, and a gateway uses the binding
  of the package whose workflow it runs for, so each domain in a monolith
  can keep its own database.

## Open questions

1. **Package-internal visibility** (Alternative A), for libraries and for
   a domain that wants the compiler's help.
2. **Configuration in-process.** Payments' `config` reads its environment
   variables in the monolith's process (ADR 0071's manifest `environment`
   mapping renames them per dependency); after the split, in its own. The
   corpus should confirm nothing more is needed.
3. **Tables owned by one package.** §6a gives each domain its own
   connection; checking that no table is used by two packages waits for
   declared tables (ADR 0066, alpha item 9).

## Implementation notes (2026-11-16)

- **Checker.** `checkRequires` accepts an imported package's alias. A
  qualified call to another package's workflow is judged by
  `checkEffect` against the gateway's `requires`: a gateway requiring
  something else is B4, an activity or workflow B8, each naming the fix.
  The general B4/B8 message for a workflow callee now says where one may
  be called (§2). P7 and C4 needed nothing: a package's alias never
  matches a capability, so a gateway requiring `payments` can't use
  `Socket` (P7), and a call to it inside `transaction Database` is C4.
  `Database` itself is still unchecked by P7, which is alpha item 9.
- **Loader.** `gatewayCapabilities` skipped capability groups but counted
  a required package as a capability; it now skips both, so §4's "the
  manifest needs nothing new" holds. What the domain reaches is in its
  lockfile entry.
- **Registry.** `Generate` writes a lowercase `requires` that names an
  import as that import's path, so `fiig catalog find --requires
  shop.payments` lists every gateway coupled to Payments.
- **Runtime.** A call to another package's workflow, and a request to a
  served dependency's adapter (`Interp.EnterPackage`), mark the context
  with that package (`runtime.WithDomain`). `CapabilityRegistry` keeps
  bindings per package (`RegisterFor`), and `Call` and `LookupIn` (a
  transaction's provider) look in the running domain's first.
  `packageScope` now keeps an import's whole `Package`, path and imports
  included, where it had kept only its universe; a dependency calling
  onward into its own dependency's workflow needs both.
- **Two gaps the corpus found.** The adapter planned routes and decoded
  bodies in the root universe alone, so a parameter typed by another
  package's model (`amount: kernel.Money`) came out unknown and every
  request was a 400. That was true before this ADR for any route; routes
  now resolve through `Interp.ParamType` and decode through
  `DecodeBodyJSON`, in the workflow's own package and file imports. (The
  OpenAPI generator still used the root universe; `fiig openapi`, alpha
  item 8f, fixed it the same way, 2026-11-16.) And a
  served dependency that no root file imports wasn't reachable by
  `declScope`; `Interp.Packages` now holds every loaded package for it.
- **`fiig serve`** takes `<root> <adapter>...`, resolves dotted names
  through the loaded packages, and builds one mux (`HTTPHandlerFor`) under
  one limiter, recovering `ServeMux`'s panic on overlapping patterns into
  an error naming both routes. `WithCORSFor` merges the adapters' methods.
  The config walk starts from every served adapter in its own package
  (`ReachableConfigFrom`), and a caps-file binding for a package the root
  doesn't depend on joins the one startup report.
- **§7 corrected.** The split differs from the monolith in five files, not
  two: the gateway, and the deployment's own manifest, lockfile, caps
  file, and a `config PaymentsService` for Payments' URL. Every caller
  and every model is the same, and `TestSplitChangesOnlyTheGateway` holds
  the rest byte for byte. The workflows are `capturePayment` and
  `submitOrder`, since `takePayment` and `placeOrder` were taken in the
  corpus, which the valid-corpus test checks as one program;
  `shop-orders-split` repeats `shop-orders`' names by design and is checked
  on its own (`CheckDir`'s new skip list).
- **Tests.** `compiler/checker/domain_test.go`,
  `compiler/adapter/served_test.go`,
  `compiler/registry/requires_path_test.go`, and in
  `tooling/cli/servecmd`: the monolith with each domain's writes in its
  own database and the fallback to a shared one, the refusals, the split
  against Payments served alone (and a 503 once Payments is down), a
  library's gateway using the running domain's binding, and a served
  dependency the root never imports.
- **Dogfooded** with `fiig serve` over two SQLite files: the monolith on
  one listener wrote orders to `orders.db` and charges, including those
  through Payments' own route, to `payments.db`; then Payments alone on
  :8081 and the split Orders on :8090 gave the same answers and rows, and
  `Unavailable` (503) once Payments was stopped. `fiig config` lists the
  bindings.

