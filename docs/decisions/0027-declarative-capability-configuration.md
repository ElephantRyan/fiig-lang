# ADR 0027: Declarative Capability Provider Configuration

Status: Accepted
Date: 2026-10-08

## Context

Milestone 5's last open deliverable: every capability so far (`libraries/
sql`'s `Provider`, `tooling/run-cli`'s scripted fakes) is wired up by
hand in Go — `caps.Register("Database", sql.New(db))`. "Capability
provider configuration" means making that a config-file concern instead,
without reopening ADR 0025's module boundary (the root `fiig-lang` module
stays at zero third-party dependencies; `libraries/sql` is the only thing
allowed to know about a real driver).

## Decision

A generic factory mechanism in `compiler/runtime` (`config.go`), with no
dependency on any specific provider:

```go
type ProviderFactory func(config map[string]any) (CapabilityProvider, error)
type FactoryRegistry map[string]ProviderFactory
func LoadCapabilities(config map[string]map[string]any, factories FactoryRegistry) (*CapabilityRegistry, error)
```

Each capability's config names which factory builds it via a `"type"`
field: `{"Database": {"type": "sql", "driver": "sqlite", "dsn": "..."}}`.
A host assembles its own `FactoryRegistry` from whichever provider
libraries it actually imports — `compiler/runtime` knows about none of
them, same reasoning `CapabilityProvider` itself already uses.

**`libraries/sql` gets a real factory** (`Factory`, its own file,
`factory.go`): opens `database/sql` by driver name + DSN from config,
pings it, wraps it in `New`. Still imports no specific driver itself —
the host's blank import (or whatever already registered it) is what makes
a `"driver"` name resolve, exactly like `sql.Open` always has.

**`tooling/run-cli` gets only a `"scripted"` factory** — registering a
`"sql"` factory there would require importing a specific driver in the
*root* module just to demonstrate one, which is exactly what ADR 0025
ruled out. `libraries/sql`'s own tests are where `Factory` is actually
proven against a real database (`factory_test.go`, reusing the same
test-only SQLite driver `provider_test.go` already has).

`run-cli -caps` stays backward compatible: a capability entry with no
`"type"` field is still read the old way (the whole object is `method ->
responses`); one with `"type"` goes through `LoadCapabilities` instead.
Both forms can coexist in the same file.

## Rationale

The factory-registry pattern is the natural generalization of what ADR
0025 already established for `CapabilityProvider` itself: define the
interface where it has zero dependencies, let whoever actually needs a
concrete implementation bring their own. Keeping `run-cli` driver-free
means the "declarative config" deliverable doesn't quietly reopen the
zero-dependency decision through a side door — the config *mechanism* is
dependency-free even though a *specific* factory (`libraries/sql`'s)
isn't.

## Consequences

- `compiler/runtime/config.go`: `ProviderFactory`, `FactoryRegistry`,
  `LoadCapabilities` — new, zero new dependencies.
- `libraries/sql/factory.go`: `Factory`, tested end-to-end against a real
  SQLite database via declarative config in `factory_test.go`.
- `tooling/run-cli/caps.go`: `scriptedFactory` wraps the existing
  `scriptedCapability` as a `ProviderFactory`; `loadCapabilities` now
  dispatches per-capability on presence of a `"type"` field, old files
  unaffected.
- Milestone 5 is now feature-complete: transactions (ADR 0025's other
  half, `compiler/runtime/transaction.go`), `libraries/sql`, HTTP/CLI
  adapter execution, OpenAPI generation (ADR 0026), structured tracing,
  and this. Nothing in fiig-lang.md §5's Milestone 5 deliverable list
  remains unaddressed.
