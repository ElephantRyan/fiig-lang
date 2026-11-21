# ADR 0126: A Gateway Can Make a Secret

Status: Accepted (2026-11-21; the owner approved it). Implemented 2026-11-21.
Date: 2026-11-21
Related: ADR 0071 §4 (`Secret`), abs-recommendations GAPS.md G52.

## Context

ADR 0071 §4 says "only config produces" a `Secret`. That holds while every
secret is an environment variable. abs-recommendations wants a first-run setup
screen: the self-hoster enters the ABS URL, a service token, and an
Authentik client secret in the browser, and the app keeps them in its
database (G52). A token read back from a table is a `string`, so nothing
stops it being interpolated into a log line or a fault message, put in a
response, or handled outside a gateway (S1, S2).

## Decision

**`text.toSecret(name)` returns a `Secret` that prints as `Secret(name)`, and
only a gateway may call it.** It is the mirror of `.reveal()`:

```fiig
gateway storedToken() -> Secret | None
    requires Database
{
    row: SettingRow | None = Database.queryOne("SELECT value FROM settings WHERE name = ?", "abs.serviceToken")
    match row {
        None -> {
            return None
        }
        found -> {
            return found.value.toSecret("abs.serviceToken")
        }
    }
}
```

- From there on the value is as protected as one from config: it can't be
  interpolated, compared with `==`, or reach a response or `fiig run -json`
  (S1, M1), and only a gateway can `.reveal()` it (S2).
- **Why a gateway.** It is where a value arrives from outside, as `.reveal()`
  is where one leaves. A call in a rule or activity is **S3**: the value
  would already have been a plain `string` there.
- Searching for `toSecret(` finds every place a program makes one, as
  searching for `reveal(` finds every place one leaves.
- The name is only what the value prints as. It should say where the value
  came from, as `Secret(ACCOUNTS_EXPORT_TOKEN)` does.

ADR 0071 §4's "only config produces one" is amended: config, and a gateway
through `toSecret`.

## What this doesn't do

- **The write path is unprotected.** The setup form's token reaches the
  workflow as a `string`, since a `Secret` can't be part of a request body
  (ADR 0071 §4), and travels as one to the gateway that stores it. Only
  what is read back is protected. A request-body `Secret` is a separate
  question.
- **It is not a config source.** Settings kept this way get none of what
  config gives: the startup check of every required value, typed parsing,
  ADR 0122's `when`, and `fiig config`'s report of what a deployment must
  set. A program must handle "not set up yet" itself. A config source backed
  by the database (`from setting "..."`) would need a rule for startup, when
  the first-run values aren't there yet, and a way to change a value while
  the process runs, which ADR 0071 §8 puts out of scope. That is G52's
  larger option and is left open.
- Nothing stops a gateway calling `toSecret` on a value that isn't secret.
  That only makes it harder to show, so it is not a leak.

## Bug classes

S3: `.toSecret()` outside a gateway.

## Implementation notes (2026-11-21)

- `compiler/types/methods.go` (`string` table), `compiler/runtime/methods.go`,
  and the S3 check beside S2 in `compiler/checker/bytes.go`.
- Tests: `TestToSecretIsLegalOnlyInAGateway` (checker) and
  `TestToSecretPrintsByNameAndMatches` (runtime).
- Dogfooded on G52's repro: the activity form now reports S3, a gateway that
  reads the row and calls `toSecret` checks clean, and an activity that
  interpolates the result is still S1.
