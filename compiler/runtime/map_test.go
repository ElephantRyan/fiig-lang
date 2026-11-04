package runtime

import (
	"fmt"
	"hash/maphash"
	"math/rand"
	"testing"
)

// refMap is the obvious model a Map must agree with: keys in insertion
// order and a value per key.
type refMap struct {
	keys []string
	vals map[string]int
}

func (r refMap) put(k string, v int) refMap {
	out := refMap{keys: append([]string(nil), r.keys...), vals: map[string]int{}}
	for kk, vv := range r.vals {
		out.vals[kk] = vv
	}
	if _, ok := out.vals[k]; !ok {
		out.keys = append(out.keys, k)
	}
	out.vals[k] = v
	return out
}

func (r refMap) remove(k string) refMap {
	out := refMap{vals: map[string]int{}}
	for _, kk := range r.keys {
		if kk != k {
			out.keys = append(out.keys, kk)
			out.vals[kk] = r.vals[kk]
		}
	}
	return out
}

func (r refMap) String() string {
	if len(r.keys) == 0 {
		return "[:]"
	}
	s := "["
	for i, k := range r.keys {
		if i > 0 {
			s += ", "
		}
		s += fmt.Sprintf("%s: %d", k, r.vals[k])
	}
	return s + "]"
}

// TestMapMatchesReference applies random puts and removes to random earlier
// versions, the branching §9 says must stay cheap and correct, and checks
// every version against the reference after each step.
func TestMapMatchesReference(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	maps := []Map{{}}
	refs := []refMap{{vals: map[string]int{}}}
	for step := 0; step < 4000; step++ {
		from := rng.Intn(len(maps))
		k := fmt.Sprintf("k%d", rng.Intn(300))
		m, r := maps[from], refs[from]
		if rng.Intn(4) == 0 {
			m, r = m.Remove(Str(k)), r.remove(k)
		} else {
			v := rng.Intn(1000)
			m, r = m.Put(Str(k), Int(v)), r.put(k, v)
		}
		maps, refs = append(maps, m), append(refs, r)
		if m.String() != r.String() || m.Len() != len(r.keys) {
			t.Fatalf("step %d: map %s, want %s", step, m, r)
		}
	}
	// Older versions are untouched by everything built from them.
	for i := 0; i < len(maps); i += 97 {
		if maps[i].String() != refs[i].String() {
			t.Fatalf("version %d changed: %s, want %s", i, maps[i], refs[i])
		}
	}
}

// TestMapAcrossSeeds runs remove and re-add under many hash seeds. A
// lookup once returned the bucket of another key sharing the hash's top
// bits, so a new key joined that bucket and a removed key survived in it
// (1 run in 16 for two keys); one process has one seed, so only a loop over
// seeds sees it.
func TestMapAcrossSeeds(t *testing.T) {
	saved := hashSeed
	defer func() { hashSeed = saved }()
	for i := 0; i < 500; i++ {
		hashSeed = maphash.MakeSeed()
		m := Map{}
		for j := 0; j < 20; j++ {
			m = m.Put(Int(j), Int(j))
		}
		for j := 0; j < 20; j += 2 {
			m = m.Remove(Int(j))
		}
		for j := 0; j < 20; j++ {
			if _, ok := m.Get(Int(j)); ok != (j%2 == 1) {
				t.Fatalf("seed %d: key %d present = %v after removing the even keys", i, j, ok)
			}
		}
		if got := m.Put(Int(0), Int(0)).String(); got != "[1: 1, 3: 3, 5: 5, 7: 7, 9: 9, 11: 11, 13: 13, 15: 15, 17: 17, 19: 19, 0: 0]" {
			t.Fatalf("seed %d: a removed key re-added goes last: %s", i, got)
		}
	}
}

// TestMapKeysByValue is §4: equal keys are one key, hashed by value, and
// the first spelling is kept.
func TestMapKeysByValue(t *testing.T) {
	m := Map{}.Put(dec(t, "1.50"), Str("a")).Put(dec(t, "1.5"), Str("b"))
	if m.Len() != 1 || m.String() != "[1.50: b]" {
		t.Fatalf("decimal keys: %s", m)
	}
	rec := func(n int) Value { return Record{TypeName: "P", Fields: map[string]Value{"x": Int(n), "y": Str("s")}} }
	m = Map{}.Put(rec(1), Int(1)).Put(rec(1), Int(2)).Put(rec(2), Int(3))
	if m.Len() != 2 {
		t.Fatalf("record keys: %s", m)
	}
	a := Map{}.Put(Str("x"), Int(1)).Put(Str("y"), Int(2))
	b := Map{}.Put(Str("y"), Int(2)).Put(Str("x"), Int(1))
	if !Equal(a, b) || hashValue(a) != hashValue(b) {
		t.Fatalf("maps equal in any order: %s vs %s", a, b)
	}
	if Equal(a, b.Put(Str("y"), Int(3))) {
		t.Fatal("maps with a different value are not equal")
	}
	if got := a.Remove(Str("x")).Put(Str("x"), Int(1)).String(); got != "[y: 2, x: 1]" {
		t.Fatalf("a removed key re-added goes last: %s", got)
	}
}

func BenchmarkMapBuild(b *testing.B) {
	for _, n := range []int{1000, 100000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				m := Map{}
				for j := 0; j < n; j++ {
					m = m.Put(Int(j), Int(j))
				}
			}
		})
	}
}

// BenchmarkMapBranchFromBase is §9's example: every put branches from one
// base map, which must not copy it.
func BenchmarkMapBranchFromBase(b *testing.B) {
	for _, n := range []int{1000, 100000} {
		base := Map{}
		for j := 0; j < n; j++ {
			base = base.Put(Int(j), Int(j))
		}
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				_ = base.Put(Str("region"), Int(i))
			}
		})
	}
}

func BenchmarkMapGet(b *testing.B) {
	for _, n := range []int{1000, 100000} {
		m := Map{}
		for j := 0; j < n; j++ {
			m = m.Put(Int(j), Int(j))
		}
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				m.Get(Int(i % n))
			}
		})
	}
}
