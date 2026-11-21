package runprogram

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fiig-lang/internal/fixture"
)

// ADR 0111 open question 1, as amended 2026-11-21: replaying one Kafka
// record by its "partition/offset" id against the real broker
// TestStreamAdapterOverAKafkaConsumerGroup uses. The fetch goes over
// broker sessions, so it joins no group: the consumer afterwards still
// starts at the topic's first record.
func TestReplayOfAKafkaRecord(t *testing.T) {
	bootstrap, container := os.Getenv("FIIG_KAFKA_BOOTSTRAP"), os.Getenv("FIIG_KAFKA_CONTAINER")
	if bootstrap == "" || container == "" {
		t.Skip("FIIG_KAFKA_BOOTSTRAP and FIIG_KAFKA_CONTAINER name a Kafka 4.0 broker to test against")
	}
	topic := fmt.Sprintf("fiig-replay-%d", time.Now().UnixNano())
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
	p := startConsumer(t, root, bootstrap, redis)
	defer p.caps.Close()
	p.publish(t, topic,
		`{"orderId":"o1","quantity":3}`, `{"orderId":"o2","quantity":500}`, `{"orderId":"o3","quantity":0}`)
	ctx := context.Background()

	cases := []struct{ id, outcome, target, result string }{
		{"0/0", "Ack", "", "Ok"},
		{"0/1", "Retry", "", "OutOfStock"},
		{"0/2", "DeadLetter", topic + ".invalid", "Invalid"},
	}
	for _, c := range cases {
		d, err := p.runner.Fetch(ctx, c.id)
		if err != nil {
			t.Fatalf("%s: %v", c.id, err)
		}
		if d.Attempt != 1 || d.Order != "0" {
			t.Errorf("%s: attempt %d, order %q", c.id, d.Attempt, d.Order)
		}
		got := p.runner.Decide(ctx, d)
		if got.Halt != nil || got.Outcome != c.outcome || got.Target != c.target || !strings.Contains(got.Result.String(), c.result) {
			t.Errorf("%s: %+v", c.id, got)
		}
	}
	// The workflow ran on each, as it would have under fiig serve.
	if got := redis.payloads("handled"); strings.Join(got, ",") != "o1,o2,o3" {
		t.Errorf("handled %q", got)
	}
	for _, id := range []string{"0/3", "0/99", "7/0"} {
		if _, err := p.runner.Fetch(ctx, id); err == nil || !strings.Contains(err.Error(), "no element with id "+id) {
			t.Errorf("fetching %s: %v", id, err)
		}
	}
	if _, err := p.runner.Fetch(ctx, "0-1"); err == nil || !strings.Contains(err.Error(), "isn't partition/offset") {
		t.Errorf("fetching 0-1: %v", err)
	}

	// Nothing was settled or committed: the group, joining now, starts at
	// o1, and o2 runs its three attempts.
	p.run(t, redis, 8)
	if got := redis.payloads("handled"); strings.Join(got, ",") != "o1,o2,o3,o1,o2,o2,o2,o3" {
		t.Errorf("handled %q after the group ran", got)
	}
}
