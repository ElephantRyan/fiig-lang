package runtime

import (
	"encoding/binary"
	"hash/maphash"
	"math"
	"sort"
	"strings"
)

// Map is `Map<K, V>` (ADR 0065 §4-§9): an immutable map that iterates in
// insertion order. byHash finds an entry by its key's hash; bySeq holds the
// same entries by insertion sequence, so walking it is iteration order.
// Both are persistent tries (maptrie.go), so put, remove, and lookup cost
// O(log n) on every Map value, including one an older value branched from.
type Map struct {
	byHash *trie // hash -> []*mapEntry, the entries whose keys share it
	bySeq  *trie // seq -> *mapEntry
	n      int
	next   uint64 // the sequence a new key gets
}

type mapEntry struct {
	key, val Value
	seq      uint64
}

func (Map) isValue() {}

// String is the literal's form: `[a: 1, b: 2]`, and `[:]` when empty.
func (m Map) String() string {
	if m.n == 0 {
		return "[:]"
	}
	parts := make([]string, 0, m.n)
	m.Each(func(k, v Value) bool {
		parts = append(parts, k.String()+": "+v.String())
		return true
	})
	return "[" + strings.Join(parts, ", ") + "]"
}

// Len is the number of keys.
func (m Map) Len() int { return m.n }

// Get is the value at k.
func (m Map) Get(k Value) (Value, bool) {
	if e := m.entry(k); e != nil {
		return e.val, true
	}
	return nil, false
}

func (m Map) entry(k Value) *mapEntry {
	bucket, _ := m.byHash.get(hashValue(k))
	for _, e := range bucketOf(bucket) {
		if Equal(e.key, k) {
			return e
		}
	}
	return nil
}

func bucketOf(v any) []*mapEntry {
	b, _ := v.([]*mapEntry)
	return b
}

// Put is m with k set to v. A key already present keeps its position and
// its first-inserted spelling (§4: put(1.5, v) over 1.50 still prints 1.50).
func (m Map) Put(k, v Value) Map {
	h := hashValue(k)
	bucket, _ := m.byHash.get(h)
	entries := bucketOf(bucket)
	for i, e := range entries {
		if Equal(e.key, k) {
			ne := &mapEntry{key: e.key, val: v, seq: e.seq}
			replaced := append([]*mapEntry(nil), entries...)
			replaced[i] = ne
			return Map{byHash: m.byHash.put(h, replaced), bySeq: m.bySeq.put(e.seq, ne), n: m.n, next: m.next}
		}
	}
	ne := &mapEntry{key: k, val: v, seq: m.next}
	grown := append(append([]*mapEntry(nil), entries...), ne)
	return Map{byHash: m.byHash.put(h, grown), bySeq: m.bySeq.put(ne.seq, ne), n: m.n + 1, next: m.next + 1}
}

// Remove is m without k, or m itself when k is absent.
func (m Map) Remove(k Value) Map {
	h := hashValue(k)
	bucket, _ := m.byHash.get(h)
	entries := bucketOf(bucket)
	for i, e := range entries {
		if !Equal(e.key, k) {
			continue
		}
		byHash := m.byHash.remove(h)
		if len(entries) > 1 {
			rest := append(append([]*mapEntry(nil), entries[:i]...), entries[i+1:]...)
			byHash = m.byHash.put(h, rest)
		}
		return Map{byHash: byHash, bySeq: m.bySeq.remove(e.seq), n: m.n - 1, next: m.next}
	}
	return m
}

// Each visits the entries in insertion order; it stops when visit returns
// false.
func (m Map) Each(visit func(k, v Value) bool) {
	m.bySeq.each(func(_ uint64, val any) bool {
		e := val.(*mapEntry)
		return visit(e.key, e.val)
	})
}

// mapsEqual is §8: the same keys with equal values, in any order.
func mapsEqual(a, b Map) bool {
	if a.n != b.n {
		return false
	}
	equal := true
	a.Each(func(k, v Value) bool {
		bv, ok := b.Get(k)
		equal = ok && Equal(v, bv)
		return equal
	})
	return equal
}

// hashSeed is chosen once per process (§9), so keys that arrive from
// outside can't be picked to collide, and no output depends on it: fiig
// exposes no hash, and iteration follows insertion, not hashes.
var hashSeed = maphash.MakeSeed()

// hashValue is a structural hash consistent with Equal: equal values hash
// equally. A decimal hashes by value, with trailing zeros trimmed, so 1.5
// and 1.50 are one key (§4).
func hashValue(v Value) uint64 {
	var h maphash.Hash
	h.SetSeed(hashSeed)
	writeHash(&h, v)
	return h.Sum64()
}

func writeHash(h *maphash.Hash, v Value) {
	var buf [8]byte
	num := func(x uint64) {
		binary.LittleEndian.PutUint64(buf[:], x)
		h.Write(buf[:])
	}
	text := func(s string) {
		num(uint64(len(s)))
		h.WriteString(s)
	}
	switch v := v.(type) {
	case Int:
		h.WriteByte('i')
		num(uint64(v))
	case Float:
		h.WriteByte('f')
		num(math.Float64bits(float64(v)))
	case Decimal:
		h.WriteByte('d')
		text(canonicalDecimal(v.String()))
	case Str:
		h.WriteByte('s')
		text(string(v))
	case Bool:
		h.WriteByte('b')
		if v {
			h.WriteByte(1)
		} else {
			h.WriteByte(0)
		}
	case None:
		h.WriteByte('n')
	case Bytes:
		h.WriteByte('y')
		text(string(v.B))
	case List:
		h.WriteByte('l')
		num(uint64(len(v.Elems)))
		for _, e := range v.Elems {
			writeHash(h, e)
		}
	case Record:
		h.WriteByte('r')
		text(v.TypeName)
		names := make([]string, 0, len(v.Fields))
		for name := range v.Fields {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			text(name)
			writeHash(h, v.Fields[name])
		}
	case EnumValue:
		h.WriteByte('e')
		text(v.TypeName)
		text(v.Variant)
		for _, f := range v.Fields {
			writeHash(h, f)
		}
	case Result:
		h.WriteByte('R')
		if v.Ok {
			h.WriteByte(1)
		}
		writeHash(h, v.Value)
	case Map:
		// Order-insensitive, as == is (§8): the sum of each entry's hash.
		h.WriteByte('m')
		var sum uint64
		v.Each(func(k, val Value) bool {
			var eh maphash.Hash
			eh.SetSeed(hashSeed)
			writeHash(&eh, k)
			writeHash(&eh, val)
			sum += eh.Sum64()
			return true
		})
		num(sum)
	default:
		text(v.String())
	}
}

// canonicalDecimal trims a decimal's trailing fractional zeros, and the
// point if nothing is left after it: 1.50 and 1.5 hash alike, as they are
// equal.
func canonicalDecimal(s string) string {
	if !strings.Contains(s, ".") {
		return s
	}
	s = strings.TrimRight(s, "0")
	return strings.TrimSuffix(s, ".")
}
