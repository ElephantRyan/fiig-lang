package runprogram

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"fiig-lang/compiler/loader"
	"fiig-lang/compiler/runtime"
	"fiig-lang/compiler/types"
	"fiig-lang/internal/fixture"
)

// ADR 0146: fiig.jwt from a program. A Secret only comes from the outside
// world, so signing and HMAC are tested here, with Go's crypto checking
// what fiig signs; and JwtKeys fetches from a local server, kept by a
// session that connects only to write (§5a).

var jwtApp = map[string]string{
	"fiig.pkg.json": `{
    "path": "jwttest.app",
    "kind": "application",
    "version": "1.0.0",
    "dependencies": { "fiig.jwt": "1.0.0" },
    "capabilities": ["Session", "Socket"]
}`,
	"signed.fiig": `use fiig.jwt;

activity signed(claims: string, key: Secret, algorithm: string, keyId: string | None) -> Result<string, jwt.JwtError> {
    decoded: Result<JsonValue, JsonError> = json.decode(claims);
    value = match (decoded) {
        Ok(given) -> given,
        Err(_) -> JsonValue.Null
    };
    return jwt.sign(value, key, jwt.algorithmNamed(algorithm) ?? jwt.Algorithm.RS256, keyId);
}`,
	"hmac_signed.fiig": `use fiig.jwt;

activity hmacSigned(claims: string, key: Secret, algorithm: string) -> Result<string, jwt.JwtError> {
    decoded: Result<JsonValue, JsonError> = json.decode(claims);
    value = match (decoded) {
        Ok(given) -> given,
        Err(_) -> JsonValue.Null
    };
    return jwt.signHmac(value, key, jwt.algorithmNamed(algorithm) ?? jwt.Algorithm.HS256);
}`,
	"hmac_verified.fiig": `use fiig.jwt;

activity hmacVerified(token: string, key: Secret, now: int) -> Result<string, jwt.JwtError> {
    hmac = [jwt.Algorithm.HS256, jwt.Algorithm.HS384, jwt.Algorithm.HS512];
    expect = jwt.Expect(issuer: "https://issuer.example", audience: "app", algorithms: hmac);
    verified = jwt.verifyHmac(token, key, expect, now)?;
    return Ok(verified.subject ?? "");
}`,
	"verified_from.fiig": `use fiig.jwt;
use fiig.jwt.JwtKeys;

activity verifiedFrom(token: string, url: string, issuer: string, discover: bool) -> Result<string, jwt.JwtError> {
    keys: List<jwt.Jwk> = [];
    if (discover) {
        keys = JwtKeys.forIssuer(issuer, jwt.keyIdOf(token))?;
    } else {
        keys = JwtKeys.fromUrl(url, jwt.keyIdOf(token))?;
    }

    algorithms = [jwt.Algorithm.RS256, jwt.Algorithm.ES256];
    expect = jwt.Expect(issuer: issuer, audience: "app", algorithms: algorithms);
    verified = jwt.verify(token, keys, expect, Clock.now())?;
    return Ok(verified.subject ?? "");
}`,
}

func jwtInterp(t *testing.T) (*runtime.Interp, *runtime.CapabilityRegistry) {
	t.Helper()
	root := t.TempDir()
	for name, src := range fixture.Module(jwtApp) {
		os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0o755)
		if err := os.WriteFile(filepath.Join(root, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
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
	RegisterDefaults(caps)
	in := runtime.New(u, caps)
	in.Imports = BuildImports(files, loaded.Packages)
	in.Groups = BuildGroups(files, loaded.Packages)
	in.Packages = loaded.Packages
	return in, caps
}

func call(t *testing.T, in *runtime.Interp, name string, args map[string]runtime.Value) runtime.Result {
	t.Helper()
	v, f := in.Call(context.Background(), name, args)
	if f != nil {
		t.Fatalf("%s: %s", name, f.Message)
	}
	return v.(runtime.Result)
}

func pemSecret(t *testing.T, key any) runtime.Secret {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return runtime.NewSecret("JWT_KEY", string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})))
}

func futureClaims(subject string) string {
	return fmt.Sprintf(`{"iss": "https://issuer.example", "sub": %q, "aud": "app", "exp": %d}`, subject, time.Now().Add(time.Hour).Unix())
}

func b64url(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// What fiig signs, Go's crypto verifies, with the header fiig wrote.
func TestFiigSignsWhatGoVerifies(t *testing.T) {
	in, caps := jwtInterp(t)
	defer caps.Close()
	rsaKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	ecKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	for _, c := range []struct {
		alg    string
		key    any
		verify func(digest, sig []byte) bool
	}{
		{"RS256", rsaKey, func(d, sig []byte) bool {
			return rsa.VerifyPKCS1v15(&rsaKey.PublicKey, crypto.SHA256, d, sig) == nil
		}},
		{"ES256", ecKey, func(d, sig []byte) bool {
			return len(sig) == 64 && ecdsa.Verify(&ecKey.PublicKey, d, new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:]))
		}},
	} {
		r := call(t, in, "signed", map[string]runtime.Value{
			"claims": runtime.Str(futureClaims("user-1")), "key": pemSecret(t, c.key), "algorithm": runtime.Str(c.alg), "keyId": runtime.Str("k1"),
		})
		if !r.Ok {
			t.Fatalf("%s: %v", c.alg, r)
		}
		parts := strings.Split(string(r.Value.(runtime.Str)), ".")
		header, _ := base64.RawURLEncoding.DecodeString(parts[0])
		var h map[string]string
		if err := json.Unmarshal(header, &h); err != nil || h["alg"] != c.alg || h["typ"] != "JWT" || h["kid"] != "k1" {
			t.Errorf("%s: header %s", c.alg, header)
		}
		sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
		digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
		if !c.verify(digest[:], sig) {
			t.Errorf("%s: Go doesn't verify fiig's signature", c.alg)
		}
	}
}

