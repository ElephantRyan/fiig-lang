package adapter

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

// ADR 0128: a fault stays on the server, and every request has an id.

var observeProgram = []string{
	`workflow boom(account: string) -> int {
    zero: int = 0;
    return 1 / zero;
}`,
	`workflow fine() -> string {
    return "fine";
}`,
	`adapter http Api {
    GET "/boom/{account}" -> boom(account: account)
        produces "application/json"
    GET "/fine" -> fine()
        produces "application/json"
}`,
}

func observedHandler(t *testing.T, access bool) (*Observer, func() []string) {
	t.Helper()
	var logged []string
	o := &Observer{Access: access, Logf: func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) }}
	return o, func() []string { return logged }
}

func TestAFaultIsABareFiveHundredWithTheRequestsId(t *testing.T) {
	o, logs := observedHandler(t, false)
	h := o.Wrap(handlerFor(t, observeProgram...))
	code, hdr, body := serve(h, httptest.NewRequest("GET", "/boom/ada@example.com", nil))
	if code != 500 {
		t.Fatalf("code = %d", code)
	}
	var reply map[string]string
	if err := json.Unmarshal([]byte(body), &reply); err != nil {
		t.Fatal(err)
	}
	id := hdr.Get(RequestIDHeader)
	if reply["error"] != "internal error" || reply["requestId"] != id || id == "" || len(reply) != 2 {
		t.Errorf("reply = %v, header id %q, want only the error and the id", reply, id)
	}
	if strings.Contains(body, "boom") || strings.Contains(body, "division") || strings.Contains(body, "ada@example.com") {
		t.Errorf("the reply says what went wrong: %s", body)
	}
	line := strings.Join(logs(), "\n")
	if !strings.Contains(line, "request "+id+" faulted: division by zero (in boom)") {
		t.Errorf("log = %q, want the fault under the request's id", line)
	}
}

func TestARequestIdIsKeptWhenItIsOneShortToken(t *testing.T) {
	o, _ := observedHandler(t, false)
	h := o.Wrap(handlerFor(t, observeProgram...))
	for id, kept := range map[string]bool{
		"trace-4f2a.9_B":        true,
		"":                      false,
		"has space":             false,
		"new\nline":             false,
		strings.Repeat("a", 65): false,
		"a;b":                   false,
	} {
		req := httptest.NewRequest("GET", "/fine", nil)
		if id != "" {
			req.Header.Set(RequestIDHeader, id)
		}
		_, hdr, _ := serve(h, req)
		got := hdr.Get(RequestIDHeader)
		if kept && got != id || !kept && (got == id || got == "") {
			t.Errorf("sent %q, got %q, want it kept: %v", id, got, kept)
		}
	}
}

func TestTheAccessLineHasNoQueryAndIsOptional(t *testing.T) {
	o, logs := observedHandler(t, true)
	h := o.Wrap(handlerFor(t, observeProgram...))
	_, hdr, _ := serve(h, httptest.NewRequest("GET", "/fine?token=secret", nil))
	line := strings.Join(logs(), "\n")
	if !strings.Contains(line, "fiig: GET /fine 200 ") || !strings.Contains(line, "id="+hdr.Get(RequestIDHeader)) || strings.Contains(line, "secret") {
		t.Errorf("access line = %q", line)
	}
	quiet, quietLogs := observedHandler(t, false)
	serve(quiet.Wrap(handlerFor(t, observeProgram...)), httptest.NewRequest("GET", "/fine", nil))
	if len(quietLogs()) != 0 {
		t.Errorf("a request without access logging logged %q", quietLogs())
	}
}
