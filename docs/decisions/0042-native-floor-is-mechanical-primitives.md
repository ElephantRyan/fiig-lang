# ADR 0042: The Native Floor Is the Socket; Protocols and Domain Capabilities Are Written in Fiig

Status: Accepted. Floor and transitional enforcement implemented 2026-10-14;
the strict form of the rule waits on ADR 0043's fiig fulfillers (see
"Implementation notes"). This record fixes the floor and the enforceable
rule; the contract/provider declarations it depends on are specified in
ADR 0043.
Date: 2026-10-10

> **Amended (2026-10-26) by ADR 0060:** `TlsSocket` is no longer a separate
> primitive capability. TLS stays native, but as `Socket.connectTls` and
> `Socket.startTls`, because Postgres and other protocols upgrade a live
> plain connection to TLS and ADR 0010 allows a gateway only one capability.
> The primitive set is `Socket`, `Clock`, `FileSystem`, `Random`
> (implemented 2026-10-26).

## Context

An outside critique asked an obvious question no existing example had
asked: how does a program choose between Stripe and Square? In every
mainstream language the answer is "both implement a generic payment
provider interface, and the caller doesn't know which it got." Fiig could
not express that at all.

Auditing why surfaced something worse than a missing feature. Every
capability in the corpus is a **business-domain concept**:

```
Database   EmailSender   PaymentGateway   ShippingProvider
```

There is no `Socket`, no `HttpClient`, no `FileSystem` anywhere. So Go was
not acting as fiig's *floor* — Go was acting as fiig's *domain layer*. A
`CapabilityProvider` (`compiler/runtime/capability.go`) is a Go interface,
so "the Stripe integration" — endpoint shape, request mapping, error
translation, retry policy, all of it genuinely business-relevant — could
only ever be Go. Fiig was left orchestrating decisions made elsewhere.

Three concrete gaps followed from that, all symptoms of the same cause:

1. A capability implementation can only be written in Go.
2. A capability's contract is a bare string. Nothing declares that
   `PaymentGateway.charge` takes `(OrderId, Money)`; the checker treats
   every capability call as deliberately unmodeled, so a mismatch is a
   runtime `Faultf`, not a compile error.
3. `Register(name, provider)` binds one provider per capability per
   process, so "determine a provider for *this* order" is inexpressible,
   and the set of available providers is not discoverable in a
   registry-first language.

This also re-raises ADR 0025's stated motivation — that nothing be
Go-only — which was satisfied in letter (the root module has no
third-party dependencies) while being violated in spirit at a far more
important layer than the one it was written about.

An intermediate floor was considered first and rejected: keeping
`HttpClient` and `SqlConnection` native and writing only *domain*
providers in fiig. It fails the actual goal. A developer needing Kafka,
gRPC or Redis cannot build those over an HTTP client, so they would be
back to writing Go — and a raw socket offered as an escape hatch while
the features needed to use it (bytes, iteration, recursion) sit
unscheduled behind other work is a permission, not a capability.

## Decision

**Fiig's native floor is the socket. Wire protocols and every capability
above them are written in fiig.**

The primitive set is closed, compiler-known, and admits a member on one
criterion: *it cannot be expressed in fiig at all*, not merely that it
would be inconvenient to.

```
Socket   TlsSocket   Clock   FileSystem   Random
```

`TlsSocket` is the one honest exception to "protocols are written in
fiig." TLS needs constant-time primitives and audited cryptographic
implementations; writing it in fiig would be irresponsible rather than
ambitious, so it is exposed as a socket that is already encrypted.

Everything else moves up into fiig:

- **Wire protocols** — HTTP, the Postgres and MySQL wire protocols,
  Redis's RESP, Kafka's protocol, gRPC's framing, JSON encoding — all
  written in fiig over `Socket`/`TlsSocket`.
- **Domain capabilities** — `PaymentGateway`, `EmailSender`,
  `ShippingProvider`, `Database` — become fiig contracts with fiig
  fulfillers (ADR 0043), layered over those protocol libraries. Stripe
  and Square become two fiig providers.

**The rule is enforceable, not aspirational.** A capability that is not
in the primitive set and has no fiig fulfiller is a checker error (new
bug class). Without that check there is nothing to stop the next
`ShippingProvider` from being registered straight from Go — which is
exactly how the floor ended up at the domain layer in the first place.

## Rationale

The precedent is unanimous on *where* a floor goes, and fiig was the
outlier:

| Language | Native floor | Written in the language |
| --- | --- | --- |
| Erlang/OTP | inet driver (C) | `gen_tcp`, then mint/hackney |
| Go | syscalls (asm) | `net`, then `net/http` |
| Python | `socket` (C module) | `http.client`, `requests` |
| Rust | libc | `std::net`, then hyper/reqwest |

Every one of them puts the boundary at a mechanical OS primitive and
writes every domain-shaped thing above it in the language itself. Not one
puts it at "payment gateway."

