# ADR 0071: Configuration from the Environment
Status: Accepted and implemented (2026-11-07; see Implementation notes), after one-decision-at-a-time review: §1
accepted; §2 and §3 accepted, §3 gaining the per-vertical note and corpus
example the owner asked for; §3a (`-env-file`) added in review; §4
accepted with the encoding rule, and a `Secret` allowed in rules with
`.reveal()` held to gateways (open question 1); §5 revised to add sharing
by import and renaming in the application manifest; §6 accepted with one
lookup, one report, and one checklist; §7 settled ADR 0070 §1 as
deployment input; §8 accepted with the testing note.
Date: 2026-11-06
Amended by: ADR 0122 (a config may be required only `when` another says so).
Related: ADR 0027 (capability configuration, `${NAME}` in the caps file),
ADR 0035 (capability security review), ADR 0042 (closed primitive set),
ADR 0043 (fiig providers; its open question on parameterized providers),
ADR 0068 §4 (local effects), ADR 0069 (`from header "X"`), ADR 0070 §1
(CORS origins, to be revisited here), charter principle 10.

## Context

A fiig program can't read a setting. Every value it uses is either a
literal in the source or an argument from an adapter. So:

- `examples/acme-stripe` calls `api.stripe.com` with no API key, because
  there is nowhere to get one from.
- `examples/accounts` hard-codes its session lifetime (`maxAge: 86400`)
  and can't point at a different database per deployment except through
  the caps file.
- abs-recommendations will need service URLs, API keys, and a signing
  secret, all of which differ between a laptop, staging, and production.

The only configuration that exists is ADR 0027's caps file, which can
read `${NAME}` from the environment. It configures Go-backed providers
(the `Database` DSN), and nothing in it reaches fiig code.

**What the twelve-factor convention and every mainstream stack agree on:**
configuration that varies per deployment comes from the environment, the
program declares it as typed settings, and the settings are checked when
the process starts, not when a request first needs them.

| Stack | Declared as | Typed | Checked at startup | Defaults | Secrets |
|---|---|---|---|---|---|
| Pydantic `BaseSettings` | a class, one field per variable | yes | yes | field defaults | `SecretStr`, hidden when printed |
| Spring `@ConfigurationProperties` | a class, bound by name | yes | yes, with `@Validated` | field defaults | none; Actuator masks by key name |
| Go `envconfig` / `caarlos0/env` | struct tags | yes | yes (on `Process`) | tag | none |
| Rust `figment` / `envy` + `secrecy` | a struct | yes | yes | `#[serde(default)]` | `Secret<T>`, `expose_secret()` |
| ASP.NET Core options | a class, bound by section | yes | opt-in `ValidateOnStart` | field defaults | none |
| Elixir `runtime.exs` | `System.fetch_env!` calls | no | yes, at boot | in code | none |
| Node `process.env` | not declared | no | no | in code | none |

The failure the typed, startup-checked form prevents is the one every
on-call engineer knows: a service starts, passes its health check, and
faults on the first payment because `STRIPE_API_KEY` was misspelled in
the deployment.

A fiig-specific concern sits on top. ADR 0035 reviews what authority a
dependency package has. Reading the environment is authority: a package
that can read `AWS_SECRET_ACCESS_KEY` and also holds `Socket` can send it
anywhere. A design that names every variable statically lets the loader
list exactly which variables each package reads.

## Decision

### 1. A `config` declaration names each value and where it comes from

```fiig
// config/sessions.fiig
config Sessions {
    maxAge: int from env "SESSION_MAX_AGE" = 86400
    exportToken: Secret from env "ACCOUNTS_EXPORT_TOKEN"
}
```

- `config` is a new declaration kind, one per file like the others,
  conventionally under `config/`.
- Each field is `name: Type from env "NAME"`, the form ADR 0069 gave route
  arguments (`from header "X"`). The variable name is a string constant,
  never computed, so the checker, the catalog, and the loader can all see
  it.
- `= constant` gives a default, used when the variable is unset or set to
  the empty string. Only a literal is allowed, the way an enum header's
  values are constants (ADR 0046 §4a).
- A field with no default and a `T | None` type is `None` when unset. A
  field with neither is required.
- Field types: `string`, `int`, `float`, `decimal`, `bool`, an enum (by
  variant name), `Secret` (§4), or any of these `| None`.
  - `bool` reads exactly `true` or `false`.
  - Numbers read as fiig literals of their type do (ADR 0063, ADR 0064).
  - A string is taken as it is, untrimmed.

