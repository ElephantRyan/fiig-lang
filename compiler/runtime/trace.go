package runtime

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"fiig-lang/compiler/logging"
)

// SpanKind classifies what a Span recorded — the five callable
// declaration kinds, plus "concurrent" for the block itself (concurrent.go),
// distinct from whatever its body calls (which gets its own nested span
// the ordinary way, parented under the concurrent span via context).
type SpanKind string

const (
	SpanRule       SpanKind = "rule"
	SpanConversion SpanKind = "conversion"
	SpanActivity   SpanKind = "activity"
	SpanGateway    SpanKind = "gateway"
	SpanWorkflow   SpanKind = "workflow"
	SpanHelper     SpanKind = "helper"  // a private helper (ADR 0054)
	SpanFormula    SpanKind = "formula" // ADR 0049
	SpanConcurrent SpanKind = "concurrent"
)

// SpanOutcome distinguishes a Fault (a genuine runtime defect) from a
// typed Result Err (an ordinary, successfully-produced value) from a
// plain Ok/non-Result success — the same three-way distinction the rest
// of this package already draws everywhere else.
type SpanOutcome string

const (
	OutcomeOk    SpanOutcome = "ok"
	OutcomeErr   SpanOutcome = "err"
	OutcomeFault SpanOutcome = "fault"
)

// Span is one completed call or concurrent block, structured enough to
// reconstruct the whole call tree: ID/ParentID link a span to whichever
// span was active in its caller's context when it started (0 = no
// parent — a top-level Call with nothing above it), the same way a
// distributed trace's span IDs work, scoped to one interpreter run
// instead of across process boundaries.
type Span struct {
	ID       int64
	ParentID int64
	Name     string
	Kind     SpanKind
	Start    time.Time
	End      time.Time
	Outcome  SpanOutcome
	Detail   string // a Fault's message, or an Err value's String() — "" for Ok
}

func (s Span) Duration() time.Duration { return s.End.Sub(s.Start) }

// Tracer receives one Span per completed Call/concurrent-block. Recording
// happens after the fact (a span is only ever reported once it's
// finished) rather than a separate start/end pair, since nothing in this
// package needs to observe an in-flight span.
//
// ctx is the context the span ran under, so a tracer can find what it
// belongs to, such as the one request it ran for (ADR 0150 §4).
type Tracer interface {
	RecordSpan(ctx context.Context, s Span)
}

// KindTracer is a Tracer that wants only some kinds of span, such as fiig
// serve's, which counts gateway calls (ADR 0150 §4). A call of a kind it
// doesn't want is run without a span, so it costs what it does untraced.
type KindTracer interface {
	Tracer
	Wants(kind SpanKind) bool
}

// tracing reports whether a call of kind gets a span.
func (in *Interp) tracing(kind SpanKind) bool {
	if in.Tracer == nil {
		return false
	}
	if k, ok := in.Tracer.(KindTracer); ok {
		return k.Wants(kind)
	}
	return true
}

// Recorder is an in-memory Tracer — the obvious one for tests, and simple
// enough to be a reasonable host-side default for anything that just wants
// to inspect a run's call tree afterward rather than stream it anywhere.
type Recorder struct {
	mu    sync.Mutex
	spans []Span
}

func (r *Recorder) RecordSpan(_ context.Context, s Span) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.spans = append(r.spans, s)
}

// Spans returns every span recorded so far, in completion order (not
// necessarily start order — a child reliably completes before its
// parent, but siblings under a `concurrent` block complete in whatever
// order their workers actually finish).
func (r *Recorder) Spans() []Span {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Span(nil), r.spans...)
}

type activeSpanKey struct{}

// startSpan assigns a new span ID, reads whichever span was active in ctx
// (0 if none) as this one's parent, and returns a context with the new
// span now active — so anything called with the returned ctx (including
// across a `concurrent` block's worker goroutines, which just copy
// context values, no extra plumbing needed) is automatically parented
// under it.
func (in *Interp) startSpan(ctx context.Context, name string, kind SpanKind) (context.Context, Span) {
	parent, _ := ctx.Value(activeSpanKey{}).(int64)
	id := atomic.AddInt64(&in.spanCounter, 1)
	span := Span{ID: id, ParentID: parent, Name: name, Kind: kind, Start: time.Now()}
	return context.WithValue(ctx, activeSpanKey{}, id), span
}

// finishSpan fills in span's outcome from how the call actually ended and
// reports it to in.Tracer. val is only consulted when fault is nil — a
// Fault always means OutcomeFault regardless of what (if anything) val
// holds.
func (in *Interp) finishSpan(ctx context.Context, span Span, val Value, fault *Fault) {
	span.End = time.Now()
	switch {
	case fault != nil:
		span.Outcome = OutcomeFault
		span.Detail = fault.Message
	default:
		if rv, ok := val.(Result); ok && !rv.Ok {
			span.Outcome = OutcomeErr
			span.Detail = rv.Value.String()
		} else {
			span.Outcome = OutcomeOk
		}
	}
	in.Tracer.RecordSpan(ctx, span)
}

// Log is where the runtime writes its own notes, such as a TLS reload that
// kept the last good files (ADR 0150 §1). nil writes text through the
// standard log; fiig serve sets the logger its flags chose.
var Log *logging.Logger
