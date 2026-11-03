# ADR 0043: Interfaces, Capabilities Written in Fiig, and Provider Values

Status: **Accepted** (2026-10-23, by the author, after the end-to-end
review recorded below). Written during the design discussion of
2026-10-10 and marked Accepted prematurely at the time; that earlier
marking was withdrawn, and this acceptance is the first approval of the
design itself. **Implemented** 2026-10-23 — see Implementation, at the end. The keyword question is settled: the selection declaration is
`interface` (decided 2026-10-11, confirmed by the author 2026-10-22).
**Revised 2026-10-22/23** in an end-to-end review with the author: the
import-alias collision settled as §3a (G4); the retroactive-conformance
limitation restated; §5 rewritten so that a capability may be written in
fiig, always as a named `capability` group, local or imported; the
"interface name resolves to a configured default" shortcut dropped; and
§2's `HttpClient` layering example removed, because it described a path
the rest of the ADR made illegal (§5 and ADR 0031 between them left a fiig
Stripe no way to reach a fiig HTTP client).
Date: 2026-10-10
Depends on: ADR 0042 (native floor), ADR 0044 (activity coordinates
effects), ADR 0049 §2.5 (groups addressable as a path tail)
Amends: ADR 0042 (meaning of `capability`), ADR 0050 §2.1 (L1 extended to
aliases of capability groups, §5c, and to members and bindings, §6) and
§2.3 (how a reader tells a capability from an enum, §6)

## Context

ADR 0042 moved fiig's native floor to the socket and declared that every
capability above it is written in fiig. That needs three things the
language does not have: a way to write a capability in fiig, a way to
declare what a family of interchangeable implementations has in common,
and a way to choose among them — the Stripe-versus-Square question that
started this.

A first draft of this ADR proposed a new `provider` declaration kind plus
an interface/`implements` pair. Both were rejected on review. The
`provider` kind would have made an eighth declaration kind in a language
already criticized for rigidity, and the Java-shaped `implements`
ceremony buys nothing a derived registry cannot compute. That draft also
routed selection through a string name resolved at runtime
(`PaymentProvider("stripe")?`), which was a way of dodging a purity
question rather than deciding it: it made the single most business-
relevant decision in the system stringly-typed.

The 2026-10-22 review found a second, quieter mistake: the draft made
*every* layer an interface. Stripe was shown as `requires HttpClient`,
with `HttpClient` a contract that some package `provides`, so that the
application had to wire Stripe's HTTP client. Nothing needs that.
Stripe's author chooses Stripe's HTTP library, the way a Java Stripe SDK
depends on OkHttp directly; and both mechanisms considered for app-level
wiring (a manifest binding table, or inferring the binding from `use`
lines) cost more than the substitution they bought — the second one
silently, by making an import in one file decide what a `requires` meant
in another package.

## Decision

The ADR separates two ideas the first draft ran together:

- A **capability** is something a gateway uses to reach the outside
  world. It is either built into the host, or written in fiig as a named
  `capability` group. (§5)
- An **interface** is a type shared by several interchangeable
  capabilities, so that a caller can *choose* one. (§1–§4)

**Declare an interface only where the caller selects the implementation.**
Stripe uses one HTTP library and needs no interface for it; an application
choosing between Stripe and Square does.

### 1. The interface declaration

```fiig
interface PaymentProvider {
    charge(orderId: OrderId, amount: Money) -> Result<PaymentReference, PaymentError>
    refund(reference: PaymentReference) -> Result<None, PaymentError>
}
```

An interface is a list of gateway signatures that a capability group
fulfills. The word is Go's sense of it (structural, no inheritance), not
Java's. Its name is usable as a type.

### 2. Fulfillment is declared in the manifest, verified structurally

```fiig
// package acme.stripe — capabilities/stripe.fiig
use acme.http.Client

capability Stripe {
    gateway charge(orderId: OrderId, amount: Money) -> Result<PaymentReference, PaymentError>
        requires Client
    { ... }

    gateway refund(reference: PaymentReference) -> Result<None, PaymentError>
        requires Client
    { ... }
}
```
```json
{
  "path": "acme.stripe",
  "kind": "library",
  "version": "1.2.0",
  "dependencies": { "acme.http": "1.4.0", "acme.payments": "1.0.0" },
  "provides": { "acme.payments.PaymentProvider": "Stripe" }
}
```