Strict about what the program says (charter principle 10): a typo in a
deployment's `SESSION_MAX_AGE=86400s` is a refusal to start, not a
silent default, since the program declared an `int`.

### 2. Config is read where `Clock` is: activities and gateways

`Sessions.maxAge` reads a value, the way `Clock.now()` does. Config is a
local effect (ADR 0068 §4):

- **Activities and gateways** read it directly. It doesn't count as a
  gateway's one capability (ADR 0010), so a gateway that `requires
  Client` can still read its API key.
- **Rules, conversions, formulas, and workflows** can't, and take the
  value as an argument instead. Reading it there is a checker error, as
  calling `Clock` there is (B10). This keeps them testable without an
  environment, and keeps a rule's answer a function of its arguments.

The case is weaker than `Clock`'s: config is fixed for the life of the
process, so a rule reading it would be deterministic within one run. It is
kept anyway so that a rule's tests don't depend on which variables are
set, the same rule gives the same answer on a laptop and in production,
and "depends on the deployment" stays in the kinds a reader already
treats as effectful (ADR 0044). The cost is that a value a conversion
needs travels through data. In `examples/accounts`, `startSession` reads
`Sessions.maxAge` into the `Session` it returns, and `loggedIn` builds
the cookie from `session.maxAge`, which the stored session's expiry needs
anyway.

### 3. Every value is read and checked before the program starts

`fiig serve` and `fiig run` read every `config` value the entry point can
reach (the served adapter's workflows, or the run declaration, and
everything they call) before serving or running anything. The checker's
call graph already knows what is reachable.

- Anything wrong stops the process, and the error lists **every**
  problem at once, not just the first: unset required variables, and
  values that don't parse, each with the declaration and type that wanted
  it.
- After that the values never change for the life of the process.
- Config that the entry point can't reach isn't read, so a one-off `fiig
  run` of a schema setup doesn't demand the payment provider's key.
- A bad value is quoted in its error (`SESSION_MAX_AGE="86400s" isn't an
  int`), except a `Secret`'s, which is never printed.
- **Provider selection.** A rule that selects a capability group (ADR 0043
  §3) makes every group it could return reachable, so a service that can
  pay through Stripe or Square needs both keys at startup. The converse is
  what lets a deployment prove what it doesn't contain: an application
  package whose selection rule can't return Square, and whose manifest
  doesn't depend on it, neither loads Square's code nor needs its key. A
  vertical that must not use a provider (for a legal reason, say) is its
  own small application package over shared ones, not a branch or a
  flag; the corpus will show it (Consequences).
- Reading on first use is rejected: it is the startup-then-fault failure
  this ADR exists to prevent. Reading every `config` in the program is
  rejected too: every one-off `fiig run` would need the production
  environment, and dummy values would follow. `fiig check` never reads the
  environment, so CI needs no secrets.

### 3a. `-env-file` supplies environment variables for development

`fiig serve -env-file .env` and `fiig run -env-file .env` read a file of
variables into the same lookup the environment feeds. It doesn't add a
second configuration system: `config` declarations stay the only thing
fiig code reads, and the file is one more way to set the environment.
This is the job Spring's `application.properties` does for local
development, and the convention `docker run --env-file`, Compose, and
Node's and Python's dotenv libraries share.

- **The real environment wins.** A variable set in the process
  environment overrides the file's, so a deployment can override one value
  without editing it.
- **Only with the flag.** No file is read by default, not even `.env` in
  the working directory, so production behaves the same with or without
  one lying around.
- **The format** is the common subset:
  - one `NAME=value` per line, with an optional leading `export `;
  - blank lines and lines starting with `#` skipped;
  - a value wrapped in matching `"` or `'` has them removed;
  - no `${}` interpolation and no escapes.

  A line that isn't one of these stops startup, naming the file and line.
- **The caps file's `${NAME}`** (ADR 0027) reads the same lookup, so a
  development DSN can come from the file too.
- A missing file named by the flag is an error, not an empty file.

The docs will recommend keeping `.env` out of version control and
committing a `.env.example` instead; `fiig config` (§5) prints the list it
should contain.

### 4. `Secret` is a prelude type for values that must not be shown

```fiig
activity exportAllowed(header: string) -> bool {
    token = bearerToken(header)
    match token {
        None -> false
        given -> Sessions.exportToken.matches(given)
    }
}
```

- `Secret` is a prelude type (not a new primitive, so ADR 0042's closed
  set is untouched). Only config produces one.
