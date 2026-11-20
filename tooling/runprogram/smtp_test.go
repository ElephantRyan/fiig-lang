package runprogram

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

// ADR 0120: fiig.smtp, the package built into the toolchain, sends a mail
// through scripted servers, byte for byte.

var smtpProgram = map[string]string{
	"fiig.pkg.json": `{
    "path": "smtptest.app",
    "kind": "application",
    "version": "1.0.0",
    "dependencies": { "fiig.smtp": "1.0.0" },
    "capabilities": ["Socket"]
}`,
	"send_welcome.fiig": `use fiig.smtp
use fiig.smtp.Smtp

gateway sendWelcome(to: string, subject: string) -> Result<None, smtp.SmtpError>
    requires Smtp
{
    return Smtp.send(smtp.Mail(from: "noreply@shop.example", to: [to], subject: subject, body: "Thanks for signing up."))
}`,
}

// The instant the manual clock starts at, as the Date header writes it.
var smtpNow = time.Date(2026, 11, 20, 13, 59, 58, 0, time.UTC)

const smtpDate = "Fri, 20 Nov 2026 13:59:58 +0000"

// smtpToken is the Message-ID's token: the first 12 bytes of the seeded
// random source the test gives the program, in hex.
func smtpToken(t *testing.T) string {
	t.Helper()
	v, fault := runtime.NewSeededRandomProvider(7).Call(context.Background(), "bytes", []runtime.Value{runtime.Int(12)})
	if fault != nil {
		t.Fatal(fault)
	}
	b, ok := v.(runtime.Bytes)
	if !ok {
		t.Fatalf("Random.bytes gave %T", v)
	}
	return fmt.Sprintf("%x", b.B)
}

