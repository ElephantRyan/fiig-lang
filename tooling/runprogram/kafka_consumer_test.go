package runprogram

import (
	"context"
	"fmt"
	"os"
	"os/exec"
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

// ADR 0117: a stream adapter reading Kafka through fiig.kafka's KIP-848
// consumer, against a real Kafka 4.0 broker, since the group protocol is
// the broker's. FIIG_KAFKA_BOOTSTRAP is its host:port and
// FIIG_KAFKA_CONTAINER the docker container it runs in, for creating
// topics and reading dead letters; the test is skipped without them.

func consumerProgram(topic string) map[string]string {
	return map[string]string{
		"fiig.pkg.json": `{
    "path": "consumertest.orders",
    "kind": "application",
    "version": "1.0.0",
    "dependencies": { "fiig.kafka": "1.0.0", "fiig.redis": "1.0.0" },
    "capabilities": ["Socket", "Session"]
}`,
		"order_placed.fiig": `model OrderPlaced {
    orderId: string;
    quantity: int;
}`,
		"order_error.fiig": `enum OrderError {
    OutOfStock(sku: string),
    Invalid(reason: string),
    Unrecorded(reason: string),
}`,
		"mark_handled.fiig": `use fiig.redis.Redis;

gateway markHandled(orderId: string) -> Result<None, OrderError>
    requires Redis
{
    return match (Redis.send(["XADD", "handled", "*", "payload", orderId])) {
        Ok(_) -> {
            return Ok(None);
        },
        Err(_) -> {
            return Err(OrderError.Unrecorded(reason: "Redis refused"));
        },
    }
}`,
		"record.fiig": `activity record(order: OrderPlaced) -> Result<None, OrderError> {
    return markHandled(order.orderId);
}`,
		"place_order.fiig": `workflow placeOrder(order: OrderPlaced) -> Result<None, OrderError> {
    record(order)?;
    if (order.quantity <= 0) {
        return Err(OrderError.Invalid(reason: "quantity ${order.quantity}"));
    }
    if (order.quantity > 100) {
        return Err(OrderError.OutOfStock(sku: order.orderId));
    }
    return Ok(None);
}`,
		"publish.fiig": `use fiig.kafka;
use fiig.kafka.Kafka;

activity publish(topic: string, values: List<string>) -> Result<List<kafka.Sent>, kafka.KafkaError> {
    records: List<kafka.KafkaRecord> = [];
    for (v in values) {
        records = append(records, kafka.KafkaRecord(key: None, value: v.utf8(), headers: None));
    }
    return Kafka.send(topic, records);
}`,
		"order_intake.fiig": `adapter stream OrderIntake {
    source "` + topic + `" group "` + topic + `-intake" from earliest key orderId -> placeOrder(order: OrderPlaced) {
        Ok(_) -> ack
        Err(OrderError.OutOfStock(_)) -> retry(attempts: 3) else deadLetter "` + topic + `.failed"
        Err(OrderError.Invalid(_)) -> deadLetter "` + topic + `.invalid"
        Err(OrderError.Unrecorded(_)) -> retry(attempts: 5)
    }
}`,
	}
}

// consumerProcess is one process running OrderIntake: its interpreter,
// its runner, and its bindings, which Close as a process stopping does.
type consumerProcess struct {
	in     *runtime.Interp
	runner *adapter.StreamRunner
	caps   *runtime.CapabilityRegistry
}

// extra adds to the process's environment, such as a SASL login.
func startConsumer(t *testing.T, root, bootstrap string, redis *fakeRedis, extra map[string]string) *consumerProcess {
	t.Helper()
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
	source, err := runtime.SourceFactory(map[string]any{"type": "source", "source": "fiig.kafka"})
	if err != nil {
		t.Fatal(err)
	}
	caps.Register(runtime.StreamSourceCapability, source)
	RegisterDefaults(caps)
	in := runtime.New(u, caps)
	in.Imports = BuildImports(files, loaded.Packages)
	in.Groups = BuildGroups(files, loaded.Packages)
	in.Packages = loaded.Packages
	d, _ := u.Lookup("OrderIntake")
	runners, err := adapter.StreamRunners([]adapter.Served{{Decl: d.(*syntax.AdapterDecl)}}, u, in, "test-1", 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	pkg, group, _ := runners[0].Source().Resolve()
	reach := Program{Universe: u, Imports: in.Imports, Groups: in.Groups}
	env := map[string]string{
		"REDIS_URL": redis.url(), "KAFKA_BOOTSTRAP": bootstrap,
		"KAFKA_RETRY_AFTER_MS": "200", "KAFKA_COMMIT_INTERVAL_MS": "200",
	}
	for k, v := range extra {
		env[k] = v
	}
	lookup := func(name string) (string, bool) { v, ok := env[name]; return v, ok }
	sources := ReachableConfigFrom(reach, []Entry{{Name: "OrderIntake"}, {Name: "publish"}, {Package: pkg, Name: group}})
	if problems := in.LoadConfig(sources, lookup); len(problems) > 0 {
		t.Fatal(problems)
	}
	runners[0].Logf = t.Logf
	return &consumerProcess{in: in, runner: runners[0], caps: caps}
}

func (p *consumerProcess) publish(t *testing.T, topic string, values ...string) {
	t.Helper()
	list := make([]runtime.Value, len(values))
	for i, v := range values {
		list[i] = runtime.Str(v)
	}
	v, f := p.in.Call(context.Background(), "publish", map[string]runtime.Value{"topic": runtime.Str(topic), "values": runtime.List{Elems: list}})
	if f != nil {
		t.Fatal(f.Message)
	}
	if r := v.(runtime.Result); !r.Ok {
		t.Fatalf("publish: %v", r.Value)
	}
}

// run runs the adapter until handled holds want entries, or fails.
func (p *consumerProcess) run(t *testing.T, redis *fakeRedis, want int) {
	t.Helper()
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- p.runner.Run(ctx) }()
	deadline := time.Now().Add(60 * time.Second)
	for len(redis.payloads("handled")) < want && time.Now().Before(deadline) {
		select {
		case err := <-done:
			stop()
			t.Fatalf("the adapter stopped: %v", err)
		case <-time.After(20 * time.Millisecond):
		}
	}
	// Long enough for the last settle and a commit.
	time.Sleep(500 * time.Millisecond)
	stop()
	if err := <-done; err != nil {
		t.Fatalf("the adapter stopped: %v", err)
	}
	if got := redis.payloads("handled"); len(got) != want {
		t.Fatalf("handled %q, want %d", got, want)
	}
}

func kafkaTool(t *testing.T, container string, args ...string) string {
	t.Helper()
	out, err := exec.Command("docker", append([]string{"exec", container}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func TestStreamAdapterOverAKafkaConsumerGroup(t *testing.T) {
	bootstrap, container := os.Getenv("FIIG_KAFKA_BOOTSTRAP"), os.Getenv("FIIG_KAFKA_CONTAINER")
	if bootstrap == "" || container == "" {
		t.Skip("FIIG_KAFKA_BOOTSTRAP and FIIG_KAFKA_CONTAINER name a Kafka 4.0 broker to test against")
	}
	topic := fmt.Sprintf("fiig-consumer-%d", time.Now().UnixNano())
	for _, name := range []string{topic, topic + ".failed", topic + ".invalid"} {
		kafkaTool(t, container, "/opt/kafka/bin/kafka-topics.sh", "--bootstrap-server", "localhost:29092",
			"--create", "--topic", name, "--partitions", "1", "--replication-factor", "1")
	}
	root := t.TempDir()
	for name, src := range fixture.Module(consumerProgram(topic)) {
		os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0o755)
		if err := os.WriteFile(filepath.Join(root, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	redis := startFakeRedis(t, "pw")

	// o2 is out of stock: retried twice, after 200 ms each, then dead-
	// lettered on its third attempt; o3 and o4 wait for it, in order (§5).
	first := startConsumer(t, root, bootstrap, redis, nil)
	first.publish(t, topic,
		`{"orderId":"o1","quantity":3}`, `{"orderId":"o2","quantity":500}`,
		`{"orderId":"o3","quantity":0}`, `{"orderId":"o4","quantity":7}`)
	first.run(t, redis, 6)
	want := []string{"o1", "o2", "o2", "o2", "o3", "o4"}
	if got := redis.payloads("handled"); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("handled %q, want %q", got, want)
	}
	// Closing commits and leaves the group.
	first.caps.Close()

	for name, wantID := range map[string]string{topic + ".failed": "/0/1", topic + ".invalid": "/0/2"} {
		out := kafkaTool(t, container, "/opt/kafka/bin/kafka-console-consumer.sh", "--bootstrap-server", "localhost:29092",
			"--topic", name, "--from-beginning", "--max-messages", "1", "--timeout-ms", "20000", "--property", "print.key=true")
		if !strings.Contains(out, topic+wantID) {
			t.Errorf("%s holds %q, want the key %s", name, out, topic+wantID)
		}
	}

	// A second process starts where the first committed: o5 is the only
	// record it handles, and it gets the partition at once, since the
	// first left the group rather than timing out of it.
	second := startConsumer(t, root, bootstrap, redis, nil)
	defer second.caps.Close()
	second.publish(t, topic, `{"orderId":"o5","quantity":1}`)
	start := time.Now()
	second.run(t, redis, 7)
	if got := redis.payloads("handled"); got[6] != "o5" {
		t.Fatalf("the second process handled %q", got[6:])
	}
	t.Logf("the second process handled o5 %s after it started", time.Since(start))
	if took := time.Since(start); took > 20*time.Second {
		t.Errorf("the second process took %s to get the partition; the first didn't leave", took)
	}
}