- **It can't be shown by accident.** It can't be interpolated into a
  string, compared with `==`, or be part of a type the adapter or `fiig run
  -json` encodes (a response or request body, or a model field that
  reaches one); each is a checker error. A model may still hold one, to
  pass credentials between gateways. Everywhere a value is printed
  (traces, fault messages, `fiig run` output) it appears as
  `Secret(ACCOUNTS_EXPORT_TOKEN)`, naming the variable and nothing else.
- **`.reveal() -> string`** gives the value, for the one place it is
  meant to go, such as an `Authorization` header. Searching for
  `reveal(` finds every such place. It is legal only in a gateway, the
  kind that hands values to the outside world; anywhere else it is a
  checker error.
- **A `Secret` may be passed anywhere a value may**, rules included, since
  checking a signature is pure computation (alpha item 8a's
  `hmacSha256(key: Secret, ...)`). Outside a gateway it can only be
  passed on, matched, or given to such an operation, so a rule can't turn
  it into a `string` and return it.
- **`.matches(candidate: string) -> bool`** compares in constant time,
  so checking a presented token doesn't leak it through timing. Like Go's
  `subtle.ConstantTimeCompare`, it still reveals the length.

Precedent: Pydantic's `SecretStr.get_secret_value()` and Rust's
`secrecy::ExposeSecret`. Neither stops interpolation or encoding before
the program runs; fiig's checker does. Not promised: zeroing the memory
afterwards, which a garbage collector makes unreliable. A token read
`from header` stays a `string` for now.

### 5. A package declares its own config, and the loader lists it

`acme-stripe` declares `config Stripe { apiKey: Secret from env
"STRIPE_API_KEY" }`, and the application sets the variable.

- The catalog card of every `config` declaration lists its variables,
  types, defaults, and whether each is a `Secret`.
- `fiig config <root> [entry]` prints every variable a program (or one
  entry point) reads, from every package, so a deployment's checklist is
  derived rather than written by hand.
- The bump check (ADR 0033) reports a dependency version that reads a
  variable the previous version didn't, the same way ADR 0035 reviews
  new capabilities. A package can't start reading `AWS_SECRET_ACCESS_KEY`
  in a patch release without it showing.
- Two declarations may name the same variable; they read one value, and
  each must parse it as its own type.

**Sharing a setting: import its declaration.** A `config` is exported
and imported like any declaration. A library that builds on another
reads that one's setting rather than declaring a copy:

```fiig
// package acme.pgmigrate
use acme.postgres.Postgres
... Postgres.url.reveal() ...
```

One declaration means one value, one type, and one catalog card, so two
libraries can't disagree about whether `DATABASE_URL` is a `Secret`.

**Pointing a library at another variable: the application renames it.**
For libraries that don't know about each other (one reads `PG_URL`,
another `DATABASE_URL`), or a name that clashes with a company's
convention, the application's manifest maps a dependency's variable to
the one the deployment sets:

```json
{
  "path": "checkout.app",
  "kind": "application",
  "dependencies": { "acme.pgmini": "1.2.0", "acme.postgres": "2.0.0" },
  "environment": { "acme.pgmini": { "PG_URL": "DATABASE_URL" } }
}
```

- Only an `application` manifest may rename, since only the application
  knows the deployment; a library renaming its dependency's variables
  would hand the application names it never chose.
- Renaming a variable the package doesn't read is a loader error, so a
  mapping a later version made stale fails loudly.
- `fiig config` and §3's errors show the effective name and where it came
  from: `DATABASE_URL is unset; config PgMini.url (acme.pgmini; PG_URL
  renamed in fiig.pkg.json)`.
- It sits in the manifest, beside ADR 0035's capability allowlist, because
  it is package wiring that a reviewer already reads there.

**Not solved by renaming: two accounts of one provider.** MVS loads
`acme.stripe` once, so its `config Stripe` has one value, and renaming
changes which variable feeds it, not how many there are. Two Stripe
accounts in one program is ADR 0043's open question on parameterized
providers; renaming is the natural building block if it is taken up.

### 6. The caps file stays, folded into the same checks

ADR 0027's caps file keeps configuring Go-backed providers, with
`${NAME}` reading the environment. `config` is for fiig code. The caps
file chooses which Go implementation backs a capability, which fiig code
can't see, so the two aren't merged. Three things make them one system to
a deployment:

1. **One lookup.** `${NAME}` reads what `config` reads, `-env-file` (§3a)
   included, with the real environment winning.
