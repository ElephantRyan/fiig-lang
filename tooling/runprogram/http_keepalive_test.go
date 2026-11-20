package runprogram

import (
	"context"
	"encoding/pem"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"fiig-lang/compiler/runtime"
)

// ADR 0109 §6 against Go's own HTTP server, plain and over TLS: three
// requests from three calls share one connection.
func TestFiigHttpKeepsConnectionsToARealServer(t *testing.T) {
	for _, secure := range []bool{false, true} {
		name := map[bool]string{false: "http", true: "https"}[secure]
		t.Run(name, func(t *testing.T) {
			var opened atomic.Int32
			srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte("hello " + r.URL.Path))
			}))
			srv.Config.ConnState = func(_ net.Conn, s http.ConnState) {
				if s == http.StateNew {
					opened.Add(1)
				}
			}
			backend := runtime.NetBackend{}
			if secure {
				srv.StartTLS()
				ca := filepath.Join(t.TempDir(), "ca.pem")
				cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
				if err := os.WriteFile(ca, cert, 0o644); err != nil {
					t.Fatal(err)
				}
				settings, problems := runtime.NewTLSSettings([]string{ca}, false, nil)
				if len(problems) > 0 {
					t.Fatal(problems)
				}
				backend.TLS = settings
			} else {
				srv.Start()
			}
			defer srv.Close()
			socket := runtime.SocketProvider{Backend: backend, Pool: runtime.NewSocketPool(runtime.DefaultIdleLimits)}
			defer socket.Close()
			in := httpInterpWith(t, socket)
			for i, path := range []string{"/a", "/b", "/c"} {
				// After the first, wait long enough that the pool's read on
				// the idle connection is blocked, so reuse has to stop it
				// with a past deadline (ADR 0109 §4). Over TLS that is the
				// case the review asked for: crypto/tls must treat the
				// timeout as temporary and leave the connection usable.
				if i > 0 {
					time.Sleep(50 * time.Millisecond)
				}
				v, fault := in.Call(context.Background(), "get", map[string]runtime.Value{"url": str(srv.URL + path)})
				if fault != nil {
					t.Fatal(fault)
				}
				if got, want := describeReply(v), "200 hello "+path; len(got) < len(want) || got[:len(want)] != want {
					t.Errorf("reply %q, want %q", got, want)
				}
			}
			if n := opened.Load(); n != 1 {
				t.Errorf("the server saw %d connections, want 1", n)
			}
		})
	}
}

// ADR 0109 §5: Socket's "idle" entry.
func TestSocketIdleSettings(t *testing.T) {
	good := map[string]any{"type": "socket", "idle": map[string]any{"perKey": float64(4), "total": float64(50), "timeout": "30s"}}
	if p, err := SocketFactory(good); err != nil || p.(runtime.SocketProvider).Pool == nil {
		t.Errorf("good settings: %v, %v", p, err)
	}
	for _, idle := range []any{
		"90s",
		map[string]any{"perKey": "ten"},
		map[string]any{"total": float64(-1)},
		map[string]any{"timeout": "soon"},
	} {
		if _, err := SocketFactory(map[string]any{"type": "socket", "idle": idle}); err == nil {
			t.Errorf("idle %v was accepted", idle)
		}
	}
}
