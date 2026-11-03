package syntax

import "strconv"

// ParseIntEncoding splits an integer encoding name (ADR 0058 §3) into its
// width, signedness, and byte order: "u8" is (8, false, false), "i32le" is
// (32, true, true), "u31" is (31, false, false). A name that is not of the
// form [ui]<digits>[be|le] gives a width of 0. Whether the combination is
// legal (byte order present exactly when required, width in range) is
// IntEncodingError's question.
func ParseIntEncoding(name string) (bits int, signed, little bool) {
	if len(name) < 2 || (name[0] != 'u' && name[0] != 'i') {
		return 0, false, false
	}
	signed = name[0] == 'i'
	rest := name[1:]
	order := ""
	if n := len(rest); n > 2 && (rest[n-2:] == "be" || rest[n-2:] == "le") {
		order = rest[n-2:]
		rest = rest[:n-2]
	}
	n, err := strconv.Atoi(rest)
	if err != nil || n <= 0 || rest[0] == '0' {
		return 0, false, false
	}
	return n, signed, order == "le"
}

// IntEncodingError says what is wrong with an integer encoding name, or ""
// when it is legal:
//   - u8 and i8: one byte, no byte order.
//   - a multiple of 8 from 16 to 56, u or i, and i64: byte order required.
//   - u1 to u63 when not a multiple of 8: big-endian bit order, no suffix.
//   - no u64, and no signed sub-byte fields.
func IntEncodingError(name string) string {
	bits, signed, _ := ParseIntEncoding(name)
	if bits == 0 {
		return "unknown encoding " + strconv.Quote(name) + "; expected an integer encoding (u8, i32be, u4, ...), bytes, or utf8"
	}
	hasOrder := len(name) > 2 && (name[len(name)-2:] == "be" || name[len(name)-2:] == "le")
	switch {
	case bits > 64:
		return name + " is wider than 64 bits"
	case bits == 64 && !signed:
		return "u64 is not supported: int is 64-bit signed, so a value above 2^63 - 1 has nothing to bind to; use i64be/i64le, or `0: u1` followed by u63 when the top bit is reserved"
	case bits == 8:
		if hasOrder {
			return name + " is one byte and has no byte order; write " + name[:len(name)-2]
		}
	case bits%8 == 0:
		if !hasOrder {
			return name + " needs a byte order: write " + name + "be or " + name + "le"
		}
	default:
		if signed {
			return name + ": a sub-byte field is unsigned; write u" + strconv.Itoa(bits)
		}
		if hasOrder {
			return name + ": a field that is not whole bytes is always big-endian bit order; drop the " + name[len(name)-2:]
		}
	}
	return ""
}

// IntValue is an integer literal's value: decimal, or hex after `0x` (ADR
// 0058 §2). A leading zero is still decimal, never octal.
func IntValue(text string) (int64, error) {
	if len(text) > 2 && text[0] == '0' && (text[1] == 'x' || text[1] == 'X') {
		u, err := strconv.ParseUint(text[2:], 16, 64)
		if err != nil || u > 1<<63-1 {
			return 0, strconv.ErrRange
		}
		return int64(u), nil
	}
	return strconv.ParseInt(text, 10, 64)
}
