package runtime

import "testing"

// ADR 0068 §3: bytes written as text, in fiig programs.
func TestBytesEncodings(t *testing.T) {
	src := `rule encode(s: string) -> string {
    b = s.utf8()
    return "${b.toHex()} ${b.toBase64()} ${b.toBase64Url()}"
}

rule symbols() -> string {
    b = <<0xfb: u8, 0xff: u8>>
    return "${b.toHex()} ${b.toBase64()} ${b.toBase64Url()}"
}`
	cases := []struct {
		name string
		args map[string]Value
		want string
	}{
		// RFC 4648 §10's test vectors: padding appears in toBase64 only.
		{"encode", map[string]Value{"s": Str("")}, "  "},
		{"encode", map[string]Value{"s": Str("f")}, "66 Zg== Zg"},
		{"encode", map[string]Value{"s": Str("fo")}, "666f Zm8= Zm8"},
		{"encode", map[string]Value{"s": Str("foo")}, "666f6f Zm9v Zm9v"},
		{"encode", map[string]Value{"s": Str("foobar")}, "666f6f626172 Zm9vYmFy Zm9vYmFy"},
		// The ADR's table: the two alphabets differ in the last two symbols.
		{"symbols", nil, "fbff +/8= -_8"},
	}
	for _, c := range cases {
		got, fault := runEach(t, src, c.name, c.args)
		if fault != nil || got == nil || got.String() != c.want {
			t.Errorf("%s(%v) = %q, %v; want %q", c.name, c.args, got, fault, c.want)
		}
	}
}
