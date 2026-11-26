package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"fiig-lang/compiler/logging"
	"fiig-lang/compiler/parser"
	"fiig-lang/compiler/runtime"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// ADR 0150 §4: a request's gateway calls are counted on its access line,
// and a failed request writes the calls that failed.

var callsProgram = []string{
	`gateway fetchSeries(id: string) -> Result<string, string> requires Clock {
    if (id == "down") {
        return Err("connection refused");
    }
    return Ok(id);
}`,
	`workflow showSeries(id: string) -> Result<None, string> {
    a = fetchSeries(id)?;
    b = fetchSeries(a)?;
    return Ok(None);
}`,
	`conversion unavailable(reason: string) -> HttpResponse<None> {
    return HttpResponse(status: 503);
}`,
	`adapter http seriesApi {
    GET "/series/{id}" -> showSeries(id: string)
        produces "application/json"
    {
        Ok(_) -> 204,
        Err(reason) -> unavailable(reason)
    }
}`,
}

func callsHandler(t *testing.T, level logging.Level) (http.Handler, func() []map[string]any) {
	t.Helper()
	var files []*syntax.File
	for _, src := range callsProgram {
		f, errs := parser.Parse(src)
		if len(errs) > 0 {
			t.Fatalf("parse: %v", errs)
		}
		files = append(files, f)
	}
	u, conflicts := types.BuildUniverse(files)
	if len(conflicts) > 0 {
		t.Fatal(conflicts)
	}
	ad, _ := u.Decls["seriesApi"].(*syntax.AdapterDecl)
	in := runtime.New(u, runtime.NewCapabilityRegistry())
	var out bytes.Buffer
	log := logging.New(logging.JSON, level, "hearsay", "0.4.0").WithOutput(&out, func(string) {}, time.Now)
	if CountsCalls(log) {
		in.Tracer = CallTracer{}
	}
	h, err := HTTPHandler(ad, u, in)
	if err != nil {
		t.Fatal(err)
	}
	return Observer{Log: log, Access: true}.Wrap(h), func() []map[string]any {
		var lines []map[string]any
		for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
			if line == "" {
				continue
			}
			var v map[string]any
			if err := json.Unmarshal([]byte(line), &v); err != nil {
				t.Fatalf("not JSON: %s", line)
			}
			lines = append(lines, v)
		}
		return lines
	}
}

func TestASucceededRequestCountsItsCalls(t *testing.T) {
	h, lines := callsHandler(t, logging.Info)
	req := httptest.NewRequest("GET", "/series/42", nil)
	req.Header.Set("traceparent", "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01")
	if code, _, _ := serve(h, req); code != 204 {
		t.Fatalf("code = %d", code)
	}
	got := lines()
	if len(got) != 1 {
		t.Fatalf("lines = %v, want the access line alone", got)
	}
	l := got[0]
	for k, want := range map[string]any{"level": "info", "event": "request", "route": "/series/{id}", "adapter": "seriesApi",
		"status": 204.0, "calls": 2.0, "callErrs": 0.0, "trace_id": "0af7651916cd43dd8448eb211c80319c", "span_id": "b7ad6b7169203331"} {
		if l[k] != want {
			t.Errorf("%s = %v, want %v (line %v)", k, l[k], want, l)
		}
	}
}

func TestAFailedRequestWritesTheCallThatFailed(t *testing.T) {
	h, lines := callsHandler(t, logging.Info)
	if code, _, _ := serve(h, httptest.NewRequest("GET", "/series/down", nil)); code != 503 {
		t.Fatalf("code = %d", code)
	}
	got := lines()
	if len(got) != 2 {
		t.Fatalf("lines = %v, want a call line and the access line", got)
	}
	call, access := got[0], got[1]
	if call["event"] != "call" || call["level"] != "warn" || call["declaration"] != "fetchSeries" ||
		call["outcome"] != "err" || call["detail"] != "connection refused" || call["requestId"] != access["requestId"] {
		t.Errorf("call line = %v", call)
	}
	if access["level"] != "error" || access["status"] != 503.0 || access["calls"] != 1.0 || access["callErrs"] != 1.0 {
		t.Errorf("access line = %v", access)
	}
}

