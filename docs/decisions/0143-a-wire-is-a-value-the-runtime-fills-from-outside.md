# ADR 0143: A Wire Is a Value the Runtime Fills From Outside the Program

Status: Accepted (2026-11-21; the owner approved it). Written in ADR 0142's
syntax (§7). Not implemented yet.
Date: 2026-11-21
Supersedes: the `principal` keyword of ADR 0137 and ADR 0141, and the
`config` keyword of ADR 0071. Their rules are kept and restated by source
in §3.
Related: ADR 0142 (semicolons, symbol operators, parenthesised
conditions), ADR 0071 (config), ADR 0122 (`when`), ADR 0127 (`from startup`),
ADR 0137 (principals), ADR 0140 (settings), ADR 0141 (named principals),
ADR 0054 (one public declaration per file), ADR 0104 (file names).

## Context

fiig has three declarations that look alike and are named apart (shown
with ADR 0142's `??` between sources, §7):

```fiig
principal me: accounts.Identity from cookie "sid" by signedIn else refused   // ADR 0137
config Abs {
    url: string | None from env "ABS_URL" ?? setting "abs.url"            // ADR 0071, 0140
}
config Health {
    storageDown: bool from startup "Preflight.verifyStorage"              // ADR 0127
}
```

Each line is `name: Type from source`: a named, typed value that the
runtime fills from outside the program (a request's credential, the
environment, a saved setting, a startup route's outcome). The program then
reads it by name. Only the keyword differs, and the source already says which
kind each one is.

`principal` names one use (who is calling), not the mechanism. The next
source that comes along, such as a tenant from a header, a locale from
`Accept-Language`, or a feature flag, would need either a new keyword or a
principal that isn't a principal. `config` has the same problem from the
other side. ADR 0140 made it hold values that change while the process
runs, which "config" doesn't suggest.

A design discussion on 2026-11-21 named the category. A **wire** is one such
value. **Wiring** is the general term for declarations that connect the
outside world to named values.

### How others handle it

| | One mechanism for credentials and settings | Consumer names it | Value typed |
| --- | --- | --- | --- |
| FastAPI | `Depends(current_user)`, `Depends(get_settings)` | yes, the dependency | yes |
| Dagger, Guice | `@Provides` methods | by type, or `@Named` | yes |
| Koin | `single { }`, `factory { }` | by type, or a qualifier | yes |
| Angular | `inject(TOKEN)` | yes, the token | yes |
| Spring | `@Value` for settings, `SecurityContext` for the caller | settings yes, caller ambient | yes |
| Axum | extractors (`CurrentUser`, `State<Config>`) | by type only | yes |

FastAPI is the closest. One mechanism covers the signed-in user, the
settings and a database session, and each consumer names the one it needs.
Spring keeps two mechanisms and makes the caller ambient, which ADR 0137
rejected. Axum picks the value by its type, which ADR 0141 rejected: one
type could then be filled only one way. A wire is named, so `Identity`
can come from a cookie in one adapter and an API key in another.

## Decision

### 1. `wire`, in two shapes

A **single** wire is one value, typed by a type declared elsewhere:

```fiig
// src/sessions/Me.fiig
wire me: accounts.Identity from cookie "sid" by signedIn else identityErrorResponse

// src/sessions/Admin.fiig
wire admin: accounts.Admin from me by accounts.signedInAdmin else accounts.notAnAdmin
```

A **grouped** wire is a record of values. It is read as `Name.field`, and it
is what `config` is today:

```fiig
// src/settings/Abs.fiig
wire Abs {
    url: string | None from env "ABS_URL" ?? setting "abs.url"
    token: Secret | None from env "ABS_SERVICE_TOKEN" ?? setting "abs.serviceToken"
    workers: int from env "INDEX_WORKERS" ?? setting "abs.indexWorkers" on restart = 4
}
```

```ebnf
wire-decl     ::= 'wire' identifier ':' type 'from' wire-source [ 'by' qualified-ref 'else' qualified-ref ]
                | 'wire' identifier [ 'when' '(' expr ')' ] '{' { wire-field } '}'
wire-field    ::= identifier ':' type 'from' wire-source { '??' wire-source } [ '=' expr ]
wire-source   ::= ( 'cookie' | 'header' | 'env' ) string-literal
                | 'setting' string-literal [ 'on' 'restart' ]
                | 'startup' string-literal
                | identifier                     (* another wire, ADR 0137 §2 *)
```

- The two shapes are told apart by what follows the name: a `:` starts a
  single wire, a `{` (or `when`) starts a grouped one. The parser already
  distinguishes these cases for `principal` and `config`.
- A single wire's type is a type declared elsewhere, usually a domain
  model (`accounts.Identity`). The wire doesn't declare it, because the type
  belongs to the domain, and two wires may fill the same type in different
  ways.
- A grouped wire is its own record type. Its consumer reads `Abs.url`,
  exactly as `config` is read today, with no separate model declaration.
- The capitalization stays as it is now: a grouped wire is capitalized
  like a record (`Abs`), and a single one is named like a value (`me`).
  N1 and its case rules carry over.

### 2. Consumers name the wire

- **An adapter** names the request wires its routes take, as ADR 0141's
  `principal sessions.me` did. It can also declare a wire inline, as ADR
  0137's inline principal did:

  ```fiig
  adapter http ratingsApi {
      wire sessions.me
      wire peer: LinkedServer from header "Authorization" by callingServer else peerRefused

      PUT "/ratings/{itemId}" -> putRating(me, itemId: string, input: RatingInput) { ... }
  }
  ```

- **An activity or a gateway** reads a grouped wire's field, `Abs.url`, as it
  reads config today (B10).

### 3. The rules follow the source

One keyword doesn't mean one set of rules. The checker derives each wire's
rules from its sources, and these are the rules ADRs 0071, 0122, 0127, 0137,
0140 and 0141 already set, with nothing new added:

| Source | Shape | Lifetime | Who may read it | When it can't be filled |
| --- | --- | --- | --- | --- |
| `cookie`, `header` | single | one request | only an adapter that names or declares it, passing it as a route argument (ADR 0137 §3: no ambient caller) | `else` answers the request (ADR 0137 §1) |
| another wire | single | as the wire it is settled from | the same as that wire | `else` |
| `env`, `setting … on restart` | grouped field | the process | an activity or a gateway (B10) | a startup problem (ADR 0071 §3), or `None`/default |
| `setting` | grouped field | one run (ADR 0140 §3) | an activity or a gateway | `None` or the default |
| `startup` | grouped field | the process, fixed after startup routes | an activity or a gateway | none: it is a bool (ADR 0127) |

The rules of the shapes are kept as well:

- **Request sources are single.** `cookie`, `header` and another wire need
  `by … else …`, and only a single wire has those. A grouped wire's fields
  take only process and run sources, since a record of request values with
  one refusal would hide which value was refused. (U-class, §6.)
- **Process and run sources are grouped.** `env`, `setting` and `startup` are
  fields of a grouped wire, so a program's settings stay together under one
  name, as `config` keeps them now, and `fiig config` reports them by group.
- `when` (ADR 0122) applies to a grouped wire only, and may read only
  process-lifetime fields (ADR 0140 §5).

### 4. Wiring, as a word

**Wiring** is the general term for declarations that connect the outside
world to named values. In this ADR it is only a word: the docs and the
IDE use it, and no `wiring { }` block is added. A block holding several wires
in one file is left open (Open questions): ADR 0141 deferred grouping
principals until a package has more than two related ones, and the same
trigger applies.

`application` and `provides` are not wires. They connect parts of the
program to each other and fill no value. They keep their own keywords.

### 5. Migration

- `fiig fmt -w` rewrites `principal` to `wire` and `config Name {` to
  `wire Name {`, in ADR 0142's rewrite pass (§7), and renames nothing
  else. File names don't
  change, since they follow the declaration's name (ADR 0104), not its
  keyword.
- In ADR 0142 §5's first step, `principal` and `config` still parse, and
  `fiig fmt -w` writes `wire`. In its second step they are errors that
  name the fix. ADR 0051 has no warnings, so it is an error, as ADR 0055's
  removed `value` is today (§7).
- The stdlib's 7 `config` declarations and the repo's other 7 move in the
  same commit as the implementation. Hearsay's 7 `config` declarations and
  12 inline `principal` lines move in one formatter pass (Lane A).

### 6. Bug classes

The U and V classes keep their numbers, and their messages say "wire":

- **U1–U9** (ADRs 0137, 0141) apply to single wires.
- **V1, V2** (ADRs 0071, 0122, 0140) apply to grouped wires.
- **U10, new:** a source in the wrong shape. That is a `cookie`, `header`
  or another wire as a grouped field, an `env`, `setting` or `startup`
  source on a single wire, or a single wire with process sources and a
  `by`.

### 7. With ADR 0142

This ADR is written in ADR 0142's syntax, and lands with it:

- **Sources are joined by `??`.** A field's sources are tried in order and
  the first one present wins, which is what `??` means in ADR 0142 §3.
  ADR 0140's `or` between sources is ADR 0099's `or`-as-default, which 0142
  retires, so `from env "X" or setting "x"` becomes `from env "X" ?? setting
  "x"`.
- **A grouped wire's `when` is a parenthesised condition** (ADR 0142 §4),
  using `!`, `&&` and `||` (§3): `wire Oidc when (SignInConfig.authMode !=
  AuthMode.Abs) { ... }`. V2's subset is unchanged apart from the
  spelling.
- **Wires take no `;`.** They are declarations, not statements (ADR 0142
  §1). That holds for a single wire, a grouped wire's fields, and a wire an
  adapter declares or names. ADR 0142 §1 lists these as "principals".
- **One migration.** ADR 0142 §5 lands in two steps, first both spellings
  then errors for the old ones, and this ADR's keyword change lands in the
  same steps. `fiig fmt -w` writes `wire`, `??` between sources and `when
  (...)` in the same pass that writes 0142's `;`, parentheses and symbol
  operators. In step 2, `config`, `principal` and `or` between sources
  become errors that name the fix. ADR 0142 already keeps `or` reserved
  for that error.

## Rationale

- **One mechanism, one word.** Wires already share their syntax, their
  checks for name and type, and their place in `fiig config` and OpenAPI.
  Separate keywords made one idea look like three.
- **The word doesn't limit what comes next.** A tenant from a header, a
  locale or a flag is another wire with another source. It doesn't need a
  keyword of its own.
- **Nothing is lost.** Each source keeps its lifetime, its readers and its
  failure, as §3 shows. The source sits on the same line as the keyword,
  where the reader is already looking.
- **Named, not typed.** Because consumers name the wire, the same type can be
  filled several ways, which is ADR 0141's reason for rejecting Axum's
  extractors.

## Alternatives considered

- **Keep `principal` and `config`.** Nothing to migrate. But each new
  source either needs a keyword or gets bent into one of these, and the
  names are already less accurate since ADR 0140.
- **A wire declares the model it produces** (`wire { model Principal ... }`).
  A consumer would get a type without another file. But the type usually
  belongs to a domain, `Identity` to `accounts`, and a second wire filling
  it would have to declare it again or depend on the first wire. The
  grouped shape already produces a record for the case where the wire
  really is the type.
- **One keyword, and one shape for everything** (every wire grouped, a
  credential as a field). That is uniform, but it puts a request's
  credential, which has a refusal and is passed to a route, next to
  process settings that have neither, and one refusal would cover fields
  that fail for different reasons.
- **`wiring { wire { ... } }` as the only form.** This was discussed and is
  deferred (§4). Making a container mandatory is the ceremony Angular's
  NgModules became and later made optional (v14 to v17).
- **`binding`, `seam`, `directive`, `provision`.** Each was rejected in the
  same discussion. `binding` is already in use for capability bindings,
  `seam` is obscure, `directive` suggests behaviour, and `provision`
  suggests infrastructure.

## Consequences

- Lexer and parser: `wire` becomes a keyword. `config` stays reserved for
  one release, for the migration message, and `principal` was only
  contextual, so it is freed. Contextual words (`cookie`, `header`, `env`,
  `setting`, `startup`, `by`, `on`, `restart`, `when`) are unchanged.
- `syntax.PrincipalDecl` and `syntax.ConfigDecl` may keep their Go names
  or become `WireDecl` and `WireGroupDecl`. That is an implementation
  choice that doesn't change the language.
- Checker: U10, and the "principal" and "config" wording in messages. The
  registry's card kinds become `wire`. `fiig config` lists grouped wires,
  and still prints `config X.y` in its report until that report's own
  wording is revisited. The IntelliJ plugin and the TextMate grammar
  highlight `wire`, and share one icon.
- Docs: `docs/terminology.md` gets wire and wiring; `docs/layers.md`,
  `docs/deploying.md` and the tour change their examples.
- Hearsay: one `fiig fmt -w` pass (with ADR 0142's), then `openapi.json`/`schema.d.ts`
  regenerate (scheme names don't change).

## Open questions

1. **A `wiring { }` block** for several wires in one file: optional, never
   a namespace (`sessions.me`, not `sessions.auth.me`), an exception to ADR
   0054's one public declaration per file, and named after the file. It is
   held until a package has more than two related wires.
2. **The report's wording.** `fiig config` and the `config` subcommand are
   named after the keyword being retired. Renaming the subcommand to `fiig
   wires`, or keeping it as "configuration", is a separate choice.
3. **The migration window.** How many releases `config` and `principal`
   keep their message before they are ordinary names again.
