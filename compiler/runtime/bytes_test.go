package runtime

import (
	"context"
	"strings"
	"testing"

	"fiig-lang/compiler/syntax"
)

// ADR 0058: bytes, binary patterns, and binary expressions.

func b(xs ...byte) Bytes { return Bytes{B: xs} }

// Captured Postgres backend messages: AuthenticationOk ('R', length 8,
// code 0) followed by ReadyForQuery ('Z', length 5, status 'I').
var (
	authOK     = []byte{'R', 0, 0, 0, 8, 0, 0, 0, 0}
	readyIdle  = []byte{'Z', 0, 0, 0, 5, 'I'}
	md5Request = []byte{'R', 0, 0, 0, 12, 0, 0, 0, 5, 0xDE, 0xAD, 0xBE, 0xEF}
)

func TestCorpusPostgresFraming(t *testing.T) {
	u := loadUniverse(t, "postgres-wire/models", "postgres-wire/conversions")
	in := newTestInterp(u, nil)
	call := func(name string, args map[string]Value) Value {
		t.Helper()
		v, fault := in.Call(context.Background(), name, args)
		if fault != nil {
			t.Fatalf("%s: %v", name, fault)
		}
		return v
	}

	buffer := append(append([]byte{}, authOK...), readyIdle...)
	frame, ok := call("readFrame", map[string]Value{"buffer": b(buffer...)}).(Record)
	if !ok {
		t.Fatalf("readFrame of a full buffer did not return a Frame")
	}
	if frame.Fields["kind"] != Int('R') || !Equal(frame.Fields["body"], b(0, 0, 0, 0)) || !Equal(frame.Fields["rest"], b(readyIdle...)) {
		t.Fatalf("frame = %s", frame)
	}

	next, ok := call("readFrame", map[string]Value{"buffer": frame.Fields["rest"]}).(Record)
	if !ok || next.Fields["kind"] != Int('Z') || !Equal(next.Fields["body"], b('I')) || !Equal(next.Fields["rest"], b()) {
		t.Fatalf("second frame = %v", next)
	}

	// Too short for the length it announces: read more.
	if v := call("readFrame", map[string]Value{"buffer": b(authOK[:6]...)}); v != (None{}) {
		t.Fatalf("readFrame of a partial buffer = %s, want None", v)
	}

	auth := call("readAuthentication", map[string]Value{"body": frame.Fields["body"]})
	if ev, ok := auth.(EnumValue); !ok || ev.Variant != "AuthenticationOk" {
		t.Fatalf("readAuthentication(ok) = %s", auth)
	}
	md5 := call("readFrame", map[string]Value{"buffer": b(md5Request...)}).(Record)
	salted := call("readAuthentication", map[string]Value{"body": md5.Fields["body"]}).(EnumValue)
	if salted.Variant != "Md5Password" || !Equal(salted.Fields[0], b(0xDE, 0xAD, 0xBE, 0xEF)) {
		t.Fatalf("readAuthentication(md5) = %s", salted)
	}

	msg := call("startupMessage", map[string]Value{"user": Str("postgres"), "database": Str("app")})
	params := "user\x00postgres\x00database\x00app\x00\x00"
	want := append([]byte{0, 0, 0, byte(8 + len(params)), 0, 3, 0, 0}, params...)
	if !Equal(msg, b(want...)) {
		t.Fatalf("startupMessage = %s, want %s", msg, b(want...))
	}
}

// TestCorpusPostgresReadFrames is ADR 0059's attested example: a while loop
// that splits one read into every complete message plus the cut-off tail.
func TestCorpusPostgresReadFrames(t *testing.T) {
	u := loadUniverse(t, "postgres-wire/models", "postgres-wire/conversions")
	in := newTestInterp(u, nil)

	var buffer []byte
	buffer = append(buffer, authOK...)
	buffer = append(buffer, readyIdle...)
	buffer = append(buffer, md5Request[:7]...)
	v, fault := in.Call(context.Background(), "readFrames", map[string]Value{"buffer": b(buffer...)})
	if fault != nil {
		t.Fatalf("readFrames: %v", fault)
	}
	got := v.(Record)
	frames := got.Fields["frames"].(List).Elems
	if len(frames) != 2 || frames[0].(Record).Fields["kind"] != Int('R') || frames[1].(Record).Fields["kind"] != Int('Z') {
		t.Fatalf("frames = %s, want R then Z", got.Fields["frames"])
	}
	if !Equal(got.Fields["rest"], b(md5Request[:7]...)) {
		t.Fatalf("rest = %s, want the partial md5 request", got.Fields["rest"])
	}

	v, fault = in.Call(context.Background(), "readFrames", map[string]Value{"buffer": b()})
	if fault != nil {
		t.Fatalf("readFrames(empty): %v", fault)
	}
	if n := len(v.(Record).Fields["frames"].(List).Elems); n != 0 {
		t.Fatalf("readFrames(empty) gave %d frames, want 0", n)
	}
}

