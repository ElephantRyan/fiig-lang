package sql

import (
	"context"
	"database/sql"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fiig-lang/compiler/loader"
	"fiig-lang/compiler/runtime"
	"fiig-lang/compiler/types"
	"fiig-lang/libraries/sql/sqlrelay"
	"fiig-lang/tooling/runprogram"
)

// ADR 0084 end to end: examples/relay-notes runs on examples/sql-relay, a
// database driver written in fiig, pooled by the runtime, talking to a
// relay over a real socket, which runs the SQL on SQLite.

type relayRig struct {
	in     *runtime.Interp
	pool   *runtime.DriverProvider
	server *sqlrelay.Server
}

func startRelay(t *testing.T, poolConfig map[string]any) *relayRig {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "relay.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	server := &sqlrelay.Server{DB: db}
	go server.Serve(ln)

	examples := findExamplesDir(t)
	root := filepath.Join(examples, "relay-notes")
	loaded, errs := loader.Load(root, []string{examples})
	if len(errs) > 0 {
		t.Fatalf("load: %v", errs)
	}
	files, diags, err := runprogram.LoadFiles(root)
	if err != nil || len(diags) > 0 {
		t.Fatalf("files: %v %v", err, diags)
	}
	if diags := runprogram.Check(root, files, loaded.Packages); len(diags) > 0 {
		t.Fatalf("check: %v", diags)
	}
	universe, _ := types.BuildUniverse(files)

	_, port, _ := net.SplitHostPort(ln.Addr().String())
	cfg := map[string]any{"type": "driver", "driver": "examples.sqlrelay",
		"settings": map[string]any{"host": "127.0.0.1", "port": port}}
	for k, v := range poolConfig {
		cfg[k] = v
	}
	caps, err := runtime.LoadCapabilities(map[string]map[string]any{"Database": cfg}, runtime.FactoryRegistry{"driver": runtime.DriverFactory})
	if err != nil {
		t.Fatal(err)
	}
	caps.Register("Socket", runtime.NewSocketProvider())
	in := runtime.New(universe, caps)
	in.Imports = runprogram.BuildImports(files, loaded.Packages)
	in.Groups = runprogram.BuildGroups(files, loaded.Packages)
	in.Packages = loaded.Packages
	t.Cleanup(caps.Close)
	p, _ := caps.Lookup("Database")
	rig := &relayRig{in: in, pool: p.(*runtime.DriverProvider), server: server}
	rig.call(t, "setupNotes", nil)
	return rig
}

func (r *relayRig) call(t *testing.T, name string, args map[string]runtime.Value) runtime.Value {
	t.Helper()
	v, f := r.in.Call(context.Background(), name, args)
	if f != nil {
		t.Fatalf("%s: %v", name, f)
	}
	return v
}

func note(id, text string) runtime.Value {
	return runtime.Record{TypeName: "Note", Fields: map[string]runtime.Value{"id": runtime.Str(id), "text": runtime.Str(text)}}
}

func notes(ns ...runtime.Value) map[string]runtime.Value {
	return map[string]runtime.Value{"notes": runtime.List{Elems: ns}}
}

func TestDriverInFiigReusesOnePooledConnection(t *testing.T) {
	r := startRelay(t, nil)
	if got := r.call(t, "addNotes", notes(note("a", "first"), note("b", "second"))); !runtime.Equal(got, runtime.Result{Ok: true, Value: runtime.Int(2)}) {
		t.Fatalf("addNotes = %v", got)
	}
	got := r.call(t, "readNote", map[string]runtime.Value{"id": runtime.Str("a")})
	if rec, ok := got.(runtime.Record); !ok || rec.TypeName != "Note" || rec.Fields["text"] != runtime.Str("first") {
		t.Fatalf("readNote = %#v, want a typed Note", got)
	}
	if got := r.call(t, "readNotes", nil).(runtime.List); len(got.Elems) != 2 {
		t.Fatalf("readNotes = %v", got)
	}
	if n := r.server.Accepted.Load(); n != 1 {
		t.Errorf("relay accepted %d connections, want 1 reused for every call", n)
	}
	if n := r.pool.Idle(); n != 1 {
		t.Errorf("pool holds %d idle, want 1", n)
	}
}

