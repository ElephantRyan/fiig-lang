package runtime

import (
	"context"
	"sync"
	"testing"
)

// kvProvider is a minimal real transactional capability — a plain
// key/value store with actual commit/rollback semantics, purpose-built to
// prove transaction.go's mechanics directly, independent of the
// customer-service corpus's shape (fakeDatabase in gateway_test.go is
// deliberately trivial instead, just enough to let `transaction Database`
// parse-and-run in those tests).
//
// Methods: "set" (2 args: key, value — always succeeds), "get" (1 arg:
// key — returns the value or None).
type kvProvider struct {
	mu   sync.Mutex
	data map[string]Value
}

func newKVProvider() *kvProvider {
	return &kvProvider{data: map[string]Value{}}
}

func (p *kvProvider) Call(ctx context.Context, method string, args []Value) (Value, *Fault) {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch method {
	case "get":
		v, ok := p.data[string(args[0].(Str))]
		if !ok {
			return None{}, nil
		}
		return v, nil
	case "set":
		p.data[string(args[0].(Str))] = args[1]
		return None{}, nil
	default:
		return nil, Faultf("kvProvider has no method %q", method)
	}
}

func (p *kvProvider) Begin(ctx context.Context, _ string) (Tx, *Fault) {
	p.mu.Lock()
	defer p.mu.Unlock()
	staged := make(map[string]Value, len(p.data))
	for k, v := range p.data {
		staged[k] = v
	}
	return &kvTx{provider: p, staged: staged}, nil
}

// kvTx stages writes in its own copy, visible to reads made through the
// same Tx (fiig-lang.md §3.8: "reads inside transaction participate in the
// current transaction and can observe preceding writes"), and only
// reaches the shared provider on Commit.
type kvTx struct {
	provider *kvProvider
	staged   map[string]Value
	done     bool
}

func (tx *kvTx) Call(ctx context.Context, method string, args []Value) (Value, *Fault) {
	switch method {
	case "get":
		v, ok := tx.staged[string(args[0].(Str))]
		if !ok {
			return None{}, nil
		}
		return v, nil
	case "set":
		tx.staged[string(args[0].(Str))] = args[1]
		return None{}, nil
	default:
		return nil, Faultf("kvTx has no method %q", method)
	}
}

func (tx *kvTx) Commit() *Fault {
	tx.provider.mu.Lock()
	defer tx.provider.mu.Unlock()
	tx.provider.data = tx.staged
	tx.done = true
	return nil
}

func (tx *kvTx) Rollback() *Fault {
	tx.done = true
	return nil
}

func TestTransactionCommitsOnSuccessAndWriteIsVisibleAfterward(t *testing.T) {
	u := universeFromSource(t, `rule noop() -> int { return 0; }
`, `workflow setAndRead(key: string, value: string) -> string {
    transaction KV {
        KV.set(key, value);
        result = KV.get(key);
        return result;
    }
}
`)
	kv := newKVProvider()
	caps := NewCapabilityRegistry()
	caps.Register("KV", kv)
	in := newTestInterp(u, caps)

	v, fault := in.Call(context.Background(), "setAndRead", map[string]Value{"key": Str("a"), "value": Str("1")})
	if fault != nil {
		t.Fatalf("Call: %v", fault)
	}
	if v != Str("1") {
		t.Fatalf("got %s, want the write to be visible to a read inside the same transaction", v)
	}
	// Committed: the write is visible outside the transaction too, via a
	// direct provider call (as any later, separate transaction would see).
	after, f := kv.Call(context.Background(), "get", []Value{Str("a")})
	if f != nil {
		t.Fatalf("Call: %v", f)
	}
	if after != Str("1") {
		t.Fatalf("got %s after commit, want the write to have landed in the shared store", after)
	}
}

func TestTransactionRollsBackOnPropagatedError(t *testing.T) {
	u := universeFromSource(t, `enum E {
    Bad(),
}
`, `workflow writeThenFail(key: string, value: string) -> Result<None, E> {
    transaction KV {
        KV.set(key, value);
        return Err(E.Bad());
    }
}
`)
	kv := newKVProvider()
	caps := NewCapabilityRegistry()
	caps.Register("KV", kv)
	in := newTestInterp(u, caps)

	v, fault := in.Call(context.Background(), "writeThenFail", map[string]Value{"key": Str("a"), "value": Str("1")})
	if fault != nil {
		t.Fatalf("Call: %v", fault)
	}
	rv := mustResult(t, v)
	if rv.Ok {
		t.Fatalf("got Ok, want the Err to propagate out of the transaction block")
	}

	after, f := kv.Call(context.Background(), "get", []Value{Str("a")})
	if f != nil {
		t.Fatalf("Call: %v", f)
	}
	if _, isNone := after.(None); !isNone {
		t.Fatalf("got %s, want the write to have been rolled back (never committed)", after)
	}
}

func TestTransactionRollsBackOnFault(t *testing.T) {
	u := universeFromSource(t, `workflow writeThenFault(key: string, value: string) -> int {
    transaction KV {
        KV.set(key, value);
        return 1 / 0;
    }
}
`)
	kv := newKVProvider()
	caps := NewCapabilityRegistry()
	caps.Register("KV", kv)
	in := newTestInterp(u, caps)

	_, fault := in.Call(context.Background(), "writeThenFault", map[string]Value{"key": Str("a"), "value": Str("1")})
	if fault == nil {
		t.Fatal("got nil fault, want one for division by zero")
	}

	after, f := kv.Call(context.Background(), "get", []Value{Str("a")})
	if f != nil {
		t.Fatalf("Call: %v", f)
	}
	if _, isNone := after.(None); !isNone {
		t.Fatalf("got %s, want the write to have been rolled back", after)
	}
}

func TestTransactionAgainstNonTransactionalProviderFaults(t *testing.T) {
	u := universeFromSource(t, `workflow doWrite(key: string, value: string) -> None {
    transaction KV {
        KV.set(key, value);
    }
}
`)
	caps := NewCapabilityRegistry()
	caps.Register("KV", FuncProvider{"set": func(_ context.Context, args []Value) (Value, *Fault) { return None{}, nil }})
	in := newTestInterp(u, caps)

	_, fault := in.Call(context.Background(), "doWrite", map[string]Value{"key": Str("a"), "value": Str("1")})
	if fault == nil {
		t.Fatal("got nil fault, want one: FuncProvider doesn't implement TransactionalProvider")
	}
}
