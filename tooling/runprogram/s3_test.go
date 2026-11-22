package runprogram

// Generated from a SigV4 reference that shares no code with fiig.s3 (see
// ADR 0121): every request's bytes, signature included, are what the
// reference signs for the same inputs.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fiig-lang/compiler/loader"
	"fiig-lang/compiler/runtime"
	"fiig-lang/compiler/types"
	"fiig-lang/internal/fixture"
)

// ADR 0121: fiig.s3, the package built into the toolchain, signs and sends
// requests through fiig.http to scripted servers.

var s3Program = map[string]string{
	"fiig.pkg.json": `{
    "path": "s3test.app",
    "kind": "application",
    "version": "1.0.0",
    "dependencies": { "fiig.s3": "1.0.0" },
    "capabilities": ["Socket", "HttpClient"]
}`,
	"upload.fiig": `use fiig.s3
use fiig.s3.S3

gateway upload(bucket: string, key: string, text: string) -> Result<None, s3.S3Error>
    requires S3
{
    return S3.put(bucket, key, text.utf8(), "text/plain");
}`,
	"download.fiig": `use fiig.s3
use fiig.s3.S3

gateway download(bucket: string, key: string) -> Result<bytes, s3.S3Error>
    requires S3
{
    return S3.get(bucket, key);
}`,
	"inspect.fiig": `use fiig.s3
use fiig.s3.S3

gateway inspect(bucket: string, key: string) -> Result<s3.ObjectInfo, s3.S3Error>
    requires S3
{
    return S3.head(bucket, key);
}`,
	"remove.fiig": `use fiig.s3
use fiig.s3.S3

gateway remove(bucket: string, key: string) -> Result<None, s3.S3Error>
    requires S3
{
    return S3.delete(bucket, key);
}`,
	"tagged.fiig": `use fiig.s3
use fiig.s3.S3

gateway tagged(bucket: string, key: string, text: string) -> Result<None, s3.S3Error>
    requires S3
{
    return S3.putWithMetadata(bucket, key, text.utf8(), "text/plain", ["run": "7", "Author": "Ada"]);
}`,
	"mistagged.fiig": `use fiig.s3
use fiig.s3.S3

gateway mistagged(bucket: string, key: string) -> Result<None, s3.S3Error>
    requires S3
{
    return S3.putWithMetadata(bucket, key, <<>>, "", ["note": "one\r\nx-amz-acl: public-read"]);
}`,
	"fetch.fiig": `use fiig.s3
use fiig.s3.S3

gateway fetch(bucket: string, key: string) -> Result<s3.S3Object, s3.S3Error>
    requires S3
{
    return S3.getObject(bucket, key);
}`,
	"middle.fiig": `use fiig.s3
use fiig.s3.S3

gateway middle(bucket: string, key: string) -> Result<bytes, s3.S3Error>
    requires S3
{
    return S3.getRange(bucket, key, 1, 3);
}`,
	"rest.fiig": `use fiig.s3
use fiig.s3.S3

gateway rest(bucket: string, key: string) -> Result<bytes, s3.S3Error>
    requires S3
{
    return S3.getRange(bucket, key, 2, None);
}`,
	"archive.fiig": `use fiig.s3
use fiig.s3.S3

gateway archive(bucket: string, key: string) -> Result<None, s3.S3Error>
    requires S3
{
    return S3.copy(bucket, key, "archive", "2026/${key}");
}`,
	"share.fiig": `use fiig.s3
use fiig.s3.S3

gateway share(bucket: string, key: string, seconds: int) -> Result<string, s3.S3Error>
    requires S3
{
    return S3.presign("GET", bucket, key, seconds);
}`,
}

