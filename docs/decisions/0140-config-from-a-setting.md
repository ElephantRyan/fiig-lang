# ADR 0140: Config From a Setting

Status: Accepted (2026-11-21; the owner approved it, choosing in review that
settings can be live and that fiig supports both soft settings (live) and hard
ones (applied on restart), §1a). Implemented 2026-11-21; see Implementation
notes.
Date: 2026-11-21
Related: ADR 0071 (config from the environment; §8 leaves reloading out),
ADR 0122 (`config ... when`), ADR 0124 (startup checks), ADR 0126 (a gateway
can make a `Secret`), ADR 0127 (`from startup`), ADR 0138 (a rename keeps
its history), ADR 0010 (one capability per gateway), Hearsay GAPS G52.

## Context

Hearsay's self-hoster sets the app up in the browser the first time they
open it. The ABS address, the service token, the sign-in mode, the
Authentik client and mirroring all come from that screen, with no env file.
ADR 0071 config can't hold them: it is read from the environment once,
before the program starts, and never changes.

ADR 0126 fixed the narrow part. A gateway can read a stored value back as a
`Secret`. Hearsay built the rest by hand (`src/settings/`, 34 files):

```fiig
activity serviceToken() -> Secret | None {
    return storedSecret("abs.serviceToken") or AbsService.token
}
```

Three costs remain:

1. **The values travel by hand.** An `HttpClient` gateway can't also read
   the `Database` (ADR 0010), so it can't read a setting. The address and
   token are loaded by an activity and passed down as an `AbsTarget`: 32
   files name it, every `*AsService` gateway takes it, and `accounts.SignIn`
   gained a `server:` parameter. Before, these were four config reads.
2. **Config's guarantees are gone.** A setting is a `string` keyed by a
   `string`. Nothing checks that `"abs.serviceToken"` names a real setting,
   parses `"true"` into a `bool`, or reports it in `fiig config`. A typo in
   a key is a setting nobody reads.
3. **The order between the two sources is spelled in every read.**
   `storedText("oidc.issuer") or Oidc.issuer` appears once per setting per
   reader, and every copy must agree.

## Precedent

- **Discourse** keeps *site settings* in the database, edited from the
  admin screen. A `DISCOURSE_*` environment variable takes precedence, and
  the screen then shows that setting as locked. **GitLab** does the same
  with `gitlab.rb`: values set there can't be changed in the UI. One
  source per setting per deployment, chosen by the operator.
- **WordPress** keeps `wp_options` in the database and only the database
  credentials in `wp-config.php`. **Nextcloud** writes `config.php` from
  its installer and keeps app settings in an `appconfig` table. Both read
  settings live.
- **Gitea**'s first-run installer writes `app.ini`, and changing it later
  needs a restart. This is the *restart to apply* model: settings stay fixed
  for the life of the process.
- **Django** has static settings. `django-constance` adds settings backed by
  the database, behind a cache, read live. **Spring Cloud**'s
  `@RefreshScope` rebuilds beans when config changes. Its known pitfall is a
  request that reads half old and half new values.
- **Kubernetes ConfigMaps** mounted as files update in place, while ones
  injected as env vars don't. The platform itself keeps both models.

The shared lessons: the operator decides which source wins, a live value
must be read consistently within one unit of work, and restart-to-apply
buys simplicity at the cost of a restart nobody can trigger from inside the
app.

## Decision

### 1. A config field may name a setting

```fiig
config AbsService {
    url: string | None from env "ABS_URL" or setting "abs.url"
    token: Secret | None from env "ABS_SERVICE_TOKEN" or setting "abs.serviceToken"
}
```

```ebnf
config-field  ::= identifier ':' type 'from' source { 'or' source } [ '=' expr ]
source        ::= 'env' string-literal | 'setting' string-literal [ 'on' 'restart' ]
```

- Sources are tried in the order written, and the first one present wins.
  The idiom, and what `docs/deploying.md` shows, is **env first**
  (Discourse, GitLab): when an operator sets the variable, the screen
  can't override it, and `fiig config` reports the setting as "set by the
  deployment". Writing `setting` first is legal, for an app that wants the
  admin to win, and the order is visible at the declaration.
- A field with a `setting` source **must be `T | None` or have a default**
  (V1). On first run no setting exists, and the process must still start.
  "Not set up yet" is then a `None` the compiler makes every reader handle,
  not a convention.
- The value is **parsed to the field's type** just as an env value is.
  `bool`, `int`, `decimal` and enums parse the same way.
- A `setting` field is **config in every other way**. It is read as
  `AbsService.token` in an activity or a gateway (B10), and faked in a test
  (`fake AbsService { token: "svc" }`, ADR 0091 §4). A `Secret` field reads
  back as a `Secret` (ADR 0126) without any `toSecret` call.

Because it is config, the `HttpClient` gateway reads `AbsService.url` and
`AbsService.token` itself, with no `requires Database`: the runtime supplies
the value. This removes cost 1.

### 1a. Soft and hard settings