func TestDriverTransactionRollsBackOnErr(t *testing.T) {
	r := startRelay(t, nil)
	got := r.call(t, "addNotes", notes(note("c", "third"), note("d", " ")))
	if res, ok := got.(runtime.Result); !ok || res.Ok {
		t.Fatalf("addNotes = %v, want Err", got)
	}
	if got := r.call(t, "readNote", map[string]runtime.Value{"id": runtime.Str("c")}); got != runtime.Value(runtime.None{}) {
		t.Fatalf("readNote(c) = %v, want None: the transaction should have rolled back", got)
	}
	if n := r.server.Accepted.Load(); n != 1 {
		t.Errorf("relay accepted %d connections, want 1: ROLLBACK keeps the connection", n)
	}
}

func TestDriverServerErrorKeepsTheConnection(t *testing.T) {
	r := startRelay(t, nil)
	r.call(t, "addNotes", notes(note("a", "first")))
	_, f := r.in.Call(context.Background(), "addNotes", notes(note("a", "again")))
	if f == nil || !strings.Contains(f.Error(), "UNIQUE constraint failed") || !strings.Contains(f.Error(), "sqlite") {
		t.Fatalf("fault = %v, want the server's error with its code", f)
	}
	r.call(t, "readNotes", nil)
	if n := r.server.Accepted.Load(); n != 1 {
		t.Errorf("relay accepted %d connections, want 1: a server error leaves the connection usable", n)
	}
}

func TestDriverDiscardsABrokenConnection(t *testing.T) {
	r := startRelay(t, nil)
	r.server.DropAll()
	time.Sleep(50 * time.Millisecond)
	_, f := r.in.Call(context.Background(), "readNotes", nil)
	if f == nil || !strings.Contains(f.Error(), "the connection failed") {
		t.Fatalf("fault = %v, want the broken connection reported", f)
	}
	r.call(t, "readNotes", nil)
	if n := r.server.Accepted.Load(); n != 2 {
		t.Errorf("relay accepted %d connections, want 2: the broken one discarded, a new one opened", n)
	}
}

func TestDriverClosesExpiredConnections(t *testing.T) {
	r := startRelay(t, map[string]any{"maxLifetime": "50ms"})
	time.Sleep(150 * time.Millisecond)
	if n := r.pool.Idle(); n != 0 {
		t.Errorf("pool holds %d idle after maxLifetime, want 0", n)
	}
	r.call(t, "readNotes", nil)
	if n := r.server.Accepted.Load(); n != 2 {
		t.Errorf("relay accepted %d connections, want 2: the expired one replaced", n)
	}
}

func TestDriverWaitsForAConnectionNoLongerThanTheDeadline(t *testing.T) {
	r := startRelay(t, map[string]any{"maxConnections": 1})
	tx, f := r.pool.Begin(context.Background())
	if f != nil {
		t.Fatal(f)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, f = r.in.Call(ctx, "readNotes", nil)
	if f == nil || !strings.Contains(f.Error(), "stayed busy") {
		t.Fatalf("fault = %v, want the pool's wait refused at the deadline", f)
	}
	if f := tx.Rollback(); f != nil {
		t.Fatal(f)
	}
	r.call(t, "readNotes", nil)
}

func TestDriverFactoryRefusesBadSettings(t *testing.T) {
	for _, cfg := range []map[string]any{
		{},
		{"driver": "x", "maxConnections": 0},
		{"driver": "x", "idleTimeout": "soon"},
		{"driver": "x", "settings": map[string]any{"port": 5433}},
	} {
		if _, err := runtime.DriverFactory(cfg); err == nil {
			t.Errorf("DriverFactory(%v) = nil error", cfg)
		}
	}
}
