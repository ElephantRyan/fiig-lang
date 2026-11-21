package types

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// Hearsay GAPS G59: the prelude's type names are listed where someone
// choosing a name would look, and the list can't drift from the code.
func TestTerminologyListsThePreludeTypes(t *testing.T) {
	data, err := os.ReadFile("../../docs/terminology.md")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	start := strings.Index(text, "<!-- prelude-types")
	end := strings.Index(text, "<!-- /prelude-types -->")
	if start < 0 || end < start {
		t.Fatal("docs/terminology.md has no prelude-types block")
	}
	var listed []string
	for _, m := range regexp.MustCompile("`([A-Za-z]+)`").FindAllStringSubmatch(text[start:end], -1) {
		listed = append(listed, m[1])
	}
	slices.Sort(listed)
	if want := PreludeTypeNames(); !slices.Equal(listed, want) {
		t.Fatalf("docs/terminology.md lists %v, the prelude has %v", listed, want)
	}
}
