// Package logging is how fiig serve writes its log (ADR 0150): the text
// lines it has always written, on stderr, or the same lines as one JSON
// object each, on stdout, for a log platform's agent to read.
package logging

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync"
	"time"
)

// Level is a line's level (ADR 0150 §3). The runtime chooses it per event;
// a program never does.
type Level int

const (
	Debug Level = iota
	Info
	Warn
	Error
)

func (l Level) String() string {
	switch l {
	case Debug:
		return "debug"
	case Info:
		return "info"
	case Warn:
		return "warn"
	}
	return "error"
}

// ParseLevel reads FIIG_LOG_LEVEL's or -log-level's value.
func ParseLevel(s string) (Level, error) {
	for _, l := range []Level{Debug, Info, Warn, Error} {
		if s == l.String() {
			return l, nil
		}
	}
	return Info, fmt.Errorf("%q isn't a log level; use debug, info, warn or error (ADR 0150 §3a)", s)
}

// Format is how a line is written.
type Format int

const (
	Text Format = iota
	JSON
)

// ParseFormat reads FIIG_LOG_FORMAT's or -log-format's value.
func ParseFormat(s string) (Format, error) {
	switch s {
	case "text":
		return Text, nil
	case "json":
		return JSON, nil
	}
	return Text, fmt.Errorf("%q isn't a log format; use text or json (ADR 0150 §3a)", s)
}

// Event names what a line is about (ADR 0150 §2). They are part of what a
// dashboard or an alert is built on, so one is never renamed.
const (
	EventRequest   = "request"
	EventFault     = "fault"
	EventCall      = "call"
	EventStartup   = "startup"
	EventSchedule  = "schedule"
	EventStream    = "stream"
	EventLifecycle = "lifecycle"
	EventRuntime   = "runtime"
)

// Field is one of an event's own fields. A nil Value, or an empty string,
// leaves the field out.
type Field struct {
	Key   string
	Value any
}

// F is a Field.
func F(key string, value any) Field { return Field{key, value} }

// Logger writes lines at its level and above. A fault is written at any
// level. The zero Logger is not usable; nil is, and writes text through
// the standard log at every level.
type Logger struct {
	format  Format
	level   Level
	service string
	version string
	now     func() time.Time
	text    func(string)

	mu sync.Mutex
	w  io.Writer
}

// New is a logger for service at version (the root manifest's path and
// version), at level, in format: text goes to the standard log, JSON to
// stdout.
func New(format Format, level Level, service, version string) *Logger {
	return &Logger{format: format, level: level, service: service, version: version, now: time.Now, w: os.Stdout, text: func(s string) { log.Print(s) }}
}

// FromLogf is a text logger at every level whose lines go to logf, the shape
// the adapter's tests and older callers pass.
func FromLogf(logf func(string, ...any)) *Logger {
	return &Logger{format: Text, level: Debug, now: time.Now, text: func(s string) { logf("%s", s) }}
}

// WithOutput is l writing JSON to w and text to text, for tests.
func (l *Logger) WithOutput(w io.Writer, text func(string), now func() time.Time) *Logger {
	c := &Logger{format: l.format, level: l.level, service: l.service, version: l.version, now: now, text: text, w: w}
	return c
}

// Level is the logger's level; Debug for nil.
func (l *Logger) Level() Level {
	if l == nil {
		return Debug
	}
	return l.level
}

// Enabled reports whether a line at level would be written.
func (l *Logger) Enabled(level Level) bool { return level >= l.Level() }

// Log writes one line, when its level is enabled or it is a fault. text is
// the line as the text format writes it, which JSON's msg is without the
// "fiig: " prefix.
func (l *Logger) Log(level Level, event, text string, fields ...Field) {
	if l == nil {
		log.Print(text)
		return
	}
	if level < l.level && event != EventFault {
		return
	}
	if l.format == Text {
		l.text(text)
		return
	}
	var b bytes.Buffer
	b.WriteByte('{')
	put := func(k string, v any) {
		switch v := v.(type) {
		case nil:
			return
		case string:
			if v == "" {
				return
			}
		}
		if b.Len() > 1 {
			b.WriteByte(',')
		}
		key, _ := json.Marshal(k)
		b.Write(key)
		b.WriteByte(':')
		val, err := json.Marshal(v)
		if err != nil {
			val, _ = json.Marshal(fmt.Sprint(v))
		}
		b.Write(val)
	}
	put("timestamp", l.now().UTC().Format("2006-01-02T15:04:05.000Z"))
	put("level", level.String())
	put("msg", strings.TrimPrefix(text, "fiig: "))
	put("event", event)
	put("service", l.service)
	put("version", l.version)
	for _, f := range fields {
		put(f.Key, f.Value)
	}
	b.WriteString("}\n")
	l.mu.Lock()
	defer l.mu.Unlock()
	l.w.Write(b.Bytes())
}

// Logf is Log with the text formatted.
func (l *Logger) Logf(level Level, event string, fields []Field, format string, args ...any) {
	if l != nil && level < l.level && event != EventFault {
		return
	}
	l.Log(level, event, fmt.Sprintf(format, args...), fields...)
}
