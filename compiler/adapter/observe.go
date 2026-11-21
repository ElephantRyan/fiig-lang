package adapter

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"time"

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
)

// Observer wraps a handler so each request has an id, a fault a route hits is
// logged under it, and, when Access is set, a line is logged for every
// request: method, path (never the query, which can carry a token), status,
// duration and id.
type Observer struct {
	Logf   func(format string, args ...any)
	Access bool
}

// Wrap is next under the observer.
func (o Observer) Wrap(next http.Handler) http.Handler {
	logf := o.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := requestID(r)
		w.Header().Set(RequestIDHeader, id)
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		ctx := context.WithValue(r.Context(), requestIDKey, id)
		ctx = context.WithValue(ctx, faultSinkKey, func(f *runtime.Fault) {
			logf("fiig: request %s faulted: %s", id, f.String())
		})
		start := time.Now()
		next.ServeHTTP(sw, r.WithContext(ctx))
		if o.Access {
			logf("fiig: %s %s %d %s id=%s", r.Method, r.URL.Path, sw.status, time.Since(start).Round(time.Millisecond), id)
		}
	})
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
