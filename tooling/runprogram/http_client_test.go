package runprogram

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fiig-lang/compiler/loader"
	"fiig-lang/compiler/runtime"
	"fiig-lang/compiler/types"
)

// ADR 0074: fiig.http, the package built into the toolchain, found by the
// loader with no workspace, run against scripted servers.

var httpProgram = map[string]string{
	"fiig.pkg.json": `{
    "path": "httptest.app",
    "kind": "application",
    "version": "1.0.0",
    "dependencies": { "fiig.http": "1.0.0" },
    "capabilities": ["Socket"]
}`,
	"get.fiig": `use fiig.http
use fiig.http.HttpClient

gateway get(url: string) -> Result<http.Reply, http.HttpError>
    requires HttpClient
{
    return HttpClient.send(http.Request(method: http.Method.Get, url: url))
}`,
	"head.fiig": `use fiig.http
use fiig.http.HttpClient

gateway head(url: string) -> Result<http.Reply, http.HttpError>
    requires HttpClient
{
    return HttpClient.send(http.Request(method: http.Method.Head, url: url))
}`,
	"post_form.fiig": `use fiig.http
use fiig.http.HttpClient

gateway postForm(url: string, amount: string, note: string) -> Result<http.Reply, http.HttpError>
    requires HttpClient
{
    return HttpClient.send(http.Request(
        method: http.Method.Post,
        url: url,
        headers: [
            http.Header(name: "Authorization", value: "Bearer sk_test"),
            http.Header(name: "Content-Type", value: "application/x-www-form-urlencoded"),
        ],
        body: http.formEncode([
            http.Header(name: "amount", value: amount),
            http.Header(name: "note", value: note),
        ]),
    ))
}`,
	"with_header.fiig": `use fiig.http
use fiig.http.HttpClient

gateway withHeader(url: string, name: string, value: string, timeout: int) -> Result<http.Reply, http.HttpError>
    requires HttpClient
{
    return HttpClient.send(http.Request(
        method: http.Method.Delete,
        url: url,
        headers: [http.Header(name: name, value: value)],
        timeoutMillis: timeout,
    ))
}`,
	"segments.fiig": `use fiig.http
activity segments() -> string {
    return "${http.pathSegment("b-12_x.y~")}|${http.pathSegment("../../api/users")}|${http.pathSegment("a b?c#d%e")}|${http.pathSegment("Zoë")}|${http.pathSegment("..")}|${http.pathSegment(".")}|${http.pathSegment("")}"
}`,
	"header_of.fiig": `use fiig.http
activity headerOf(token: string) -> string {
    bare = http.Request(method: http.Method.Get, url: "http://x.example.com")
    sent = http.Request(method: http.Method.Get, url: "http://x.example.com", headers: [http.Header(name: "Authorization", value: token)])
    return "${http.headerValue(bare.headers, "authorization")} ${http.headerValue(sent.headers, "AUTHORIZATION")}"
}`,
}

// TestHeaderValueTakesARequestsHeaders is abs-recommendations GAPS G11:
// headerValue takes Request.headers as it is, List<Header> | None.
func TestHeaderValueTakesARequestsHeaders(t *testing.T) {
	in := httpInterp(t, &runtime.ScriptedSocket{})
	got, fault := in.Call(context.Background(), "headerOf", map[string]runtime.Value{"token": str("Bearer t1")})
	if fault != nil || got.String() != "None Bearer t1" {
		t.Fatalf("got %v, %v; want None Bearer t1", got, fault)
	}
}

