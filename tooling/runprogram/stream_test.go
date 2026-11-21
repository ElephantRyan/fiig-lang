package runprogram

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fiig-lang/compiler/adapter"
	"fiig-lang/compiler/loader"
	"fiig-lang/compiler/runtime"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
	"fiig-lang/internal/fixture"
)

// ADR 0111: a stream adapter reading Redis Streams through fiig.redis
// (ADR 0110), against an in-memory Redis.

var streamProgram = map[string]string{
	"fiig.pkg.json": `{
    "path": "streamtest.orders",
    "kind": "application",
    "version": "1.0.0",
    "dependencies": { "fiig.redis": "1.0.0" },
    "capabilities": ["Socket"]
}`,
	"order_placed.fiig": `model OrderPlaced {
    orderId: string
    quantity: int
}`,
	"order_error.fiig": `enum OrderError {
    OutOfStock(sku: string)
    Invalid(reason: string)
    Unrecorded(reason: string)
}`,
	"mark_processed.fiig": `use fiig.redis.Redis

gateway markProcessed(orderId: string) -> Result<None, OrderError>
    requires Redis
{
    match Redis.send(["INCR", "processed:${orderId}"]) {
        Ok(_) -> {
            return Ok(None)
        }
        Err(_) -> {
            return Err(OrderError.Unrecorded(reason: "Redis refused"))
        }
    }
}`,
	"record.fiig": `activity record(order: OrderPlaced) -> Result<None, OrderError> {
    return markProcessed(order.orderId)
}`,
	"place_order.fiig": `workflow placeOrder(order: OrderPlaced) -> Result<None, OrderError> {
    if order.quantity <= 0 {
        return Err(OrderError.Invalid(reason: "quantity ${order.quantity}"))
    }
    if order.quantity > 100 {
        return Err(OrderError.OutOfStock(sku: order.orderId))
    }
    return record(order)
}`,
	"order_intake.fiig": `adapter stream OrderIntake {
    source "orders" group "intake" from earliest key orderId -> placeOrder(order: OrderPlaced) {
        Ok(_) -> ack
        Err(OrderError.OutOfStock(_)) -> retry(attempts: 2) else deadLetter "orders.failed"
        Err(OrderError.Invalid(_)) -> deadLetter "orders.invalid"
        Err(OrderError.Unrecorded(_)) -> retry(attempts: 5)
    }
}`,
}

// streamRunner loads streamProgram with fiig.redis from the toolchain and
// returns the runner for OrderIntake, reading from redis.
func streamRunner(t *testing.T, redis *fakeRedis) *adapter.StreamRunner {
	t.Helper()
	return streamRunnerOf(t, redis, streamProgram)
}

// The test runner's timing (ADR 0111 §5a): a workflow and a settle may take
// 250 ms each, so an element may take 500 ms, and an unsettled one comes
// back after a second, which fits a batch of two.
const (
	testElementTimeout = 250 * time.Millisecond
	testRetryAfter     = "1000"
)

// streamRunnerOf is streamRunner over program, a variant of streamProgram.
func streamRunnerOf(t *testing.T, redis *fakeRedis, program map[string]string) *adapter.StreamRunner {
	t.Helper()
	return streamRunnerWith(t, redis, program, testRetryAfter, "OrderIntake")
}