Some settings can change under a running process, and some can't. A token
can change between two requests. A pool size, a listen port or a cache
capacity is used up when the process starts. Every serious system has both
kinds: Discourse marks some site settings as requiring a restart, Grafana
and PostgreSQL (`pg_settings.context`: `sighup` against `postmaster`) say
which parameters reload, and Kubernetes keeps both ConfigMap models. The
declaration says which kind a setting is, because only the program knows
whether its code can take a change mid-life.

```fiig
config AbsService {
    token: Secret | None from env "ABS_SERVICE_TOKEN" or setting "abs.serviceToken"
}

config Library {
    indexWorkers: int from env "INDEX_WORKERS" or setting "library.indexWorkers" on restart = 4
}
```

- **Soft** (`setting "..."`, the default) is read live, under §3's snapshot
  rule. A save applies from the next run.
- **Hard** (`setting "..." on restart`) is read **once, at start**, like an
  env source, and stays fixed for the life of the process, as ADR 0071
  promises for all config today. A save is stored at once and applies on the
  next start. Until then `Settings.source` answers `Pending`, so a screen can
  say "saved; applies when the server restarts".
- Only soft settings make config change while the process runs. Everything
  ADR 0071 and ADR 0122 say about fixed config still holds for env sources
  and hard settings.
- What the runtime consumes before the program runs can't be a setting of
  either kind: the `Database` itself (the settings live in it), `-caps`
  provider settings, and `fiig serve`'s own variables. They stay env-only.

Soft is the default because live adjustment is why settings exist. A
setting that never changes without a restart could have been env. `on
restart` is a visible opt-out at the declaration, for code that sizes
something at start.

### 2. Where settings live

The runtime keeps them in its own table, `fiig_settings (package, name,
value, updated_at, updated_by)`, in the application's `Database`, next to
`fiig_migrations` (ADR 0096). It is keyed by package path, so ADR 0138's
`formerly` moves settings, the module's and its folders', along with the
migration history. Which `Database` a program has is the deployment's
choice (the capabilities file), so a program that reads a setting with none
bound is a **startup problem**, reported with the others, not a check
error.

### 3. Reading: one snapshot per run

This section is about soft settings. A hard setting is read with the env
sources, at start.

A *run* is one request, one stream element, one scheduled firing, or one
startup route. A run sees **one snapshot** of the settings, taken when it
first reads one, so it never mixes old and new values (the `@RefreshScope`
pitfall). A run that reads no setting costs nothing. The snapshot is kept
per process and reloaded when a version row changes. A write bumps that
row, so with several replicas each one sees a change on its next run, with
no time-to-live and no staleness window.

### 4. Writing: the `Settings` capability

```fiig
gateway saveServiceToken(token: string, by: string) -> Result<None, SettingError>
    requires Settings
{
    return Settings.save("abs.serviceToken", token, by: by)
}
```

- `Settings` is a prelude capability with three operations: `save(name,
  text, by:)`, `clear(name, by:)` and `source(name) -> SettingSource`
  (`Deployment`, `Saved`, `Pending` for a hard setting saved since the
  start, `Default`, `Unset`, for an admin screen that shows which source a
  value comes from).
- `name` must be a **string literal naming a declared `setting` source** in
  the calling package (V1), so a typo is a check error. This answers cost 2.
- `save` **parses before it stores**, using the field's type. A bad value is
  `Err(SettingError.Invalid(...))` at write time, not a fault on the next
  read.
- `save` of a field whose env source is set answers
  `Err(SettingError.SetByDeployment)` when env comes first. The screen
  must not pretend to change what the deployment fixed.
- The write path stays a `string`, as ADR 0126 left it: a `Secret` in a
  request body is a separate question.

### 5. What stays fixed at startup

- **ADR 0122's `when` may not read a soft setting** (V1). Which config is
  present is decided before the program starts. If that could change
  mid-process, startup checks would be lying. A hard setting is fixed at
  start, so a `when` may read it.
- **Startup checks (ADR 0124)** check the env sources and the hard settings
  as they stand at start. A setting is never required at startup, by §1.
- `fiig config` lists each `setting` source beside the env one: `ABS_URL,
  or setting abs.url (saved in the app)`.

## Rationale

The app needs values that change while it runs, and fiig's answer for
values the program reads is config. Making config able to change, under a
snapshot rule, keeps one way to read a setting, along with everything config
already checks. The other way leaves a second, untyped system, which
Hearsay built and paid 32 files for. The narrow rules keep ADR 0071's
promises where they matter: settings are optional, `when` can't read them,
and a run never sees two versions.

## Alternatives considered

- **Restart to apply only (Gitea).** Every setting would be read once at
  start. It keeps ADR 0071 whole, but a token change needs a restart the
  app can't do itself (`fiig serve` doesn't supervise itself). The owner
  chose live in review. Restart-to-apply survives as `on restart`, for the
  settings that need it (§1a), and `Pending` answers the "screen shows a
  value the process doesn't use" problem.