`provides` maps an interface to the capability group in this package that
fulfills it. The checker inspects that group's gateways and verifies each
interface member is present with a matching signature. No declaration in
the package mentions `PaymentProvider`.

The interface is named **by package path**, not bare: interfaces live in
packages, and ADR 0032 lets two majors of one package coexist, so a bare
`PaymentProvider` can be ambiguous. The fulfilling package must depend on
the interface's package, and the types in its gateway signatures
(`OrderId`, `Money`) must be *the* types the interface names, not
same-shaped look-alikes declared locally.

**Where types live.** Keep a type in the same package as the one
capability that uses it (`HttpRequest` beside `Client`, as Go keeps
`http.Request` beside `http.Client`): the two must version in lockstep,
because changing the type breaks the capability. Move types into a
package of their own once two *independently released* packages must
agree on them — which is exactly the interface case above, where
`acme.payments` holds `PaymentProvider`, `OrderId`, and `Money` for both
Stripe and Square. This is guidance to library authors, not a checked
rule.

### 3. A capability group is a value of the interface type

```fiig
// rules/choose_payment_provider.fiig
use acme.stripe.Stripe
use acme.square.Square

rule choosePaymentProvider(merchant: Merchant) -> PaymentProvider {
    if merchant.region == Region.EU {
        return Square
    }
    return Stripe
}
```

The imported group's name *is* the value — Elixir's module-as-value,
capitalization included. No new naming mechanism and no string
indirection.

### 3a. An import alias may not share a name with anything else in scope

Because an alias can now be a value, a bare identifier in a file must
mean exactly one thing. An import alias — explicit (`use acme.stripe.Stripe
as Pay`) or the implicit last path segment — is an error if the same name
is also:

- a parameter or an `=` binding in a declaration in that file,
- the name of any declaration in the package (the flat `Universe`),
  including a local `capability` group (§5a), or
- another import alias in the file (already G3).

The fix is always the same and always explicit: **bind the import under a
different alias with `as`**. A collision never resolves silently, in
either direction — locals do not shadow the import, and the import does
not shadow locals. Adding an import therefore can never change what an
existing line means; at worst it fails to check until it is aliased.

This is one rule, not three: G3 was already "two things in this file
claim one alias," and §3a extends the set of things an alias can collide
with. It reports at both sites, as duplicate declarations already do, with
the message naming the remedy (`alias the import: use acme.stripe.Stripe
as StripePay`). Implemented as a new bug class, **G4**, beside G3 in
`CheckFile`. It fires on every import, not just ones that provide an
interface: whether an import is a provider is a manifest question, and a
naming rule should not need the manifest to apply.

Scope is small because imports are file-scoped and §3.9 allows one
declaration per file: an alias can only meet that one declaration's
parameters and bindings, plus package-level names. It matters more than
it first did, because a capability-group alias is capitalized (§5c) and
can therefore meet model and enum names, which are capitalized too.

### 4. Invoking a provider is a gateway call

```fiig
// activities/process_payment.fiig
activity processPayment(merchant: Merchant, orderId: OrderId, amount: Money)
    -> Result<PaymentReference, PaymentError>
{
    Provider = choosePaymentProvider(merchant)
    return Provider.charge(orderId, amount)
}
```

`Provider.charge(...)` dispatches to the `charge` gateway of whichever
group was chosen, so it is legal exactly where gateway calls are legal —
an `Activity` or a `Workflow` (ADR 0044). The binding is capitalized
because it holds a capability (§6).

**A rule may select and return a provider value but may never invoke a
method on one.** Returning a capability is not an effect; nothing happens
until a method is called, and that call site is already effect-tracked.
So `choosePaymentProvider` stays pure in the strict sense while "which
provider does this merchant use" lives where business decisions belong.
ADR 0029's rule isolation is untouched.

