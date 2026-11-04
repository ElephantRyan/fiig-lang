package runtime

import (
	"context"
	"regexp"
	"strings"
	"testing"
)

// ADR 0068 §2: the four operations, in a fiig activity.
func TestRandomInAnActivity(t *testing.T) {
	src := `activity draw() -> string {
    die = Random.int(min: 1, max: 6)
    token = Random.bytes(count: 32).toBase64Url()
    f = Random.float()
    return "${die} ${token} ${f >= 0.0 and f < 1.0} ${Random.uuid()}"
}
`
	caps := NewCapabilityRegistry()
	caps.Register("Random", NewRandomProvider())
	got, fault := newTestInterp(universeFromSource(t, src), caps).Call(context.Background(), "draw", nil)
	if fault != nil {
		t.Fatal(fault)
	}
	re := regexp.MustCompile(`^[1-6] [A-Za-z0-9_-]{43} true [0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	if !re.MatchString(got.String()) {
		t.Errorf("draw() = %q", got)
	}
}

func call(t *testing.T, p *RandomProvider, method string, args ...Value) Value {
	t.Helper()
	v, fault := p.Call(context.Background(), method, args)
	if fault != nil {
		t.Fatalf("%s%v: %v", method, args, fault)
	}
	return v
}

// int includes both ends and covers the range evenly, including a range as
// wide as int itself.
func TestRandomIntRange(t *testing.T) {
	p := NewRandomProvider()
	counts := map[Int]int{}
	for range 6000 {
		counts[call(t, p, "int", Int(1), Int(6)).(Int)]++
	}
	for face := Int(1); face <= 6; face++ {
		if c := counts[face]; c < 800 || c > 1200 {
			t.Errorf("face %d drawn %d times of 6000", face, c)
		}
	}
	if len(counts) != 6 {
		t.Errorf("drew %v, want only 1..6", counts)
	}
	if v := call(t, p, "int", Int(7), Int(7)); v != Int(7) {
		t.Errorf("int(7, 7) = %v", v)
	}
	const minInt, maxInt = Int(-1 << 63), Int(1<<63 - 1)
	call(t, p, "int", minInt, maxInt)
	if v := call(t, p, "int", maxInt-1, maxInt).(Int); v < maxInt-1 {
		t.Errorf("int(max-1, max) = %v", v)
	}
	if v := call(t, p, "int", minInt, minInt+1).(Int); v > minInt+1 {
		t.Errorf("int(min, min+1) = %v", v)
	}
}

func TestRandomFaults(t *testing.T) {
	p := NewRandomProvider()
	cases := []struct {
		method string
		args   []Value
		want   string
	}{
		{"int", []Value{Int(1), Int(0)}, "Random.int(min: 1, max: 0) has no value: max is less than min"},
		{"bytes", []Value{Int(-1)}, "Random.bytes(count: -1) is outside 0 to 65536"},
		{"bytes", []Value{Int(65537)}, "Random.bytes(count: 65537) is outside 0 to 65536"},
	}
	for _, c := range cases {
		_, fault := p.Call(context.Background(), c.method, c.args)
		if fault == nil || !strings.Contains(fault.Message, c.want) {
			t.Errorf("%s%v fault = %v, want %q", c.method, c.args, fault, c.want)
		}
	}
	if b := call(t, p, "bytes", Int(0)).(Bytes); len(b.B) != 0 {
		t.Errorf("bytes(0) = %v", b)
	}
	if b := call(t, p, "bytes", Int(65536)).(Bytes); len(b.B) != 65536 {
		t.Errorf("bytes(65536) has %d bytes", len(b.B))
	}
}

// §6: the same seed gives the same draws, and another seed others.
func TestSeededRandomRepeats(t *testing.T) {
	draws := func(seed uint64) string {
		p := NewSeededRandomProvider(seed)
		return call(t, p, "uuid").String() + " " + call(t, p, "int", Int(1), Int(1000000)).String() + " " + call(t, p, "float").String()
	}
	a, b, c := draws(42), draws(42), draws(43)
	if a != b {
		t.Errorf("seed 42 gave %q then %q", a, b)
	}
	if a == c {
		t.Errorf("seeds 42 and 43 both gave %q", a)
	}
	if !NewSeededRandomProvider(42).Seeded || NewRandomProvider().Seeded {
		t.Error("Seeded marks only the seeded provider")
	}
}