func run(t *testing.T, src, name string, args map[string]Value) (Value, *Fault) {
	t.Helper()
	in := newTestInterp(universeFromSource(t, src), nil)
	return in.Call(context.Background(), name, args)
}

// TestSubByteFields is §3 on HTTP/2's frame header: a 24-bit length, a
// reserved bit, and a 31-bit stream identifier.
func TestSubByteFields(t *testing.T) {
	src := `conversion stream(frame: bytes) -> int {
    return match (frame) {
        <<length: u24be, kind: u8, flags: u8, _: u1, stream: u31, payload: bytes(length)>> -> stream * 1000 + length
        _ -> -1
    }
}
`
	header := []byte{0, 0, 3, 0, 1, 0x80, 0, 1, 0x2C} // length 3, reserved bit set, stream 300
	v, fault := run(t, src, "stream", map[string]Value{"frame": b(append(header, 'a', 'b', 'c')...)})
	if fault != nil || v != Int(300*1000+3) {
		t.Fatalf("stream = %v, %v; want 300003", v, fault)
	}
}

func TestWebSocketHeaderAndLittleEndian(t *testing.T) {
	src := `conversion opcode(frame: bytes) -> int {
    return match (frame) {
        <<1: u1, _: u3, op: u4, 0: u1, length: u7, word: u16le>> -> op * 100000 + length * 1000 + word
        _ -> -1
    }
}
`
	// FIN set, opcode 1 (text), unmasked, length 2, then 0x0102 little-endian.
	v, fault := run(t, src, "opcode", map[string]Value{"frame": b(0x81, 0x02, 0x02, 0x01)})
	if fault != nil || v != Int(100000+2000+0x0102) {
		t.Fatalf("opcode = %v, %v", v, fault)
	}
	// A masked frame fails the `0: u1` literal and falls through.
	if v, _ := run(t, src, "opcode", map[string]Value{"frame": b(0x81, 0x82, 0x02, 0x01)}); v != Int(-1) {
		t.Fatalf("masked frame matched: %v", v)
	}
}

func TestSignedFieldsAndRoundTrip(t *testing.T) {
	src := `conversion roundTrip(n: int) -> int {
    encoded = <<n: i16be, n: i32le, 0xFF: u8>>;
    return match (encoded) {
        <<a: i16be, b: i32le, 255: u8>> -> a + b
        _ -> 0
    }
}
`
	v, fault := run(t, src, "roundTrip", map[string]Value{"n": Int(-2)})
	if fault != nil || v != Int(-4) {
		t.Fatalf("roundTrip(-2) = %v, %v", v, fault)
	}
}

// TestValueThatDoesNotFitFaults is §4: no silent truncation.
func TestValueThatDoesNotFitFaults(t *testing.T) {
	src := "conversion one(n: int) -> bytes {\n    return <<n: u8>>;\n}\n"
	if _, fault := run(t, src, "one", map[string]Value{"n": Int(300)}); fault == nil || !strings.Contains(fault.Message, "does not fit in u8") {
		t.Fatalf("<<300: u8>> = %v, want a fault", fault)
	}
}

