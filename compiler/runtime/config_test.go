package runtime

import (
	"context"
	"testing"
)

func TestLoadCapabilitiesDispatchesByType(t *testing.T) {
	factories := FactoryRegistry{
		"const": func(cfg map[string]any) (CapabilityProvider, error) {
			v := cfg["value"].(string)
			return FuncProvider{"get": func(_ context.Context, _ []Value) (Value, *Fault) {
				return Str(v), nil
			}}, nil
		},
	}
	config := map[string]map[string]any{
		"Greeting": {"type": "const", "value": "hello"},
	}
	reg, err := LoadCapabilities(config, factories)
	if err != nil {
		t.Fatalf("LoadCapabilities: %v", err)
	}
	v, fault := reg.Call(context.Background(), "Greeting", "get", nil)
	if fault != nil {
		t.Fatalf("Call: %v", fault)
	}
	if v != Str("hello") {
		t.Fatalf("got %s, want hello", v)
	}
}

func TestLoadCapabilitiesMissingTypeErrors(t *testing.T) {
	_, err := LoadCapabilities(map[string]map[string]any{"X": {"value": "y"}}, FactoryRegistry{})
	if err == nil {
		t.Fatal("got nil error, want one: no \"type\" field")
	}
}

func TestLoadCapabilitiesUnknownTypeErrors(t *testing.T) {
	_, err := LoadCapabilities(map[string]map[string]any{"X": {"type": "nope"}}, FactoryRegistry{})
	if err == nil {
		t.Fatal("got nil error, want one: no factory registered for type \"nope\"")
	}
}

func TestLoadCapabilitiesFactoryErrorPropagates(t *testing.T) {
	factories := FactoryRegistry{
		"broken": func(cfg map[string]any) (CapabilityProvider, error) {
			return nil, Faultf("simulated construction failure")
		},
	}
	_, err := LoadCapabilities(map[string]map[string]any{"X": {"type": "broken"}}, factories)
	if err == nil {
		t.Fatal("got nil error, want the factory's own error propagated")
	}
}
