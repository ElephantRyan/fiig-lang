package runtime

import (
	"context"
	"testing"
	"time"
)

func TestClockProviderIsTheInterpretersClock(t *testing.T) {
	start := time.Unix(1_700_000_000, 0)
	clock := NewManualClock(start)
	caps := NewCapabilityRegistry()
	caps.Register("Clock", ClockProvider{Clock: clock})

	v, fault := caps.Call(context.Background(), "Clock", "now", nil)
	if fault != nil || v != Int(start.UnixMilli()) {
		t.Fatalf("now = %v, %v; want %d", v, fault, start.UnixMilli())
	}
	if _, fault := caps.Call(context.Background(), "Clock", "sleep", []Value{Int(250)}); fault != nil {
		t.Fatalf("sleep: %v", fault)
	}
	if got := clock.Now().Sub(start); got != 250*time.Millisecond {
		t.Fatalf("clock advanced %v, want 250ms: fiig code and the runtime must share one clock", got)
	}
	if _, fault := caps.Call(context.Background(), "Clock", "tomorrow", nil); fault == nil {
		t.Fatal("an unknown method must fault")
	}
}