// httpInterp writes httpProgram to a fresh root, loads it with no
// workspace roots, so fiig.http can only come from the toolchain, and
// returns an interpreter whose Socket is script.
func httpInterp(t *testing.T, script *runtime.ScriptedSocket) *runtime.Interp {
	t.Helper()
	root := t.TempDir()
	for name, src := range httpProgram {
		if err := os.WriteFile(filepath.Join(root, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	loaded, errs := loader.Load(root, nil)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	if e := loaded.Lockfile.Dependencies; len(e) != 1 || e[0].Path != "fiig.http" || e[0].Source != "builtin:http" {
		t.Errorf("lockfile = %+v, want fiig.http from builtin:http", e)
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
	caps.Register("Socket", runtime.SocketProvider{Backend: script})
	RegisterDefaults(caps)
	in := runtime.New(u, caps)
	in.Imports = BuildImports(files, loaded.Packages)
	in.Groups = BuildGroups(files, loaded.Packages)
	return in
}

func peer(host string, port int, steps ...runtime.SocketStep) *runtime.SocketPeer {
	return &runtime.SocketPeer{Host: host, Port: port, Steps: steps}
}

func expect(s string) runtime.SocketStep { return runtime.SocketStep{Expect: []byte(s)} }
func send(s string) runtime.SocketStep   { return runtime.SocketStep{Send: []byte(s)} }

var (
	tlsStep = runtime.SocketStep{StartTLS: true}
	hangup  = runtime.SocketStep{Hangup: true}
)

type httpCase struct {
	name  string
	entry string
	args  map[string]runtime.Value
	peers []*runtime.SocketPeer
	// want is the reply as "status body header=value...", or the error
	// variant and a fragment of its reason.
	want string
}

func str(s string) runtime.Value { return runtime.Str(s) }

func describeReply(v runtime.Value) string {
	r, ok := v.(runtime.Result)
	if !ok {
		return v.String()
	}
	if !r.Ok {
		ev := r.Value.(runtime.EnumValue)
		parts := []string{ev.Variant}
		for _, f := range ev.Fields {
			parts = append(parts, f.String())
		}
		return strings.Join(parts, " ")
	}
	rec := r.Value.(runtime.Record)
	out := rec.Fields["status"].String() + " " + string(rec.Fields["body"].(runtime.Bytes).B)
	for _, h := range rec.Fields["headers"].(runtime.List).Elems {
		hr := h.(runtime.Record)
		out += " " + string(hr.Fields["name"].(runtime.Str)) + "=" + string(hr.Fields["value"].(runtime.Str))
	}
	return out
}

func TestFiigHttpClient(t *testing.T) {
	const ua = "User-Agent: fiig-http/1\r\n"
	cases := []httpCase{
		{"https GET with Content-Length", "get", map[string]runtime.Value{"url": str("https://api.example.com/v1/items?limit=2#top")},
			[]*runtime.SocketPeer{peer("api.example.com", 443, tlsStep,
				expect("GET /v1/items?limit=2 HTTP/1.1\r\nHost: api.example.com\r\n"+ua+"Connection: close\r\n\r\n"),
				send("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 11\r\n\r\n{\"ok\":true}"))},
			`200 {"ok":true} Content-Type=application/json Content-Length=11`},
		{"plain http, a port, and a 404 is a reply", "get", map[string]runtime.Value{"url": str("http://localhost:8080")},
			[]*runtime.SocketPeer{peer("localhost", 8080,
				expect("GET / HTTP/1.1\r\nHost: localhost:8080\r\n"+ua+"Connection: close\r\n\r\n"),
				send("HTTP/1.1 404 Not Found\r\nContent-Length: 9\r\n\r\nnot found"))},
			"404 not found Content-Length=9"},
		{"a redirect is a reply", "get", map[string]runtime.Value{"url": str("http://old.example.com/a")},
			[]*runtime.SocketPeer{peer("old.example.com", 80,
				expect("GET /a HTTP/1.1\r\nHost: old.example.com\r\n"+ua+"Connection: close\r\n\r\n"),
				send("HTTP/1.1 301 Moved Permanently\r\nLocation: https://new.example.com/a\r\nContent-Length: 0\r\n\r\n"))},
			"301  Location=https://new.example.com/a Content-Length=0"},
		{"a form POST; 100 Continue skipped; a chunked reply in pieces", "postForm",
			map[string]runtime.Value{"url": str("https://pay.example.com/v1/charges"), "amount": str("1250"), "note": str("tea & cake/2")},
			[]*runtime.SocketPeer{peer("pay.example.com", 443, tlsStep,
				expect("POST /v1/charges HTTP/1.1\r\nHost: pay.example.com\r\nAuthorization: Bearer sk_test\r\nContent-Type: application/x-www-form-urlencoded\r\n"+ua+"Content-Length: 33\r\nConnection: close\r\n\r\namount=1250&note=tea+%26+cake%2F2"),
				send("HTTP/1.1 100 Continue\r\n\r\nHTTP/1.1 201 Created\r\nTransfer-Encoding: chunked\r\n\r\n4\r\nch_1"),
				send("\r\n6;ext=1\r\n23\r\n45\r\n0\r\nX-Trailer: dropped\r\n\r\n"))},
			"201 ch_123\r\n45 Transfer-Encoding=chunked"},
		{"HEAD has no body, whatever Content-Length says", "head", map[string]runtime.Value{"url": str("http://h.example.com/file")},
			[]*runtime.SocketPeer{peer("h.example.com", 80,
				expect("HEAD /file HTTP/1.1\r\nHost: h.example.com\r\n"+ua+"Connection: close\r\n\r\n"),
				send("HTTP/1.1 200 OK\r\nContent-Length: 5000\r\n\r\n"))},
			"200  Content-Length=5000"},
		{"no length: the body runs until close; folded header joined", "get", map[string]runtime.Value{"url": str("http://[::1]:9000/x")},
			[]*runtime.SocketPeer{peer("::1", 9000,
				expect("GET /x HTTP/1.1\r\nHost: [::1]:9000\r\n"+ua+"Connection: close\r\n\r\n"),
				send("HTTP/1.0 200 OK\r\nX-Long: one\r\n two\r\n\r\npart one, "), send("part two"), hangup)},
			"200 part one, part two X-Long=one two"},
		{"a reply cut short is malformed", "get", map[string]runtime.Value{"url": str("http://short.example.com/")},
			[]*runtime.SocketPeer{peer("short.example.com", 80,
				expect("GET / HTTP/1.1\r\nHost: short.example.com\r\n"+ua+"Connection: close\r\n\r\n"),
				send("HTTP/1.1 200 OK\r\nContent-Length: 10\r\n\r\nabc"), hangup)},
			"MalformedReply the server closed the connection after 3 of the 10 bytes its Content-Length promised"},
		{"a reply that isn't HTTP", "get", map[string]runtime.Value{"url": str("http://ssh.example.com:22/")},
			[]*runtime.SocketPeer{peer("ssh.example.com", 22,
				expect("GET / HTTP/1.1\r\nHost: ssh.example.com:22\r\n"+ua+"Connection: close\r\n\r\n"),
				send("SSH-2.0-OpenSSH_9.6\r\n\r\n"))},
			`MalformedReply the status line "SSH-2.0-OpenSSH_9.6" isn't HTTP/1.1`},
		{"the server's socket error", "get", map[string]runtime.Value{"url": str("http://down.example.com/")},
			[]*runtime.SocketPeer{{Host: "down.example.com", Port: 80, DialError: &runtime.SocketFailure{Variant: "Refused"}}},
			"Socket SocketError.Refused"},
		{"a header that sets its own User-Agent", "withHeader",
			map[string]runtime.Value{"url": str("http://a.example.com/r/1"), "name": str("user-agent"), "value": str("billing/2"), "timeout": runtime.Int(1000)},
			[]*runtime.SocketPeer{peer("a.example.com", 80,
				expect("DELETE /r/1 HTTP/1.1\r\nHost: a.example.com\r\nuser-agent: billing/2\r\nConnection: close\r\n\r\n"),
				send("HTTP/1.1 204 No Content\r\n\r\n"))},
			"204 "},
	}
	refusals := []struct{ name, url, header, value, fragment string }{
		{"user info", "https://me:pw@api.example.com/", "X-A", "1", "has user information"},
		{"scheme", "ftp://files.example.com/", "X-A", "1", "must start with http:// or https://"},
		{"space", "http://a.example.com/a b", "X-A", "1", "has a space, a control character, or a character outside ASCII"},
		{"no host", "http:///path", "X-A", "1", "has no host"},
		{"port", "http://a.example.com:99999/", "X-A", "1", "a port is 1 to 65535"},
		{"owned header", "http://a.example.com/", "content-length", "3", "send writes the Content-Length header itself"},
		{"header name", "http://a.example.com/", "X A", "1", "isn't an HTTP token"},
		{"CRLF in a value", "http://a.example.com/", "X-A", "1\r\nX-Admin: yes", "has a CR, LF, or NUL"},
	}
	for _, r := range refusals {
		cases = append(cases, httpCase{"refuses " + r.name, "withHeader",
			map[string]runtime.Value{"url": str(r.url), "name": str(r.header), "value": str(r.value), "timeout": runtime.Int(1000)},
			nil, "InvalidRequest " + r.fragment})
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			script := &runtime.ScriptedSocket{Peers: c.peers}
			v, fault := httpInterp(t, script).Call(context.Background(), c.entry, c.args)
			if fault != nil {
				t.Fatalf("fault: %v", fault)
			}
			got := describeReply(v)
			variant, fragment, _ := strings.Cut(c.want, " ")
			if !strings.HasPrefix(got, variant+" ") || !strings.Contains(got, fragment) {
				t.Errorf("got  %q\nwant %q", got, c.want)
			}
			if err := script.Done(); err != nil {
				t.Error(err)
			}
			for _, p := range c.peers {
				if p.DialError == nil && !p.Closed() {
					t.Errorf("%s:%d was left open", p.Host, p.Port)
				}
			}
		})
	}
}

// §5: a server that accepts and never answers ends at timeoutMillis, as a
// value the caller can act on.
func TestFiigHttpTimesOut(t *testing.T) {
	script := &runtime.ScriptedSocket{Peers: []*runtime.SocketPeer{peer("slow.example.com", 80,
		expect("DELETE / HTTP/1.1\r\nHost: slow.example.com\r\nX-A: 1\r\nUser-Agent: fiig-http/1\r\nConnection: close\r\n\r\n"),
		runtime.SocketStep{Stall: true})}}
	v, fault := httpInterp(t, script).Call(context.Background(), "withHeader", map[string]runtime.Value{
		"url": str("http://slow.example.com"), "name": str("X-A"), "value": str("1"), "timeout": runtime.Int(50),
	})
	if fault != nil {
		t.Fatal(fault)
	}
	if got := describeReply(v); got != "Socket SocketError.TimedOut" {
		t.Errorf("got %q, want Socket(TimedOut)", got)
	}
}

// §1: fiig. is the toolchain's.
func TestFiigPrefixIsReserved(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "fiig.pkg.json"), []byte(`{"path": "fiig.mine", "kind": "library", "version": "1.0.0"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, errs := loader.Load(root, nil)
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), `"fiig.mine" starts with "fiig.", which is reserved`) {
		t.Errorf("errs = %v", errs)
	}
}

// Three checker gaps fiig.http's first caller found: another package's
// enum variant written with its alias, a qualified type whose alias the
// file never imports, and a field of another package's model whose type
// names that package's declarations (reply.headers is List<Header>).
func TestCheckingAgainstAnotherPackage(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"variant and field types", `use fiig.http

conversion firstHeader(reply: http.Reply, method: http.Method) -> string {
    match method {
        http.Method.Get -> None
        _ -> None
    }
    failure = http.HttpError.MalformedReply(reason: "x")
    match failure {
        http.HttpError.MalformedReply(reason) -> None
        _ -> None
    }
    for header in reply.headers {
        return header.name
    }
    return ""
}`, ""},
		{"no such variant", `use fiig.http

conversion pick() -> http.Method {
    return http.Method.Fetch
}`, `Method has no variant "Fetch"`},
		{"a declaration as a value", `use fiig.http

conversion pick() -> string {
    m = http.Method
    return ""
}`, "http.Method is a declaration, not a value"},
		{"an alias the file never imports", `model Holder {
    reply: http.Reply
}`, `unresolved type "http.Reply": this file has no use line for "http"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			files := map[string]string{"fiig.pkg.json": httpProgram["fiig.pkg.json"], "decl.fiig": c.src}
			for name, src := range files {
				if err := os.WriteFile(filepath.Join(root, name), []byte(src), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			loaded, errs := loader.Load(root, nil)
			if len(errs) > 0 {
				t.Fatal(errs)
			}
			parsed, _, err := LoadFiles(root)
			if err != nil {
				t.Fatal(err)
			}
			diags := Check(root, parsed, loaded.Packages)
			if c.want == "" {
				if len(diags) > 0 {
					t.Errorf("diags = %v", diags)
				}
				return
			}
			found := false
			for _, d := range diags {
				found = found || strings.Contains(d.Msg, c.want)
			}
			if !found {
				t.Errorf("diags = %v, want one containing %q", diags, c.want)
			}
		})
	}
}

// ADR 0101 (abs-recommendations GAPS G34): pathSegment encodes everything
// that could change a URL's shape, and refuses what encoding can't save.
func TestPathSegment(t *testing.T) {
	in := httpInterp(t, &runtime.ScriptedSocket{})
	got, fault := in.Call(context.Background(), "segments", nil)
	want := "b-12_x.y~|..%2F..%2Fapi%2Fusers|a%20b%3Fc%23d%25e|Zo%C3%AB|None|None|None"
	if fault != nil || got.String() != want {
		t.Fatalf("got %v, %v\nwant %s", got, fault, want)
	}
}
