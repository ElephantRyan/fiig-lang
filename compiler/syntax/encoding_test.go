package syntax

import "testing"

func TestIntEncodingRules(t *testing.T) {
	legal := []string{"u8", "i8", "u16be", "i16le", "u24be", "i32be", "u48le", "i64be", "i64le", "u1", "u4", "u31", "u63"}
	for _, name := range legal {
		if msg := IntEncodingError(name); msg != "" {
			t.Errorf("%s: unexpected error %q", name, msg)
		}
	}
	illegal := []string{"u32", "i16", "u8be", "u64be", "u64", "i4", "u4be", "u65", "x8", "u08", "word", "u"}
	for _, name := range illegal {
		if IntEncodingError(name) == "" {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestIntValue(t *testing.T) {
	for text, want := range map[string]int64{"0": 0, "010": 10, "0x52": 82, "0XfF": 255, "0x7FFFFFFFFFFFFFFF": 1<<63 - 1} {
		if got, err := IntValue(text); err != nil || got != want {
			t.Errorf("IntValue(%q) = %d, %v; want %d", text, got, err, want)
		}
	}
	if _, err := IntValue("0x8000000000000000"); err == nil {
		t.Error("a hex literal above int's range must be an error")
	}
}