- **Live only.** Simpler, but a setting the program uses up at start (a
  pool size, a worker count) would appear to change and not change, or the
  program would have to re-read it on every run where nothing can use the
  new value.
- **The admin chooses, per save** ("apply now" / "apply on restart"). Whether
  a value can change mid-life is a fact about the code that reads it, not a
  preference of the person saving it.
- **Keep ADR 0126 as it is.** It works and it is shipped. The cost is the
  three above, paid again by every app with an admin screen.
- **A general key-value store** (`Store.get("abs.url")`). It is untyped,
  unchecked, and invisible to `fiig config`. This is ADR 0071's rejected
  `Env.get` (§Alternatives A) with a database behind it.
- **Settings win over env by default.** This is what Hearsay does today. It
  lets an admin fix a bad deployment value without shell access, but the
  deployment's value then means nothing, and an operator can't tell what
  the process uses from its environment. The order in §1 lets an app choose
  this; the idiom doesn't.

## Consequences

- Parser and formatter: `or` between sources, `setting` and `on restart`.
  `setting`, `on` and `restart` mean something only after a config field's
  `from`, so they stay usable as names everywhere else. Checker: V1
  widened (`| None`/default, a `Database`, `when` not reading a setting, the
  name literal in `Settings`). Runtime: the snapshot, `fiig_settings` and
  its version row, and the `Settings` provider. `fiig config` and the
  registry list setting sources.
- ADR 0071 §8's "reloading values while running" is amended: settings only,
  per run.
- Hearsay: `src/settings/`'s `stored*`/`remember*`/`load*` activities and
  `AbsTarget` go away. Gateways read `AbsService` again, `accounts.SignIn`
  loses `server:`, and `migrations/0004_settings.sql`'s rows move into
  `fiig_settings` (a one-off migration). The precedence flips to env first
  unless Hearsay writes `setting` first. I estimate 40-50 files removed or
  shrunk; to be measured.

## Open questions

1. **Restarting from the app.** `Pending` tells the admin a restart is
   due. Whether `fiig serve` should offer a graceful self-restart (exit with
   a code a supervisor restarts on) is left open.
2. **Audit.** `updated_by` is a string the caller passes. Once ADR 0137's
   principal reaches gateways, it could be filled in automatically.
3. **A request-body `Secret`** (ADR 0126's write-path gap) is still
   separate.
4. **A declaration named `Settings`.** `Settings` is a primitive, and a
   call `Settings.op(...)` reaches it before a declaration of that name, as
   `Session` does today (Hearsay and `examples/accounts` declare `model
   Session`). A config or model named `Settings` still reads its fields.
   Refusing every primitive's name (K2) would break those programs, so it is
   left for its own decision.

## Implementation notes (2026-11-21)

- Syntax and parser: `ConfigField.Setting`, `SettingFirst`, `OnRestart`
  and `SoftSetting()` (`compiler/syntax`); `parseConfigSources` reads
  either order, with `setting`, `on` and `restart` contextual
  (`compiler/parser`). The formatter writes the sources as they were
  written.
- Types: the prelude's `SettingError` and `SettingSource`, `Settings`'s
  operation table, and `Settings` added to the closed primitive set
  (`compiler/capability`), which amends ADR 0042's list: only the runtime
  can save a setting it reads back as config. An application lists
  `Settings` in its manifest's `capabilities` (ADR 0035), as it does
  `Session`.
- Checker (`compiler/checker/config.go`): V1 for the field and for a
  `Settings` call's name, V2 for a `when` reading a soft setting.
- Runtime (`compiler/runtime/settings.go`): `SettingsStore`,
  `MemorySettings` (each `fiig test` test has its own, so a test can save
  and read back), the snapshot a run takes at its first read (installed by
  `Interp.Call`, so every adapter's run has one), the version check, and
  `SettingsProvider`. A hard setting is resolved in `LoadConfig` and never
  again.
- Store: `runprogram.DatabaseSettings` over the bound `Database`, with
  `migrate.SettingsTables`; a save or clear and the version bump are one
  transaction. `fiig serve`, `fiig serve -replay` and `fiig run` set it up
  before `LoadConfig`. ADR 0138's `moveHistory` moves the settings.
- `fiig config` and the registry card show each field's setting and
  whether it waits for a restart.
- Tests: `TestSettingFieldsCheck`, `TestASettingsCallNamesADeclaredSetting`,
  `TestAWhenReadsOnlyHardSettings`, `TestSettingSourcesFormatAsWritten`
  (checker); `TestSoftSettingsAreLiveAndHardOnesWaitForARestart`,
  `TestARunReadsOneSnapshot`, `TestTheOrderOfSourcesDecidesWhoWins`
  (runtime); `TestSettingsLiveInTheDatabaseAndMoveWithARename` (SQLite).
- Dogfooded: a scratch app served twice on one SQLite file. A save through
  one process was read by the other on its next request; a hard setting
  saved as 8 stayed 4 until a restart, then read 8; `eight` was refused as
  not an int; with `ABS_URL` set, the variable won and a save was refused.
  Under `fiig test`, a test saved a setting and read it back.
