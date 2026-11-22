package runtime

import (
	"strings"
	"testing"
)

// ADR 0067: string methods in fiig programs.
func TestStringPrograms(t *testing.T) {
	src := `rule counts(s: string) -> string {
    return "${s.length()} ${s.codePointCount()} ${s.utf8().length()}";
}

rule cut(s: string, from: int, to: int) -> string {
    return s.slice(from, to);
}

rule affixes(s: string) -> string {
    return "${s.startsWith("Bearer ")} ${s.endsWith("3")} [${s.removePrefix("Bearer ")}] [${s.removeSuffix("x")}]";
}

rule trims(s: string) -> string {
    return "[${s.trim()}] [${s.trimStart()}] [${s.trimEnd()}] ${s.isBlank() == (s.trim() == "")}";
}

rule cases(s: string) -> string {
    return "${s.lowercase()} ${s.uppercase()}";
}

rule same(a: string, b: string) -> bool {
    return a.equalsIgnoringCase(b);
}

rule pieces(s: string, sep: string) -> string {
    parts = s.split(sep);
    return "${parts.length()} ${parts.join("|")}";
}

rule replace(s: string, old: string, new: string) -> string {
    return s.replaceAll(old, new);
}

rule toInt(s: string) -> int | None {
    return s.toIntOrNone();
}

rule toFloat(s: string) -> float | None {
    return s.toFloatOrNone();
}

rule dec(s: string) -> decimal | None {
    return s.toDecimalOrNone();
}`
	str := func(s string) map[string]Value { return map[string]Value{"s": Str(s)} }
	cases := []struct {
		name string
		args map[string]Value
		want string
	}{
		// §1: three counts, each named.
		{"counts", str("Zoë 👍🏽"), "5 6 13"},
		{"counts", str("é"), "1 2 3"},
		{"counts", str("👨‍👩‍👧"), "1 5 18"},
		{"counts", str(""), "0 0 0"},
		// §2: slice by grapheme, so the modifier stays with its emoji.
		{"cut", map[string]Value{"s": Str("Zoë 👍🏽!"), "from": Int(4), "to": Int(5)}, "👍🏽"},
		{"cut", map[string]Value{"s": Str("Zoë 👍🏽!"), "from": Int(0), "to": Int(6)}, "Zoë 👍🏽!"},
		{"cut", map[string]Value{"s": Str("abc"), "from": Int(1), "to": Int(1)}, ""},
		// §3.
		{"affixes", str("Bearer abc123"), "true true [abc123] [Bearer abc123]"},
		{"affixes", str("bearer abc"), "false false [bearer abc] [bearer abc]"},
		{"trims", str(" \t Ada  \n"), "[Ada] [Ada  \n] [ \t Ada] true"},
		{"trims", str(" "), "[] [] [] true"},
		// §4: full mapping, no locale, no Final_Sigma.
		{"cases", str("Straße TITLE"), "straße title STRASSE TITLE"},
		{"cases", str("ΟΔΟΣ"), "οδοσ ΟΔΟΣ"},
		{"same", map[string]Value{"a": Str("straße"), "b": Str("STRASSE")}, "true"},
		{"same", map[string]Value{"a": Str("ΟΔΟΣ"), "b": Str("οδος")}, "true"},
		{"same", map[string]Value{"a": Str(" Ada@Example.com"), "b": Str("ada@example.com")}, "false"},
		// §5: empty pieces kept; searching matches code points.
		{"pieces", map[string]Value{"s": Str("a,b,,c,"), "sep": Str(",")}, "5 a|b||c|"},
		{"pieces", map[string]Value{"s": Str(""), "sep": Str(",")}, "1 "},
		{"pieces", map[string]Value{"s": Str("👨‍👩‍👧"), "sep": Str("‍")}, "3 👨|👩|👧"},
		// §6: left to right, no overlap.
		{"replace", map[string]Value{"s": Str("aaa"), "old": Str("aa"), "new": Str("b")}, "ba"},
		{"replace", map[string]Value{"s": Str("a-b-c"), "old": Str("-"), "new": Str("/")}, "a/b/c"},
		// §7: the text number grammar.
		{"toInt", str("42"), "42"},
		{"toInt", str("+42"), "42"},
		{"toInt", str("-007"), "-7"},
		{"toInt", str(" 42"), "None"},
		{"toInt", str("3.0"), "None"},
		{"toInt", str("1e3"), "None"},
		{"toInt", str("1_000"), "None"},
		{"toInt", str("+-1"), "None"},
		{"toInt", str("-"), "None"},
		{"toInt", str("9223372036854775807"), "9223372036854775807"},
		{"toInt", str("9223372036854775808"), "None"},
		{"toFloat", str("2.5"), "2.5"},
		{"toFloat", str(".5"), "0.5"},
		{"toFloat", str("5."), "5.0"},
		{"toFloat", str("+1e3"), "1000.0"},
		{"toFloat", str("."), "None"},
		{"toFloat", str("NaN"), "None"},
		{"toFloat", str("Infinity"), "None"},
		{"toFloat", str("0x1p3"), "None"},
		{"toFloat", str("1e400"), "None"},
		{"dec", str("+.50"), "0.50"},
		{"dec", str("5."), "5"},
		{"dec", str("007.25"), "7.25"},
		{"dec", str("1,5"), "None"},
	}
	for _, c := range cases {
		got, fault := runEach(t, src, c.name, c.args)
		if fault != nil || got == nil || got.String() != c.want {
			t.Errorf("%s(%v) = %v, %v; want %s", c.name, c.args, got, fault, c.want)
		}
	}
	faults := []struct {
		name  string
		args  map[string]Value
		fault string
	}{
		{"cut", map[string]Value{"s": Str("Zoë 👍🏽"), "from": Int(0), "to": Int(6)}, "slice(0, 6) out of range for string of length 5"},
		{"pieces", map[string]Value{"s": Str("abc"), "sep": Str("")}, "separator is empty"},
		{"replace", map[string]Value{"s": Str("abc"), "old": Str(""), "new": Str("x")}, "text to replace is empty"},
	}
	for _, c := range faults {
		_, fault := runEach(t, src, c.name, c.args)
		if fault == nil || !strings.Contains(fault.Message, c.fault) {
			t.Errorf("%s(%v) fault = %v, want %q", c.name, c.args, fault, c.fault)
		}
	}
}
