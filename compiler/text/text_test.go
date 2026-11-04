package text

import (
	"bufio"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

// TestGraphemeBreakConformance runs every case of Unicode's own
// GraphemeBreakTest.txt, for the version the tables come from.
func TestGraphemeBreakConformance(t *testing.T) {
	f, err := os.Open("testdata/GraphemeBreakTest.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Scan()
	if want := "# GraphemeBreakTest-" + UnicodeVersion + ".txt"; sc.Text() != want {
		t.Fatalf("test data is %q, tables are %s", sc.Text(), UnicodeVersion)
	}
	cases := 0
	for sc.Scan() {
		line, _, _ := strings.Cut(sc.Text(), "#")
		if strings.TrimSpace(line) == "" {
			continue
		}
		var s strings.Builder
		var want []int
		for _, tok := range strings.Fields(line) {
			switch tok {
			case "÷":
				want = append(want, s.Len())
			case "×":
			default:
				n, err := strconv.ParseUint(tok, 16, 32)
				if err != nil {
					t.Fatalf("bad line %q", line)
				}
				s.WriteRune(rune(n))
			}
		}
		got := GraphemeOffsets(s.String())
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: offsets %v, want %v", strings.TrimSpace(line), got, want)
		}
		if n := GraphemeCount(s.String()); n != len(want)-1 {
			t.Errorf("%s: count %d, want %d", strings.TrimSpace(line), n, len(want)-1)
		}
		cases++
	}
	if cases < 600 {
		t.Fatalf("read only %d cases", cases)
	}
}

func TestVersionMatchesGo(t *testing.T) {
	if UnicodeVersion != unicode.Version {
		t.Fatalf("tables are Unicode %s, Go's unicode package is %s: run go generate", UnicodeVersion, unicode.Version)
	}
}

func TestGraphemeCountExamples(t *testing.T) {
	for s, want := range map[string]int{
		"":       0,
		"Zoë 👍🏽": 5,
		"é":     1,
		"👨‍👩‍👧":  1,
		"🇳🇿🇦🇺":   2,
		"a\r\nb": 3,
		"한국어":    3,
		"각":    1, // conjoining jamo
	} {
		if got := GraphemeCount(s); got != want {
			t.Errorf("GraphemeCount(%q) = %d, want %d", s, got, want)
		}
	}
}

func TestCase(t *testing.T) {
	cases := []struct{ in, upper, lower, fold string }{
		{"straße", "STRASSE", "straße", "strasse"},
		{"ΟΔΟΣ", "ΟΔΟΣ", "οδοσ", "οδοσ"},
		{"ﬁ", "FI", "ﬁ", "fi"},
		{"İ", "İ", "i̇", "i̇"},
		{"TITLE", "TITLE", "title", "title"},
		{"ǅ", "Ǆ", "ǆ", "ǆ"},
	}
	for _, c := range cases {
		if got := Upper(c.in); got != c.upper {
			t.Errorf("Upper(%q) = %q, want %q", c.in, got, c.upper)
		}
		if got := Lower(c.in); got != c.lower {
			t.Errorf("Lower(%q) = %q, want %q", c.in, got, c.lower)
		}
		if got := Fold(c.in); got != c.fold {
			t.Errorf("Fold(%q) = %q, want %q", c.in, got, c.fold)
		}
	}
	for _, pair := range [][2]string{{"straße", "STRASSE"}, {"ΟΔΟΣ", "οδος"}, {"Ada@Example.com", "ada@example.COM"}} {
		if !EqualFold(pair[0], pair[1]) {
			t.Errorf("EqualFold(%q, %q) = false", pair[0], pair[1])
		}
	}
	if EqualFold("a", "b") {
		t.Error("EqualFold(a, b) = true")
	}
}