// streamRunnerWith is streamRunnerOf with REDIS_STREAM_RETRY_AFTER_MS.
func streamRunnerWith(t *testing.T, redis *fakeRedis, program map[string]string, retryAfter, adapterName string) *adapter.StreamRunner {
	t.Helper()
	root := t.TempDir()
	for name, src := range fixture.Module(program) {
		os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0o755)
		if err := os.WriteFile(filepath.Join(root, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	loaded, errs := loader.Load(root, nil)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	files, _, err := LoadFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	if diags := Check(root, files, loaded.Packages); len(diags) > 0 {
		t.Fatal(diags)
	}
	u, _ := types.BuildUniverse(files)
	caps := runtime.NewCapabilityRegistry()
	source, err := runtime.SourceFactory(map[string]any{"type": "source", "source": "fiig.redis"})
	if err != nil {
		t.Fatal(err)
	}
	caps.Register(runtime.StreamSourceCapability, source)
	RegisterDefaults(caps)
	t.Cleanup(caps.Close)
	in := runtime.New(u, caps)
	in.Imports = BuildImports(files, loaded.Packages)
	in.Groups = BuildGroups(files, loaded.Packages)
	in.Packages = loaded.Packages
	d, _ := u.Lookup(adapterName)
	runners, err := adapter.StreamRunners([]adapter.Served{{Decl: d.(*syntax.AdapterDecl)}}, u, in, "test-1", testElementTimeout)
	if err != nil {
		t.Fatal(err)
	}
	pkg, group, _ := runners[0].Source().Resolve()
	reach := Program{Universe: u, Imports: in.Imports, Groups: in.Groups}
	env := map[string]string{"REDIS_URL": redis.url(), "REDIS_STREAM_RETRY_AFTER_MS": retryAfter}
	lookup := func(name string) (string, bool) { v, ok := env[name]; return v, ok }
	sources := ReachableConfigFrom(reach, []Entry{{Name: adapterName}, {Package: pkg, Name: group}})
	if problems := in.LoadConfig(sources, lookup); len(problems) > 0 {
		t.Fatal(problems)
	}
	runners[0].SettleTimeout = testElementTimeout
	return runners[0]
}

func TestStreamAdapterOverRedisStreams(t *testing.T) {
	redis := startFakeRedis(t, "pw")
	redis.add("orders", "payload", `{"orderId":"o1","quantity":3}`)
	redis.add("orders", "payload", `{"orderId":"o2","quantity":0}`)
	redis.add("orders", "payload", `{"orderId":"o3","quantity":500}`)
	bad := redis.add("orders", "payload", `not json`)
	runner := streamRunner(t, redis)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// §4: o1 is acked, o2 dead-lettered, o3 left to retry, and the payload
	// that isn't JSON halts, unsettled.
	var halt *adapter.Halt
	if err := runner.Run(ctx); !errors.As(err, &halt) || !strings.Contains(halt.Reason, "isn't JSON") || !strings.Contains(halt.Element, bad) {
		t.Fatalf("first run: %v", err)
	}
	if n := redis.counter("processed:o1"); n != 1 {
		t.Errorf("o1 processed %d times", n)
	}
	if got := redis.payloads("orders.invalid"); len(got) != 1 || !strings.Contains(got[0], `"o2"`) {
		t.Errorf("orders.invalid = %v", got)
	}

	// A restart: o3 comes back as attempt 2, its last, and goes to
	// orders.failed; the bad payload is delivered again and halts again.
	time.Sleep(1100 * time.Millisecond)
	if err := runner.Run(ctx); !errors.As(err, &halt) || !strings.Contains(halt.Element, bad) {
		t.Fatalf("second run: %v", err)
	}
	if got := redis.payloads("orders.failed"); len(got) != 1 || !strings.Contains(got[0], `"o3"`) {
		t.Errorf("orders.failed = %v", got)
	}

	// An operator deals with the bad entry; an order written while the
	// runner waits is processed, and shutdown stops it cleanly.
	redis.ack("orders", "intake", bad)
	done := make(chan error, 1)
	live, stop := context.WithCancel(ctx)
	go func() { done <- runner.Run(live) }()
	time.Sleep(100 * time.Millisecond)
	redis.add("orders", "payload", `{"orderId":"o5","quantity":7}`)
	deadline := time.Now().Add(5 * time.Second)
	for redis.counter("processed:o5") == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	stop()
	if err := <-done; err != nil {
		t.Fatalf("third run: %v", err)
	}
	if n := redis.counter("processed:o5"); n != 1 {
		t.Errorf("o5 processed %d times", n)
	}
	if n := redis.pending("orders", "intake"); n != 0 {
		t.Errorf("%d entries left pending", n)
	}
	// ADR 0109: the client keeps its connection, so three runs and their
	// polling share a handful at most.
	if n := redis.accepted.Load(); n > 3 {
		t.Errorf("Redis saw %d connections", n)
	}
}

// Review fix 3 (ADR 0111 §5a): a receive takes no more entries than can be
// handled before the first is reclaimed, here two of the runtime's 16; and
// a delay no element fits in halts, naming the setting.
func TestStreamBatchFitsTheRedeliveryDelay(t *testing.T) {
	redis := startFakeRedis(t, "pw")
	runner := streamRunner(t, redis)
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	if err := runner.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if n := redis.lastCount.Load(); n != 2 {
		t.Errorf("XREADGROUP asked for %d entries, want 2 (1000 ms / 500 ms an element)", n)
	}
	var halt *adapter.Halt
	err := streamRunnerWith(t, redis, streamProgram, "400", "OrderIntake").Run(context.Background())
	if !errors.As(err, &halt) || !strings.Contains(halt.Reason, "REDIS_STREAM_RETRY_AFTER_MS is 400") {
		t.Errorf("a 400 ms delay with 500 ms elements: %v", err)
	}
}

// ADR 0111 open question 4, as amended 2026-11-21: a receive asks for the
// batch and waits the wait its settings name, which fiig serve reads from
// STREAM_BATCH_SIZE and STREAM_WAIT_MS.
func TestStreamReceivesWithItsSettings(t *testing.T) {
	redis := startFakeRedis(t, "pw")
	runner := streamRunner(t, redis)
	runner.Settings = adapter.StreamSettings{Batch: 1, Wait: 150 * time.Millisecond}
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	if err := runner.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if n, ms := redis.lastCount.Load(), redis.lastBlock.Load(); n != 1 || ms != 150 {
		t.Errorf("XREADGROUP asked for COUNT %d BLOCK %d, want 1 and 150", n, ms)
	}
}

// Item 6 of the review: a payload that isn't UTF-8 is dead-lettered as it
// is, through Redis.sendBytes, rather than failing and coming back forever.
func TestStreamDeadLettersBinaryPayloads(t *testing.T) {
	redis := startFakeRedis(t, "pw")
	raw := string([]byte{0xff, 0xfe, 0x00, 'x'})
	id := redis.add("raw", "payload", raw)
	program := map[string]string{}
	for name, src := range streamProgram {
		program[name] = src
	}
	program["take_raw.fiig"] = `workflow takeRaw(payload: bytes) -> Result<None, OrderError> {
    return Err(OrderError.Invalid(reason: "unreadable"))
}`
	program["raw_intake.fiig"] = `adapter stream RawIntake {
    source "raw" group "raw" from earliest -> takeRaw(payload: bytes) {
        Ok(_) -> ack
        Err(_) -> deadLetter "raw.failed"
    }
}`
	runner := streamRunnerWith(t, redis, program, testRetryAfter, "RawIntake")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()
	deadline := time.Now().Add(4 * time.Second)
	for len(redis.payloads("raw.failed")) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := redis.payloads("raw.failed"); len(got) != 1 || got[0] != raw {
		t.Errorf("raw.failed = %q, want [%q]", got, raw)
	}
	if n := redis.pending("raw", "raw"); n != 0 {
		t.Errorf("%d left pending; %s should be acked once moved", n, id)
	}
}

// ADR 0111 §3a: a new group declared `from latest` skips what the stream
// already holds and reads what arrives after it starts.
func TestStreamGroupFromLatest(t *testing.T) {
	redis := startFakeRedis(t, "pw")
	redis.add("orders", "payload", `{"orderId":"old","quantity":1}`)
	program := map[string]string{}
	for name, src := range streamProgram {
		program[name] = src
	}
	program["order_intake.fiig"] = strings.Replace(streamProgram["order_intake.fiig"], "from earliest", "from latest", 1)
	runner := streamRunnerOf(t, redis, program)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()
	time.Sleep(150 * time.Millisecond)
	redis.add("orders", "payload", `{"orderId":"new","quantity":1}`)
	deadline := time.Now().Add(5 * time.Second)
	for redis.counter("processed:new") == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if redis.counter("processed:new") != 1 || redis.counter("processed:old") != 0 {
		t.Errorf("processed new %d, old %d; want 1 and 0", redis.counter("processed:new"), redis.counter("processed:old"))
	}
}
