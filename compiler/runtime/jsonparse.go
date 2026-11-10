package runtime

import (
	"fmt"
	"strconv"
	"strings"
)

// ParseJSON reads one JSON value into the same Go shapes encoding/json
// produces for an `any` (nil/bool/string/[]any/map[string]any), except that
// every number is a json.Number holding its source text (ADR 0062 §1, §6):
// converting it is DecodeJSON's job, by the reader's declared type. It is
// readJSON's reader, so a duplicate member name and trailing data are
// errors; only member order is lost, which tooling's callers don't need.
func ParseJSON(data []byte) (any, error) {
	v, err := readJSON(data)
	if err != nil {
		return nil, err
	}
	return plainJSON(v), nil
}

// ReadJSON is ParseJSON keeping each object's member order, for DecodeJSON
// into a Map or a JsonValue (ADR 0073 §4): what an HTTP request body is
// read with.
func ReadJSON(data []byte) (any, error) { return readJSON(data) }

// jsonInt converts a JSON number's text to an int by ADR 0062 §2's value
// rule: it succeeds when the exact value is a whole number within int64,
// however the text writes it, so `3.0`, `3e0`, and `1e3` convert and `2.5`
// does not. The text is evaluated exactly without building the number, so a
// huge exponent such as `1e1000000` costs nothing.
func jsonInt(text string) (int64, error) {
	s := text
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	mantissa, exp := s, ""
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		mantissa, exp = s[:i], s[i+1:]
	}
	intPart, frac := mantissa, ""
	if i := strings.IndexByte(mantissa, '.'); i >= 0 {
		intPart, frac = mantissa[:i], mantissa[i+1:]
	}

	// e is the power of ten applied to digits. A JSON exponent can be any
	// length; clamping it keeps the arithmetic below in range, and every
	// clamped magnitude is far outside int64 (or far below 1) either way.
	e := int64(0)
	if exp != "" {
		n, err := strconv.ParseInt(exp, 10, 64)
		if err != nil {
			n = 1 << 40
			if strings.HasPrefix(exp, "-") {
				n = -n
			}
		}
		e = max(min(n, 1<<40), -(1 << 40))
	}
	digits := strings.TrimLeft(intPart+frac, "0")
	e -= int64(len(frac))
	if digits == "" {
		return 0, nil // every spelling of zero, e.g. `0.0` or `-0e5`
	}
	for e < 0 && strings.HasSuffix(digits, "0") {
		digits = digits[:len(digits)-1]
		e++
	}
	if e < 0 {
		return 0, fmt.Errorf("%s has a fractional part", text)
	}
	if int64(len(digits))+e > 19 {
		return 0, fmt.Errorf("%s is out of int range", text)
	}
	whole := digits + strings.Repeat("0", int(e))
	if neg {
		whole = "-" + whole
	}
	n, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s is out of int range", text)
	}
	return n, nil
}
