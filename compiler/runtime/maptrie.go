package runtime

import "math/bits"

// trie is a persistent hash array mapped trie over uint64 keys (ADR 0065
// §9): 32-way nodes indexed by five bits of the key at a time, most
// significant first, with a child stored as a leaf until a second key needs
// the slot. Every update copies only the nodes on one path, so any version
// of a map stays valid and every operation on any version is O(log₃₂ n),
// at most 13 levels. Walking children in slot order visits keys in
// ascending order, which is what lets the same structure keyed by insertion
// sequence give a map its iteration order.
type trie struct {
	bitmap uint32
	kids   []any // *trie or trieLeaf, in slot order
}

type trieLeaf struct {
	key uint64
	val any
}

// The top level takes the key's highest four bits, every level below five.
const trieTopShift = 60

func trieSlot(key uint64, shift uint) uint32 { return uint32(key>>shift) & 31 }

func (t *trie) index(bit uint32) int { return bits.OnesCount32(t.bitmap & (bit - 1)) }

func (t *trie) get(key uint64) (any, bool) {
	n, shift := t, uint(trieTopShift)
	for n != nil {
		bit := uint32(1) << trieSlot(key, shift)
		if n.bitmap&bit == 0 {
			return nil, false
		}
		switch kid := n.kids[n.index(bit)].(type) {
		case trieLeaf:
			return kid.val, kid.key == key
		case *trie:
			n, shift = kid, shift-5
		}
	}
	return nil, false
}

// put returns a trie with key set to val, sharing every untouched node.
func (t *trie) put(key uint64, val any) *trie { return t.putAt(key, val, trieTopShift) }

func (t *trie) putAt(key uint64, val any, shift uint) *trie {
	if t == nil {
		t = &trie{}
	}
	bit := uint32(1) << trieSlot(key, shift)
	i := t.index(bit)
	if t.bitmap&bit == 0 {
		kids := make([]any, len(t.kids)+1)
		copy(kids, t.kids[:i])
		kids[i] = trieLeaf{key, val}
		copy(kids[i+1:], t.kids[i:])
		return &trie{bitmap: t.bitmap | bit, kids: kids}
	}
	var kid any
	switch old := t.kids[i].(type) {
	case trieLeaf:
		if old.key == key {
			kid = trieLeaf{key, val}
		} else {
			kid = (&trie{}).putAt(old.key, old.val, shift-5).putAt(key, val, shift-5)
		}
	case *trie:
		kid = old.putAt(key, val, shift-5)
	}
	kids := append([]any(nil), t.kids...)
	kids[i] = kid
	return &trie{bitmap: t.bitmap, kids: kids}
}

// remove returns a trie without key, or t itself when key is absent. A node
// left holding one leaf is folded into its parent, so a trie has the shape
// its keys alone would give it.
func (t *trie) remove(key uint64) *trie {
	out, _ := t.removeAt(key, trieTopShift)
	return out
}

func (t *trie) removeAt(key uint64, shift uint) (*trie, bool) {
	if t == nil {
		return nil, false
	}
	bit := uint32(1) << trieSlot(key, shift)
	if t.bitmap&bit == 0 {
		return t, false
	}
	i := t.index(bit)
	var kid any
	switch old := t.kids[i].(type) {
	case trieLeaf:
		if old.key != key {
			return t, false
		}
	case *trie:
		sub, removed := old.removeAt(key, shift-5)
		if !removed {
			return t, false
		}
		if sub != nil {
			kid = sub
			if leaf, ok := sub.onlyLeaf(); ok {
				kid = leaf
			}
		}
	}
	if kid != nil {
		kids := append([]any(nil), t.kids...)
		kids[i] = kid
		return &trie{bitmap: t.bitmap, kids: kids}, true
	}
	if len(t.kids) == 1 {
		return nil, true
	}
	kids := make([]any, 0, len(t.kids)-1)
	kids = append(kids, t.kids[:i]...)
	kids = append(kids, t.kids[i+1:]...)
	return &trie{bitmap: t.bitmap &^ bit, kids: kids}, true
}

func (t *trie) onlyLeaf() (trieLeaf, bool) {
	if len(t.kids) != 1 {
		return trieLeaf{}, false
	}
	leaf, ok := t.kids[0].(trieLeaf)
	return leaf, ok
}

// each visits every leaf in ascending key order; it stops when visit
// returns false.
func (t *trie) each(visit func(key uint64, val any) bool) bool {
	if t == nil {
		return true
	}
	for _, k := range t.kids {
		switch k := k.(type) {
		case trieLeaf:
			if !visit(k.key, k.val) {
				return false
			}
		case *trie:
			if !k.each(visit) {
				return false
			}
		}
	}
	return true
}
