package runtime

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// ParseJSON reads one JSON value into the same Go shapes encoding/json
// produces for an `any` (nil/bool/string/[]any/map[string]any), except that
// every number is a json.Number holding its source text (ADR 0062 §1, §6):
// converting it is DecodeJSON's job, by the reader's declared type. It reads
// objects token by token so a duplicate member name is an error rather than
// Go's silent last-wins (ADR 0062 §1 and §6), and it rejects trailing data
// after the value.
func ParseJSON(data []byte) (any, error) {
	return ParseJSONFrom(bytes.NewReader(data))
}

// ParseJSONFrom is ParseJSON over a stream, for an HTTP request body.
func ParseJSONFrom(r io.Reader) (any, error) {
	dec := json.NewDecoder(r)
	dec.UseNumber()
	v, err := parseValue(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		if err == nil {
			return nil, errors.New("unexpected data after the JSON value")
		}
		return nil, err
	}
	return v, nil
}

func parseValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		if err == io.EOF {
			return nil, io.ErrUnexpectedEOF
		}
		return nil, err
	}
	switch tok := tok.(type) {
	case json.Delim:
		switch tok {
		case '{':
			obj := map[string]any{}
			for dec.More() {
				keyTok, err := dec.Token()
				if err != nil {
					return nil, err
				}
				key := keyTok.(string) // the decoder only yields a string in key position
				if _, dup := obj[key]; dup {
					return nil, fmt.Errorf("duplicate member name %q", key)
				}
				v, err := parseValue(dec)
				if err != nil {
					return nil, err
				}
				obj[key] = v
			}
			if _, err := dec.Token(); err != nil { // '}'
				return nil, err
			}
			return obj, nil
		case '[':
			arr := []any{}
			for dec.More() {
				v, err := parseValue(dec)
				if err != nil {
					return nil, err
				}
				arr = append(arr, v)
			}
			if _, err := dec.Token(); err != nil { // ']'
				return nil, err
			}
			return arr, nil
		}
		return nil, fmt.Errorf("unexpected %q", tok)
	default:
		return tok, nil // nil, bool, string, or json.Number
	}
}

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
