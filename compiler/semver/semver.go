// Package semver implements the minimal version comparison ADR 0032
// needs: strict major.minor.patch triples. No pre-release or build
// metadata — nothing in the corpus has needed it, and guessing at a format
// past what's attested isn't this project's habit.
package semver

import (
	"fmt"
	"strconv"
	"strings"
)

// Version is a parsed major.minor.patch triple.
type Version struct {
	Major, Minor, Patch int
}

// Parse requires exactly three dot-separated non-negative integers —
// manifest versions and dependency minimums (ADR 0032) are never ranges,
// so there's nothing more to this grammar than that.
func Parse(s string) (Version, error) {
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return Version{}, fmt.Errorf("want major.minor.patch, got %q", s)
	}
	var v Version
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return Version{}, fmt.Errorf("want major.minor.patch, got %q", s)
		}
		switch i {
		case 0:
			v.Major = n
		case 1:
			v.Minor = n
		case 2:
			v.Patch = n
		}
	}
	return v, nil
}

func (v Version) String() string {
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
}

// Compare returns -1, 0, or 1 as v is less than, equal to, or greater than other.
func (v Version) Compare(other Version) int {
	if v.Major != other.Major {
		return compareInt(v.Major, other.Major)
	}
	if v.Minor != other.Minor {
		return compareInt(v.Minor, other.Minor)
	}
	return compareInt(v.Patch, other.Patch)
}

// SameMajor reports whether v and other share a major version — ADR
// 0032's boundary between "compatible, resolve by Minimum Version
// Selection" and "different major, allowed to coexist as distinct
// identities instead of being forced to agree."
func (v Version) SameMajor(other Version) bool {
	return v.Major == other.Major
}

func compareInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}