// ADR 0150 §3a: at error nothing is recorded, and only a failure is written.
func TestAtErrorNothingIsCounted(t *testing.T) {
	h, lines := callsHandler(t, logging.Error)
	serve(h, httptest.NewRequest("GET", "/series/42", nil))
	if got := lines(); len(got) != 0 {
		t.Errorf("a success at error wrote %v", got)
	}
	serve(h, httptest.NewRequest("GET", "/series/down", nil))
	got := lines()
	if len(got) != 1 || got[0]["status"] != 503.0 || got[0]["calls"] != nil {
		t.Errorf("a failure at error = %v, want its access line without counts", got)
	}
}

func TestDebugWritesEveryCall(t *testing.T) {
	h, lines := callsHandler(t, logging.Debug)
	serve(h, httptest.NewRequest("GET", "/series/42", nil))
	got := lines()
	if len(got) != 3 || got[0]["level"] != "debug" || got[0]["event"] != "call" || got[2]["event"] != "request" {
		t.Errorf("lines = %v, want two debug calls then the access line", got)
	}
}

func TestTraceparent(t *testing.T) {
	good := "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01"
	if tr, sp, ok := traceparent(good); !ok || tr != "0af7651916cd43dd8448eb211c80319c" || sp != "b7ad6b7169203331" {
		t.Errorf("traceparent(%q) = %q, %q, %v", good, tr, sp, ok)
	}
	if _, _, ok := traceparent("01-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01-what-comes-later"); !ok {
		t.Error("a later version's first four parts weren't read")
	}
	for _, bad := range []string{
		"",
		"00-0AF7651916CD43DD8448EB211C80319C-b7ad6b7169203331-01",
		"00-00000000000000000000000000000000-b7ad6b7169203331-01",
		"00-0af7651916cd43dd8448eb211c80319c-0000000000000000-01",
		"ff-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01",
		"00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01-extra",
		"00_0af7651916cd43dd8448eb211c80319c_b7ad6b7169203331_01",
	} {
		if _, _, ok := traceparent(bad); ok {
			t.Errorf("traceparent(%q) was read", bad)
		}
	}
}

// A stream element's or schedule firing's calls carry its id, and only
// gateway spans are counted.
func TestAnElementsCallsCarryItsId(t *testing.T) {
	var out bytes.Buffer
	log := logging.New(logging.JSON, logging.Info, "w", "1.0.0").WithOutput(&out, func(string) {}, time.Now)
	fields := []logging.Field{logging.F("route", "orders"), logging.F("elementId", "1763704800000-0")}
	ctx, c := elementCalls(context.Background(), log, "stream orders id 1763704800000-0", fields)
	start := time.Now()
	CallTracer{}.RecordSpan(ctx, runtime.Span{Name: "pay", Kind: runtime.SpanGateway, Start: start, End: start.Add(5 * time.Millisecond), Outcome: runtime.OutcomeErr, Detail: "Declined"})
	CallTracer{}.RecordSpan(ctx, runtime.Span{Name: "price", Kind: runtime.SpanRule, Outcome: runtime.OutcomeErr})
	CallTracer{}.RecordSpan(context.Background(), runtime.Span{Name: "elsewhere", Kind: runtime.SpanGateway, Outcome: runtime.OutcomeErr})
	c.writeFailed(log, "stream orders id 1763704800000-0", fields)
	var line map[string]any
	if err := json.Unmarshal(out.Bytes(), &line); err != nil {
		t.Fatalf("%v: %s", err, out.String())
	}
	if line["elementId"] != "1763704800000-0" || line["route"] != "orders" || line["declaration"] != "pay" || line["durationMs"] != 5.0 {
		t.Errorf("line = %v", line)
	}
	if n := strings.Count(out.String(), "\n"); n != 1 {
		t.Errorf("%d lines, want the gateway's alone: %s", n, out.String())
	}
}
