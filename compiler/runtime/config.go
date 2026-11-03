package runtime

import "fmt"

// ProviderFactory builds a CapabilityProvider from one capability's own
// declarative config blob (already JSON-decoded into a plain
// map[string]any) — the generic shape every concrete provider factory
// implements. libraries/sql's Factory (its own package, since it's the
// one that actually knows how to open a *sql.DB) is the first real one;
// tooling/run-cli's "scripted" factory (caps.go) is the test-double
// equivalent, same interface either way.
type ProviderFactory func(config map[string]any) (CapabilityProvider, error)

// FactoryRegistry maps a provider "type" name (what a capability's config
// entry names in its own "type" field — "sql", "scripted", ...) to the
// factory that builds it. A host assembles one of these from whichever
// provider libraries it actually imports; compiler/runtime itself knows
// about none of them, same reasoning as CapabilityProvider being a plain
// interface instead of a fixed list of built-ins.
type FactoryRegistry map[string]ProviderFactory

// LoadCapabilities builds a CapabilityRegistry from declarative config —
// one entry per capability name, each a config blob with its own "type"
// field naming which factory builds it. This is Milestone 5's "capability
// provider configuration" deliverable: before this, a provider could only
// ever be wired up by hand in Go (`caps.Register("Database", sql.New(db))`);
// this is the config-file-driven equivalent; the Go-level call is still
// what it compiles down to, just not what the host has to write anymore.
func LoadCapabilities(config map[string]map[string]any, factories FactoryRegistry) (*CapabilityRegistry, error) {
	reg := NewCapabilityRegistry()
	for capability, cfg := range config {
		typeName, ok := cfg["type"].(string)
		if !ok || typeName == "" {
			return nil, fmt.Errorf("capability %q: config has no \"type\" field", capability)
		}
		factory, ok := factories[typeName]
		if !ok {
			return nil, fmt.Errorf("capability %q: no factory registered for type %q", capability, typeName)
		}
		provider, err := factory(cfg)
		if err != nil {
			return nil, fmt.Errorf("capability %q: %w", capability, err)
		}
		reg.Register(capability, provider)
	}
	return reg, nil
}