func TestHmacTokens(t *testing.T) {
	in, caps := jwtInterp(t)
	defer caps.Close()
	secret := runtime.NewSecret("JWT_SECRET", "a secret every instance shares")
	now := runtime.Int(time.Now().UnixMilli())
	for _, alg := range []string{"HS256", "HS384", "HS512"} {
		r := call(t, in, "hmacSigned", map[string]runtime.Value{"claims": runtime.Str(futureClaims("user-1")), "key": secret, "algorithm": runtime.Str(alg)})
		if !r.Ok {
			t.Fatalf("%s: %v", alg, r)
		}
		if v := call(t, in, "hmacVerified", map[string]runtime.Value{"token": r.Value, "key": secret, "now": now}); !v.Ok || v.Value != runtime.Str("user-1") {
			t.Errorf("%s: verified as %v", alg, v)
		}
		other := runtime.NewSecret("JWT_SECRET", "another")
		if v := call(t, in, "hmacVerified", map[string]runtime.Value{"token": r.Value, "key": other, "now": now}); v.Ok || !strings.Contains(v.Value.String(), "BadSignature") {
			t.Errorf("%s with another secret: %v", alg, v)
		}
	}
	// A token in Go's own HS256, checked by fiig.
	in2 := b64url([]byte(`{"alg":"HS256","typ":"JWT"}`)) + "." + b64url([]byte(futureClaims("user-2")))
	mac := hmacSha256([]byte("a secret every instance shares"), []byte(in2))
	if v := call(t, in, "hmacVerified", map[string]runtime.Value{"token": runtime.Str(in2 + "." + b64url(mac)), "key": secret, "now": now}); !v.Ok || v.Value != runtime.Str("user-2") {
		t.Errorf("Go's HS256: %v", v)
	}
	// sign signs no HMAC and no PSS; signHmac signs nothing else.
	rsaKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	for _, alg := range []string{"HS256", "PS256"} {
		r := call(t, in, "signed", map[string]runtime.Value{"claims": runtime.Str(futureClaims("x")), "key": pemSecret(t, rsaKey), "algorithm": runtime.Str(alg), "keyId": runtime.None{}})
		if r.Ok || !strings.Contains(r.Value.String(), "AlgorithmRefused") {
			t.Errorf("sign %s: %v", alg, r)
		}
	}
	if r := call(t, in, "hmacSigned", map[string]runtime.Value{"claims": runtime.Str(futureClaims("x")), "key": secret, "algorithm": runtime.Str("RS256")}); r.Ok {
		t.Errorf("signHmac RS256: %v", r)
	}
}

// keyServer serves a key set and a discovery document, counting fetches.
type keyServer struct {
	*httptest.Server
	mu      sync.Mutex
	keys    []map[string]string
	fetches int
	status  int
	issuer  string
}

func newKeyServer(t *testing.T) *keyServer {
	s := &keyServer{status: 200}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		switch r.URL.Path {
		case "/jwks.json":
			s.fetches++
			if s.status != 200 {
				w.WriteHeader(s.status)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"keys": s.keys})
		case "/.well-known/openid-configuration":
			issuer := s.issuer
			if issuer == "" {
				issuer = s.URL
			}
			json.NewEncoder(w).Encode(map[string]string{"issuer": issuer, "jwks_uri": s.URL + "/jwks.json"})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *keyServer) set(f func(s *keyServer)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f(s)
}

func (s *keyServer) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.fetches
}

func rsaJwk(kid string, k *rsa.PrivateKey) map[string]string {
	return map[string]string{"kty": "RSA", "kid": kid, "n": b64url(k.N.Bytes()), "e": b64url(big.NewInt(int64(k.E)).Bytes())}
}

// A token signed in Go with key, by issuer.
func goToken(t *testing.T, issuer, kid string, key *rsa.PrivateKey) string {
	t.Helper()
	header := b64url([]byte(fmt.Sprintf(`{"alg":"RS256","typ":"JWT","kid":%q}`, kid)))
	payload := b64url([]byte(fmt.Sprintf(`{"iss":%q,"sub":"user-1","aud":"app","exp":%d}`, issuer, time.Now().Add(time.Hour).Unix())))
	d := sha256.Sum256([]byte(header + "." + payload))
	sig, err := rsa.SignPKCS1v15(nil, key, crypto.SHA256, d[:])
	if err != nil {
		t.Fatal(err)
	}
	return header + "." + payload + "." + b64url(sig)
}

