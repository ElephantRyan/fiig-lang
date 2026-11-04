package text

import (
	"strings"
	"unicode"
)

// Upper is s in Unicode's full uppercase mapping with no locale and no
// context (ADR 0067 §4): SpecialCasing.txt's unconditional entries, else
// the simple mapping. "straße" is "STRASSE".
func Upper(s string) string { return mapRunes(s, specialUpper, unicode.ToUpper) }

// Lower is Upper's lowercase counterpart. Final_Sigma is a context
// condition, so Σ is always σ.
func Lower(s string) string { return mapRunes(s, specialLower, unicode.ToLower) }

// Fold is s in full case folding (CaseFolding.txt statuses C and F): the
// form two strings are compared in when case doesn't matter, where ß is
// "ss" and Σ, σ, and ς are one.
func Fold(s string) string {
	return mapRunes(s, foldings, func(r rune) rune { return r })
}

// EqualFold reports whether a and b are equal under full case folding.
func EqualFold(a, b string) bool { return Fold(a) == Fold(b) }

func mapRunes(s string, special map[rune]string, simple func(rune) rune) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if m, ok := special[r]; ok {
			b.WriteString(m)
		} else {
			b.WriteRune(simple(r))
		}
	}
	return b.String()
}
