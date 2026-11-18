package runtime

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// ADR 0088: `transaction Database retry(attempts: N)` reruns a block that
// ends in a retryable failure the program didn't handle, from its starting
// locals; a domain Err is never retried.

// flakyDB fails COMMIT with a serialization failure while failCommits is
// above zero, and records each BEGIN's isolation level.
type flakyDB struct {
	mu          sync.Mutex
	failCommits int
	begins      []string
	committed   []int64
	pending     []int64
}

func (db *flakyDB) Call(_ context.Context, method string, args []Value) (Value, *Fault) {
	return Int(1), nil
}

func (db *flakyDB) Begin(_ context.Context, isolation string) (Tx, *Fault) {
	db.mu.Lock()
	defer db.mu.Unlock()
	db.begins = append(db.begins, isolation)
	db.pending = nil
	return flakyTx{db}, nil
}

type flakyTx struct{ db *flakyDB }

func (tx flakyTx) Call(_ context.Context, method string, args []Value) (Value, *Fault) {
	tx.db.mu.Lock()
	defer tx.db.mu.Unlock()
	if len(args) > 1 {
		if n, ok := args[1].(Int); ok {
			tx.db.pending = append(tx.db.pending, int64(n))
		}
	}
	return Int(1), nil
}

func (tx flakyTx) Commit() *Fault {
	tx.db.mu.Lock()
	defer tx.db.mu.Unlock()
	if tx.db.failCommits > 0 {
		tx.db.failCommits--
		f := Faultf("transaction Database: COMMIT: 40001 could not serialize access")
		f.Conflict = &Conflict{Variant: "Retryable", Name: "could not serialize access"}
		return f
	}
	tx.db.committed = append(tx.db.committed, tx.db.pending...)
	return nil
}

func (tx flakyTx) Rollback() *Fault { return nil }

var retrySources = []string{
	`gateway record(n: int) -> int
    requires Database
{
    return Database.execute("INSERT INTO t VALUES (?)", n)
}`,
	`activity recordThree() -> int {
    written: int = 0
    transaction Database serializable retry(attempts: 4) {
        for n in [1, 2, 3] {
            record(n)
            written = written + 1
        }
    }
    return written
}`,
	`activity refuseEven(n: int) -> Result<int, string> {
    transaction Database retry(attempts: 4) {
        record(n)
        if n % 2 == 0 {
            return Err("even")
        }
    }
    return Ok(n)
}`,
	`activity once() -> int {
    transaction Database repeatableRead {
        record(1)
    }
    return 1
}`,
}

func retryInterp(t *testing.T, db *flakyDB) (*Interp, *ManualClock) {
	t.Helper()
	in := checkedInterp(t, retrySources...)
	caps := NewCapabilityRegistry()
	caps.Register("Database", db)
	in.Capabilities = caps
	clock := NewManualClock(time.Unix(0, 0))
	in.Clock = clock
	return in, clock
}

func TestTransactionRetryRerunsFromItsStartingLocals(t *testing.T) {
	db := &flakyDB{failCommits: 2}
	in, clock := retryInterp(t, db)
	v, f := in.Call(context.Background(), "recordThree", nil)
	if f != nil {
		t.Fatal(f)
	}
	if v != Value(Int(3)) {
		t.Fatalf("written = %v, want 3: each attempt starts from written = 0", v)
	}
	if len(db.begins) != 3 || db.begins[0] != "serializable" {
		t.Errorf("begins = %v, want three serializable attempts", db.begins)
	}
	if len(db.committed) != 3 {
		t.Errorf("committed = %v, want one attempt's three rows", db.committed)
	}
	if len(clock.Sleeps()) != 2 {
		t.Errorf("backoffs = %v, want one before each rerun", clock.Sleeps())
	}
}

func TestTransactionRetryGivesUp(t *testing.T) {
	db := &flakyDB{failCommits: 10}
	in, _ := retryInterp(t, db)
	_, f := in.Call(context.Background(), "recordThree", nil)
	if f == nil || !strings.Contains(f.Message, "gave up after 4 attempts") || !strings.Contains(f.Message, "40001") {
		t.Fatalf("fault = %v, want the last failure after 4 attempts", f)
	}
	if len(db.begins) != 4 {
		t.Errorf("begins = %d, want 4", len(db.begins))
	}
}

func TestTransactionRetryLeavesDomainErrorsAlone(t *testing.T) {
	db := &flakyDB{failCommits: 1}
	in, _ := retryInterp(t, db)
	v, f := in.Call(context.Background(), "refuseEven", map[string]Value{"n": Int(2)})
	if f != nil {
		t.Fatal(f)
	}
	if r := v.(Result); r.Ok || len(db.begins) != 1 {
		t.Fatalf("got %v after %d attempts, want Err(even) after one", v, len(db.begins))
	}
}

func TestTransactionWithoutRetryRunsOnce(t *testing.T) {
	db := &flakyDB{failCommits: 1}
	in, _ := retryInterp(t, db)
	if _, f := in.Call(context.Background(), "once", nil); f == nil || strings.Contains(f.Message, "gave up") {
		t.Fatalf("fault = %v, want the commit failure, unretried", f)
	}
	if len(db.begins) != 1 || db.begins[0] != "repeatableRead" {
		t.Errorf("begins = %v, want one repeatableRead", db.begins)
	}
}
