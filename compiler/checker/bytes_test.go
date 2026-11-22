package checker

import (
	"testing"

	"fiig-lang/compiler/syntax"
)

// ADR 0058: bytes, binary patterns, and binary expressions.

func TestBinaryPatternsAndExpressionsTypeCheck(t *testing.T) {
	f := mustParse(t, `conversion frame(buffer: bytes) -> int {
    return match (buffer) {
        <<kind: u8, length: i32be, body: bytes(length - 4), rest: bytes>> -> {
            out = <<kind: u8, body.length() + 4: i32be, body: bytes, rest: bytes>>;
            return out.length() + body.at(0) + kind;
        }
        <<0x52: u8, _: u16le, "ok": utf8, word: utf8(2)>> -> {
            return word.utf8().length();
        }
        _ -> {
            return 0;
        }
    }
}
`)
	if diags := Check([]*syntax.File{f}, nil); len(diags) != 0 {
		t.Fatalf("want a clean build, got %v", diags)
	}
}

func TestBytesMethodsAreTyped(t *testing.T) {
	f := mustParse(t, `rule methods(b: bytes, s: string) -> bool {
    n: int = b.length();
    first: int = b.at(0);
    head: bytes = b.slice(0, 2);
    raw: bytes = s.utf8();
    for (x in b) {
        n = n + x;
    }
    return match (b.utf8()) {
        None -> {
            return false;
        }
        text -> {
            return text.contains("@");
        }
    }
}
`)
	if diags := Check([]*syntax.File{f}, nil); len(diags) != 0 {
		t.Fatalf("want a clean build, got %v", diags)
	}

	bad := mustParse(t, `rule bad(b: bytes) -> int {
    s: string = b.at(0);
    return b.size();
}
`)
	got := Check([]*syntax.File{bad}, nil)
	if counts := rules(got); counts["A1"] != 1 || len(got) != 2 {
		t.Fatalf("want A1 (int into string) and an unknown-method error, got %v", got)
	}
}

func TestBinaryPatternWithoutCatchAllIsA9(t *testing.T) {
	f := mustParse(t, `conversion code(body: bytes) -> int {
    return match (body) {
        <<n: i32be>> -> n
    }
}
`)
	if got := rules(Check([]*syntax.File{f}, nil)); got["A9"] != 1 || len(got) != 1 {
		t.Fatalf("want A9 for a binary match with no catch-all, got %v", got)
	}
}

// TestSegmentErrorsAreA14 covers each compile-time segment rule in §3 and
// §4, one source per rule so each error is counted alone.
func TestSegmentErrorsAreA14(t *testing.T) {
	cases := map[string]string{
		"no byte order":       "<<n: u32>> -> n",
		"byte order on u8":    "<<n: u8be>> -> n",
		"no u64":              "<<n: u64be>> -> n",
		"signed sub-byte":     "<<n: i4, _: u4>> -> n",
		"unknown encoding":    "<<n: word>> -> n",
		"misaligned bytes":    "<<n: u4, rest: bytes>> -> n",
		"misaligned end":      "<<n: u4>> -> n",
		"rest not last":       "<<rest: bytes, n: u8>> -> n",
		"utf8 needs a size":   "<<s: utf8>> -> 0",
		"literal too wide":    "<<300: u8>> -> 0",
		"size uses outside":   "<<b: bytes(outside)>> -> 0",
		"size calls":          "<<b: bytes(width())>> -> 0",
		"duplicate name":      "<<n: u8, n: u8>> -> n",
		"literal wrong size":  `<<"PG": utf8(3)>> -> 0`,
		"int literal as utf8": "<<7: utf8(1)>> -> 0",
	}
	for name, arm := range cases {
		src := "conversion c(b: bytes, outside: int) -> int {\n    return match (b) {\n        " + arm + "\n        _ -> 0\n    }\n}\n"
		got := rules(Check([]*syntax.File{mustParse(t, src)}, nil))
		if got["A14"] != 1 {
			t.Errorf("%s: want one A14, got %v", name, got)
		}
	}

	exprs := map[string]string{
		"value too wide":   "<<256: u8>>",
		"negative u8":      "<<-1: u8>>",
		"size in an expr":  "<<b: bytes(2)>>",
		"misaligned build": "<<1: u4>>",
	}
	for name, e := range exprs {
		src := "conversion c(b: bytes) -> bytes {\n    return " + e + "\n}\n"
		got := rules(Check([]*syntax.File{mustParse(t, src)}, nil))
		if got["A14"] != 1 {
			t.Errorf("%s: want one A14, got %v", name, got)
		}
	}
}

func TestBinaryPatternNeedsBytes(t *testing.T) {
	f := mustParse(t, `conversion c(n: int) -> int {
    return match (n) {
        <<x: u8>> -> x
        _ -> 0
    }
}
`)
	if got := rules(Check([]*syntax.File{f}, nil)); got["A1"] != 1 {
		t.Fatalf("want A1 for a binary pattern over an int, got %v", got)
	}
}

// ADR 0068 §3: the encodings are pure bytes methods, usable in a rule, and
// take no argument.
func TestBytesEncodingsCheck(t *testing.T) {
	if diags := checkSrc(t, `rule r(b: bytes) -> string {
    return "${b.toHex()} ${b.toBase64()} ${b.toBase64Url()}";
}`); len(diags) != 0 {
		t.Fatalf("diags = %v, want none", diags)
	}
	wantDiag(t, checkSrc(t, "rule r(b: bytes) -> string {\n    return b.toHex(2);\n}"), "", "bytes.toHex takes 0 argument(s), got 1")
	wantDiag(t, checkSrc(t, "rule r(s: string) -> string {\n    return s.toHex();\n}"), "K1", `string has no method "toHex"`)
}
