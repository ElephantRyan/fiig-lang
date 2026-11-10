package runtime

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// ADR 0073: the one JSON reader json.decode and an HTTP request body share.

// JsonSyntaxError is input that isn't JSON (ADR 0073 §5's Syntax): Offset
// is the byte offset of the problem, and Reason what was expected.
type JsonSyntaxError struct {
	Offset int
	Reason string
}

func (e *JsonSyntaxError) Error() string {
	return fmt.Sprintf("at byte %d: %s", e.Offset, e.Reason)
}

// JsonMismatchError is JSON that doesn't fit the type it is read as (ADR
// 0073 §5's Mismatch): Path is where, written `data.items[2].amount`, and
// "" for the whole value.
type JsonMismatchError struct {
	Path   string
	Reason string
}

func (e *JsonMismatchError) Error() string {
	if e.Path == "" {
		return e.Reason
	}
	return e.Path + ": " + e.Reason
}

// jsonObject is a parsed JSON object whose members keep the order they
// arrived in (ADR 0062 §1), which a Go map doesn't.
type jsonObject struct {
	names   []string
	members map[string]any
}

func (o *jsonObject) get(name string) (any, bool) {
	v, ok := o.members[name]
	return v, ok
}

// maxJSONDepth bounds nesting, so a body of a million `[` can't exhaust
// memory; encoding/json uses the same limit.
const maxJSONDepth = 10000

// readJSON parses data as one JSON value (RFC 8259) into nil, bool,
// json.Number (its source text, ADR 0062 §1), string, []any, or
// *jsonObject. Invalid UTF-8 (RFC 8259 §8.1), a duplicate member name (ADR
// 0062 §1), and anything after the value but whitespace are errors, each a
// *JsonSyntaxError.
func readJSON(data []byte) (any, error) {
	if !utf8.Valid(data) {
		at := 0
		for at < len(data) {
			r, size := utf8.DecodeRune(data[at:])
			if r == utf8.RuneError && size <= 1 {
				break
			}
			at += size
		}
		return nil, &JsonSyntaxError{Offset: at, Reason: "invalid UTF-8"}
	}
	r := &jsonReader{data: data}
	r.space()
	v, err := r.value(0)
	if err != nil {
		return nil, err
	}
	r.space()
	if r.at < len(data) {
		return nil, r.errorf("unexpected %s after the JSON value", r.describe())
	}
	return v, nil
}

type jsonReader struct {
	data []byte
	at   int
}

func (r *jsonReader) errorf(format string, args ...any) error {
	return &JsonSyntaxError{Offset: r.at, Reason: fmt.Sprintf(format, args...)}
}

// describe names the byte at the read position for an error message.
func (r *jsonReader) describe() string {
	if r.at >= len(r.data) {
		return "end of input"
	}
	c, _ := utf8.DecodeRune(r.data[r.at:])
	return strconv.QuoteRune(c)
}

func (r *jsonReader) space() {
	for r.at < len(r.data) {
		switch r.data[r.at] {
		case ' ', '\t', '\n', '\r':
			r.at++
		default:
			return
		}
	}
}

func (r *jsonReader) value(depth int) (any, error) {
	if depth > maxJSONDepth {
		return nil, r.errorf("nested more than %d deep", maxJSONDepth)
	}
	if r.at >= len(r.data) {
		return nil, r.errorf("expected a value, got end of input")
	}
	switch c := r.data[r.at]; {
	case c == '{':
		return r.object(depth)
	case c == '[':
		return r.array(depth)
	case c == '"':
		return r.string()
	case c == '-' || c >= '0' && c <= '9':
		n := jsonNumberLength(r.data[r.at:])
		if n == 0 {
			return nil, r.errorf("invalid number")
		}
		text := string(r.data[r.at : r.at+n])
		r.at += n
		return json.Number(text), nil
	}
	for _, lit := range []struct {
		text  string
		value any
	}{{"true", true}, {"false", false}, {"null", nil}} {
		if strings.HasPrefix(string(r.data[r.at:min(len(r.data), r.at+len(lit.text))]), lit.text) {
			r.at += len(lit.text)
			return lit.value, nil
		}
	}
	return nil, r.errorf("expected a value, got %s", r.describe())
}

func (r *jsonReader) object(depth int) (any, error) {
	r.at++ // '{'
	obj := &jsonObject{members: map[string]any{}}
	r.space()
	if r.at < len(r.data) && r.data[r.at] == '}' {
		r.at++
		return obj, nil
	}
	for {
		r.space()
		if r.at >= len(r.data) || r.data[r.at] != '"' {
			return nil, r.errorf("expected a member name, got %s", r.describe())
		}
		nameAt := r.at
		name, err := r.string()
		if err != nil {
			return nil, err
		}
		if _, dup := obj.members[name]; dup {
			return nil, &JsonSyntaxError{Offset: nameAt, Reason: fmt.Sprintf("duplicate name %q", name)}
		}
		r.space()
		if r.at >= len(r.data) || r.data[r.at] != ':' {
			return nil, r.errorf("expected ':', got %s", r.describe())
		}
		r.at++
		r.space()
		v, err := r.value(depth + 1)
		if err != nil {
			return nil, err
		}
		obj.names = append(obj.names, name)
		obj.members[name] = v
		r.space()
		if r.at < len(r.data) && r.data[r.at] == ',' {
			r.at++
			continue
		}
		if r.at < len(r.data) && r.data[r.at] == '}' {
			r.at++
			return obj, nil
		}
		return nil, r.errorf("expected ',' or '}', got %s", r.describe())
	}
}