*Removed 2026-10-22:* an earlier draft let an interface name used as a
value (`PaymentProvider.charge(...)`) resolve to "the configured default
provider." It never said where the default was configured, nor what
happened with two fulfillers and no default. It is unnecessary: an
application with one provider imports that group and calls it.

### 5. A capability is a primitive or a capability group

A gateway's `requires` clause names exactly one capability (ADR 0010,
unchanged). A capability is either one of ADR 0042's host-backed
primitives — `Socket`, `TlsSocket`, `Clock`, `FileSystem`, `Random`, the
only things backed by Go — or a **capability group** written in fiig,
declared in this package or imported from another. **All of them look the
same to the gateway that uses them**:

| Form | Implemented by | Example |
|---|---|---|
| Primitive | the host (Go) | `TlsSocket` |
| Local capability group | gateways in this package (§5a) | `PayrollDataApi` |
| Imported capability group | gateways in another package | `Client` (`use acme.http.Client`) |

```fiig
// package acme.http — capabilities/client.fiig
capability Client {
    gateway send(request: HttpRequest) -> Result<HttpResponse, HttpError>
        requires TlsSocket
    { ... }          // opening, TLS and closing live in here
}
```
```fiig
// package acme.http — formulas/url.fiig
formulas url {
    formula parse(raw: string) -> Result<Url, HttpError> { ... }
}
```
```fiig
// a consumer
use acme.http                 // for http.HttpRequest
use acme.http.Client
use acme.http.url

gateway fetch(raw: string) -> Result<HttpResponse, HttpError>
    requires Client
{
    target = url.parse(raw)?
    return Client.send(http.HttpRequest(url: target, ...))
}
```

