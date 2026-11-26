package logging

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

var at = time.Date(2026, 11, 25, 9, 14, 3, 512_000_000, time.FixedZone("x", 3600))

func jsonLogger(level Level) (*Logger, *bytes.Buffer, *[]string) {
	var out bytes.Buffer
	var text []string
	l := New(JSON, level, "hearsay", "0.4.0").WithOutput(&out, func(s string) { text = append(text, s) }, func() time.Time { return at })
	return l, &out, &text
}

// ADR 0150 §2: the fields every line has, in order, and the event's own,
// with an empty one left out.
func TestAJSONLine(t *testing.T) {
	l, out, _ := jsonLogger(Info)
	l.Log(Error, EventRequest, "fiig: GET /x 503 3ms id=ab", F("requestId", "ab"), F("status", 503), F("route", ""))
	want := `{"timestamp":"2026-11-25T08:14:03.512Z","level":"error","msg":"GET /x 503 3ms id=ab","event":"request","service":"hearsay","version":"0.4.0","requestId":"ab","status":503}` + "\n"
	if out.String() != want {
		t.Errorf("line =\n%s\nwant\n%s", out.String(), want)
	}
	var v map[string]any
	if err := json.Unmarshal(out.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
}

// ADR 0150 §3: lines below the level are dropped, but never a fault.
func TestTheLevelDropsLinesButNotFaults(t *testing.T) {
	l, out, _ := jsonLogger(Error)
	l.Log(Info, EventRequest, "fiig: GET /x 200 1ms id=a")
	l.Log(Warn, EventCall, "fiig: request a: g returned Err(x) after 1ms")
	l.Log(Error, EventFault, "fiig: request a faulted: boom")
	if got := strings.Count(out.String(), "\n"); got != 1 || !strings.Contains(out.String(), `"event":"fault"`) {
		t.Errorf("at error = %q, want only the fault", out.String())
	}
}

// The text format writes the line as it always has.
func TestTextIsUnchanged(t *testing.T) {
	var text []string
	l := New(Text, Info, "hearsay", "0.4.0").WithOutput(nil, func(s string) { text = append(text, s) }, time.Now)
	l.Log(Info, EventRequest, "fiig: GET /x 200 1ms id=a", F("status", 200))
	l.Log(Debug, EventCall, "dropped")
	if len(text) != 1 || text[0] != "fiig: GET /x 200 1ms id=a" {
		t.Errorf("text = %q", text)
	}
}

func TestParsing(t *testing.T) {
	for _, s := range []string{"debug", "info", "warn", "error"} {
		if l, err := ParseLevel(s); err != nil || l.String() != s {
			t.Errorf("ParseLevel(%q) = %v, %v", s, l, err)
		}
	}
	if _, err := ParseLevel("verbose"); err == nil {
		t.Error("ParseLevel(verbose) = nil error")
	}
	if f, err := ParseFormat("json"); err != nil || f != JSON {
		t.Errorf("ParseFormat(json) = %v, %v", f, err)
	}
	if _, err := ParseFormat("logfmt"); err == nil {
		t.Error("ParseFormat(logfmt) = nil error")
	}
}
