package runprogram

import (
	"context"
	"errors"
	"strings"
	"testing"

	"fiig-lang/compiler/adapter"
)

// ADR 0111 open question 1, as amended 2026-11-21: replaying one element
// by its id fetches it from the source and runs the route's workflow on
// it, settling nothing, against the in-memory Redis.
func TestReplayDecidesWithoutSettling(t *testing.T) {
	redis := startFakeRedis(t, "pw")
	o1 := redis.add("orders", "payload", `{"orderId":"o1","quantity":3}`)
	o2 := redis.add("orders", "payload", `{"orderId":"o2","quantity":0}`)
	o3 := redis.add("orders", "payload", `{"orderId":"o3","quantity":500}`)
	bad := redis.add("orders", "payload", `not json`)
	runner := streamRunner(t, redis)
	ctx := context.Background()

	decide := func(id string) adapter.Disposition {
		t.Helper()
		d, err := runner.Fetch(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if d.ID != id {
			t.Fatalf("fetched %s, want %s", d.ID, id)
		}
		return runner.Decide(ctx, d)
	}

	// Before the group exists: each element on attempt 1.
	if d := decide(o1); d.Outcome != "Ack" || d.Halt != nil || !strings.Contains(d.Result.String(), "Ok") {
		t.Errorf("o1: %+v", d)
	}
	// The workflow's own effects happen.
	if n := redis.counter("processed:o1"); n != 1 {
		t.Errorf("o1 processed %d times", n)
	}
	if d := decide(o2); d.Outcome != "DeadLetter" || d.Target != "orders.invalid" {
		t.Errorf("o2: %+v", d)
	}
	if d := decide(o3); d.Outcome != "Retry" || d.Attempts != 2 || !strings.Contains(d.Result.String(), "OutOfStock") {
		t.Errorf("o3: %+v", d)
	}
	if d := decide(bad); d.Halt == nil || !strings.Contains(d.Halt.Reason, "isn't JSON") {
		t.Errorf("bad: %+v", d)
	}
	// Nothing was settled or moved.
	if got := redis.payloads("orders.invalid"); len(got) != 0 {
		t.Errorf("replay dead-lettered %v", got)
	}

	// The group handles them for real; o3 is left pending to retry, and
	// the bad entry pending after its halt.
	var halt *adapter.Halt
	if err := runner.Run(ctx); !errors.As(err, &halt) {
		t.Fatalf("run: %v", err)
	}
	// Delivered twice now, o3 is on its last attempt, so the mapping's
	// else applies; replaying it still leaves it pending.
	redis.mu.Lock()
	redis.group("orders", "intake").pending[o3].count = 2
	redis.mu.Unlock()
	if d := decide(o3); d.Outcome != "DeadLetter" || d.Target != "orders.failed" || d.Attempts != 2 {
		t.Errorf("o3 on attempt 2: %+v", d)
	}
	if got := redis.payloads("orders.failed"); len(got) != 0 {
		t.Errorf("replay dead-lettered %v", got)
	}
	if n := redis.pending("orders", "intake"); n != 2 {
		t.Errorf("%d entries pending, want o3 and the bad one", n)
	}

	// Ids the stream doesn't hold.
	if _, err := runner.Fetch(ctx, "99-0"); err == nil || !strings.Contains(err.Error(), "no element with id 99-0") {
		t.Errorf("fetching 99-0: %v", err)
	}
	if _, err := runner.Fetch(ctx, strings.TrimSuffix(o1, "-0")+"-7"); err == nil {
		t.Error("fetching an id one sequence number off found an element")
	}
}