2. **One report.** An unset `${NAME}` joins §3's startup report, so every
   problem in both is listed at once:
   ```
   error: configuration (2 problems):
     DB_PATH is unset; caps.json Database.dsn
     STRIPE_API_KEY is unset; config Stripe.apiKey (acme.stripe)
   ```
3. **One checklist.** `fiig config -caps caps.json <root>` lists the caps
   file's variables too, so its output is the deployment's whole list.

The caps file shrinks as providers move into fiig (ADR 0043; Postgres over
`Socket`, alpha item 9's checked `Database`): their settings become
`config`, shared by §5's import. What remains at the end configures the
primitives. Moving the DSN into `config` before `Database` is written in
fiig is rejected: fiig code would read a value only to hand it back to Go.

### 7. Revisiting ADR 0070 §1: CORS origins stay in deployment

ADR 0070 §1 kept allowed origins in `fiig serve -allow-origin` or
`$ALLOW_ORIGINS` until fiig code could read configuration. Now it can,
and they stay where they are. An adapter clause reading a `config` field
(`allows origins from Browser.origins`) was considered and rejected:

- Origins are the deployment deciding whom it trusts, not something the
  program reads (charter principle 10). The routes don't change when the
  list does, and no fiig code would use the value.
- Everything else ADR 0070 needs is derived from the routes; origins are
  the one input that genuinely varies per environment.
- It would need a list type in config, which §8 leaves out.

They join this ADR's machinery instead, as does `LISTEN_ADDR`, `fiig
serve`'s other setting from the environment:

1. Both read §3a's lookup, so `.env` can hold `ALLOW_ORIGINS=http://localhost:5173`.
2. A bad origin joins §3's startup report.
3. `fiig config` lists both, as optional and read by `fiig serve`.
4. An adapter's catalog card says browser calls are allowed from
   `ALLOW_ORIGINS` at deploy (ADR 0070), so a reader sees it without a
   clause.

This would be revisited if a real service needed different origins per
adapter in one process (ADR 0070 §9's split into two adapters covers it
today), or code that must never be callable from a browser whatever the
deployment says.

### 8. Scope

- **Not in this ADR:**
  - config files other than §3a's (YAML, TOML, properties): the
    environment is the one source, and `-env-file` only feeds it;
  - reloading values while running (amended by ADR 0140: a setting saved
    in the app is read live, one snapshot per run; env stays fixed);
  - profiles in the source (Spring's `application-prod.yml`): the
    deployment *is* the profile;
  - remote stores (Vault, AWS Secrets Manager), which inject into the
    environment in every common setup anyway;
  - lists (`ALLOWED_HOSTS=a,b`), until a real need settles how one is
    split.
- **Lists** can be read as a `string` and split in a rule with ADR 0067's
  `split`, so the program, not a convention, decides how.
- **Testing.** Rules, conversions, and formulas never read config (§2).
  An activity or gateway can be run with `fiig run -env-file test.env`.
  Go tests in this repository supply the lookup directly rather than
  setting process variables, which leak between parallel tests. A future
  `fiig test` should take values per test; that is its own ADR.
- **Unchanged:** `fiig check` (it never reads the environment), the route
  grammar, and OpenAPI.

## Alternatives considered

**A. `Env.get("NAME") -> string | None`**, a local effect read anywhere
(Node, Elixir). Rejected: untyped, so every caller parses; unchecked at
startup, so a missing variable surfaces on the first request that needs
it; and computed names hide what a package reads from ADR 0035's review.

**B. Fields on a `model`** (`model Sessions { maxAge: int from env ... }`)
rather than a new kind. Rejected: a model is a value anyone constructs;
config is a single value the runtime fills in once. Sharing the
declaration would make `Sessions(maxAge: 5)` legal and meaningless.

**C. Config as a capability** (`requires Stripe`). Rejected: ADR 0010
gives a gateway exactly one capability, which an API client spends on
`Client`. Config is a value, like `Clock`'s, not a service.

**D. Readable from rules too**, since values never change after startup.
Rejected: a rule's tests would then depend on the environment, and its
answer would no longer be a function of its arguments.

**E. A properties file as a second source** (Spring's
`application.properties` with relaxed binding). Rejected in review in
favor of §3a: defaults already live on the declaration, values per
environment in the environment, and relaxed binding means learning a
mapping to tell which variable feeds which field.

**F. Config in the caps file** (`"config": {"maxAge": 86400}`). Rejected:
untyped, invisible to the checker, and a second place for the same
thing the environment already holds.

## Consequences

- `acme-stripe` can authenticate, `accounts` can configure its session
  lifetime, and abs-recommendations can take its URLs and keys from the
  deployment.
- A deployment with a missing or malformed variable fails at startup,
  listing every problem.
- Every variable a program reads is listed by `fiig config` and on its
  catalog cards, and a dependency reading a new one is caught by the bump
  check.
- One new declaration kind, one prelude type, and one new command.
- **Corpus, requested in review:** a per-vertical deployment. Shared
  logic moves into a package that depends only on `acme.payments`, and
  two small application packages over it choose providers: the existing
  checkout app (Stripe or Square) and a vertical whose manifest has only
  `acme.stripe`, so `fiig config` lists no Square key for it and a `use
  acme.square` there fails to load.

## Open questions

1. ~~Whether a `Secret` may be passed to a rule.~~ Yes, for HMAC (§4),
   with `.reveal()` legal only in gateways.

## Implementation notes (2026-11-07)

Built in six steps: the `config` declaration (parser, formatter, catalog
card); the checker (V1, B10 widened, S1, S2, and `Secret` as an opaque
prelude type); the runtime's one read (`Interp.LoadConfig`); the lookup,
reachability, and one report in `tooling/runprogram`, with `-env-file` on
`fiig run` and `fiig serve`; manifest renames, the loader's check of them,
`fiig config`, and the bump diff; and the corpus. Dogfooded with the real
binary: the accounts service's report, `.env.example`, a session cookie
lasting `SESSION_MAX_AGE`, and the export token; and the vertical's
payment through a scripted TLS socket, its renamed Stripe key going out as
one `Authorization` header and appearing nowhere in the trace. Decisions
made while building, beyond the text above:

- **Defaults may be constant arithmetic** (`= 24 * 60 * 60`), not only a
  literal: the same constants an enum header takes, plus Go-style constant
  folding. A literal default keeps its written scale (`= 1.50` for a
  decimal is 1.50).
- **Bug-class codes:** V1 for a config declaration the runtime couldn't
  read, S1 for a `Secret` shown, S2 for `.reveal()` outside a gateway, and
  B10 widened to reading config. `==` on a `Secret` is M1, the existing
  comparability class, whose message points at `.matches()`.
- **Where S1 looks for encoding:** a workflow's parameters and result (the
  adapter boundary, where H5 already keeps a `Connection` out) and an
  `HttpResponse` body. A model may hold a `Secret`. `fiig run -json` of an
  activity that returns one prints `"Secret(NAME)"`.
- **Reachability** walks resolved names from the entry: calls, path-tail
  group and config imports, qualified calls into packages, and a capability
  group used as a value, which reaches every gateway in it. `fiig config`
  with no entry lists what any root declaration reaches.
- **The one report** holds config, the caps file's unset `${NAME}`s (now
  every one, with where it is: `caps.json Database.dsn`), and a bad
  `-allow-origin`. A malformed or missing `-env-file` is reported by itself
  first, since nothing else can be read without it. A caps `${NAME}` set to
  the empty string is still the empty string; only config reads empty as
  unset.
