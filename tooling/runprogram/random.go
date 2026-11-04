package runprogram

import (
	"encoding/json"
	"fmt"
	"strconv"

	"fiig-lang/compiler/runtime"
)

// RandomSeededFactory builds a seeded Random (ADR 0068 §6) from a -caps
// entry, for tests and demos: the same seed gives the same draws.
//
//	{"Random": {"type": "random-seeded", "seed": 42}}
//
// `fiig serve` refuses it (RefuseSeededRandom).
func RandomSeededFactory(config map[string]any) (runtime.CapabilityProvider, error) {
	raw, ok := config["seed"].(json.Number)
	if !ok {
		return nil, fmt.Errorf("random-seeded needs a \"seed\", a non-negative integer")
	}
	seed, err := strconv.ParseUint(raw.String(), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("random-seeded's seed %s is not a non-negative integer", raw)
	}
	return runtime.NewSeededRandomProvider(seed), nil
}

// RegisterDefaults registers the local effects (ADR 0068 §4), which need
// no configuration, unless caps already has one: the secure Random (§6)
// and the wall clock.
func RegisterDefaults(caps *runtime.CapabilityRegistry) {
	if _, configured := caps.Lookup("Random"); !configured {
		caps.Register("Random", runtime.NewRandomProvider())
	}
	if _, configured := caps.Lookup("Clock"); !configured {
		caps.Register("Clock", runtime.ClockProvider{Clock: runtime.RealClock{}})
	}
}

// RefuseSeededRandom is `fiig serve`'s check that a seeded Random, whose
// tokens can be predicted from its seed, hasn't reached a deployed service
// by a copied capabilities file (ADR 0068 §6).
func RefuseSeededRandom(caps *runtime.CapabilityRegistry) error {
	if p, ok := caps.Lookup("Random"); ok {
		if r, ok := p.(*runtime.RandomProvider); ok && r.Seeded {
			return fmt.Errorf("Random is random-seeded, whose draws repeat; fiig serve uses only the secure generator, so remove the Random entry from the capabilities file (ADR 0068 §6)")
		}
	}
	return nil
}