The Elixir/Erlang ecosystem is the direct proof the stronger floor is
achievable rather than merely principled: **Postgrex** (Postgres),
**MyXQL**, **Redix** (Redis), **Brod**/**kafka_ex** (Kafka) and **Mint**
(HTTP/1 and HTTP/2) are all written in pure Elixir/Erlang over
`gen_tcp` — no C driver anywhere. An entire production database and
messaging ecosystem exists above a socket, in a garbage-collected
language with no pointer arithmetic. What made that practical was a small
and identifiable feature set: binaries as a first-class type, binary
pattern matching, and recursion. Those are precisely the features fiig
is missing, which makes the gap a known quantity rather than an open
research question.

## Implementation notes

The rule as written — "a capability that is not in the primitive set and
has no fiig fulfiller is a checker error" — cannot be enforced literally
yet: fulfillers need ADR 0043 (Proposed), and every capability in the
corpus is a non-primitive domain one, so a literal check would reject all
of it. It is implemented as a **ratchet** instead:

- `compiler/capability` holds the closed set (`Socket`, `TlsSocket`,
  `Clock`, `FileSystem`, `Random`) as an unexported table; membership is
  not extensible by a package or a host.
- A manifest may list `nativeCapabilities`: non-primitive capabilities still
  provided natively, pending a fiig fulfiller. A primitive may not appear
  there (manifest validation rejects it).
- **Bug class H2:** `compiler/loader` rejects a resolved build whose root
  or dependency gateways require a capability that is neither a primitive
  nor on the application manifest's grandfather list. Like H1 it is judged
  against the application's manifest over the whole dependency closure.
  The grandfather list is the visible debt; when ADR 0043 lands, the
  intended next step is to retire it entry by entry, then remove the field.
- Enforcement lives in the loader, not `compiler/checker`, because the
  manifest does. A loose directory with no manifest is not a package build
  and is not checked — which includes the `examples/` corpus, so the four
  domain capabilities there are not listed anywhere.
- `Clock` is now a primitive capability proper: `runtime.ClockProvider`
  serves `Clock.now()` (Unix milliseconds, `int`) and `Clock.sleep(ms)`
  through the interpreter's own `Clock`, so a `ManualClock` drives fiig code
  and retry backoff alike. Milliseconds as a plain `int` is a floor-level
  placeholder; a richer instant type belongs to a time library above it.
- Not done: `Socket`, `TlsSocket`, `FileSystem` and `Random` providers (each
  needs `bytes`, which does not exist yet), a stale-entry check (a
  `nativeCapabilities` name nothing requires is not flagged), and a runtime
  guard against `CapabilityRegistry.Register` of a non-primitive.

## Consequences

- A new closed primitive-capability set lives in the compiler, with
  membership checked rather than conventional.
- New bug class: a non-primitive capability with no fiig fulfiller.
- `compiler/runtime/capability.go`'s `CapabilityProvider` stays exactly
  as it is, but its role narrows to the five primitives above. ADR 0027's
  `ProviderFactory`/`FactoryRegistry` likewise stays, now configuring
  primitives only.
- **`libraries/sql` is eventually replaced**, not restructured: the
  Postgres wire protocol becomes a fiig library, and `database/sql` plus
  its drivers leave the picture. This is the largest single piece of work
  the decision implies and should not be attempted first.
- `compiler/runtime/json.go` likewise becomes a fiig library rather than
  Go, once bytes and iteration exist.
- `Clock` already exists as a Go interface internal to the interpreter
  (`compiler/runtime/clock.go`) rather than as a capability. It is
  already the right shape and becomes a primitive capability proper.
- **Blocking prerequisites, now first-class deliverables rather than
  roadmap items** — nothing above `Socket` can be written without them:
  a `bytes` primitive type (today's primitives are `string`/`int`/`bool`,
  `compiler/types/universe.go`), iteration, binary pattern matching in
  the style of Erlang's `<<Len:16, Body:Len/binary>>`, and recursion.
- **The declaration-kind matrix already accommodates this, under
  `Conversion`.** A first draft of this ADR claimed the kind rules
  blocked deep pure decomposition — `allowedCallees[kindRule]` is empty
  (ADR 0029) and an activity may not call an activity (ADR 0030, bug
  class B8), so `parseRequest` → `parseHeaders` → `parseHeaderLine` looked
  illegal at every layer. That was wrong: it assigned parsers to the
  wrong kind. A protocol parser *is* a conversion — `bytes ->
  Result<HttpRequest, ParseError>` relates two types, and it is
  "mechanical, judgment-free" in exactly ADR 0029's sense, which is the
  precise reason that ADR allowed `Conversion -> Conversion` while
  forbidding `Rule -> Rule`. Recursion is already legal (there is no
  cycle or self-call check anywhere in `compiler/checker`) and was the
  stated intent. No ADR needs reversing; ADR 0029's distinction turns out
  to be load-bearing in a case it was not written for, and
  conversion-calls-conversion — flagged in ADR 0029 as legal but
  unattested — finally gets a real attestation.
- Effects stay concentrated, which the kind rules enforce for free: one
  gateway owns the `Socket` and does the reading, pure conversions do all
  the parsing. Incremental framing (read a length, then read that many
  bytes) is a loop inside that single gateway body rather than gateway
  recursion, which `kindGateway` would reject anyway — so iteration, not
  a matrix change, is what unblocks streaming protocols.
- Performance becomes a real concern for the first time. A tree-walking
  interpreter (`compiler/runtime`) parsing byte streams is a different
  workload from orchestrating a handful of capability calls, and the
  interpreter was never designed for it.
- `HttpClient` and `SqlConnection` are deliberately *not* primitives,
  reversing this ADR's own first draft. Convenience is not the criterion;
  inexpressibility is.