- **The bump diff** reports a release that reads a new variable: Major when
  every deployment must now set it, Minor when it has a default or is
  optional; a type change is Major, a changed default Minor, and a
  variable no longer read Patch.
- **`fiig serve`'s own settings** (`LISTEN_ADDR`, `ALLOW_ORIGINS`) read the
  lookup, appear in `fiig config` for an http adapter, and an http
  adapter's catalog card says where its browser origins come from (§7).
- **`types.Package` gained `Path` and `Dependencies`,** so a dependency's
  imports can be followed to the package they name.
- **`acme.http`'s `WebRequest` gained an optional `authorization`,** the
  one header its client now sends, so the providers can authenticate.
- **The per-vertical example** extracted `checkout.core` (the merchant
  model and the ledger capability) from `checkout-app`. The vertical's own
  declarations have distinct names (`chooseClientProvider`) because the
  whole `examples/` tree is also checked as one universe.
  `tooling/runprogram`'s `TestAVerticalProvesWhatItDoesNotContain`
  attests it.
- **Two checker bugs the corpus found.** A call into another package
  checked its argument expressions in the callee's package, so any
  package-level name in an argument (an enum, a group, a config) was
  "undefined"; arguments now check where they were written
  (`TestCrossPackageArgumentsResolveInTheCaller`). And an import of a
  package that isn't a dependency reported nothing on its own line, only
  "undefined name" where it was used; it now says the package isn't a
  dependency.
