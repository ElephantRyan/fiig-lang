package runtime

import (
	"encoding/json"
	"strings"
	"testing"

	"fiig-lang/compiler/types"
)

// TestJSONIntValueRule is ADR 0062 §2's table: a number converts to int when
// its exact value is a whole number within int64, however the text writes it.
func TestJSONIntValueRule(t *testing.T) {
	ok := map[string]int64{
		"3": 3, "-7": -7, "0": 0, "-0": 0, "0.0": 0, "0e999999999999": 0,
		"3.0": 3, "3.00": 3, "3e0": 3, "1e3": 1000, "1.5e1": 15, "12300e-2": 123,
		"9223372036854775807": 9223372036854775807, "-9223372036854775808": -9223372036854775808,
		"9007199254740993": 9007199254740993, "9.223372036854775807e18": 9223372036854775807,
	}
	for text, want := range ok {
		got, err := jsonInt(text)
		if err != nil || got != want {
			t.Errorf("jsonInt(%s) = %d, %v; want %d", text, got, err, want)
		}
	}
	bad := map[string]string{
		"2.5": "fractional", "12345e-2": "fractional", "1e-400": "fractional", "1.5": "fractional",
		"1e19": "out of int range", "9223372036854775808": "out of int range",
		"-9223372036854775809": "out of int range", "1e400": "out of int range",
		"1e1000000000000000000000": "out of int range",
	}
	for text, want := range bad {
		_, err := jsonInt(text)
		if err == nil || !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), text) {
			t.Errorf("jsonInt(%s) error = %v; want one naming the text and %q", text, err, want)
		}
	}
}

// TestDecodeJSONIntNoLongerTruncates is ADR 0062's Context table, which
// float64 decoding got wrong before §6.
func TestDecodeJSONIntNoLongerTruncates(t *testing.T) {
	intT := types.Primitive{Name: "int"}
	for text, want := range map[string]int64{"9007199254740993": 9007199254740993, "9223372036854775807": 9223372036854775807, "1e3": 1000} {
		v, err := DecodeJSON(decodeRawJSON(t, text), intT, nil)
		if err != nil || v != Int(want) {
			t.Errorf("%s decoded to %v, %v; want %d", text, v, err, want)
		}
	}
	if v, err := DecodeJSON(decodeRawJSON(t, "1.5"), intT, nil); err == nil {
		t.Errorf("1.5 decoded to %v; want an error, not a truncation", v)
	}
}

func TestParseJSONRejectsDuplicateNames(t *testing.T) {
	for _, src := range []string{`{"a": 1, "a": 2}`, `{"x": {"a": 1, "a": 1}}`, `[{"k": true, "k": false}]`} {
		if _, err := ParseJSON([]byte(src)); err == nil || !strings.Contains(err.Error(), "duplicate member name") {
			t.Errorf("ParseJSON(%s) error = %v; want a duplicate-name error", src, err)
		}
	}
}

func TestParseJSONShapes(t *testing.T) {
	v, err := ParseJSON([]byte(`{"n": 1.50, "s": "x", "b": false, "z": null, "l": [], "o": {}}`))
	if err != nil {
		t.Fatal(err)
	}
	obj := v.(map[string]any)
	if obj["n"] != json.Number("1.50") {
		t.Errorf("n = %#v, want the source text 1.50", obj["n"])
	}
	if obj["s"] != "x" || obj["b"] != false || obj["z"] != nil {
		t.Errorf("got %#v", obj)
	}
	if l, ok := obj["l"].([]any); !ok || len(l) != 0 {
		t.Errorf("l = %#v, want an empty array", obj["l"])
	}
	for _, src := range []string{`{"a": 1} {"b": 2}`, `1 2`, `{"a": 1`, ``, `[1,]`} {
		if _, err := ParseJSON([]byte(src)); err == nil {
			t.Errorf("ParseJSON(%q) succeeded; want an error", src)
		}
	}
}

func TestDecodeUntypedJSONRejectsFractions(t *testing.T) {
	_, err := DecodeUntypedJSON(decodeRawJSON(t, `{"total": [1, 1.5]}`))
	if err == nil || !strings.Contains(err.Error(), "1.5 has a fractional part") {
		t.Fatalf("error = %v; want 1.5 reported, not truncated to 1", err)
	}
	v, err := DecodeUntypedJSON(decodeRawJSON(t, `3.0`))
	if err != nil || v != Int(3) {
		t.Fatalf("3.0 = %v, %v; want 3 by the value rule", v, err)
	}
}