A group is imported by naming it as the tail of its package's path, the
mechanism ADR 0049 §2.5 designed for formula groups. (ADR 0049 is not
implemented yet, so this ADR's implementation is the first to build it.) A package's pure side
(its formula groups and types) and its effectful side (its capability
groups) are therefore imported separately, each under the case of what
it is.

**No gateway calls a gateway.** `fetch` calls a *capability*, exactly as
a gateway calls `TlsSocket.write(...)`; that the capability is
implemented by fiig gateways rather than Go is its own business. ADR
0031's composition table is unchanged.

**A package's capability surface is exactly its capability groups.** A
loose gateway (one not inside a group) remains legal for one-off use
within its own package, but no other package can reach it. Nothing in the
corpus is affected: every gateway today lives in an application and is
called only from within it, and none of the three library packages
exports one.

#### 5a. Local capability groups

A capability does not have to start life in a library. An application can
declare one in place and extract it later, when a second application
needs it:

```fiig
// bff/capabilities/payroll_data_api.fiig
use acme.http.Client

capability PayrollDataApi {
    gateway getEmployee(id: EmployeeId) -> Result<Employee, PayrollError>
        requires Client
    { ... }

    gateway getPayStubs(id: EmployeeId) -> Result<List<PayStub>, PayrollError>
        requires Client
    { ... }
}
```
```fiig
// bff/activities/load_pay_view.fiig
activity loadPayView(id: EmployeeId) -> Result<PayView, PayrollError> {
    employee = PayrollDataApi.getEmployee(id)?
    stubs    = PayrollDataApi.getPayStubs(id)?
    return toPayView(employee, stubs)
}
```

The group follows ADR 0049's `formulas` groups structurally: one group is
the one declaration in its file (§3.9), its members are gateways,
members reference each other unqualified inside it, and outside it they
are addressed as `PayrollDataApi.member(...)`. Within its own package a
group needs no import.

**Extraction changes no call site.** Move the file into a package
`company.payrolldata`, and the consumer adds one line —
`use company.payrolldata.PayrollDataApi` — and nothing else.

#### 5b. Cycles

A capability group's gateways may themselves require another group, so
`A requires B requires A` must be rejected. Between packages the loader
already rejects dependency cycles (`loader.go:356`); within a package
this is a new bug class, analogous to B9's activity-cycle check.

#### 5c. Case comes from the declaration

A capability group is named in UpperCamelCase, because it denotes a
capability (ADR 0050 §2.1); a formula group in lowerCamelCase, because
it is pure (ADR 0050 §2.6). Imported without `as`, a group's alias *is*
its declared name, so its case is decided by the declaration, not by
the importer. With `as`, the alias must keep that case: `use
acme.http.Client as HttpClient` is legal, `as httpClient` is not.

**Package aliases stay lowercase, always.** A package is never itself a
capability or a provider value; only its groups are. So ADR 0050's
checked rule L1 is unchanged for packages and extended in one respect:
an alias takes the checked case of what it binds. The result is the
property ADR 0050 §2.3 wanted from case — a lowercase dotted prefix is a
package or a formula group, and a capitalized one is a capability or an
enum — with no exception for packages that mix pure and effectful code,
because such a package is imported once per side.

### 6. Case tells a reader whether a line can reach the outside world

Decided with the author 2026-10-23. ADR 0050 §2.3 let a reader tell a
capability from an enum by checking the enclosing declaration's
`requires`. After this ADR, activities — which have no `requires` — call
capability groups and provider values directly, so that shortcut fails
exactly where it is most needed. Two checked rules replace it.

**6a. The member's case separates a capability call from an enum
variant.** L1 extends from the dotted prefix to the member: an enum
variant is UpperCamelCase, a capability operation lowerCamelCase.

```fiig
PayrollDataApi.getEmployee(id)    // Upper.lower(...)  capability call — may do I/O
PayrollError.NotFound(id: id)     // Upper.Upper(...)  enum variant — pure
vector.magnitude(v)               // lower.lower(...)  package or formula group — pure
```

Enum variants stay UpperCamelCase rather than SCREAMING_CASE. SCREAMING
was considered: it would make variants look more distinct, but the
checked distinction needs only the member's first letter, and fiig
variants carry data (`Md5Password(salt: bytes)`), where every language
with payload-carrying variants — Rust, Swift, Kotlin, Haskell, OCaml —
uses UpperCamelCase. It would also have forced `Ok`/`Err`/`None` to
become `OK`/`ERR`/`NONE` or be exempted. The corpus already conforms.

**6b. A binding that holds a capability is capitalized.** A local, a
parameter, or a loop variable whose type is an interface (or a list of
one) must be UpperCamelCase, and no other binding may be:

```fiig
activity refundOrder(Provider: PaymentProvider, order: Order) -> Result<None, PaymentError> {
    return Provider.refund(order.paymentReference)
}

Providers = [Stripe, Square]
for Candidate in Providers { ... Candidate.charge(orderId, amount) ... }

provider = choosePaymentProvider(merchant)   // error: interface-typed binding must be capitalized
Total = order.subtotal + order.tax           // error: only interface-typed bindings are capitalized
```

Together, 6a and 6b give §2.3 a rule with no exceptions: **a lowercase
prefix never does I/O; `Upper.lower(...)` may; `Upper.Upper` is an enum
variant.** This is a fourth call-site tone *avoided*, not added — an
effectful call on a lowercase local was the alternative, and it would
have made "does this line do I/O?" require looking up a local's type.

Capitalized bindings can now collide with type and declaration names,
which lowercase ones never could (`PaymentProvider = choose(...)`). G4
(§3a) extends to cover it: a capitalized binding may not share a name
with an import alias or any package-level declaration.

**6c. A provider value is never stored in a model.** An interface type
may appear as a local, a parameter, a return type, or a list element —
never as a model field. In the author's words: providers can use models,
but models don't dictate how they are used. A model is data that is
serialized, persisted, and sent over the wire; a capability inside one
would invert that relationship, and choosing a provider is a rule's job
(§3), made fresh from the data rather than carried inside it.

## Rationale

The implementation side was surveyed against Go (structural, no
ceremony), Rust (`impl Trait for Type`, retroactive), Swift (`extension
T: P`, retroactive, default implementations), Elixir (`@behaviour`, one
line), Clojure (`extend-protocol`, retroactive, data-driven dispatch),
OCaml (`module M : S`, structural) and Java (`implements`, nominal,
ceremonial, no retroactive conformance).

The decisive one is Go's structural satisfaction, and it required
retracting an objection raised earlier in the design discussion: that an
undeclared implementation leaves a registry-first language nothing to
discover. That is true of Go, which has no registry. Fiig's registry is
*derived* — it already walks declarations and computes cards — so
"what fulfills `PaymentProvider`" is computable without anyone writing
`implements`. Go's weakness does not transfer.

What Go genuinely lacks is a way to answer "did this package forget
`refund`?", which is why Go needs the `var _ I = T{}` assertion idiom.
Putting `provides` in the manifest supplies the declared intent that
makes completeness checkable, at the one place ADR 0035 already put
capability metadata, and with zero grammar cost. The result is declared
intent with structural verification and no per-declaration ceremony:
better than Go on completeness, better than Java on ceremony.

**Why a group, not the package, is the provider value (revised
2026-10-23).** An earlier revision made the package itself the value
(`use acme.stripe as Stripe`). That forced the alias's case to describe
the whole package, and almost every capability package also exports pure
code and the types its signatures use, so no single case was right. A
group is a declaration with one kind and one case; making it the unit of
capability, of provision, and of import removes the mixed-package
problem rather than ruling on it. It needs no new design: ADR 0049 §2.5
had already specified importing a group by path tail.

**Why capabilities are concrete by default (added 2026-10-22).** Every
mainstream ecosystem lets a library pick its own dependencies: stripe-go
uses `net/http`, the Java SDK uses OkHttp. Substitution is the exception,
and where it is wanted, the library's author opts in by exposing an
interface. Making every layer substitutable from the application, as the
first draft did, needed a global binding — either a manifest table that
every application must maintain, or bindings inferred from `use` lines,
which would let an import in one file change what a `requires` means in
a package that cannot see that file. Fiig has rejected that shape of
action-at-a-distance every time it has met it (G3, G4, duplicate-
declaration reporting). File-local, concrete imports have none of it, and
versioning is already solved for them by MVS and ADR 0032.

**Why a local capability group rather than "make it a package."** The
cost of a package on day one is a manifest and a directory for code with
one consumer; the cost of loose gateways is that every call site changes
on the day they are extracted. A group whose call sites already read as
an import removes both, which is the same argument ADR 0050 §2.6 made for
formula groups.

## Consequences

- Grammar: `interface-decl`, and `capability-decl` (a named group of
  gateways). `requires` takes a primitive name or a capability-group
  name, local or imported.
- `compiler/syntax`: `InterfaceDecl`, `CapabilityDecl`.
  `compiler/manifest`: `Provides`, a map from package-qualified interface
  name to the fulfilling group's name.
- `compiler/checker` (`ResolveFileGroups`): a `use` path's tail may name a
  capability group, resolved against the package that is the path minus its
  last segment (ADR 0049 §2.5's longest-declared-prefix rule, with only
  direct dependencies loaded).
- `compiler/types`: an interface type, and **the first assignability rule
  beyond equality and union membership** — `AssignableTo` gains "a
  capability group is assignable to an interface its package provides
  through it." One hop, nominal against the manifest, no variance and no
  hierarchy. Noted plainly because fiig has had no subtyping of any kind
  until now.
- `compiler/checker`:
  - interface-completeness checking against `provides`, with signature
    types compared by identity;
  - typed capability calls, replacing "every capability call is
    deliberately unmodeled";
  - rejection of provider invocation inside a `Rule` or `Conversion`;
  - rejection of a cross-package reference to a loose gateway;
  - G4 (§3a);
  - the capability-group cycle check (§5b);
  - L1 extended to aliases (§5c).

  New bug classes: a `provides` entry naming a group that does not cover
  the interface, a signature mismatch, a rule invoking a provider, G4,
  and the group cycle.
- `compiler/registry`: an interface card listing its fulfilling groups,
  each linking back; a capability-group card listing its members.
  Effect derivation follows `requires` through capability groups, local
  and imported, down to the primitives it ultimately reaches.
- `compiler/runtime`: provider values; dispatch of `provider.method(...)`
  and `Group.method(...)` to the fulfilling gateway; `CapabilityProvider`
  continues to back primitives only.
- **ADR 0035's allowlist** is checked over the primitives a package
  reaches *transitively*. Allowlisting a capability or an interface grants
  the use of it, never the primitives behind it, so selecting a provider
  cannot smuggle in a `Socket` that the application did not allow.
- **ADR 0042** reserved `capability` for the five primitives. This ADR
  widens the word to capability groups; the primitives remain the only
  host-backed capabilities, which was the point of the reservation.
- **ADR 0046** (Proposed) would let interfaces hold rule signatures as
  well as gateway signatures, which changes §1 and the rationale for §4's
  "a rule may never invoke a provider." This ADR is written to stand
  without it; if 0046 is accepted, it amends §1 and §4 here.
- **Limitation, accepted**: conformance is declared only in the
  implementing package's own manifest, so fiig has no Rust- or Swift-style
  retroactive `impl`. A package may adopt an interface declared
  elsewhere; what it cannot do is have a *third party* declare that
  conformance for it. The cost depends on the case:
  - *A group already matches the interface structurally.* No code is
    needed, only a `provides` entry, which the consumer cannot add without
    editing the package's manifest. The lost capability is declaring
    conformance, not adapting behavior.
  - *It does not match.* A thin wrapper package fixes it: the wrapper
    declares its own capability group whose gateways require the wrapped
    group and translate, and maps the interface to it in `provides`. More
    ceremony than Rust's `impl`, not a missing capability.
  - *Adapting at the call site* also works: an activity calls the
    vendor's group directly and translates. It yields no provider
    *value*, so it cannot be returned from a rule or selected by §3.

  Closing the first case would mean letting the *consuming* manifest
  assert `provides` for an imported group, checked structurally so it
  cannot claim what is not there. Small, unneeded by the corpus, and not
  made here.
- **Flagged open**: how `transaction` names a transactional capability
  once `Database` is written in fiig rather than a Go provider. A
  transaction block scopes a capability it does not call, and the
  underlying connection now lives inside a fiig package. This is the
  piece with the least precedent and should be settled against a real
  Postgres implementation rather than guessed at.
- **Flagged open**: whether a provider can be parameterized — one
  `Stripe` serving several merchants with different credentials, versus
  one wired instance per credential set. Configuration-level
  parameterization likely covers it; nothing in the corpus needs it yet.
- **Resolved 2026-10-23**: a package exporting both pure declarations and
  gateways has no single correct alias case. Dissolved by §5c: the
  package's sides are separate groups, imported separately.
- **Resolved 2026-10-23**: ADR 0050 §2.3's `requires`-based way to tell a
  capability from an enum fails inside activities. Settled by §6; ADR
  0050 §2.3 should be updated to point here when this ADR is accepted.
- `compiler/checker` additionally: member-case checking (§6a),
  capitalized-binding checking in both directions (§6b), G4's extension
  to capitalized bindings, and rejection of an interface-typed model
  field (§6c).
- **Closed by ADR 0050 (2026-10-27) as no; see its Open questions.**
  Original note follows. **Routed here by ADR 0050 (2026-10-13): whether `.` should split into a
  namespace-navigation operator and a value-access operator**, Rust-style
  (`::` versus `.`). An import alias could not compete with a parameter
  name if the two never appeared in the same syntactic position, so
  `Stripe::charge(...)` and `customer.name` would be unconfusable by
  construction.

  **Correction, 2026-10-22:** that does not settle §3. The provider
  *value* is a bare identifier (`return Stripe`), which sits in the
  expression namespace whatever operator is used for navigation, so `::`
  would not remove the collision; §3a's G4 is needed regardless. The `::`
  question therefore stands only on its merits and is no longer coupled
  to §3.

  Today `.` serves at least six distinct lookups — package alias to
  declaration, formula group to member, capability to operation
  (`Database.queryOne`), enum to variant (`CheckoutError.OutOfStock`),
  value to field (`customer.name`), and the `value`-type unwrap
  (`email.value`). The first four are compile-time navigation and the
  last two are runtime access. The checker already separates them by
  lookup rather than by syntax, at `compiler/checker/expr.go:180-181`
  (`if _, isLocal := env[targetIdent.Name]; !isLocal`), which is evidence
  the distinction is real and currently implicit.

  Against it: every capability call in the corpus changes shape, and `::`
  is unfamiliar in a language that deliberately relies on an LLM's
  dot-notation priors (ADR 0050 §2.3's three call-site tones are built on
  those priors). ADR 0050 §2.2 rejected a *different* separator change
  (`/` for package paths) on the grounds that it fixed nothing real; this
  one does fix something real, and should be decided on that basis.

## Implementation (2026-10-23)

Built in one pass across every layer, then dogfooded with the real CLIs
against a new five-package example (`examples/acme-http`, `acme-payments`,
`acme-stripe`, `acme-square`, and the application `checkout-app`):
`check-cli -workspace examples examples/checkout-app` is clean, and
`run-cli ... takePayment` routes an EU merchant through
`choosePaymentProvider -> Square.charge -> Client.send` and a US merchant
through Stripe, with the local `LedgerApi` group alongside.

- **Syntax.** `interface` and `capability` are declaration-start keywords
  (ADR 0018), and so are the adapter kinds `http`/`cli` outside an adapter
  header; any keyword is accepted as a `use` path segment. Without both,
  `use acme.http` could neither be written nor its alias used.
- **Bug classes.** G4 as §3a. The case rules are N1–N3 rather than "L1
  extended": ADR 0053 had already used L1 for `break`/`continue` outside a
  loop, and ADR 0050's L1 was never implemented. N1 covers names and
  aliases (§5c), N2 members (§6a), N3 bindings (§6b). The capability
  rules are P1 (`provides` incomplete), P2 (signature mismatch), P3 (a
  rule or conversion invokes a provider), P4 (a cross-package reference to
  a loose gateway), P5 (a capability-group cycle), P6 (a capability in a
  model or enum field), P7 (a gateway using a capability it doesn't
  require). See bug-classes.md.
- **G4 reports at the import only** when the alias meets a declaration in
  another file; a per-file checker can't attribute a diagnostic to a file
  it isn't checking. An alias meeting a local in the same file is reported
  at both sites.
- **`provides` is bound by `types.BindProvides`**, called by
  `checker.CheckDir`/`CheckPackage` for the root package and by the loader
  for every dependency, before any file is checked; `run-cli` and
  `fiig-sql-run` now check through `runprogram.Check`, which reads the
  root manifest the same way.
- **The allowlist (ADR 0035)** counts a `requires` naming a capability
  group as nothing: the group's own gateways' requirements are counted
  where they are declared. The example's lockfile records `TlsSocket` for
  `acme.http` and nothing for Stripe or Square, and dropping `TlsSocket`
  from the application's manifest fails the load naming `acme.http`.
- **A list of providers infers its interface**: `[Stripe, Square]` is
  `List<PaymentProvider>` when exactly one interface is provided by every
  group in it.
- **Registry.** `interface` and `capability` cards, with `operations`,
  `provides`, and `fulfillers`; and a `uses` list on every callable card —
  the `Upper.lower(...)` calls, which §6 makes exactly the calls that may
  reach the outside world. Activity purity now accounts for it: before
  this ADR only gateways could touch a capability, so an activity calling
  `PayrollDataApi.getEmployee(...)` would otherwise have been marked pure.
- **Found and fixed on the way: a runtime scope bug older than this ADR.**
  An unqualified call between two files of the root package ran the callee
  under the *caller's* file imports, so a rule using an alias its caller's
  file didn't import would silently treat `alias.name(...)` as an opaque
  capability call. `choosePaymentProvider` called from `takePayment` hit
  it. The callee now runs with its own file's imports
  (`runtime.Interp.localScope`).
- **Not built.** Cross-package `fulfillers` on interface cards (the
  registry's `Merge` would have to read every package's manifest);
  `transaction` over a fiig-written capability and provider
  parameterization stay open, as above.
