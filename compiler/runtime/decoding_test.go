package runtime

import "testing"

// ADR 0069 §7: text decoded back to bytes, reading what real producers
// send, and None for text that isn't in the alphabet.
func TestTextDecodings(t *testing.T) {
	src := `rule decode(s: string, how: string) -> string {
    decoded: bytes | None = None;
    if (how == "hex") {
        decoded = s.fromHex();
    } else if (how == "base64") {
        decoded = s.fromBase64();
    } else {
        decoded = s.fromBase64Url();
    }
    return match (decoded) {
        None -> {
            return "None";
        },
        b -> {
            return b.toHex();
        }
    }
}`
	cases := []struct{ s, how, want string }{
		{"", "hex", ""},
		{"fbff", "hex", "fbff"},
		{"FBFF", "hex", "fbff"}, // either case
		{"fBf", "hex", "None"},  // odd length
		{"zz", "hex", "None"},
		{"Zm9vYmFy", "base64", "666f6f626172"},
		{"Zg==", "base64", "66"},
		{"Zg", "base64", "66"},                     // padding optional
		{"Zg=", "base64", "None"},                  // but complete when present
		{"Zm9v\r\nYmFy", "base64", "666f6f626172"}, // MIME and PEM line breaks
		{"Zm9v YmFy", "base64", "None"},            // any other whitespace is damage
		{"+/8=", "base64", "fbff"},
		{"-_8=", "base64", "None"}, // the other alphabet
		{"-_8", "base64Url", "fbff"},
		{"-_8=", "base64Url", "fbff"},
		{"+/8", "base64Url", "None"},
		{"QR", "base64", "41"}, // leftover bits ignored, as in "QQ"
		{"QQ", "base64", "41"},
		{"Q", "base64", "None"}, // one symbol is never a whole byte
	}
	for _, c := range cases {
		got, fault := runEach(t, src, "decode", map[string]Value{"s": Str(c.s), "how": Str(c.how)})
		if fault != nil || got == nil || got.String() != c.want {
			t.Errorf("decode(%q, %s) = %v, %v; want %q", c.s, c.how, got, fault, c.want)
		}
	}
}

// Basic auth, the case §7 exists for: a pure rule over the header.
func TestBasicAuthIsARule(t *testing.T) {
	src := `rule basicUser(header: string) -> string | None {
    if (!header.startsWith(prefix: "Basic ")) {
        return None;
    }
    decoded = header.removePrefix(prefix: "Basic ").fromBase64();
    return match (decoded) {
        None -> {
            return None;
        },
        b -> {
            return match (b.utf8()) {
                None -> {
                    return None;
                },
                text -> {
                    return text.split(separator: ":").first();
                }
            }
        }
    }
}`
	got, fault := runEach(t, src, "basicUser", map[string]Value{"header": Str("Basic QWxhZGRpbjpvcGVuIHNlc2FtZQ==")})
	if fault != nil || got.String() != "Aladdin" {
		t.Fatalf("basicUser = %v, %v", got, fault)
	}
}