func (r *jsonReader) array(depth int) (any, error) {
	r.at++ // '['
	arr := []any{}
	r.space()
	if r.at < len(r.data) && r.data[r.at] == ']' {
		r.at++
		return arr, nil
	}
	for {
		r.space()
		v, err := r.value(depth + 1)
		if err != nil {
			return nil, err
		}
		arr = append(arr, v)
		r.space()
		if r.at < len(r.data) && r.data[r.at] == ',' {
			r.at++
			continue
		}
		if r.at < len(r.data) && r.data[r.at] == ']' {
			r.at++
			return arr, nil
		}
		return nil, r.errorf("expected ',' or ']', got %s", r.describe())
	}
}

// string reads a string at '"'. A `\u` escape of a lone surrogate becomes
// U+FFFD, as encoding/json and most decoders do, since a fiig string holds
// UTF-8.
func (r *jsonReader) string() (string, error) {
	r.at++ // '"'
	var b strings.Builder
	for {
		if r.at >= len(r.data) {
			return "", r.errorf("unterminated string")
		}
		c := r.data[r.at]
		switch {
		case c == '"':
			r.at++
			return b.String(), nil
		case c < 0x20:
			return "", r.errorf("control character %s in a string; write it escaped", strconv.QuoteRune(rune(c)))
		case c != '\\':
			b.WriteByte(c)
			r.at++
			continue
		}
		escAt := r.at
		r.at++
		if r.at >= len(r.data) {
			return "", r.errorf("unterminated string")
		}
		switch e := r.data[r.at]; e {
		case '"', '\\', '/':
			b.WriteByte(e)
		case 'b':
			b.WriteByte('\b')
		case 'f':
			b.WriteByte('\f')
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		case 'u':
			c1, ok := r.hex4(r.at + 1)
			if !ok {
				return "", &JsonSyntaxError{Offset: escAt, Reason: `invalid \u escape`}
			}
			r.at += 4
			if utf16.IsSurrogate(c1) {
				if c2, ok := r.hex4(r.at + 3); ok && r.data[r.at+1] == '\\' && r.data[r.at+2] == 'u' {
					if pair := utf16.DecodeRune(c1, c2); pair != utf8.RuneError {
						b.WriteRune(pair)
						r.at += 6
						break
					}
				}
				c1 = utf8.RuneError
			}
			b.WriteRune(c1)
		default:
			return "", &JsonSyntaxError{Offset: escAt, Reason: fmt.Sprintf("invalid escape \\%c", e)}
		}
		r.at++
	}
}

// hex4 reads four hex digits at i.
func (r *jsonReader) hex4(i int) (rune, bool) {
	if i+4 > len(r.data) {
		return 0, false
	}
	n, err := strconv.ParseUint(string(r.data[i:i+4]), 16, 32)
	if err != nil {
		return 0, false
	}
	return rune(n), true
}

// jsonNumberLength is the length of the RFC 8259 number at the start of b,
// or 0 when none is: `-? (0 | [1-9][0-9]*) (. [0-9]+)? ([eE] [+-]? [0-9]+)?`.
func jsonNumberLength(b []byte) int {
	i := 0
	digits := func() int {
		n := 0
		for i < len(b) && b[i] >= '0' && b[i] <= '9' {
			i++
			n++
		}
		return n
	}
	if i < len(b) && b[i] == '-' {
		i++
	}
	switch {
	case i < len(b) && b[i] == '0':
		i++
	case digits() == 0:
		return 0
	}
	if i < len(b) && b[i] == '.' {
		i++
		if digits() == 0 {
			return 0
		}
	}
	if i < len(b) && (b[i] == 'e' || b[i] == 'E') {
		i++
		if i < len(b) && (b[i] == '+' || b[i] == '-') {
			i++
		}
		if digits() == 0 {
			return 0
		}
	}
	return i
}

// IsJSONNumber reports whether text is exactly one RFC 8259 number, the
// check the encoder makes of a JsonValue.Number built by hand (ADR 0062 §5).
func IsJSONNumber(text string) bool {
	return text != "" && jsonNumberLength([]byte(text)) == len(text)
}

// plainJSON turns readJSON's ordered objects into map[string]any, the
// shape ParseJSON's callers in tooling read.
func plainJSON(v any) any {
	switch v := v.(type) {
	case *jsonObject:
		m := make(map[string]any, len(v.names))
		for _, n := range v.names {
			m[n] = plainJSON(v.members[n])
		}
		return m
	case []any:
		out := make([]any, len(v))
		for i, e := range v {
			out[i] = plainJSON(e)
		}
		return out
	}
	return v
}
