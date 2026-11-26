package adapter

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"fiig-lang/compiler/logging"
	"fiig-lang/compiler/runtime"
)

// ADR 0128: what a server tells its operator about a request, and what it
// tells the caller. A fault is the server's business: the client gets a bare
// 500 with the id of the request, and the operator gets the fault under that
// id in the log.

// RequestIDHeader carries a request's id in and out.
const RequestIDHeader = "X-Request-Id"

type observeKey int

const (
	requestIDKey observeKey = iota
	faultSinkKey
	recordKey
)

// TraceparentHeader is W3C Trace Context's header (ADR 0150 §5).
const TraceparentHeader = "traceparent"

// Observer wraps a handler so each request has an id, a fault a route hits is
// logged under it, and, when Access is set, a line is logged for every
// request: method, path (never the query, which can carry a token), status,
// duration and id. Log is where lines go (ADR 0150); without it they go to
// Logf as text, and without either nowhere.
type Observer struct {
	Logf   func(format string, args ...any)
	Log    *logging.Logger
	Access bool
}

// record is what a route tells the observer about the request it served.
type record struct {
	adapter string
	route   string
}

// routed marks ctx's request as served by route of adapter, for its access
// line.
func routed(ctx context.Context, adapter, route string) {
	if rec, ok := ctx.Value(recordKey).(*record); ok {
		rec.adapter, rec.route = adapter, route
	}
}

func (o Observer) logger() *logging.Logger {
	switch {
	case o.Log != nil:
		return o.Log
	case o.Logf != nil:
		return logging.FromLogf(o.Logf)
	}
	return logging.FromLogf(func(string, ...any) {})
}

// Wrap is next under the observer.
func (o Observer) Wrap(next http.Handler) http.Handler {
	log := o.logger()
	counting := CountsCalls(log)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := requestID(r)
		w.Header().Set(RequestIDHeader, id)
		ids := []logging.Field{logging.F("requestId", id)}
		if traceID, spanID, ok := traceparent(r.Header.Get(TraceparentHeader)); ok {
			ids = append(ids, logging.F("trace_id", traceID), logging.F("span_id", spanID))
		}
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		var faulted atomic.Bool
		ctx := context.WithValue(r.Context(), requestIDKey, id)
		ctx = context.WithValue(ctx, faultSinkKey, func(f *runtime.Fault) {
			faulted.Store(true)
			fields := append(append([]logging.Field(nil), ids...), logging.F("detail", f.Message))
			if len(f.Stack) > 0 {
				fields = append(fields, logging.F("declaration", f.Stack[len(f.Stack)-1]))
			}
			log.Log(logging.Error, logging.EventFault, fmt.Sprintf("fiig: request %s faulted: %s", id, f.String()), fields...)
		})
		rec := &record{}
		ctx = context.WithValue(ctx, recordKey, rec)
		var c *calls
		if counting {
			c = &calls{}
			if log.Enabled(logging.Debug) {
				c.debug = func(s runtime.Span) { writeCall(log, logging.Debug, "request "+id, s, ids) }
			}
			ctx = withCalls(ctx, c)
		}
		start := time.Now()
		next.ServeHTTP(sw, r.WithContext(ctx))
		elapsed := time.Since(start)
		failed := faulted.Load() || sw.status >= 500
		if !o.Access {
			return
		}
		if failed && c != nil {
			c.writeFailed(log, "request "+id, ids)
		}
		level := logging.Info
		if failed {
			level = logging.Error
		}
		fields := append(append([]logging.Field(nil), ids...),
			logging.F("method", r.Method), logging.F("path", r.URL.Path), logging.F("route", rec.route),
			logging.F("adapter", rec.adapter), logging.F("status", sw.status), logging.F("durationMs", elapsed.Milliseconds()))
		if c != nil {
			fields = append(fields, c.counts()...)
		}
		log.Log(level, logging.EventRequest, fmt.Sprintf("fiig: %s %s %d %s id=%s", r.Method, r.URL.Path, sw.status, elapsed.Round(time.Millisecond), id), fields...)
	})
}

// traceparent is a W3C traceparent header's trace id and parent id, the
// caller's span (ADR 0150 §5). An invalid one is ignored, as the spec
// requires: the wrong length or shape, a character that isn't lowercase
// hex, version ff, or an all-zero id. A version above 00 is read for its
// first four parts, as the spec says a reader of version 00 does.
func traceparent(h string) (traceID, spanID string, ok bool) {
	if len(h) < 55 || (len(h) > 55 && h[:2] == "00") || (len(h) > 55 && h[55] != '-') {
		return "", "", false
	}
	if h[2] != '-' || h[35] != '-' || h[52] != '-' {
		return "", "", false
	}
	version, traceID, spanID, flags := h[:2], h[3:35], h[36:52], h[53:55]
	for _, part := range []string{version, traceID, spanID, flags} {
		if !lowerHex(part) {
			return "", "", false
		}
	}
	if version == "ff" || allZero(traceID) || allZero(spanID) {
		return "", "", false
	}
	return traceID, spanID, true
}

func lowerHex(s string) bool {
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func allZero(s string) bool {
	for _, c := range s {
		if c != '0' {
			return false
		}
	}
	return true
}

// requestID is the caller's X-Request-Id when it is one short token, so a
// trace through a proxy keeps its id, and a new one otherwise. A value with
// anything else in it is never echoed into a log line or a header.
func requestID(r *http.Request) string {
	if id := r.Header.Get(RequestIDHeader); validRequestID(id) {
		return id
	}
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func validRequestID(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_', c == '.':
		default:
			return false
		}
	}
	return true
}

// writeFault answers a fault: the cause goes to the observer's log, if the
// request has one, and the caller gets a 500 that says nothing of it (ADR
// 0128 §1). A fault names declarations and can name data, such as a key that
// was already taken.
func writeFault(w http.ResponseWriter, r *http.Request, fault *runtime.Fault) {
	if sink, ok := r.Context().Value(faultSinkKey).(func(*runtime.Fault)); ok {
		sink(fault)
	}
	body := map[string]any{"error": "internal error"}
	if id, ok := r.Context().Value(requestIDKey).(string); ok {
		body["requestId"] = id
	}
	writeJSON(w, http.StatusInternalServerError, body)
}

// statusWriter records the status a handler wrote.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
