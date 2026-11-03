package semver

import "testing"

func TestParseValid(t *testing.T) {
	v, err := Parse("2.4.1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v != (Version{Major: 2, Minor: 4, Patch: 1}) {
		t.Fatalf("got %+v", v)
	}
	if v.String() != "2.4.1" {
		t.Fatalf("String() = %q", v.String())
	}
}

func TestParseRejectsWrongShape(t *testing.T) {
	for _, s := range []string{"2.4", "2", "2.4.1.0", "2.x.1", "-1.0.0", "", "2..1"} {
		if _, err := Parse(s); err == nil {
			t.Fatalf("Parse(%q): want error, got none", s)
		}
	}
}

func TestCompare(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "2.0.0", -1},
		{"2.0.0", "1.0.0", 1},
		{"1.2.0", "1.3.0", -1},
		{"1.2.3", "1.2.3", 0},
		{"1.2.4", "1.2.3", 1},
	}
	for _, c := range cases {
		a, _ := Parse(c.a)
		b, _ := Parse(c.b)
		if got := a.Compare(b); got != c.want {
			t.Fatalf("Compare(%s, %s) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestSameMajor(t *testing.T) {
	v1, _ := Parse("2.4.0")
	v2, _ := Parse("2.9.3")
	v3, _ := Parse("3.0.0")
	if !v1.SameMajor(v2) {
		t.Fatalf("2.4.0 and 2.9.3 should share a major")
	}
	if v1.SameMajor(v3) {
		t.Fatalf("2.4.0 and 3.0.0 should not share a major")
	}
}
