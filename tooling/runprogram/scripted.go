package runprogram

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
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
	reg, problems, err := LoadCapabilitiesFileWith(path, extraFactories, os.LookupEnv)
	if err == nil && len(problems) > 0 {
		err = fmt.Errorf("%s", strings.Join(problems, "; "))
	}
	return reg, err
}

// LoadCapabilitiesFileWith is LoadCapabilitiesFile reading `${NAME}`
// through lookup (ADR 0071 §6), so an -env-file feeds it too. Every unset
// variable is returned as a problem, for the one startup report; with any,
// no provider is built.
func LoadCapabilitiesFileWith(path string, extraFactories runtime.FactoryRegistry, lookup runtime.Lookup) (*runtime.CapabilityRegistry, []string, error) {
	reg := runtime.NewCapabilityRegistry()
	if path == "" {
		return reg, nil, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	// ParseJSON, not json.Unmarshal, so scripted response numbers keep their
	// text for DecodeUntypedJSON (ADR 0062 §6).
	parsed, err := runtime.ParseJSON(raw)
	if err != nil {
		return nil, nil, err
	}
	var problems []string
	parsed = expandEnv(parsed, lookup, filepath.Base(path), &problems)
	if len(problems) > 0 {
		return nil, problems, nil
	}
	top, ok := parsed.(map[string]any)
	if !ok {
		return nil, nil, fmt.Errorf("capabilities file must be a JSON object")
	}
	factories := runtime.FactoryRegistry{"scripted": ScriptedFactory, "socket": SocketFactory, "socket-script": SocketScriptFactory, "random-seeded": RandomSeededFactory, "driver": runtime.DriverFactory}
	for name, f := range extraFactories {
		factories[name] = f
	}
	// "packages" binds capabilities for one package's workflows (ADR 0079
	// §6a): package path -> the same shape as the file's top level.
	if v, has := top["packages"]; has {
		delete(top, "packages")
		perPackage, ok := v.(map[string]any)
		if !ok {
			return nil, nil, fmt.Errorf("packages must be a JSON object of package path to capabilities")
		}
		for path, v := range perPackage {
			caps, ok := v.(map[string]any)
			if !ok {
				return nil, nil, fmt.Errorf("packages.%s must be a JSON object of capabilities", path)
			}
			providers, err := buildProviders(caps, factories, "packages."+path+".")
			if err != nil {
				return nil, nil, err
			}
			for capability, p := range providers {
				reg.RegisterFor(path, capability, p)
			}
		}
	}
	providers, err := buildProviders(top, factories, "")
	if err != nil {
		return nil, nil, err
	}
	for capability, p := range providers {
		reg.Register(capability, p)
	}
	return reg, nil, nil
}

// buildProviders builds one capability -> configuration object: a "type"
// picks a factory (ADR 0027), and a map without one is the legacy flat
// form of method -> scripted responses. prefix names where the object is,
// for errors.
func buildProviders(top map[string]any, factories runtime.FactoryRegistry, prefix string) (map[string]runtime.CapabilityProvider, error) {
	out := map[string]runtime.CapabilityProvider{}
	declarative := map[string]map[string]any{}
	for capability, v := range top {
		cfg, ok := v.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("capability %q must be a JSON object", prefix+capability)
		}
		if _, hasType := cfg["type"]; hasType {
			declarative[capability] = cfg
			continue
		}
		// Legacy flat form: the whole map IS method -> responses.
		responses := make(map[string][]any, len(cfg))
		for method, v := range cfg {
			list, ok := v.([]any)
			if !ok {
				return nil, fmt.Errorf("%s%s.%s must be a JSON array", prefix, capability, method)
			}
			responses[method] = list
		}
		out[capability] = &ScriptedCapability{responses: responses, calls: map[string]int{}}
	}
	if len(declarative) == 0 {
		return out, nil
	}
	declReg, err := runtime.LoadCapabilities(declarative, factories)
	if err != nil {
		return nil, err
	}
	for capability := range declarative {
		p, _ := declReg.Lookup(capability)
		out[capability] = p
	}
	return out, nil
}

// envRef is `${NAME}` inside a capabilities file's string.
var envRef = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// expandEnv replaces every `${NAME}` in v's strings with that variable, so
// a deployed capabilities file names its secrets and paths instead of
// holding them: `"dsn": "file:${DB_PATH}"`. An unset variable is a problem,
// named with where it is (`caps.json Database.dsn`), never an empty string.
func expandEnv(v any, lookup runtime.Lookup, where string, problems *[]string) any {
	switch v := v.(type) {
	case string:
		return envRef.ReplaceAllStringFunc(v, func(ref string) string {
			name := envRef.FindStringSubmatch(ref)[1]
			val, ok := lookup(name)
			if !ok {
				*problems = append(*problems, fmt.Sprintf("%s is unset; %s", name, where))
			}
			return val
		})
	case map[string]any:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		sep := " "
		if strings.Contains(where, " ") {
			sep = "."
		}
		for _, k := range keys {
			v[k] = expandEnv(v[k], lookup, where+sep+k, problems)
		}
	case []any:
		for i, e := range v {
			v[i] = expandEnv(e, lookup, fmt.Sprintf("%s[%d]", where, i), problems)
		}
	}
	return v
}
