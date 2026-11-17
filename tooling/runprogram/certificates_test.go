package runprogram

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fiig-lang/compiler/checker"
	"fiig-lang/compiler/parser"
	"fiig-lang/compiler/runtime"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// ADR 0086: Socket's trusted CAs and host-scoped client certificates,
// against real TLS servers signed by a throwaway CA.

type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pem  []byte
}

var serial int64

func newCA(t *testing.T, name string) *testCA {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	serial++
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: name},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	return &testCA{cert: cert, key: key, pem: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}
}

// issue signs a leaf for host (a server) or for a client, returning the
// certificate and key as PEM.
func (ca *testCA) issue(t *testing.T, host string, client bool) ([]byte, []byte) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	serial++
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: host},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, DNSNames: []string{host},
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if client {
		tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, _ := x509.MarshalPKCS8PrivateKey(key)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
}

func writeTemp(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// tlsServer answers "ok" over TLS as host, signed by ca, and when
// clients isn't nil requires a client certificate it signed. seen gets
// each client certificate's common name ("" for none).
func tlsServer(t *testing.T, ca *testCA, host string, clients *testCA, seen chan<- string) int {
	t.Helper()
	certPEM, keyPEM := ca.issue(t, host, false)
	pair, _ := tls.X509KeyPair(certPEM, keyPEM)
	cfg := &tls.Config{Certificates: []tls.Certificate{pair}}
	if clients != nil {
		pool := x509.NewCertPool()
		pool.AddCert(clients.cert)
		cfg.ClientCAs, cfg.ClientAuth = pool, tls.VerifyClientCertIfGiven
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				tc := c.(*tls.Conn)
				if tc.Handshake() != nil {
					return
				}
				name := ""
				if peers := tc.ConnectionState().PeerCertificates; len(peers) > 0 {
					name = peers[0].Subject.CommonName
				}
				if seen != nil {
					seen <- name
				}
				c.Write([]byte("ok"))
			}(c)
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port
}

// pinnedHost dials host names as 127.0.0.1, so a certificate for a real
// name can be served locally.
type pinnedHost struct{ runtime.NetBackend }

func (p pinnedHost) Dial(ctx context.Context, host string, port int) (runtime.Transport, error) {
	return p.NetBackend.Dial(ctx, "127.0.0.1", port)
}

func socketWith(t *testing.T, cfg map[string]any) runtime.SocketProvider {
	t.Helper()
	cfg["type"] = "socket"
	p, err := SocketFactory(cfg)
	if err != nil {
		t.Fatal(err)
	}
	sp := p.(runtime.SocketProvider)
	nb, _ := sp.Backend.(runtime.NetBackend)
	return runtime.SocketProvider{Backend: pinnedHost{nb}}
}

// fetchSource is a gateway that connects with TLS, reads one reply, and
// says what happened, as a program would.
const fetchSource = `gateway fetch(host: string, port: int) -> string
    requires Socket
{
    match Socket.connectTls(host, port) {
        Err(failure) -> {
            return "failed: ${failure}"
        }
        Ok(connection) -> {
            match Socket.read(connection) {
                Ok(data) -> {
                    match data.utf8() {
                        None -> {
                            return "not text"
                        }
                        text -> {
                            return text
                        }
                    }
                }
                Err(failure) -> {
                    return "failed: ${failure}"
                }
            }
        }
    }
}`

// fetch connects with TLS to host:port through p and reads the reply; the
// second result is the failure, when there is one.
func fetch(t *testing.T, p runtime.SocketProvider, host string, port int) (string, string) {
	t.Helper()
	f, errs := parser.Parse(fetchSource)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	files := []*syntax.File{f}
	if diags := checker.Check(files, nil); len(diags) > 0 {
		t.Fatal(diags)
	}
	u, _ := types.BuildUniverse(files)
	caps := runtime.NewCapabilityRegistry()
	caps.Register("Socket", p)
	in := runtime.New(u, caps)
	v, fault := in.Call(context.Background(), "fetch", map[string]runtime.Value{"host": runtime.Str(host), "port": runtime.Int(int64(port))})
	if fault != nil {
		t.Fatal(fault)
	}
	out := string(v.(runtime.Str))
	if failure, failed := strings.CutPrefix(out, "failed: "); failed {
		return "", failure
	}
	return out, ""
}

func TestTrustAddsACompanyCA(t *testing.T) {
	ca := newCA(t, "corp root")
	port := tlsServer(t, ca, "db.internal", nil, nil)
	if _, failure := fetch(t, socketWith(t, map[string]any{}), "db.internal", port); !strings.Contains(failure, "Tls") {
		t.Fatalf("without trust: %q, want a TLS failure", failure)
	}
	dir := t.TempDir()
	p := socketWith(t, map[string]any{"trust": []any{writeTemp(t, dir, "corp.pem", ca.pem)}})
	if out, failure := fetch(t, p, "db.internal", port); out != "ok" {
		t.Fatalf("with trust: %q %q", out, failure)
	}
	if _, failure := fetch(t, p, "other.internal", port); !strings.Contains(failure, "Tls") {
		t.Fatalf("wrong host name: %q, want verification to stay on", failure)
	}
}

func TestClientCertificateGoesOnlyToItsHosts(t *testing.T) {
	ca := newCA(t, "corp root")
	clientCA := newCA(t, "client root")
	seen := make(chan string, 4)
	port := tlsServer(t, ca, "db.internal", clientCA, seen)
	other := tlsServer(t, ca, "api.partner.example", clientCA, seen)
	dir := t.TempDir()
	certPEM, keyPEM := clientCA.issue(t, "orders-service", true)
	p := socketWith(t, map[string]any{
		"trust": []any{writeTemp(t, dir, "corp.pem", ca.pem)},
		"clientCertificates": []any{map[string]any{
			"hosts":       []any{"db.internal", "*.mesh.internal"},
			"certificate": writeTemp(t, dir, "client.crt", certPEM),
			"key":         writeTemp(t, dir, "client.key", keyPEM),
		}},
	})
	if out, _ := fetch(t, p, "db.internal", port); out != "ok" || <-seen != "orders-service" {
		t.Fatal("db.internal didn't receive the client certificate")
	}
	if out, _ := fetch(t, p, "api.partner.example", other); out != "ok" || <-seen != "" {
		t.Fatal("api.partner.example received a certificate meant for db.internal")
	}
}

func TestRenewedFilesAreReadWithoutARestart(t *testing.T) {
	old, renewed := newCA(t, "old root"), newCA(t, "new root")
	port := tlsServer(t, renewed, "db.internal", nil, nil)
	dir := t.TempDir()
	path := writeTemp(t, dir, "ca.pem", old.pem)
	p := socketWith(t, map[string]any{"trust": []any{path}, "systemRoots": false})
	if _, failure := fetch(t, p, "db.internal", port); !strings.Contains(failure, "Tls") {
		t.Fatalf("old CA: %q, want a TLS failure", failure)
	}
	writeTemp(t, dir, "ca.pem", renewed.pem)
	later := time.Now().Add(time.Minute)
	os.Chtimes(path, later, later)
	if out, failure := fetch(t, p, "db.internal", port); out != "ok" {
		t.Fatalf("after renewal: %q %q", out, failure)
	}
	// A renewal that breaks the file keeps the last good one.
	writeTemp(t, dir, "ca.pem", []byte("not pem"))
	later = later.Add(time.Minute)
	os.Chtimes(path, later, later)
	if out, failure := fetch(t, p, "db.internal", port); out != "ok" {
		t.Fatalf("after a broken renewal: %q %q", out, failure)
	}
}

func TestBadCertificateSettingsAreStartupProblems(t *testing.T) {
	dir := t.TempDir()
	ca := newCA(t, "root")
	certPEM, keyPEM := ca.issue(t, "svc", true)
	_, otherKey := ca.issue(t, "other", true)
	caps := `{"Socket": {"type": "socket",
  "trust": ["` + filepath.ToSlash(filepath.Join(dir, "missing.pem")) + `", "` + filepath.ToSlash(writeTemp(t, dir, "junk.pem", []byte("junk"))) + `"],
  "clientCertificates": [
    {"hosts": ["db.internal"], "certificate": "` + filepath.ToSlash(writeTemp(t, dir, "c.crt", certPEM)) + `", "key": "` + filepath.ToSlash(writeTemp(t, dir, "wrong.key", otherKey)) + `"},
    {"hosts": ["*"], "certificate": "` + filepath.ToSlash(writeTemp(t, dir, "c2.crt", certPEM)) + `", "key": "` + filepath.ToSlash(writeTemp(t, dir, "c2.key", keyPEM)) + `"}
  ]}}`
	_, problems, err := LoadCapabilitiesFileWith(writeTemp(t, dir, "caps.json", []byte(caps)), nil, os.LookupEnv)
	if err != nil {
		t.Fatal(err)
	}
	all := strings.Join(problems, "\n")
	for _, want := range []string{"missing.pem", `"*" is neither`} {
		if !strings.Contains(all, want) {
			t.Errorf("problems = %q, want one naming %s", all, want)
		}
	}
	// Once the files exist and the hosts are right, a key that doesn't
	// match and PEM with no certificate are reported too.
	caps = strings.Replace(caps, filepath.ToSlash(filepath.Join(dir, "missing.pem")), filepath.ToSlash(writeTemp(t, dir, "ok.pem", ca.pem)), 1)
	caps = strings.Replace(caps, `"*"`, `"svc.internal"`, 1)
	_, problems, _ = LoadCapabilitiesFileWith(writeTemp(t, dir, "caps2.json", []byte(caps)), nil, os.LookupEnv)
	if all := strings.Join(problems, "\n"); !strings.Contains(all, "junk.pem holds no PEM certificate") {
		t.Errorf("problems = %q, want junk.pem refused", all)
	}
	_, problems, _ = LoadCapabilitiesFileWith(writeTemp(t, dir, "caps3.json", []byte(`{"Socket": {"type": "socket", "systemRoots": false}}`)), nil, os.LookupEnv)
	if len(problems) != 1 || !strings.Contains(problems[0], "trusts nothing") {
		t.Errorf("systemRoots false alone: %q", problems)
	}
}