func TestBytesMethods(t *testing.T) {
	val := b('h', 'i', 0xFF)
	for _, tc := range []struct {
		method string
		args   []Value
		want   Value
	}{
		{"length", nil, Int(3)},
		{"at", []Value{Int(2)}, Int(255)},
		{"slice", []Value{Int(0), Int(2)}, b('h', 'i')},
		{"utf8", nil, None{}},
	} {
		got, fault := evalBuiltinMethod(val, tc.method, tc.args)
		if fault != nil || !Equal(got, tc.want) {
			t.Errorf("%s = %v, %v; want %s", tc.method, got, fault, tc.want)
		}
	}
	if got, _ := evalBuiltinMethod(b('h', 'i'), "utf8", nil); got != Str("hi") {
		t.Errorf("utf8 of valid bytes = %v", got)
	}
	if _, fault := evalBuiltinMethod(val, "at", []Value{Int(3)}); fault == nil {
		t.Error("at past the end must fault")
	}
	if _, fault := evalBuiltinMethod(val, "slice", []Value{Int(2), Int(1)}); fault == nil {
		t.Error("slice with to < from must fault")
	}
}

// TestGrowingBytesCopiesLogarithmically is §4's cost guarantee, as for
// lists in ADR 0057.
func TestGrowingBytesCopiesLogarithmically(t *testing.T) {
	before := bytesCopies.Load()
	out := Bytes{}
	for i := 0; i < 100_000; i++ {
		out = growBytes(out, []byte{byte(i)})
	}
	if copies := bytesCopies.Load() - before; copies > 20 {
		t.Fatalf("100,000 one-byte growths copied %d times", copies)
	}
	if len(out.B) != 100_000 || out.B[99_999] != byte(99_999%256) {
		t.Fatalf("built %d bytes", len(out.B))
	}

	base := growBytes(Bytes{}, []byte{1, 2})
	x := growBytes(base, []byte{3})
	y := growBytes(base, []byte{4})
	if !Equal(base, b(1, 2)) || !Equal(x, b(1, 2, 3)) || !Equal(y, b(1, 2, 4)) {
		t.Fatalf("base=%s x=%s y=%s", base, x, y)
	}
}

func TestMatchedSegmentsShareTheInput(t *testing.T) {
	input := b(1, 2, 3, 4)
	env := NewEnv()
	p := &syntax.BytesPattern{Segments: []syntax.Segment{
		{Name: "head", Encoding: syntax.Encoding{Name: "bytes", Kind: syntax.SegBytes, Size: &syntax.IntLit{Text: "1"}}},
		{Name: "rest", Encoding: syntax.Encoding{Name: "bytes", Kind: syntax.SegBytes}},
	}}
	if !matchBytesPattern(p, input, env) {
		t.Fatal("no match")
	}
	rest, _ := env.Get("rest")
	if &rest.(Bytes).B[0] != &input.B[1] {
		t.Fatal("rest is a copy, want a view of the input")
	}
}

// TestCorpusHTTPRequestBytes pins acme-http's request to the exact bytes on
// the wire, CRLF line endings included (ADR 0061's first attested `\r`).
func TestCorpusHTTPRequestBytes(t *testing.T) {
	u := loadUniverse(t, "acme-http/src")
	in := newTestInterp(u, nil)
	request := Record{TypeName: "WebRequest", Fields: map[string]Value{
		"host": Str("pay.example"), "path": Str("/v1/charges"), "body": Str("amount=5"), "authorization": None{},
	}}
	v, fault := in.Call(context.Background(), "httpRequest", map[string]Value{"request": request})
	if fault != nil {
		t.Fatalf("httpRequest: %v", fault)
	}
	want := "POST /v1/charges HTTP/1.1\r\nHost: pay.example\r\nContent-Length: 8\r\nConnection: close\r\n\r\namount=5"
	if !Equal(v, Bytes{B: []byte(want)}) {
		t.Fatalf("httpRequest = %s, want %q", v, want)
	}
	// ADR 0071 §4: the key a provider revealed goes out as one header.
	request.Fields["authorization"] = Str("Bearer sk_test")
	v, _ = in.Call(context.Background(), "httpRequest", map[string]Value{"request": request})
	want = "POST /v1/charges HTTP/1.1\r\nHost: pay.example\r\nAuthorization: Bearer sk_test\r\nContent-Length: 8\r\nConnection: close\r\n\r\namount=5"
	if !Equal(v, Bytes{B: []byte(want)}) {
		t.Fatalf("httpRequest = %s, want %q", v, want)
	}
}
