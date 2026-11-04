package runprogram

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"sync"

	"fiig-lang/compiler/runtime"
)

// ScriptedCapability answers every call to one capability from a fixed,
// pre-recorded list of responses per method — a CLI's own stand-in for
// the "simulated gateway provider" compiler/runtime/capability.go
// describes, configured from a JSON file instead of Go code, since
// there's no real capability-contract system yet to generate one from.
// Responses cycle: a single-element list means "always return this"; a
// longer list lets a -caps file script a sequence (e.g. the first
// Database.queryOne call returns a row, the second returns null).
//
// Responses are matched by call order only, never by the call's actual
// arguments — fine for a sequential rule/gateway/workflow call, but
// inside a `concurrent` block, which response a given input gets is
// whatever order its worker happens to reach the capability in, not
// which input it is (compiler/runtime's own concurrency is genuinely
// concurrent; nothing here makes it deterministic). Argument-sensitive
// capability behavior under concurrency is what compiler/runtime's own
// Go-level tests use a real CapabilityProvider for instead
// (gateway_test.go's fakeDatabase) — this is a CLI convenience for
// dogfooding, not a substitute for that.
type ScriptedCapability struct {
	mu        sync.Mutex
	responses map[string][]any
	calls     map[string]int
}

func (c *ScriptedCapability) Call(_ context.Context, method string, _ []runtime.Value) (runtime.Value, *runtime.Fault) {
	c.mu.Lock()
	defer c.mu.Unlock()
	responses, ok := c.responses[method]
	if !ok || len(responses) == 0 {
		return nil, runtime.Faultf("no scripted response configured for method %q (-caps file)", method)
	}
	idx := c.calls[method] % len(responses)
	c.calls[method]++
	v, err := runtime.DecodeUntypedJSON(responses[idx])
	if err != nil {
		return nil, runtime.Faultf("scripted response for %q: %v", method, err)
	}
	return v, nil
}

// ScriptedFactory is a runtime.ProviderFactory wrapping
// ScriptedCapability for the declarative config path (ADR 0027):
// `{"type": "scripted", "responses": {...}}`.
func ScriptedFactory(cfg map[string]any) (runtime.CapabilityProvider, error) {
	responsesRaw, ok := cfg["responses"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf(`scripted provider config requires a "responses" object`)
	}
	responses := make(map[string][]any, len(responsesRaw))
	for method, v := range responsesRaw {
		list, ok := v.([]any)
		if !ok {
			return nil, fmt.Errorf("responses.%s must be a JSON array", method)
		}
		responses[method] = list
	}
	return &ScriptedCapability{responses: responses, calls: map[string]int{}}, nil
}

// LoadCapabilitiesFile reads a -caps JSON file. Two shapes are accepted
// per capability, dispatched on whether its entry has a "type" field:
//
//   - Legacy/flat (no "type"): `{"Database": {"queryOne": [...], ...}}`
//     — the whole object is method -> responses, same as every -caps
//     file written before declarative config existed.
//   - Declarative (runtime.LoadCapabilities): `{"Database": {"type":
//     "scripted", "responses": {"queryOne": [...]}}}`, or any other
//     type extraFactories registers — e.g. libraries/sql's own "sql"
//     factory, which this package deliberately never registers itself
//     (doing so would mean a specific database/sql driver landing in
//     whatever imports this package; that choice stays with the host).
//
// An empty path returns an empty registry — any capability call then
// faults with "no provider registered", the right failure for a
// program that needs a capability the caller never configured.
func LoadCapabilitiesFile(path string, extraFactories runtime.FactoryRegistry) (*runtime.CapabilityRegistry, error) {
	reg := runtime.NewCapabilityRegistry()
	if path == "" {
		return reg, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	// ParseJSON, not json.Unmarshal, so scripted response numbers keep their
	// text for DecodeUntypedJSON (ADR 0062 §6).
	parsed, err := runtime.ParseJSON(raw)
	if err != nil {
		return nil, err
	}
	if parsed, err = expandEnv(parsed); err != nil {
		return nil, err
	}
	top, ok := parsed.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("capabilities file must be a JSON object")
	}
	config := make(map[string]map[string]any, len(top))
	for capability, v := range top {
		cfg, ok := v.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("capability %q must be a JSON object", capability)
		}
		config[capability] = cfg
	}

	factories := runtime.FactoryRegistry{"scripted": ScriptedFactory, "socket": SocketFactory, "socket-script": SocketScriptFactory, "random-seeded": RandomSeededFactory}
	for name, f := range extraFactories {
		factories[name] = f
	}
	declarative := map[string]map[string]any{}
	for capability, cfg := range config {
		if _, hasType := cfg["type"]; hasType {
			declarative[capability] = cfg
			continue
		}
		// Legacy flat form: the whole map IS method -> responses.
		responses := make(map[string][]any, len(cfg))
		for method, v := range cfg {
			list, ok := v.([]any)
			if !ok {
				return nil, fmt.Errorf("%s.%s must be a JSON array", capability, method)
			}
			responses[method] = list
		}
		reg.Register(capability, &ScriptedCapability{responses: responses, calls: map[string]int{}})
	}
	if len(declarative) == 0 {
		return reg, nil
	}
	declReg, err := runtime.LoadCapabilities(declarative, factories)
	if err != nil {
		return nil, err
	}
	for capability := range declarative {
		p, _ := declReg.Lookup(capability)
		reg.Register(capability, p)
	}
	return reg, nil
}

// envRef is `${NAME}` inside a capabilities file's string.
var envRef = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// expandEnv replaces every `${NAME}` in v's strings with that environment
// variable, so a deployed capabilities file names its secrets and paths
// instead of holding them: `"dsn": "file:${DB_PATH}"`. An unset variable is
// an error, never an empty string.
func expandEnv(v any) (any, error) {
	switch v := v.(type) {
	case string:
		var missing string
		out := envRef.ReplaceAllStringFunc(v, func(ref string) string {
			name := envRef.FindStringSubmatch(ref)[1]
			val, ok := os.LookupEnv(name)
			if !ok && missing == "" {
				missing = name
			}
			return val
		})
		if missing != "" {
			return nil, fmt.Errorf("environment variable %s is not set", missing)
		}
		return out, nil
	case map[string]any:
		for k, e := range v {
			x, err := expandEnv(e)
			if err != nil {
				return nil, err
			}
			v[k] = x
		}
	case []any:
		for i, e := range v {
			x, err := expandEnv(e)
			if err != nil {
				return nil, err
			}
			v[i] = x
		}
	}
	return v, nil
}
