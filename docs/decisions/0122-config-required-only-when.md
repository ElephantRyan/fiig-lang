# ADR 0122: A Config That Is Required Only When Another Says So

Status: Accepted (2026-11-20; the owner approved the design in review).
Implemented 2026-11-20.
Date: 2026-11-20
Amends: ADR 0071 (§1: a config declaration may have a condition; §3: what
"every value is read and checked before the program starts" covers).
Related: ADR 0043 (providers a rule selects), ADR 0054 (a package's names),
ADR 0091 §5 (config in tests), ADR 0105 (a package may not import another in a
cycle), abs-recommendations GAPS G50.

## Context

ADR 0071 §3 reads every config value a program can reach before it starts, so
a deployment that forgot a variable finds out then and not on the first
request. "Can reach" is static. abs-recommendations has two ways to sign in,
chosen by `AUTH_MODE` (`abs`, `oidc` or `both`), and its sign-in rule *can*
return the Authentik provider, so Authentik's four `OIDC_*` variables are
required in every deployment. A self-hoster who only wants password sign-in
sets four placeholder values to start the server, which is what its
`serve-dev.sh` does.

Whether Authentik is used depends on another setting, and that is the shape of
the fix: **a requirement that is a function of another setting, checked once at
startup.**

### How others handle it

| Ecosystem | How "required only when X" is expressed |
| --- | --- |
| Spring Boot, Micronaut, Quarkus | `@ConditionalOnProperty`, `@Requires`, `@IfBuildProperty`: the configuration class doesn't exist unless a property says so |
| Python, Pydantic Settings | optional fields and a `model_validator` that raises when the mode needs them; or a **discriminated union**, a tagged union whose variants carry their own required fields |
| Go, `envconfig` and `caarlos0/env` | only an unconditional `required`; anything conditional is a hand-written `Validate()` |
| Rust, `clap` and `serde` | `required_if_eq("mode", "oidc")` on a flag; for files, a `#[serde(tag = "mode")]` enum |
| TypeScript, `zod` | `z.discriminatedUnion("AUTH_MODE", [...])`, one object shape per mode |
| JSON Schema, Helm values | `if`/`then`/`else` and `dependentRequired` |
| Nix modules | lazy: an option with no default errors when something evaluates it, and `mkIf cfg.enable` gates the rest |
| Rails, Elixir `runtime.exs` | `ENV.fetch("X")` inside an `if`: the conditional is code, and it fails when the branch runs |

Three families: a **condition on the requirement** (Spring, `clap`, JSON
Schema), a **sum type** (Pydantic unions, serde tags, `zod`), and **validation
in code** (Pydantic validators, Go, Rails). Validation in code needs a startup
hook to keep ADR 0071's fail-fast, and fiig has none, so it would find a
missing variable on first use. A sum type is the best type design but the
largest change to config, and it would put a provider package's settings
inside the selector's. A condition on the requirement is declarative, so
tools can read it, and it keeps the startup check.

## Decision

### 1. `config Name when condition { ... }`

```fiig
config Oidc when SignInConfig.authMode != "abs" {
    issuer: string from env "OIDC_ISSUER"
    clientId: string from env "OIDC_CLIENT_ID"
    clientSecret: Secret from env "OIDC_CLIENT_SECRET"
    redirectUri: string from env "OIDC_REDIRECT_URI"
}
```

The config is **present** when the condition holds, and then every field is
required as ADR 0071 says. It is **absent as a whole** when it doesn't.
`when` is a contextual word.

A condition is a bool made of:

- comparisons with `==` and `!=`, and `and`, `or`, `not` and parentheses;
- literals (`"abs"`, `3`, `true`);
- enum variants (`Flavour.Spicy`);
- fields of config declarations (`SignInConfig.authMode`) **that have no
  `when` of their own**, in this package or in one it imports as `use
  acme.signin.SignInConfig`.

Anything else is V2: arithmetic, ordering, calls, interpolated strings,
names, the config's own fields. The subset is small on purpose. The condition
is read once at startup, before any program code runs, so it can only use
what is known then; the loader evaluates it, and nothing else can.

**Why no `when` on a config a condition reads.** A condition that read a
conditional config would read something that might be absent, and chains need
ordering and cycles need finding. One level covers the case, and a second
level is reachable by `and`.

### 2. Evaluated at startup, after the unconditional config

`LoadConfig` reads every config with no `when` first, then evaluates each
`when` against those values:

- **true**: the config is read as ever, and a missing required variable is a
  problem in the same report;
- **false**: its fields aren't read at all. Variables set for it are ignored,
  not complained about (charter principle 10: lenient about what it ignores).

If a condition reads a field that didn't load, that is already a problem
above, and the condition is not reported again. `fiig test` loads config
partially (ADR 0091 §5), so a conditional config is present there and a test
fakes what it reads.

### 3. Reading an absent config is a fault

A field keeps its declared type: there is no `T | None` and nothing to unwrap.
Reading a field of a config whose condition is false faults:

```
config Oidc isn't set in this deployment: its condition (SignInConfig.authMode != "abs") is false (ADR 0122)
```

That is a defect in the program, as an out-of-range index is: code that reads
`Oidc` under a condition that said it isn't there. It is a 500 on a request and
a clear message in the log.

**What this gives up.** The condition and the code that decides to use the
config both say when it is in play (`AuthentikSignIn` is chosen only when the
mode isn't `abs`), and nothing forces them to agree. A test that fakes the
config exercises the code path, and a deployment that disagrees finds out at
the first read. A sum-type config (open question 1) would remove the
duplication, at a much larger cost.

### 4. `fiig config` prints the condition

The checklist says when a variable applies, which a bean-based framework
can't, since its requirements are in code:

```
AUTH_MODE           string  = "both"                                      config SignInConfig.authMode
OIDC_CLIENT_ID      string  required when SignInConfig.authMode != "abs"  config Oidc.clientId
OIDC_ISSUER         string  required when SignInConfig.authMode != "abs"  config Oidc.issuer
```

The config a condition reads is listed even if no code reads it, since it is
needed to start.

### 5. Where the condition's config lives

A condition names config the declaring package can import, and ADR 0105
forbids import cycles. In abs-recommendations `signin` imports `authentik` (it
chooses the provider), so `Oidc` can't read `SignInConfig` if that stays in
`signin`. **The config a condition reads must live in a package below both**:
abs-recommendations moves `SignInConfig` into `accounts`, which both already
import. The language doesn't hide this; the import error says it.

## Alternatives considered

- **Optional fields, validated in code.** No language change, and the
  conditions can be anything. But fiig has no startup hook, so a deployment that
  wants OIDC and forgot a variable finds out on the first login. A startup hook
  is a feature worth having on its own (open question 2).
- **A sum type in config** (Pydantic unions, serde tags). The best types: absence
  is unrepresentable, so no `| None` and no fault. But it is the largest change
  to config, and it puts `authentik`'s settings inside `signin`'s type, which is
  the dependency direction the packages avoid. See open question 1.
- **Providers bound by the capabilities file** (as `Database` and
  `StreamSource` are). An unbound provider's package is never reached, so its
  config disappears. It suits an infrastructure choice and fits a product mode
  switch badly: `AUTH_MODE=both` binds two, and a mode and a binding can then
  disagree, which is the problem in a new place.
- **`T | None` for a conditional config's fields.** Every use must handle
  `None`, even in code that runs only when the condition holds, which is noise
  and a branch that can't be tested.
- **Lazy: required when read** (Nix, Rails). It gives up the startup check that
  ADR 0071 chose.
- **Leave it**, and document that a password-only deployment sets placeholders.
  The friction stays, and placeholders hide a deployment that really is
  misconfigured.

## Consequences

- `ConfigDecl.When` in the syntax, the parser, and the formatter.
- V2 in the checker, and the condition is checked as a bool.
- `LoadConfig` reads in two phases, and `Interp.ConfigAbsent` says why a config
  isn't there. `ConfigSource` carries the condition's text and the imports it
  resolves in.
- `fiig config` prints `required when ...`, and lists the config a condition
  reads.
- abs-recommendations moves `SignInConfig` into `accounts` and adds a
  `when` to `Oidc`, which retires its placeholder `OIDC_*` values.

## Open questions

1. **A sum-type config** (the Pydantic and serde shape), if a program grows
   many modes with their own settings (several auth schemes, several storage
   backends). One `when` per mode is where this design starts to strain, and a
   tagged config would carry the variants' fields in its type.
2. **A startup hook**: an activity the runtime calls before it listens, for
   checks no declaration can express (the database answers, a bucket exists).
3. **A richer condition**: `in` over a list of values, ordering on ints, a
   variable being set at all. Each one makes the checker's subset harder to
   state, so none is added until a program needs it.
4. **`fiig test` and absence.** A test can't check that code behaves when a
   config is absent, since a test sees every conditional config as present. A
   `fake` that says "absent" would.

## Implementation notes (2026-11-20)

- **Syntax and formatting:** `ConfigDecl.When`; `parseConfigDecl` reads `when`
  after the name and parses an ordinary expression; the formatter writes it
  back and is stable.
- **V2 (`compiler/checker/config.go`):** a structural walk over the allowed
  nodes, then a type check as a bool, with the condition read as a gateway
  reads config (B10 is about code, and a condition is the loader's).
- **`compiler/runtime/envconfig.go`:** `loadConfig` reads the unconditional
  sources, sets them as `in.Config`, evaluates each `when` with the ordinary
  expression evaluator in the declaring package's scope, then reads the ones
  that hold. `in.Config` is restored if there are problems. A fault on an
  absent config names it and its condition.
- **`tooling/runprogram/config.go`:** the walker adds the config a condition
  reads (`addConfig`), and `ConfigSource` carries `Groups` and `WhenText`.
- **Tests:** parser, formatter, and checker (accepted conditions, every V2
  case, bool-ness, a missing field, a condition reading a conditional
  config); the runtime, in both directions (not required when false, required
  as always when true, `and` across configs, a problem in what a condition
  reads reported once, a test's partial load); and `fiig config`.
- **Dogfooded on abs-recommendations**, in a scratch copy with `SignInConfig`
  moved to `accounts` and `Oidc` given `when SignInConfig.authMode != "abs"`:
  `fiig check` is clean and its 48 tests pass; **`AUTH_MODE=abs` with no
  `OIDC_*` variables starts** (G50's repro); `AUTH_MODE=both` with none
  refuses and names the four; `AUTH_MODE=both` with them starts; and `fiig
  config` lists them as `required when SignInConfig.authMode != "abs"`.
  Their repository is unchanged.
- **Language friction.** (1) The import cycle in §5 was found by trying it,
  and the real constraint is not obvious from the syntax: a condition's config
  must sit below the provider. (2) The condition and the selector duplicate
  the same fact (§3).
