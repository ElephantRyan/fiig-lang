package runprogram

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"fiig-lang/compiler/runtime"
)

// ADR 0085 §3b against a real Postgres (postgres_live_test.go's
// variables), through a proxy that records each Parse the driver sends.

// parseRecorder forwards connections to target and records the SQL text
// of every Parse message clients send, after their startup message.
type parseRecorder struct {
	mu     sync.Mutex
	parsed []string
}

func (r *parseRecorder) listen(t *testing.T, target string) string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			client, err := ln.Accept()
			if err != nil {
				return
			}
			server, err := net.Dial("tcp", target)
			if err != nil {
				client.Close()
				continue
			}
			go func() {
				io.Copy(client, server)
				client.Close()
			}()
			go r.forward(client, server)
		}
	}()
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	return port
}

func (r *parseRecorder) forward(client, server net.Conn) {
	defer server.Close()
	head := make([]byte, 4)
	if _, err := io.ReadFull(client, head); err != nil {
		return
	}
	startup := make([]byte, binary.BigEndian.Uint32(head)-4)
	if _, err := io.ReadFull(client, startup); err != nil {
		return
	}
	server.Write(append(head, startup...))
	for {
		kind, body, err := readMsg(client)
		if err != nil {
			return
		}
		if kind == 'P' {
			rest := body[bytes.IndexByte(body, 0)+1:]
			r.mu.Lock()
			r.parsed = append(r.parsed, string(rest[:bytes.IndexByte(rest, 0)]))
			r.mu.Unlock()
		}
		if _, err := server.Write(msg(kind, body)); err != nil {
			return
		}
	}
}

// parses is how many times sql was parsed.
func (r *parseRecorder) parses(sql string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, s := range r.parsed {
		if s == sql {
			n++
		}
	}
	return n
}

func liveRecorded(t *testing.T) (runtime.CapabilityProvider, *runtime.DriverProvider, *parseRecorder) {
	t.Helper()
	host := os.Getenv("PGHOST")
	if host == "" {
		t.Skip("PGHOST and the other PG* variables name a Postgres to test against")
	}
	port := os.Getenv("PGPORT")
	if port == "" {
		port = "5432"
	}
	rec := &parseRecorder{}
	// The proxy reads the protocol, so the driver speaks it in the clear.
	settings := map[string]any{"host": "127.0.0.1", "port": rec.listen(t, net.JoinHostPort(host, port)),
		"user": os.Getenv("PGUSER"), "password": os.Getenv("PGPASSWORD"), "database": os.Getenv("PGDATABASE"),
		"sslmode": "disable", "applicationName": "fiig-live-test"}
	in, pool := pgInterp(t, settings)
	p, _ := in.Capabilities.Lookup("Database")
	return p, pool, rec
}

func liveColumns(t *testing.T, p runtime.CapabilityProvider, sql string, args ...runtime.Value) int {
	t.Helper()
	v, f := p.Call(context.Background(), "queryOne", append([]runtime.Value{runtime.Str(sql)}, args...))
	if f != nil {
		t.Fatal(f)
	}
	return len(v.(runtime.Record).Fields)
}

// The second and later runs of the same SQL send no Parse.
func TestLivePostgresParsesAStatementOnce(t *testing.T) {
	p, _, rec := liveRecorded(t)
	for i := range 5 {
		v, f := p.Call(context.Background(), "queryOne", []runtime.Value{runtime.Str("SELECT ?::int + 1 AS n"), runtime.Int(i)})
		if f != nil {
			t.Fatal(f)
		}
		if got := v.(runtime.Record).Fields["n"]; got != runtime.Value(runtime.Int(i+1)) {
			t.Errorf("run %d = %v, want %d", i, got, i+1)
		}
	}
	if n := rec.parses("SELECT $1::int + 1 AS n"); n != 1 {
		t.Errorf("parsed %d times in 5 runs, want once", n)
	}
	if got := liveCount(t, p, "SELECT count(*) AS n FROM pg_prepared_statements WHERE name LIKE 'fiig_s%'"); got != 2 {
		t.Errorf("the connection holds %d fiig statements, want 2 (the SELECT and this count)", got)
	}
}

// A schema change that alters a cached statement's columns: outside a
// transaction the driver prepares it again and answers with the new
// columns; inside one the server's 0A000 stands, and the next use outside
// is prepared again. DEALLOCATE ALL is survived the same way.
func TestLivePostgresStatementCacheSurvivesSchemaChanges(t *testing.T) {
	p, pool, rec := liveRecorded(t)
	table := fmt.Sprintf("fiig_cache_%d", time.Now().UnixNano())
	selectAll := "SELECT * FROM " + table + " WHERE a = ?"
	numbered := strings.Replace(selectAll, "?", "$1", 1)
	for _, sql := range []string{"CREATE TABLE " + table + " (a int, b text)", "INSERT INTO " + table + " VALUES (1, 'x')"} {
		if f := pgExecute(p, sql); f != nil {
			t.Fatal(f)
		}
	}
	t.Cleanup(func() { pgExecute(p, "DROP TABLE "+table) })
	if n := liveColumns(t, p, selectAll, runtime.Int(1)); n != 2 {
		t.Fatalf("%d columns, want 2", n)
	}
	if f := pgExecute(p, "ALTER TABLE "+table+" ADD COLUMN c int"); f != nil {
		t.Fatal(f)
	}
	if n := liveColumns(t, p, selectAll, runtime.Int(1)); n != 3 {
		t.Errorf("after ADD COLUMN c: %d columns, want 3", n)
	}

	if f := pgExecute(p, "ALTER TABLE "+table+" ADD COLUMN d int"); f != nil {
		t.Fatal(f)
	}
	tx, f := pool.Begin(context.Background(), "")
	if f != nil {
		t.Fatal(f)
	}
	if _, f := tx.Call(context.Background(), "queryOne", []runtime.Value{runtime.Str(selectAll), runtime.Int(1)}); f == nil || !strings.Contains(f.Error(), "0A000") {
		t.Errorf("in a transaction after ADD COLUMN d: %v, want the server's 0A000", f)
	}
	tx.Rollback()
	if n := liveColumns(t, p, selectAll, runtime.Int(1)); n != 4 {
		t.Errorf("after the transaction: %d columns, want 4", n)
	}

	if f := pgExecute(p, "DEALLOCATE ALL"); f != nil {
		t.Fatal(f)
	}
	for range 3 {
		if n := liveColumns(t, p, selectAll, runtime.Int(1)); n != 4 {
			t.Errorf("after DEALLOCATE ALL: %d columns, want 4", n)
		}
	}
	// Prepared first, after ADD COLUMN c, after the transaction, and after
	// DEALLOCATE ALL; never for the runs between.
	if n := rec.parses(numbered); n != 4 {
		t.Errorf("parsed the SELECT %d times, want 4", n)
	}
	if pool.Idle() != 1 {
		t.Errorf("idle = %d, want the one connection throughout", pool.Idle())
	}
}
