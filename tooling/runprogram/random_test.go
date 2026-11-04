package runprogram

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fiig-lang/compiler/runtime"
)

func loadCaps(t *testing.T, content string) (*runtime.CapabilityRegistry, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "caps.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return LoadCapabilitiesFile(path, nil)
}

// ADR 0068 §6: a seeded Random from the capabilities file repeats, and
// serve refuses it.
func TestRandomSeededFromCapabilitiesFile(t *testing.T) {
	uuid := func() string {
		caps, err := loadCaps(t, `{"Random": {"type": "random-seeded", "seed": 42}}`)
		if err != nil {
			t.Fatal(err)
		}
		v, fault := caps.Call(context.Background(), "Random", "uuid", nil)
		if fault != nil {
			t.Fatal(fault)
		}
		if err := RefuseSeededRandom(caps); err == nil || !strings.Contains(err.Error(), "random-seeded") {
			t.Errorf("RefuseSeededRandom = %v", err)
		}
		return v.String()
	}
	if a, b := uuid(), uuid(); a != b {
		t.Errorf("seed 42 gave %s then %s", a, b)
	}
	for _, bad := range []string{`{}`, `{"seed": -1}`, `{"seed": 1.5}`, `{"seed": "42"}`} {
		cfg := strings.Replace(bad, "{", `{"type": "random-seeded", `, 1)
		cfg = strings.Replace(cfg, ", }", "}", 1)
		if _, err := loadCaps(t, `{"Random": `+cfg+`}`); err == nil {
			t.Errorf("%s: no error", cfg)
		}
	}
}

// With no entry, Random is the secure default, which serve accepts.
func TestRandomDefaultsToSecure(t *testing.T) {
	caps, err := LoadCapabilitiesFile("", nil)
	if err != nil {
		t.Fatal(err)
	}
	RegisterDefaults(caps)
	p, ok := caps.Lookup("Random")
	if r, isRandom := p.(*runtime.RandomProvider); !ok || !isRandom || r.Seeded {
		t.Fatalf("Random = %#v", p)
	}
	if err := RefuseSeededRandom(caps); err != nil {
		t.Error(err)
	}
	if _, ok := caps.Lookup("Clock"); !ok {
		t.Error("Clock has no default")
	}
}
