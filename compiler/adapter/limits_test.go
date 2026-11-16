package adapter

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"fiig-lang/compiler/parser"
	"fiig-lang/compiler/syntax"
)

// ADR 0076: the limits on what a client can make the adapter hold.

var limitsProgram = []string{
	`workflow echo(text: string) -> string {
    return text
}`,
	`workflow spin() -> int {
    n: int = 0
    while n >= 0 {
        n = 0
    }
    return n
}`,
	`workflow hello() -> string {
    return "hello"
}`,
	`adapter http limitsApi {
    POST "/echo" -> echo(text: string)
        consumes "text/plain"
        produces "text/plain"
    GET "/spin" -> spin()
        produces "application/json"
    GET "/hello" -> hello()
        produces "text/plain"
}`,
}

func post(body string, chunked bool) *http.Request {
	req := httptest.NewRequest("POST", "/echo", strings.NewReader(body))
	req.Header.Set("Content-Type", "text/plain")
	if chunked {
		req.ContentLength = -1
	}
	return req
}

func TestBodyOverTheLimitIs413(t *testing.T) {
	h := handlerWithLimits(t, Limits{MaxBodyBytes: 8, MaxRequests: 4, RequestTimeout: time.Second}, limitsProgram...)
	if code, _, body := serve(h, post("12345678", false)); code != 200 || body != "12345678" {
		t.Errorf("at the limit: got %d %q, want 200", code, body)
	}
	// §1: a declared length over the limit is refused before reading, and
	// a body without one at the byte that passes it.
	for _, chunked := range []bool{false, true} {
		code, _, body := serve(h, post("123456789", chunked))
		if code != 413 || !strings.Contains(body, "limit of 8 bytes (MAX_BODY_BYTES)") {
			t.Errorf("chunked=%v: got %d %s, want 413 naming MAX_BODY_BYTES", chunked, code, body)
		}
	}
}

func TestARequestPastItsDeadlineIs503(t *testing.T) {
	h := handlerWithLimits(t, Limits{MaxBodyBytes: 8, MaxRequests: 4, RequestTimeout: 100 * time.Millisecond}, limitsProgram...)
	start := time.Now()
	code, _, body := serve(h, httptest.NewRequest("GET", "/spin", nil))
	if code != 503 || !strings.Contains(body, "limit of 100ms (REQUEST_TIMEOUT)") {
		t.Errorf("got %d %s, want 503 naming REQUEST_TIMEOUT", code, body)
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("took %s to stop a 100ms request", took)
	}
}

// §2: MaxRequests run, as many more wait, and the rest are refused at
// once; a waiting request runs when a slot frees.
func TestRequestsWaitForASlotThenAreRefused(t *testing.T) {
	h := handlerWithLimits(t, Limits{MaxBodyBytes: 8, MaxRequests: 1, RequestTimeout: 500 * time.Millisecond}, limitsProgram...)
	var wg sync.WaitGroup
	wg.Add(2)
	var spinCode, waitCode int
	go func() { defer wg.Done(); spinCode, _, _ = serve(h, httptest.NewRequest("GET", "/spin", nil)) }()
	time.Sleep(100 * time.Millisecond) // spin holds the slot
	go func() { defer wg.Done(); waitCode, _, _ = serve(h, httptest.NewRequest("GET", "/hello", nil)) }()
	time.Sleep(100 * time.Millisecond) // hello is waiting

	start := time.Now()
	code, header, body := serve(h, httptest.NewRequest("GET", "/hello", nil))
	if code != 503 || header.Get("Retry-After") != "1" || !strings.Contains(body, "MAX_REQUESTS") {
		t.Errorf("third request: got %d Retry-After=%q %s, want an immediate 503", code, header.Get("Retry-After"), body)
	}
	if took := time.Since(start); took > 200*time.Millisecond {
		t.Errorf("third request waited %s; with the wait full it should be refused at once", took)
	}
	wg.Wait()
	if spinCode != 503 || waitCode != 200 {
		t.Errorf("spin %d (want 503, its deadline), waiting hello %d (want 200 once the slot freed)", spinCode, waitCode)
	}
}

func TestAWaitThatRunsOutIs503(t *testing.T) {
	defer func(d time.Duration) { slotWait = d }(slotWait)
	slotWait = 50 * time.Millisecond
	h := handlerWithLimits(t, Limits{MaxBodyBytes: 8, MaxRequests: 1, RequestTimeout: 500 * time.Millisecond}, limitsProgram...)
	done := make(chan struct{})
	go func() { serve(h, httptest.NewRequest("GET", "/spin", nil)); close(done) }()
	time.Sleep(100 * time.Millisecond)
	if code, _, _ := serve(h, httptest.NewRequest("GET", "/hello", nil)); code != 503 {
		t.Errorf("got %d, want 503 after the wait", code)
	}
	<-done
	if code, _, _ := serve(h, httptest.NewRequest("GET", "/hello", nil)); code != 200 {
		t.Errorf("after the slot freed: got %d, want 200", code)
	}
}

// A preflight runs no workflow, so it takes no slot.
func TestAPreflightTakesNoSlot(t *testing.T) {
	inner := handlerWithLimits(t, Limits{MaxBodyBytes: 8, MaxRequests: 1, RequestTimeout: 500 * time.Millisecond}, limitsProgram...)
	c, err := ParseOrigins([]string{"https://app.example"})
	if err != nil {
		t.Fatal(err)
	}
	h, err := WithCORS(inner, findLimitsAdapter(t), c)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { serve(h, httptest.NewRequest("GET", "/spin", nil)); close(done) }()
	time.Sleep(100 * time.Millisecond)
	start := time.Now()
	code, _, _ := serve(h, preflight("/echo", "https://app.example", "POST", "content-type"))
	if code != 204 && code != 200 {
		t.Errorf("preflight while the slot is taken: got %d", code)
	}
	if took := time.Since(start); took > 200*time.Millisecond {
		t.Errorf("preflight waited %s for a slot", took)
	}
	<-done
}

func findLimitsAdapter(t *testing.T) *syntax.AdapterDecl {
	t.Helper()
	f, errs := parser.Parse(limitsProgram[len(limitsProgram)-1])
	if len(errs) > 0 {
		t.Fatalf("parse: %v", errs)
	}
	return f.Decls[0].(*syntax.AdapterDecl)
}

// A body sent slowly stops at the request's deadline (§3), not at the
// server's read timeout: the test server has none, so only the
// connection deadline the limiter sets can end it.
func TestASlowBodyStopsAtTheDeadline(t *testing.T) {
	h := handlerWithLimits(t, Limits{MaxBodyBytes: 1024, MaxRequests: 4, RequestTimeout: 200 * time.Millisecond}, limitsProgram...)
	srv := httptest.NewServer(h)
	defer srv.Close()
	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	start := time.Now()
	fmt.Fprint(conn, "POST /echo HTTP/1.1\r\nHost: x\r\nContent-Type: text/plain\r\nContent-Length: 100\r\n\r\n0123456789")
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("no answer: %v", err)
	}
	if resp.StatusCode != 503 {
		t.Errorf("got %d, want 503", resp.StatusCode)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("took %s to stop a body past its 200ms deadline", took)
	}
}