func smtpInterp(t *testing.T, script *runtime.ScriptedSocket, url string) *runtime.Interp {
	t.Helper()
	root := t.TempDir()
	for name, src := range fixture.Module(smtpProgram) {
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
	caps.Register("Clock", runtime.ClockProvider{Clock: runtime.NewManualClock(smtpNow)})
	caps.Register("Random", runtime.NewSeededRandomProvider(7))
	RegisterDefaults(caps)
	in := runtime.New(u, caps)
	in.Imports = BuildImports(files, loaded.Packages)
	in.Groups = BuildGroups(files, loaded.Packages)
	in.Packages = loaded.Packages
	env := map[string]string{"SMTP_URL": url, "SMTP_HELO_NAME": "client.test"}
	lookup := func(name string) (string, bool) { v, ok := env[name]; return v, ok }
	program := Program{Universe: u, Imports: in.Imports, Groups: in.Groups}
	if problems := in.LoadConfig(ReachableConfigFrom(program, []Entry{{Name: "sendWelcome"}}), lookup); len(problems) > 0 {
		t.Fatal(problems)
	}
	return in
}

// send is the outcome of sendWelcome to ann@y.org, as text, and the script
// is checked to have run to its end.
func sendWelcome(t *testing.T, url, subject string, peers ...*runtime.SocketPeer) string {
	t.Helper()
	script := &runtime.ScriptedSocket{Peers: peers}
	v, fault := smtpInterp(t, script, url).Call(context.Background(), "sendWelcome",
		map[string]runtime.Value{"to": str("ann@y.org"), "subject": str(subject)})
	if fault != nil {
		t.Fatalf("fault: %v", fault)
	}
	if err := script.Done(); err != nil {
		t.Errorf("script: %v", err)
	}
	return fmt.Sprint(v)
}

func welcomeMessage(t *testing.T, subject string) string {
	return "From: noreply@shop.example\r\nTo: ann@y.org\r\nSubject: " + subject + "\r\nDate: " + smtpDate +
		"\r\nMessage-ID: <" + smtpToken(t) + "@shop.example>\r\nMIME-Version: 1.0\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: 7bit\r\n\r\nThanks for signing up.\r\n.\r\n"
}

// The whole of a submission on port 587: STARTTLS, a login, one recipient.
func TestSmtpSendsThroughStartTlsAndALogin(t *testing.T) {
	got := sendWelcome(t, "smtp://user:pass@mail.example", "Welcome",
		peer("mail.example", 587,
			send("220 mail.example ESMTP\r\n"),
			expect("EHLO client.test\r\n"),
			send("250-mail.example\r\n250-STARTTLS\r\n250 AUTH PLAIN\r\n"),
			expect("STARTTLS\r\n"),
			send("220 Ready to start TLS\r\n"),
			tlsStep,
			// After the handshake the server's offers are asked for again.
			expect("EHLO client.test\r\n"),
			send("250-mail.example\r\n250 AUTH PLAIN LOGIN\r\n"),
			expect("AUTH PLAIN AHVzZXIAcGFzcw==\r\n"),
			send("235 2.7.0 Authentication successful\r\n"),
			expect("MAIL FROM:<noreply@shop.example>\r\n"),
			send("250 OK\r\n"),
			expect("RCPT TO:<ann@y.org>\r\n"),
			send("250 OK\r\n"),
			expect("DATA\r\n"),
			send("354 End data with <CR><LF>.<CR><LF>\r\n"),
			expect(welcomeMessage(t, "Welcome")),
			send("250 OK queued as 12345\r\n"),
			expect("QUIT\r\n")))
	if got != "Ok(None)" {
		t.Errorf("sendWelcome = %s, want Ok(None)", got)
	}
}

// smtp+plain:// for a server on the same machine: no TLS, no login.
func TestSmtpPlainToALocalServer(t *testing.T) {
	got := sendWelcome(t, "smtp+plain://localhost:1025", "Welcome",
		peer("localhost", 1025,
			send("220 localhost ESMTP\r\n"),
			expect("EHLO client.test\r\n"),
			send("250 localhost\r\n"),
			expect("MAIL FROM:<noreply@shop.example>\r\n"),
			send("250 OK\r\n"),
			expect("RCPT TO:<ann@y.org>\r\n"),
			send("250 OK\r\n"),
			expect("DATA\r\n"),
			send("354 go\r\n"),
			expect(welcomeMessage(t, "Welcome")),
			send("250 OK\r\n"),
			expect("QUIT\r\n")))
	if got != "Ok(None)" {
		t.Errorf("sendWelcome = %s, want Ok(None)", got)
	}
}

// smtp:// never sends a mail or a password without TLS: the script ends
// after the EHLO reply, so any further byte written fails it.
func TestSmtpRefusesToSendWithoutTlsOnAnSmtpUrl(t *testing.T) {
	got := sendWelcome(t, "smtp://user:hunter2@mail.example", "Welcome",
		peer("mail.example", 587,
			send("220 mail.example ESMTP\r\n"),
			expect("EHLO client.test\r\n"),
			send("250-mail.example\r\n250 AUTH PLAIN\r\n")))
	if !strings.Contains(got, "Protocol") || !strings.Contains(got, "STARTTLS") {
		t.Errorf("sendWelcome = %s, want a Protocol error naming STARTTLS", got)
	}
	if strings.Contains(got, "hunter2") {
		t.Errorf("the error repeats the password: %s", got)
	}
}

// Bytes that arrive after the STARTTLS reply and before the handshake came
// in the clear, possibly from someone other than the server (CVE-2011-0411).
func TestSmtpRefusesBytesInjectedBeforeTheHandshake(t *testing.T) {
	got := sendWelcome(t, "smtp://mail.example", "Welcome",
		peer("mail.example", 587,
			send("220 mail.example ESMTP\r\n"),
			expect("EHLO client.test\r\n"),
			send("250-mail.example\r\n250 STARTTLS\r\n"),
			expect("STARTTLS\r\n"),
			send("220 Ready\r\n250 AUTH PLAIN injected\r\n")))
	if !strings.Contains(got, "Protocol") || !strings.Contains(got, "TLS handshake") {
		t.Errorf("sendWelcome = %s, want a Protocol error about bytes before the handshake", got)
	}
}

// A refused recipient ends the send with the server's own code, and nothing
// after RCPT TO is written.
func TestSmtpReportsARefusedRecipient(t *testing.T) {
	got := sendWelcome(t, "smtp+plain://localhost:1025", "Welcome",
		peer("localhost", 1025,
			send("220 localhost ESMTP\r\n"),
			expect("EHLO client.test\r\n"),
			send("250 localhost\r\n"),
			expect("MAIL FROM:<noreply@shop.example>\r\n"),
			send("250 OK\r\n"),
			expect("RCPT TO:<ann@y.org>\r\n"),
			send("550 5.1.1 No such user here\r\n")))
	if !strings.Contains(got, "Refused(550") || !strings.Contains(got, "ann@y.org") || !strings.Contains(got, "No such user") {
		t.Errorf("sendWelcome = %s, want Refused(550, ...) naming ann@y.org", got)
	}
}

// A server that hangs up mid-dialogue is a socket failure, not a hang.
func TestSmtpReportsAServerThatHangsUp(t *testing.T) {
	got := sendWelcome(t, "smtp+plain://localhost:1025", "Welcome",
		peer("localhost", 1025,
			send("220 localhost ESMTP\r\n"),
			expect("EHLO client.test\r\n"),
			hangup))
	if !strings.Contains(got, "Socket") {
		t.Errorf("sendWelcome = %s, want a Socket error", got)
	}
}

// A subject with a line break is refused before a connection is made: the
// script has no peers, so any dial fails it.
func TestSmtpRefusesAMailItCantSendBeforeConnecting(t *testing.T) {
	got := sendWelcome(t, "smtp+plain://localhost:1025", "hi\r\nBcc: z@z.org")
	if !strings.Contains(got, "InvalidMail") {
		t.Errorf("sendWelcome = %s, want InvalidMail", got)
	}
}

// A URL that can't be read is an error naming what's wrong, never the URL.
func TestSmtpRefusesAUrlItCantRead(t *testing.T) {
	got := sendWelcome(t, "mailto:hunter2@mail.example", "Welcome")
	if !strings.Contains(got, "InvalidUrl") || strings.Contains(got, "hunter2") {
		t.Errorf("sendWelcome = %s, want InvalidUrl without the URL's text", got)
	}
}
