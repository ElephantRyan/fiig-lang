package adapter

import (
	"context"
	"fmt"
	"sync"
	"time"

	"fiig-lang/compiler/logging"
	"fiig-lang/compiler/runtime"
)

// ADR 0150 §4: the gateway calls a request, a stream element or a schedule
// firing made, counted on its access line, and written out when it failed.

// calls is one request's or element's gateway calls. Its methods are safe
// from a concurrent block's workers.
type calls struct {
	mu     sync.Mutex
	n      int
	errs   int
	spent  time.Duration
	failed []runtime.Span
	// debug, when set, writes each call as it ends (`-log-level debug`).
	debug func(runtime.Span)
}

type callsKey struct{}

func withCalls(ctx context.Context, c *calls) context.Context {
	return context.WithValue(ctx, callsKey{}, c)
}

func (c *calls) add(s runtime.Span) {
	c.mu.Lock()
	c.n++
	c.spent += s.Duration()
	if s.Outcome != runtime.OutcomeOk {
		c.errs++
		c.failed = append(c.failed, s)
	}
	debug := c.debug
	c.mu.Unlock()
	if debug != nil {
		debug(s)
	}
}

// counts are the access line's calls, callErrs and callMs.
func (c *calls) counts() []logging.Field {
	c.mu.Lock()
	defer c.mu.Unlock()
	return []logging.Field{logging.F("calls", c.n), logging.F("callErrs", c.errs), logging.F("callMs", c.spent.Milliseconds())}
}

// writeFailed writes a warn line for each call that returned an Err or
// faulted, with the fields of what it ran for (a request's ids, an
// element's id), in the order they ended.
func (c *calls) writeFailed(log *logging.Logger, what string, fields []logging.Field) {
	c.mu.Lock()
	failed := append([]runtime.Span(nil), c.failed...)
	c.mu.Unlock()
	for _, s := range failed {
		writeCall(log, logging.Warn, what, s, fields)
	}
}

// writeCall writes one gateway call.
func writeCall(log *logging.Logger, level logging.Level, what string, s runtime.Span, fields []logging.Field) {
	ms := s.Duration().Milliseconds()
	var text string
	switch s.Outcome {
	case runtime.OutcomeErr:
		text = fmt.Sprintf("fiig: %s: %s returned Err(%s) after %dms", what, s.Name, s.Detail, ms)
	case runtime.OutcomeFault:
		text = fmt.Sprintf("fiig: %s: %s faulted after %dms: %s", what, s.Name, ms, s.Detail)
	default:
		text = fmt.Sprintf("fiig: %s: %s returned after %dms", what, s.Name, ms)
	}
	all := append(append([]logging.Field(nil), fields...),
		logging.F("declaration", s.Name), logging.F("kind", string(s.Kind)),
		logging.F("outcome", string(s.Outcome)), logging.F("detail", s.Detail), logging.F("durationMs", ms))
	log.Log(level, logging.EventCall, text, all...)
}

// CallTracer is the Tracer fiig serve sets when its log level counts calls
// (ADR 0150 §3a): each gateway span is added to the calls of the request
// or element its context belongs to. Spans of other kinds, and spans
// outside a request or element, are dropped.
type CallTracer struct{}

// Wants is gateway spans alone, so nothing else pays for a span.
func (CallTracer) Wants(kind runtime.SpanKind) bool { return kind == runtime.SpanGateway }

func (CallTracer) RecordSpan(ctx context.Context, s runtime.Span) {
	if s.Kind != runtime.SpanGateway {
		return
	}
	if c, ok := ctx.Value(callsKey{}).(*calls); ok {
		c.add(s)
	}
}

// CountsCalls reports whether a logger at this level wants calls recorded:
// warn and below (ADR 0150 §3a). At error, nothing is recorded per call.
func CountsCalls(log *logging.Logger) bool { return log.Enabled(logging.Warn) }

// elementCalls starts the calls of a stream element or schedule firing,
// writing each one as it ends when the log is at debug.
func elementCalls(ctx context.Context, log *logging.Logger, what string, fields []logging.Field) (context.Context, *calls) {
	c := &calls{}
	if log.Enabled(logging.Debug) {
		c.debug = func(s runtime.Span) { writeCall(log, logging.Debug, what, s, fields) }
	}
	return withCalls(ctx, c), c
}

// pickLogger is a runner's log: Log when set, else Logf as text, else
// nowhere.
func pickLogger(log *logging.Logger, logf func(string, ...any)) *logging.Logger {
	return Observer{Log: log, Logf: logf}.logger()
}
