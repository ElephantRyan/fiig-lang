// Package text is the Unicode a string's methods need beyond Go's unicode
// package (ADR 0067): extended grapheme clusters (UAX #29), which
// `length()` and `slice` count, and full case mapping and folding. Its
// tables are generated from one Unicode version, the one Go's unicode
// package carries, so a toolchain's answers never mix versions.
package text

//go:generate go run ./internal/gen

import (
	"sort"
	"unicode/utf8"
)

type prop uint8

// The Grapheme_Cluster_Break values; propOther is every code point the
// property file doesn't list.
const (
	propOther prop = iota
	propCR
	propLF
	propControl
	propExtend
	propZWJ
	propRegionalIndicator
	propPrepend
	propSpacingMark
	propL
	propV
	propT
	propLV
	propLVT
)

type breakRange struct {
	lo, hi uint32
	prop   prop
}

func lookup(table []breakRange, r rune) (prop, bool) {
	i := sort.Search(len(table), func(i int) bool { return table[i].hi >= uint32(r) })
	if i < len(table) && table[i].lo <= uint32(r) {
		return table[i].prop, true
	}
	return propOther, false
}

func breakProp(r rune) prop {
	p, _ := lookup(breakRanges, r)
	return p
}

func pictographic(r rune) bool {
	_, ok := lookup(pictographicRanges, r)
	return ok
}

// segmenter walks a string's grapheme cluster boundaries, rule by rule from
// UAX #29 for the version the tables come from.
type segmenter struct {
	prev prop
	// pict is true after Extended_Pictographic Extend*, and zwjAfterPict
	// when a ZWJ followed that: GB11's left side.
	pict, zwjAfterPict bool
	// ri counts the Regional_Indicators immediately before, for GB12-13.
	ri int
}

// breaks reports whether a boundary falls before r, and moves past it.
func (s *segmenter) breaks(r rune) bool {
	cur := breakProp(r)
	b := s.rule(cur, r)
	s.zwjAfterPict = cur == propZWJ && s.pict
	switch {
	case pictographic(r):
		s.pict = true
	case cur != propExtend:
		s.pict = false
	}
	if cur == propRegionalIndicator {
		s.ri++
	} else {
		s.ri = 0
	}
	s.prev = cur
	return b
}

func (s *segmenter) rule(cur prop, r rune) bool {
	prev := s.prev
	switch {
	case prev == propCR && cur == propLF: // GB3
		return false
	case prev == propCR || prev == propLF || prev == propControl: // GB4
		return true
	case cur == propCR || cur == propLF || cur == propControl: // GB5
		return true
	case prev == propL && (cur == propL || cur == propV || cur == propLV || cur == propLVT): // GB6
		return false
	case (prev == propLV || prev == propV) && (cur == propV || cur == propT): // GB7
		return false
	case (prev == propLVT || prev == propT) && cur == propT: // GB8
		return false
	case cur == propExtend || cur == propZWJ: // GB9
		return false
	case cur == propSpacingMark: // GB9a
		return false
	case prev == propPrepend: // GB9b
		return false
	case prev == propZWJ && s.zwjAfterPict && pictographic(r): // GB11
		return false
	case prev == propRegionalIndicator && cur == propRegionalIndicator && s.ri%2 == 1: // GB12, GB13
		return false
	}
	return true // GB999
}

// GraphemeCount is the number of extended grapheme clusters in s: what a
// reader would count as characters, and what a fiig string's `length()`
// is.
func GraphemeCount(s string) int {
	n := 0
	var seg segmenter
	for i, r := range s {
		// breaks runs first, for its state; there is no boundary to
		// decide before the first code point.
		if seg.breaks(r) || i == 0 {
			n++
		}
	}
	return n
}

// GraphemeOffsets is the byte offset where each grapheme cluster of s
// starts, followed by len(s), so cluster i is s[o[i]:o[i+1]]. An empty s
// gives [0].
func GraphemeOffsets(s string) []int {
	offsets := make([]int, 0, utf8.RuneCountInString(s)+1)
	var seg segmenter
	for i, r := range s {
		if seg.breaks(r) || i == 0 {
			offsets = append(offsets, i)
		}
	}
	return append(offsets, len(s))
}