// s3Interp loads s3Program with the given settings; the clock stands at
// 2023-11-14T22:13:20Z.
func s3Interp(t *testing.T, script *runtime.ScriptedSocket, env map[string]string) *runtime.Interp {
	t.Helper()
	root := t.TempDir()
	for name, src := range fixture.Module(s3Program) {
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
	caps.Register("Socket", runtime.SocketProvider{Backend: script})
	caps.Register("Clock", runtime.ClockProvider{Clock: runtime.NewManualClock(time.UnixMilli(1700000000000).UTC())})
	RegisterDefaults(caps)
	in := runtime.New(u, caps)
	in.Imports = BuildImports(files, loaded.Packages)
	in.Groups = BuildGroups(files, loaded.Packages)
	in.Packages = loaded.Packages
	lookup := func(name string) (string, bool) { v, ok := env[name]; return v, ok }
	program := Program{Universe: u, Imports: in.Imports, Groups: in.Groups}
	entries := []Entry{{Name: "upload"}, {Name: "download"}, {Name: "inspect"}, {Name: "remove"},
		{Name: "tagged"}, {Name: "mistagged"}, {Name: "fetch"}, {Name: "middle"}, {Name: "rest"}, {Name: "archive"},
		{Name: "share"}}
	if problems := in.LoadConfig(ReachableConfigFrom(program, entries), lookup); len(problems) > 0 {
		t.Fatal(problems)
	}
	return in
}

func s3Env() map[string]string {
	return map[string]string{
		"S3_ENDPOINT": "http://localhost:19000", "S3_ACCESS_KEY_ID": "AKIDTEST",
		"S3_SECRET_ACCESS_KEY": "SECRETTEST", "S3_REGION": "us-east-1",
	}
}

// s3Call runs one gateway against one scripted reply and returns its result
// as text; the script must run to its end.
func s3Call(t *testing.T, env map[string]string, entry string, args map[string]runtime.Value, steps ...runtime.SocketStep) string {
	t.Helper()
	script := &runtime.ScriptedSocket{Peers: []*runtime.SocketPeer{peer("localhost", 19000, steps...)}}
	v, fault := s3Interp(t, script, env).Call(context.Background(), entry, args)
	if fault != nil {
		t.Fatalf("%s faulted: %v", entry, fault)
	}
	if err := script.Done(); err != nil {
		t.Errorf("script: %v", err)
	}
	return fmt.Sprint(v)
}

func s3Args(key string) map[string]runtime.Value {
	return map[string]runtime.Value{"bucket": str("photos"), "key": str(key)}
}

// A PUT sends its body's SHA-256 in base64 for S3 to check, and signs it.
func TestS3PutSendsTheSignedRequestTheReferenceSigns(t *testing.T) {
	args := s3Args("a b.txt")
	args["text"] = str("hello")
	got := s3Call(t, s3Env(), "upload", args,
		expect("PUT /photos/a%20b.txt HTTP/1.1\r\nHost: localhost:19000\r\nx-amz-content-sha256: 2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824\r\nx-amz-date: 20231114T221320Z\r\nAuthorization: AWS4-HMAC-SHA256 Credential=AKIDTEST/20231114/us-east-1/s3/aws4_request, SignedHeaders=host;x-amz-checksum-sha256;x-amz-content-sha256;x-amz-date, Signature=e1760d9994cdb2712709738342d87eb1f98ac2fe505eab3fb1a7796a8b40d4f7\r\nx-amz-checksum-sha256: LPJNul+wow4m6DsqxbninhsWHlwfp0JecwQzYpOLmCQ=\r\nContent-Type: text/plain\r\nUser-Agent: fiig-http/1\r\nContent-Length: 5\r\n\r\nhello"),
		send("HTTP/1.1 200 OK\r\nETag: \"5d41402abc4b2a76b9719d911017c592\"\r\nContent-Length: 0\r\n\r\n"))
	if got != "Ok(None)" {
		t.Errorf("upload = %s, want Ok(None)", got)
	}
}

func TestS3GetReturnsTheBody(t *testing.T) {
	got := s3Call(t, s3Env(), "download", s3Args("a b.txt"),
		expect("GET /photos/a%20b.txt HTTP/1.1\r\nHost: localhost:19000\r\nx-amz-content-sha256: e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855\r\nx-amz-date: 20231114T221320Z\r\nAuthorization: AWS4-HMAC-SHA256 Credential=AKIDTEST/20231114/us-east-1/s3/aws4_request, SignedHeaders=host;x-amz-content-sha256;x-amz-date, Signature=4fb19e420ec6c4c1b1dfc6335654acf2f3d81371dc8f6d7ceef2f265c139b128\r\nUser-Agent: fiig-http/1\r\n\r\n"),
		send("HTTP/1.1 200 OK\r\nContent-Length: 5\r\n\r\nhello"))
	if !strings.HasPrefix(got, "Ok(") || !strings.Contains(got, "104, 101, 108, 108, 111") {
		t.Errorf("download = %s, want the bytes of hello", got)
	}
}

func TestS3AMissingKeyIsNotFound(t *testing.T) {
	got := s3Call(t, s3Env(), "download", s3Args("a b.txt"),
		expect("GET /photos/a%20b.txt HTTP/1.1\r\nHost: localhost:19000\r\nx-amz-content-sha256: e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855\r\nx-amz-date: 20231114T221320Z\r\nAuthorization: AWS4-HMAC-SHA256 Credential=AKIDTEST/20231114/us-east-1/s3/aws4_request, SignedHeaders=host;x-amz-content-sha256;x-amz-date, Signature=4fb19e420ec6c4c1b1dfc6335654acf2f3d81371dc8f6d7ceef2f265c139b128\r\nUser-Agent: fiig-http/1\r\n\r\n"),
		send("HTTP/1.1 404 Not Found\r\nContent-Type: application/xml\r\nContent-Length: 62\r\n\r\n<Error><Code>NoSuchKey</Code><Message>Not here</Message></Error>"))
	if got != "Err(S3Error.NotFound)" {
		t.Errorf("download = %s, want NotFound", got)
	}
}

func TestS3ARefusalCarriesTheServersCode(t *testing.T) {
	body := "<Error><Code>SignatureDoesNotMatch</Code><Message>bad signature</Message></Error>"
	got := s3Call(t, s3Env(), "download", s3Args("a b.txt"),
		expect("GET /photos/a%20b.txt HTTP/1.1\r\nHost: localhost:19000\r\nx-amz-content-sha256: e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855\r\nx-amz-date: 20231114T221320Z\r\nAuthorization: AWS4-HMAC-SHA256 Credential=AKIDTEST/20231114/us-east-1/s3/aws4_request, SignedHeaders=host;x-amz-content-sha256;x-amz-date, Signature=4fb19e420ec6c4c1b1dfc6335654acf2f3d81371dc8f6d7ceef2f265c139b128\r\nUser-Agent: fiig-http/1\r\n\r\n"),
		send(fmt.Sprintf("HTTP/1.1 403 Forbidden\r\nContent-Length: %d\r\n\r\n%s", len(body), body)))
	if !strings.Contains(got, "Refused(403, SignatureDoesNotMatch, bad signature)") {
		t.Errorf("download = %s, want Refused(403, SignatureDoesNotMatch, ...)", got)
	}
	if strings.Contains(got, "SECRETTEST") {
		t.Errorf("the error repeats the secret key: %s", got)
	}
}

func TestS3HeadReadsTheObjectsInfo(t *testing.T) {
	got := s3Call(t, s3Env(), "inspect", s3Args("a b.txt"),
		expect("HEAD /photos/a%20b.txt HTTP/1.1\r\nHost: localhost:19000\r\nx-amz-content-sha256: e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855\r\nx-amz-date: 20231114T221320Z\r\nAuthorization: AWS4-HMAC-SHA256 Credential=AKIDTEST/20231114/us-east-1/s3/aws4_request, SignedHeaders=host;x-amz-content-sha256;x-amz-date, Signature=f98daa355ff949d8143cfabce675027a828575ca989f390ceef0cad1495b8eca\r\nUser-Agent: fiig-http/1\r\n\r\n"),
		send("HTTP/1.1 200 OK\r\nContent-Length: 1234\r\nContent-Type: image/png\r\nETag: \"abc\"\r\n\r\n"))
	if !strings.Contains(got, "size: 1234") || !strings.Contains(got, "contentType: image/png") {
		t.Errorf("inspect = %s", got)
	}
}

func TestS3DeleteIsDoneOn204(t *testing.T) {
	got := s3Call(t, s3Env(), "remove", s3Args("a b.txt"),
		expect("DELETE /photos/a%20b.txt HTTP/1.1\r\nHost: localhost:19000\r\nx-amz-content-sha256: e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855\r\nx-amz-date: 20231114T221320Z\r\nAuthorization: AWS4-HMAC-SHA256 Credential=AKIDTEST/20231114/us-east-1/s3/aws4_request, SignedHeaders=host;x-amz-content-sha256;x-amz-date, Signature=408f0fbec0af747614c0fafb978f30086624a6c415040eb33359fdfbda7ac9c2\r\nUser-Agent: fiig-http/1\r\n\r\n"),
		send("HTTP/1.1 204 No Content\r\n\r\n"))
	if got != "Ok(None)" {
		t.Errorf("remove = %s, want Ok(None)", got)
	}
}

// A session token (temporary credentials) is sent and signed.
func TestS3ASessionTokenIsSentAndSigned(t *testing.T) {
	env := s3Env()
	env["S3_SESSION_TOKEN"] = "TOKEN-XYZ"
	got := s3Call(t, env, "download", s3Args("a b.txt"),
		expect("GET /photos/a%20b.txt HTTP/1.1\r\nHost: localhost:19000\r\nx-amz-content-sha256: e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855\r\nx-amz-date: 20231114T221320Z\r\nAuthorization: AWS4-HMAC-SHA256 Credential=AKIDTEST/20231114/us-east-1/s3/aws4_request, SignedHeaders=host;x-amz-content-sha256;x-amz-date;x-amz-security-token, Signature=fe659081ba884f20b51d0cab8e3a1e60117398e503fae97853275741351f32b4\r\nx-amz-security-token: TOKEN-XYZ\r\nUser-Agent: fiig-http/1\r\n\r\n"),
		send("HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n"))
	if !strings.HasPrefix(got, "Ok(") {
		t.Errorf("download = %s", got)
	}
}

// A bad call or bad settings never reach the network: the script has no peer
// steps, so any byte written fails it.
func TestS3ABadKeyOrEndpointNeverConnects(t *testing.T) {
	script := &runtime.ScriptedSocket{}
	in := s3Interp(t, script, s3Env())
	v, fault := in.Call(context.Background(), "download", s3Args(".."))
	if fault != nil || !strings.Contains(fmt.Sprint(v), "InvalidRequest") {
		t.Errorf("a .. key: %v %v, want InvalidRequest", v, fault)
	}
	env := s3Env()
	env["S3_ENDPOINT"] = "ftp://storage.example.com"
	v, fault = s3Interp(t, &runtime.ScriptedSocket{}, env).Call(context.Background(), "download", s3Args("a"))
	if fault != nil || !strings.Contains(fmt.Sprint(v), "InvalidConfig") {
		t.Errorf("an ftp endpoint: %v %v, want InvalidConfig", v, fault)
	}
}

// putWithMetadata sends each key as x-amz-meta-<key> in lower case, sorted,
// beside the checksum, and signs them all.
func TestS3PutWithMetadataSignsTheMetadata(t *testing.T) {
	args := s3Args("a b.txt")
	args["text"] = str("hello")
	got := s3Call(t, s3Env(), "tagged", args,
		expect("PUT /photos/a%20b.txt HTTP/1.1\r\nHost: localhost:19000\r\nx-amz-content-sha256: 2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824\r\nx-amz-date: 20231114T221320Z\r\nAuthorization: AWS4-HMAC-SHA256 Credential=AKIDTEST/20231114/us-east-1/s3/aws4_request, SignedHeaders=host;x-amz-checksum-sha256;x-amz-content-sha256;x-amz-date;x-amz-meta-author;x-amz-meta-run, Signature=06d7c3627bfbbbc5fd1fae8296f7d06413ead34ed256bc5446e2fa492568f457\r\nx-amz-meta-author: Ada\r\nx-amz-meta-run: 7\r\nx-amz-checksum-sha256: LPJNul+wow4m6DsqxbninhsWHlwfp0JecwQzYpOLmCQ=\r\nContent-Type: text/plain\r\nUser-Agent: fiig-http/1\r\nContent-Length: 5\r\n\r\nhello"),
		send("HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n"))
	if got != "Ok(None)" {
		t.Errorf("tagged = %s, want Ok(None)", got)
	}
}

// A metadata value that would add a header of its own is refused before
// any byte is written.
func TestS3MetadataWithALineBreakNeverConnects(t *testing.T) {
	v, fault := s3Interp(t, &runtime.ScriptedSocket{}, s3Env()).Call(context.Background(), "mistagged", s3Args("a"))
	if fault != nil || !strings.Contains(fmt.Sprint(v), "InvalidRequest") {
		t.Errorf("a value with CR LF: %v %v, want InvalidRequest", v, fault)
	}
}

func TestS3GetObjectReturnsTheBodyAndItsMetadata(t *testing.T) {
	got := s3Call(t, s3Env(), "fetch", s3Args("a b.txt"),
		expect("GET /photos/a%20b.txt HTTP/1.1\r\nHost: localhost:19000\r\nx-amz-content-sha256: e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855\r\nx-amz-date: 20231114T221320Z\r\nAuthorization: AWS4-HMAC-SHA256 Credential=AKIDTEST/20231114/us-east-1/s3/aws4_request, SignedHeaders=host;x-amz-content-sha256;x-amz-date, Signature=4fb19e420ec6c4c1b1dfc6335654acf2f3d81371dc8f6d7ceef2f265c139b128\r\nUser-Agent: fiig-http/1\r\n\r\n"),
		send("HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nX-Amz-Meta-Author: Ada\r\nx-amz-meta-run: 7\r\nContent-Length: 5\r\n\r\nhello"))
	for _, want := range []string{"104, 101, 108, 108, 111", "size: 5", "contentType: text/plain", "author: Ada", "run: 7"} {
		if !strings.Contains(got, want) {
			t.Errorf("fetch = %s, want %q in it", got, want)
		}
	}
}

func TestS3HeadReadsTheMetadata(t *testing.T) {
	got := s3Call(t, s3Env(), "inspect", s3Args("a b.txt"),
		expect("HEAD /photos/a%20b.txt HTTP/1.1\r\nHost: localhost:19000\r\nx-amz-content-sha256: e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855\r\nx-amz-date: 20231114T221320Z\r\nAuthorization: AWS4-HMAC-SHA256 Credential=AKIDTEST/20231114/us-east-1/s3/aws4_request, SignedHeaders=host;x-amz-content-sha256;x-amz-date, Signature=f98daa355ff949d8143cfabce675027a828575ca989f390ceef0cad1495b8eca\r\nUser-Agent: fiig-http/1\r\n\r\n"),
		send("HTTP/1.1 200 OK\r\nContent-Length: 12\r\nx-amz-meta-run: 7\r\n\r\n"))
	if !strings.Contains(got, "size: 12") || !strings.Contains(got, "run: 7") {
		t.Errorf("inspect = %s", got)
	}
}

// A ranged GET signs its Range header and returns a 206's body.
func TestS3GetRangeSignsTheRangeAndReturnsThePart(t *testing.T) {
	got := s3Call(t, s3Env(), "middle", s3Args("a b.txt"),
		expect("GET /photos/a%20b.txt HTTP/1.1\r\nHost: localhost:19000\r\nx-amz-content-sha256: e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855\r\nx-amz-date: 20231114T221320Z\r\nAuthorization: AWS4-HMAC-SHA256 Credential=AKIDTEST/20231114/us-east-1/s3/aws4_request, SignedHeaders=host;range;x-amz-content-sha256;x-amz-date, Signature=57928a65bfd91963d74c3ce774aa4efa884200b7e0e8c376d1e912e42dac797f\r\nRange: bytes=1-3\r\nUser-Agent: fiig-http/1\r\n\r\n"),
		send("HTTP/1.1 206 Partial Content\r\nContent-Range: bytes 1-3/5\r\nContent-Length: 3\r\n\r\nell"))
	if !strings.HasPrefix(got, "Ok(") || !strings.Contains(got, "101, 108, 108") || strings.Contains(got, "104") {
		t.Errorf("middle = %s, want the bytes of ell", got)
	}
}

// A range that starts past the object's end is OutOfRange.
func TestS3ARangePastTheEndIsOutOfRange(t *testing.T) {
	body := "<Error><Code>InvalidRange</Code><Message>The requested range is not satisfiable</Message></Error>"
	got := s3Call(t, s3Env(), "rest", s3Args("a b.txt"),
		expect("GET /photos/a%20b.txt HTTP/1.1\r\nHost: localhost:19000\r\nx-amz-content-sha256: e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855\r\nx-amz-date: 20231114T221320Z\r\nAuthorization: AWS4-HMAC-SHA256 Credential=AKIDTEST/20231114/us-east-1/s3/aws4_request, SignedHeaders=host;range;x-amz-content-sha256;x-amz-date, Signature=782785e974d003f02c61c9a26466db5c6e84d5f8388802213b08de6bf386d198\r\nRange: bytes=2-\r\nUser-Agent: fiig-http/1\r\n\r\n"),
		send(fmt.Sprintf("HTTP/1.1 416 Requested Range Not Satisfiable\r\nContent-Range: bytes */1\r\nContent-Length: %d\r\n\r\n%s", len(body), body)))
	if got != "Err(S3Error.OutOfRange)" {
		t.Errorf("rest = %s, want OutOfRange", got)
	}
}

// copyExpect is the request archive sends: a PUT of archive/2026/a b.txt
// naming its source path-style and encoded, signed.
func copyExpect() runtime.SocketStep {
	return expect("PUT /archive/2026/a%20b.txt HTTP/1.1\r\nHost: localhost:19000\r\nx-amz-content-sha256: e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855\r\nx-amz-date: 20231114T221320Z\r\nAuthorization: AWS4-HMAC-SHA256 Credential=AKIDTEST/20231114/us-east-1/s3/aws4_request, SignedHeaders=host;x-amz-content-sha256;x-amz-copy-source;x-amz-date, Signature=0b471285867b826375b08986224267f5f2cb3a2d190f42887370d1e7909b457e\r\nx-amz-copy-source: /photos/a%20b.txt\r\nUser-Agent: fiig-http/1\r\nContent-Length: 0\r\n\r\n")
}

func TestS3CopySendsTheSignedSource(t *testing.T) {
	body := "<CopyObjectResult><LastModified>2023-11-14T22:13:20.000Z</LastModified><ETag>\"5d41\"</ETag></CopyObjectResult>"
	got := s3Call(t, s3Env(), "archive", s3Args("a b.txt"), copyExpect(),
		send(fmt.Sprintf("HTTP/1.1 200 OK\r\nContent-Length: %d\r\n\r\n%s", len(body), body)))
	if got != "Ok(None)" {
		t.Errorf("archive = %s, want Ok(None)", got)
	}
}

// S3 can answer a copy 200 and fail it in the body; that is a refusal.
func TestS3ACopyThatFailsAfter200IsRefused(t *testing.T) {
	body := "<Error><Code>InternalError</Code><Message>try again</Message></Error>"
	got := s3Call(t, s3Env(), "archive", s3Args("a b.txt"), copyExpect(),
		send(fmt.Sprintf("HTTP/1.1 200 OK\r\nContent-Length: %d\r\n\r\n%s", len(body), body)))
	if !strings.Contains(got, "Refused(200, InternalError, try again)") {
		t.Errorf("archive = %s, want Refused(200, InternalError, ...)", got)
	}
}

// presign reads the settings and the clock and signs a URL, and makes no
// connection: the script has no peers, so any byte written fails it.
func TestS3PresignMakesAURLWithoutConnecting(t *testing.T) {
	script := &runtime.ScriptedSocket{}
	in := s3Interp(t, script, s3Env())
	args := s3Args("a b.txt")
	args["seconds"] = runtime.Int(600)
	v, fault := in.Call(context.Background(), "share", args)
	if fault != nil {
		t.Fatalf("share faulted: %v", fault)
	}
	want := "http://localhost:19000/photos/a%20b.txt?X-Amz-Algorithm=AWS4-HMAC-SHA256&X-Amz-Credential=AKIDTEST%2F20231114%2Fus-east-1%2Fs3%2Faws4_request&X-Amz-Date=20231114T221320Z&X-Amz-Expires=600&X-Amz-SignedHeaders=host&X-Amz-Signature=b0067905d3e91059962b10b8d9211020cf850c55e7c716c7b296b8b01b40710b"
	if got := fmt.Sprint(v); !strings.Contains(got, want) {
		t.Errorf("share = %s, want Ok(%s)", got, want)
	}
	if err := script.Done(); err != nil {
		t.Errorf("script: %v", err)
	}
	args["seconds"] = runtime.Int(604801)
	v, fault = in.Call(context.Background(), "share", args)
	if fault != nil || !strings.Contains(fmt.Sprint(v), "InvalidRequest") {
		t.Errorf("an eight-day link: %v %v, want InvalidRequest", v, fault)
	}
}