func TestJwtKeysKeepsTheKeySet(t *testing.T) {
	server := newKeyServer(t)
	in, caps := jwtInterp(t)
	defer caps.Close()
	k1, _ := rsa.GenerateKey(rand.Reader, 2048)
	k2, _ := rsa.GenerateKey(rand.Reader, 2048)
	server.set(func(s *keyServer) { s.keys = []map[string]string{rsaJwk("k1", k1)} })
	url := server.URL + "/jwks.json"
	verified := func(token string) runtime.Result {
		return call(t, in, "verifiedFrom", map[string]runtime.Value{"token": runtime.Str(token), "url": runtime.Str(url), "issuer": runtime.Str("https://issuer.example"), "discover": runtime.Bool(false)})
	}
	token := goToken(t, "https://issuer.example", "k1", k1)
	for i := 0; i < 3; i++ {
		if r := verified(token); !r.Ok || r.Value != runtime.Str("user-1") {
			t.Fatalf("verify %d: %v", i, r)
		}
	}
	if n := server.count(); n != 1 {
		t.Fatalf("three verifications fetched the key set %d times, want 1", n)
	}

	// The server closes the connection, as an idle HTTPS server does: the
	// keys stay, and the next verification fetches nothing (§5a).
	server.CloseClientConnections()
	time.Sleep(50 * time.Millisecond)
	if r := verified(token); !r.Ok {
		t.Fatalf("after the server closed the connection: %v", r)
	}
	if n := server.count(); n != 1 {
		t.Fatalf("after the close, fetches = %d, want 1", n)
	}

	// A kid the set doesn't have fetches again, but not within 30 seconds
	// of the last fetch, so made-up kids can't make the verifier hammer the
	// provider; a rotated-in key is found after that (jwksStep's own tests,
	// which set the clock, show it).
	server.set(func(s *keyServer) { s.keys = []map[string]string{rsaJwk("k1", k1), rsaJwk("k2", k2)} })
	if r := verified(goToken(t, "https://issuer.example", "k2", k2)); r.Ok || !strings.Contains(r.Value.String(), "NoKey") {
		t.Fatalf("a new kid within the cooldown: %v", r)
	}
	for i := 0; i < 3; i++ {
		if r := verified(goToken(t, "https://issuer.example", "made-up", k1)); r.Ok || !strings.Contains(r.Value.String(), "NoKey") {
			t.Fatalf("a made-up kid: %v", r)
		}
	}
	if n := server.count(); n != 1 {
		t.Fatalf("new kids within the cooldown fetched %d times in all, want 1", n)
	}
}

func TestJwtKeysDiscoversAnIssuersKeys(t *testing.T) {
	server := newKeyServer(t)
	in, caps := jwtInterp(t)
	defer caps.Close()
	k1, _ := rsa.GenerateKey(rand.Reader, 2048)
	server.set(func(s *keyServer) { s.keys = []map[string]string{rsaJwk("k1", k1)} })
	verified := func(issuer, token string) runtime.Result {
		return call(t, in, "verifiedFrom", map[string]runtime.Value{"token": runtime.Str(token), "url": runtime.Str(""), "issuer": runtime.Str(issuer), "discover": runtime.Bool(true)})
	}
	if r := verified(server.URL, goToken(t, server.URL, "k1", k1)); !r.Ok || r.Value != runtime.Str("user-1") {
		t.Fatalf("discovered: %v", r)
	}

	// A discovery document naming another issuer hands over no keys.
	other := newKeyServer(t)
	other.set(func(s *keyServer) { s.issuer = "https://someone-else.example" })
	if r := verified(other.URL, goToken(t, other.URL, "k1", k1)); r.Ok || !strings.Contains(r.Value.String(), "names another issuer") {
		t.Fatalf("another issuer's document: %v", r)
	}
}

func TestJwtKeysFailsClosed(t *testing.T) {
	server := newKeyServer(t)
	in, caps := jwtInterp(t)
	defer caps.Close()
	k1, _ := rsa.GenerateKey(rand.Reader, 2048)
	server.set(func(s *keyServer) { s.status = 503 })
	token := goToken(t, "https://issuer.example", "k1", k1)
	r := call(t, in, "verifiedFrom", map[string]runtime.Value{"token": runtime.Str(token), "url": runtime.Str(server.URL + "/jwks.json"), "issuer": runtime.Str("https://issuer.example"), "discover": runtime.Bool(false)})
	if r.Ok || !strings.Contains(r.Value.String(), "the server answered 503") {
		t.Fatalf("a 503: %v", r)
	}
	// Only https, but for a loopback host.
	r = call(t, in, "verifiedFrom", map[string]runtime.Value{"token": runtime.Str(token), "url": runtime.Str("http://keys.example/jwks.json"), "issuer": runtime.Str("https://issuer.example"), "discover": runtime.Bool(false)})
	if r.Ok || !strings.Contains(r.Value.String(), "must be https") {
		t.Fatalf("an http URL: %v", r)
	}
}

func hmacSha256(key, data []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write(data)
	return m.Sum(nil)
}
