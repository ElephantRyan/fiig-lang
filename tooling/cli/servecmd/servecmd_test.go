package servecmd

import (
	"os"
	"strings"
	"testing"
	"time"

	"fiig-lang/compiler/adapter"
	"fiig-lang/tooling/runprogram"
)

// ADR 0076: each limit is its flag, else its variable, else the default,
// and a bad value is a problem for the one report.
func TestReadLimits(t *testing.T) {
	env, _ := runprogram.NewEnvironment("")
	if l, problems := readLimits(env, 0, 0, 0); l != adapter.DefaultLimits() || len(problems) > 0 {
		t.Errorf("defaults: got %+v %v", l, problems)
	}

	t.Setenv("MAX_BODY_BYTES", "1024")
	t.Setenv("MAX_REQUESTS", "8")
	t.Setenv("REQUEST_TIMEOUT", "2m")
	want := adapter.Limits{MaxBodyBytes: 1024, MaxRequests: 8, RequestTimeout: 2 * time.Minute}
	if l, problems := readLimits(env, 0, 0, 0); l != want || len(problems) > 0 {
		t.Errorf("from the environment: got %+v %v, want %+v", l, problems, want)
	}
	want = adapter.Limits{MaxBodyBytes: 5, MaxRequests: 3, RequestTimeout: time.Second}
	if l, _ := readLimits(env, 5, 3, time.Second); l != want {
		t.Errorf("flags win: got %+v, want %+v", l, want)
	}

	t.Setenv("MAX_BODY_BYTES", "2MiB")
	t.Setenv("MAX_REQUESTS", "0")
	t.Setenv("REQUEST_TIMEOUT", "30")
	_, problems := readLimits(env, 0, 0, 0)
	got := strings.Join(problems, "\n")
	for _, name := range []string{"MAX_BODY_BYTES", "MAX_REQUESTS", "REQUEST_TIMEOUT"} {
		if !strings.Contains(got, name) {
			t.Errorf("no problem reported for %s in:\n%s", name, got)
		}
	}
}

// Review of ADR 0111 §5: the consumer name is the host's alone, the same
// across restarts, so a stream group doesn't gain a dead consumer for
// every process that ever ran.
func TestConsumerNameIsStableAcrossRestarts(t *testing.T) {
	host, err := os.Hostname()
	if err != nil {
		t.Skip(err)
	}
	if got := consumerName(); got != host {
		t.Errorf("consumerName() = %q, want the host name %q", got, host)
	}
}
