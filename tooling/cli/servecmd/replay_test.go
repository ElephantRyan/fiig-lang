package servecmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fiig-lang/compiler/adapter"
	"fiig-lang/compiler/runtime"
	"fiig-lang/tooling/migrate"
	"fiig-lang/tooling/runprogram"
)

// ADR 0111 open question 1, as amended 2026-11-21: what `fiig run -replay`
// refuses before it reaches a source, and how it reports a disposition.
// tooling/runprogram replays elements of Redis and Kafka themselves.

var replayApp = map[string]string{
	"app/fiig.pkg.json": `{"path": "t.replay", "kind": "application", "version": "1.0.0",
    "dependencies": {"fiig.redis": "1.0.0"}, "capabilities": ["Socket"]}`,
	"app/order_placed.fiig": "model OrderPlaced {\n    orderId: string\n}\n",
	"app/place_order.fiig":  "workflow placeOrder(order: OrderPlaced) -> Result<None, string> {\n    return Ok(None)\n}\n",
	"app/hello.fiig":        "workflow hello() -> string {\n    return \"hi\"\n}\n",
	"app/api.fiig":          "adapter http api {\n    GET \"/hello\" -> hello()\n        produces \"application/json\"\n}\n",
	"app/intake.fiig": "adapter stream intake {\n" +
		"    source \"orders\" group \"intake\" from earliest key orderId -> placeOrder(order: OrderPlaced) {\n        Ok(_) -> ack\n        Err(_) -> halt\n    }\n" +
		"    source \"returns\" group \"intake\" from earliest key orderId -> placeOrder(order: OrderPlaced) {\n        Ok(_) -> ack\n        Err(_) -> halt\n    }\n}\n",
}

func replayWith(t *testing.T, adapterName, source string) error {
	t.Helper()
	root := t.TempDir()
	writeTree(t, root, replayApp)
	caps := filepath.Join(root, "caps.json")
	if err := os.WriteFile(caps, []byte(`{"StreamSource": {"type": "source", "source": "fiig.redis"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// Nothing listens on port 1.
	envFile := filepath.Join(root, "replay.env")
	if err := os.WriteFile(envFile, []byte("REDIS_URL=redis://127.0.0.1:1\nREDIS_TIMEOUT_MS=500\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env, err := runprogram.NewEnvironment(envFile)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Replay(context.Background(), ReplayOptions{
		Root: filepath.Join(root, "app"), Adapter: adapterName, ID: "1-0", Source: source,
		CapsPath: caps, Env: env, Migrate: migrate.Apply, Timeout: adapter.DefaultRequestTimeout,
	})
	return err
}

func TestReplayRefusesWhatItCantReplay(t *testing.T) {
	cases := []struct{ adapter, source, want string }{
		{"api", "", "isn't a stream adapter"},
		{"intake", "", `reads 2 sources ("orders", "returns"); -source names the one`},
		{"intake", "refunds", `has no route reading source "refunds"`},
		// The route is found, and the source can't be reached.
		{"intake", "returns", "fetching id 1-0 failed: Unavailable"},
	}
	for _, c := range cases {
		err := replayWith(t, c.adapter, c.source)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s -source %q: %v, want %q", c.adapter, c.source, err, c.want)
		}
	}
}

func TestReplayDescribesTheDisposition(t *testing.T) {
	d := runtime.Delivery{ID: "0/7", Attempt: 3}
	failed := runtime.Result{Ok: false, Value: runtime.Str("out of stock")}
	cases := []struct {
		disp adapter.Disposition
		want string
	}{
		{adapter.Disposition{Outcome: "Ack", Result: runtime.Result{Ok: true, Value: runtime.None{}}}, "ack, for Ok("},
		{adapter.Disposition{Outcome: "Retry", Attempts: 5, Result: failed}, "retry, attempt 3 of 5, for Err("},
		{adapter.Disposition{Outcome: "DeadLetter", Target: "orders.failed", Attempts: 3, Result: failed}, `deadLetter "orders.failed", its 3 attempts used`},
		{adapter.Disposition{Outcome: "DeadLetter", Target: "orders.invalid", Result: failed}, `deadLetter "orders.invalid", for Err(`},
		{adapter.Disposition{Halt: &adapter.Halt{Reason: "its mapping says halt"}}, "halt: its mapping says halt"},
	}
	for _, c := range cases {
		r := &Replayed{Route: `intake source "orders"`, Delivery: d, Disposition: c.disp}
		got := r.Describe()
		if !strings.HasPrefix(got, `stream intake source "orders", id 0/7 (attempt 3): `) || !strings.Contains(got, c.want) {
			t.Errorf("Describe = %q, want %q", got, c.want)
		}
		if r.JSON()["settled"] != false {
			t.Errorf("JSON = %v", r.JSON())
		}
	}
}
