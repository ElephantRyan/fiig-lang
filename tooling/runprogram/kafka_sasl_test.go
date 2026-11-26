package runprogram

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fiig-lang/internal/fixture"
)

// ADR 0113 §6, as amended 2026-11-21: com.degeneratedevelopment.kafka's SASL logins, PLAIN,
// SCRAM-SHA-256 and SCRAM-SHA-512, against a real Kafka 4.0 broker with a
// SASL_PLAINTEXT listener, for the producer and the consumer.
// FIIG_KAFKA_SASL_BOOTSTRAP is that listener's host:port and
// FIIG_KAFKA_SASL_CONTAINER the docker container it runs in, whose
// PLAINTEXT listener on localhost:29092 creates topics and SCRAM users;
// the listener's JAAS config must hold PLAIN's user_plainuser="plain-secret".
// The test is skipped without them.
func TestKafkaOverSasl(t *testing.T) {
	bootstrap, container := os.Getenv("FIIG_KAFKA_SASL_BOOTSTRAP"), os.Getenv("FIIG_KAFKA_SASL_CONTAINER")
	if bootstrap == "" || container == "" {
		t.Skip("FIIG_KAFKA_SASL_BOOTSTRAP and FIIG_KAFKA_SASL_CONTAINER name a Kafka 4.0 broker with a SASL_PLAINTEXT listener")
	}
	configs := "/opt/kafka/bin/kafka-configs.sh"
	kafkaTool(t, container, configs, "--bootstrap-server", "localhost:29092", "--alter",
		"--add-config", "SCRAM-SHA-256=[iterations=4096,password=scram256-secret]", "--entity-type", "users", "--entity-name", "fiig256")
	kafkaTool(t, container, configs, "--bootstrap-server", "localhost:29092", "--alter",
		"--add-config", "SCRAM-SHA-512=[iterations=8192,password=scram512-secret]", "--entity-type", "users", "--entity-name", "fiig512")
	topic := fmt.Sprintf("fiig-sasl-%d", time.Now().UnixNano())
	kafkaTool(t, container, "/opt/kafka/bin/kafka-topics.sh", "--bootstrap-server", "localhost:29092",
		"--create", "--topic", topic, "--partitions", "3", "--replication-factor", "1")

	logins := []struct{ mechanism, user, password string }{
		{"PLAIN", "plainuser", "plain-secret"},
		{"SCRAM-SHA-256", "fiig256", "scram256-secret"},
		{"SCRAM-SHA-512", "fiig512", "scram512-secret"},
	}
	for _, l := range logins {
		publish := kafkaPublisher(t, map[string]string{
			"KAFKA_BOOTSTRAP": bootstrap, "KAFKA_SASL_MECHANISM": l.mechanism,
			"KAFKA_SASL_USERNAME": l.user, "KAFKA_SASL_PASSWORD": l.password,
		})
		for i := range 2 {
			if got := publish(topic); !strings.HasPrefix(got, "Ok([Sent{") {
				t.Errorf("%s send %d: %s", l.mechanism, i+1, got)
			}
		}
	}

	// A wrong password is the broker's refusal; no login is no session.
	wrong := kafkaPublisher(t, map[string]string{
		"KAFKA_BOOTSTRAP": bootstrap, "KAFKA_SASL_MECHANISM": "SCRAM-SHA-512",
		"KAFKA_SASL_USERNAME": "fiig512", "KAFKA_SASL_PASSWORD": "not-it",
	})
	if got := wrong(topic); !strings.Contains(got, "Authentication") || !strings.Contains(got, "SASL_AUTHENTICATION_FAILED") {
		t.Errorf("a wrong password: %s", got)
	}
	none := kafkaPublisher(t, map[string]string{"KAFKA_BOOTSTRAP": bootstrap, "KAFKA_TIMEOUT_MS": "3000"})
	if got := none(topic); strings.HasPrefix(got, "Ok(") {
		t.Errorf("a send with no login: %s", got)
	}

	// The consumer logs in on its group session and on each leader's.
	orders := topic + "-orders"
	kafkaTool(t, container, "/opt/kafka/bin/kafka-topics.sh", "--bootstrap-server", "localhost:29092",
		"--create", "--topic", orders, "--partitions", "1", "--replication-factor", "1")
	root := t.TempDir()
	for name, src := range fixture.Module(consumerProgram(orders)) {
		os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0o755)
		if err := os.WriteFile(filepath.Join(root, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	redis := startFakeRedis(t, "pw")
	consumer := startConsumer(t, root, bootstrap, redis, map[string]string{
		"KAFKA_SASL_MECHANISM": "SCRAM-SHA-512", "KAFKA_SASL_USERNAME": "fiig512", "KAFKA_SASL_PASSWORD": "scram512-secret",
	})
	defer consumer.caps.Close()
	consumer.publish(t, orders, `{"orderId":"s1","quantity":1}`, `{"orderId":"s2","quantity":2}`)
	consumer.run(t, redis, 2)
}
