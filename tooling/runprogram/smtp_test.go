package runprogram

import (
	"context"
	"encoding/base64"
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
	"send_welcome.fiig": `use fiig.smtp;
use fiig.smtp.Smtp;

gateway sendWelcome(to: string, subject: string) -> Result<None, smtp.SmtpError>
    requires Smtp
{
    return Smtp.send(smtp.Mail(from: "noreply@shop.example", to: [to], subject: subject, body: "Thanks for signing up."));
}`,
	"send_report.fiig": `use fiig.smtp;
use fiig.smtp.Smtp;

gateway sendReport() -> Result<None, smtp.SmtpError>
    requires Smtp
{
    return Smtp.send(
        smtp.Mail(
            from: "reports@shop.example",
            to: ["ann@y.org"],
            subject: "Weekly report",
            body: "All well.",
            cc: ["bob@y.org"],
            bcc: ["audit@shop.example"],
            replyTo: "help@shop.example",
            names: ["reports@shop.example": "Shop Reports", "ann@y.org": "Ann Smith", "bob@y.org": "Bob Ünal"],
        ),
    );
}`,
	"send_invoice.fiig": `use fiig.smtp;
use fiig.smtp.Smtp;

gateway sendInvoice(pdf: bytes) -> Result<None, smtp.SmtpError>
    requires Smtp
{
    return Smtp.send(
        smtp.Mail(
            from: "billing@shop.example",
            to: ["ann@y.org"],
            subject: "Invoice",
            body: "Your invoice is attached.",
            html: "<p>Your invoice is <b>attached</b>.</p>",
            attachments: [smtp.Attachment(name: "Rechnung März.pdf", contentType: "application/pdf", data: pdf)],
        ),
    );
}`,
}

// The instant the manual clock starts at, as the Date header writes it.
var smtpNow = time.Date(2026, 11, 20, 13, 59, 58, 0, time.UTC)

const smtpDate = "Fri, 20 Nov 2026 13:59:58 +0000"

// smtpToken is the Message-ID's token: the first 12 bytes of the seeded
// random source the test gives the program, in hex.
func smtpToken(t *testing.T) string {
	t.Helper()
	return smtpTokenOf(t, 1)
}

// smtpTokenOf is the token of the program's nth mail, from the nth 12 bytes.
func smtpTokenOf(t *testing.T, n int) string {
	t.Helper()
	random := runtime.NewSeededRandomProvider(7)
	var token string
	for range n {
		v, fault := random.Call(context.Background(), "bytes", []runtime.Value{runtime.Int(12)})
		if fault != nil {
			t.Fatal(fault)
		}
		b, ok := v.(runtime.Bytes)
		if !ok {
			t.Fatalf("Random.bytes gave %T", v)
		}
		token = fmt.Sprintf("%x", b.B)
	}
	return token
}

func smtpInterp(t *testing.T, script *runtime.ScriptedSocket, url string) *runtime.Interp {
	t.Helper()
	return smtpInterpWith(t, runtime.SocketProvider{Backend: script}, url)
}

func smtpInterpWith(t *testing.T, socket runtime.SocketProvider, url string) *runtime.Interp {
	t.Helper()
	return smtpInterpEnv(t, socket, map[string]string{"SMTP_URL": url})
}

// smtpInterpEnv is smtpInterpWith with settings beyond SMTP_URL; the HELO
// name is always client.test.
func smtpInterpEnv(t *testing.T, socket runtime.SocketProvider, settings map[string]string) *runtime.Interp {
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
	caps.Register("Socket", socket)
	caps.Register("Clock", runtime.ClockProvider{Clock: runtime.NewManualClock(smtpNow)})
	caps.Register("Random", runtime.NewSeededRandomProvider(7))
	RegisterDefaults(caps)
	in := runtime.New(u, caps)
	in.Imports = BuildImports(files, loaded.Packages)
	in.Groups = BuildGroups(files, loaded.Packages)
	in.Packages = loaded.Packages
	env := map[string]string{"SMTP_HELO_NAME": "client.test"}
	for k, v := range settings {
		env[k] = v
	}
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
	return welcomeMessageOf(t, subject, 1)
}

// welcomeMessageOf is the message of the program's nth mail.
func welcomeMessageOf(t *testing.T, subject string, n int) string {
	return "From: noreply@shop.example\r\nTo: ann@y.org\r\nSubject: " + subject + "\r\nDate: " + smtpDate +
		"\r\nMessage-ID: <" + smtpTokenOf(t, n) + "@shop.example>\r\nMIME-Version: 1.0\r\n" +
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
			send("250 OK queued as 12345\r\n")))
	if got != "Ok(None)" {
		t.Errorf("sendWelcome = %s, want Ok(None)", got)
	}
}

// A server that offers AUTH LOGIN alone, as older Exchange servers do, is
// logged in to with LOGIN's two prompts.
func TestSmtpLogsInWithAuthLoginWhenPlainIsNotOffered(t *testing.T) {
	got := sendWelcome(t, "smtps://user:pass@mail.example", "Welcome",
		peer("mail.example", 465,
			tlsStep,
			send("220 mail.example ESMTP\r\n"),
			expect("EHLO client.test\r\n"),
			send("250-mail.example\r\n250 AUTH LOGIN\r\n"),
			expect("AUTH LOGIN\r\n"),
			send("334 VXNlcm5hbWU6\r\n"),
			expect("dXNlcg==\r\n"),
			send("334 UGFzc3dvcmQ6\r\n"),
			expect("cGFzcw==\r\n"),
			send("235 2.7.0 Authentication successful\r\n"),
			expect("MAIL FROM:<noreply@shop.example>\r\n"),
			send("250 OK\r\n"),
			expect("RCPT TO:<ann@y.org>\r\n"),
			send("250 OK\r\n"),
			expect("DATA\r\n"),
			send("354 go\r\n"),
			expect(welcomeMessage(t, "Welcome")),
			send("250 OK\r\n")))
	if got != "Ok(None)" {
		t.Errorf("sendWelcome = %s, want Ok(None)", got)
	}
}

// ADR 0120 §2a: To, Cc and Bcc are each sent RCPT TO, Bcc is written in no
// header, and display names are written beside their addresses.
func TestSmtpSendsToCcAndBccWithDisplayNames(t *testing.T) {
	script := &runtime.ScriptedSocket{Peers: []*runtime.SocketPeer{peer("localhost", 1025,
		send("220 localhost ESMTP\r\n"),
		expect("EHLO client.test\r\n"),
		send("250 localhost\r\n"),
		expect("MAIL FROM:<reports@shop.example>\r\n"), send("250 OK\r\n"),
		expect("RCPT TO:<ann@y.org>\r\n"), send("250 OK\r\n"),
		expect("RCPT TO:<bob@y.org>\r\n"), send("250 OK\r\n"),
		expect("RCPT TO:<audit@shop.example>\r\n"), send("250 OK\r\n"),
		expect("DATA\r\n"), send("354 go\r\n"),
		expect("From: Shop Reports <reports@shop.example>\r\nTo: Ann Smith <ann@y.org>\r\n"+
			"Cc: =?UTF-8?B?Qm9iIMOcbmFs?= <bob@y.org>\r\nReply-To: help@shop.example\r\n"+
			"Subject: Weekly report\r\nDate: "+smtpDate+"\r\nMessage-ID: <"+smtpToken(t)+"@shop.example>\r\n"+
			"MIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: 7bit\r\n\r\nAll well.\r\n.\r\n"),
		send("250 OK\r\n"))}}
	v, fault := smtpInterp(t, script, "smtp+plain://localhost:1025").Call(context.Background(), "sendReport", nil)
	if fault != nil {
		t.Fatalf("fault: %v", fault)
	}
	if got := fmt.Sprint(v); got != "Ok(None)" {
		t.Errorf("sendReport = %s, want Ok(None)", got)
	}
	if err := script.Done(); err != nil {
		t.Errorf("script: %v", err)
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
			send("250 OK\r\n")))
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

// ADR 0120 §3a: a mail accepted leaves its connection kept, and the next
// mail goes on it after RSET. A kept connection found dead before the
// message is written is replaced, once; one that dies after it is not, since
// the server may have the mail already.
func TestSmtpKeepsConnectionsBetweenMails(t *testing.T) {
	steps := func(parts ...[]runtime.SocketStep) []runtime.SocketStep {
		var all []runtime.SocketStep
		for _, p := range parts {
			all = append(all, p...)
		}
		return all
	}
	// The nth mail from MAIL FROM to the server accepting it.
	mailSteps := func(t *testing.T, subject string, n int) []runtime.SocketStep {
		return []runtime.SocketStep{
			expect("MAIL FROM:<noreply@shop.example>\r\n"), send("250 OK\r\n"),
			expect("RCPT TO:<ann@y.org>\r\n"), send("250 OK\r\n"),
			expect("DATA\r\n"), send("354 go\r\n"),
			expect(welcomeMessageOf(t, subject, n)), send("250 OK\r\n"),
		}
	}
	// The nth mail on a new connection to a local server, without a login.
	fresh := func(t *testing.T, subject string, n int) []runtime.SocketStep {
		return steps([]runtime.SocketStep{
			send("220 localhost ESMTP\r\n"),
			expect("EHLO client.test\r\n"), send("250 localhost\r\n"),
		}, mailSteps(t, subject, n))
	}
	rset := []runtime.SocketStep{expect("RSET\r\n"), send("250 2.0.0 OK\r\n")}
	local := func(s []runtime.SocketStep) *runtime.SocketPeer {
		return &runtime.SocketPeer{Host: "localhost", Port: 1025, Steps: s}
	}
	cases := []struct {
		name   string
		url    string
		peers  func(t *testing.T) []*runtime.SocketPeer
		want   string // the second mail's outcome
		kept   int    // connections the pool holds at the end
		closed []bool // which peers end closed
	}{
		{"the next mail goes on the same connection", "smtp+plain://localhost:1025",
			func(t *testing.T) []*runtime.SocketPeer {
				return []*runtime.SocketPeer{local(steps(fresh(t, "One", 1), rset, mailSteps(t, "Two", 2)))}
			}, "Ok(None)", 1, []bool{false}},
		{"after STARTTLS and a login too", "smtp://user:pass@mail.example",
			func(t *testing.T) []*runtime.SocketPeer {
				login := []runtime.SocketStep{
					send("220 mail.example ESMTP\r\n"),
					expect("EHLO client.test\r\n"), send("250-mail.example\r\n250 STARTTLS\r\n"),
					expect("STARTTLS\r\n"), send("220 Ready\r\n"), tlsStep,
					expect("EHLO client.test\r\n"), send("250-mail.example\r\n250 AUTH PLAIN\r\n"),
					expect("AUTH PLAIN AHVzZXIAcGFzcw==\r\n"), send("235 OK\r\n"),
				}
				return []*runtime.SocketPeer{{Host: "mail.example", Port: 587,
					Steps: steps(login, mailSteps(t, "One", 1), rset, mailSteps(t, "Two", 2))}}
			}, "Ok(None)", 1, []bool{false}},
		{"closed while idle, so sent on a new connection", "smtp+plain://localhost:1025",
			func(t *testing.T) []*runtime.SocketPeer {
				return []*runtime.SocketPeer{
					local(steps(fresh(t, "One", 1), []runtime.SocketStep{expect("RSET\r\n"), hangup})),
					local(fresh(t, "Two", 2)),
				}
			}, "Ok(None)", 1, []bool{true, false}},
		{"421 to RSET, so sent on a new connection", "smtp+plain://localhost:1025",
			func(t *testing.T) []*runtime.SocketPeer {
				return []*runtime.SocketPeer{
					local(steps(fresh(t, "One", 1), []runtime.SocketStep{expect("RSET\r\n"), send("421 4.4.2 Idle too long\r\n")})),
					local(fresh(t, "Two", 2)),
				}
			}, "Ok(None)", 1, []bool{true, false}},
		{"never sent twice once the message is written", "smtp+plain://localhost:1025",
			func(t *testing.T) []*runtime.SocketPeer {
				two := mailSteps(t, "Two", 2)
				return []*runtime.SocketPeer{local(steps(fresh(t, "One", 1), rset, two[:len(two)-1], []runtime.SocketStep{hangup}))}
			}, "Err(SmtpError.Socket(", 0, []bool{true}},
		{"a refusal closes the connection rather than keep it", "smtp+plain://localhost:1025",
			func(t *testing.T) []*runtime.SocketPeer {
				return []*runtime.SocketPeer{local(steps(fresh(t, "One", 1), rset, []runtime.SocketStep{
					expect("MAIL FROM:<noreply@shop.example>\r\n"), send("550 5.7.1 Not today\r\n"),
				}))}
			}, "Err(SmtpError.Refused(550", 0, []bool{true}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			peers := c.peers(t)
			script := &runtime.ScriptedSocket{Peers: peers}
			socket := runtime.SocketProvider{Backend: script, Pool: runtime.NewSocketPool(runtime.DefaultIdleLimits)}
			defer socket.Close()
			in := smtpInterpWith(t, socket, c.url)
			var got string
			for _, subject := range []string{"One", "Two"} {
				v, fault := in.Call(context.Background(), "sendWelcome",
					map[string]runtime.Value{"to": str("ann@y.org"), "subject": str(subject)})
				if fault != nil {
					t.Fatalf("fault: %v", fault)
				}
				got = fmt.Sprint(v)
			}
			if !strings.HasPrefix(got, c.want) {
				t.Errorf("second mail %s, want %s", got, c.want)
			}
			if err := script.Done(); err != nil {
				t.Error(err)
			}
			if n := socket.Pool.Idle(); n != c.kept {
				t.Errorf("pool holds %d, want %d", n, c.kept)
			}
			for i, p := range peers {
				if p.Closed() != c.closed[i] {
					t.Errorf("peer %d closed = %v, want %v", i, p.Closed(), c.closed[i])
				}
			}
		})
	}
}

// ADR 0120 §3b: with SMTP_OAUTH_TOKEN set, a server that offers XOAUTH2 is
// logged in to with the token in one step; one that refuses it sends a
// base64 JSON challenge, is answered with an empty line, and its refusal
// carries the JSON's status but never the token.
func TestSmtpLogsInWithXOAuth2(t *testing.T) {
	// user=ann@gmail.com, 0x01, auth=Bearer ya29.tok, 0x01 0x01.
	const login = "AUTH XOAUTH2 dXNlcj1hbm5AZ21haWwuY29tAWF1dGg9QmVhcmVyIHlhMjkudG9rAQE=\r\n"
	hello := []runtime.SocketStep{
		tlsStep,
		send("220 smtp.gmail.com ESMTP\r\n"),
		expect("EHLO client.test\r\n"),
		send("250-smtp.gmail.com\r\n250 AUTH LOGIN PLAIN XOAUTH2 PLAIN-CLIENTTOKEN OAUTHBEARER\r\n"),
		expect(login),
	}
	run := func(t *testing.T, steps ...runtime.SocketStep) string {
		t.Helper()
		script := &runtime.ScriptedSocket{Peers: []*runtime.SocketPeer{
			{Host: "smtp.gmail.com", Port: 465, Steps: append(append([]runtime.SocketStep{}, hello...), steps...)},
		}}
		in := smtpInterpEnv(t, runtime.SocketProvider{Backend: script}, map[string]string{
			"SMTP_URL":         "smtps://ann%40gmail.com@smtp.gmail.com",
			"SMTP_OAUTH_TOKEN": "ya29.tok",
		})
		v, fault := in.Call(context.Background(), "sendWelcome",
			map[string]runtime.Value{"to": str("bob@y.org"), "subject": str("Welcome")})
		if fault != nil {
			t.Fatalf("fault: %v", fault)
		}
		if err := script.Done(); err != nil {
			t.Errorf("script: %v", err)
		}
		return fmt.Sprint(v)
	}
	t.Run("accepted", func(t *testing.T) {
		got := run(t,
			send("235 2.7.0 Accepted\r\n"),
			expect("MAIL FROM:<noreply@shop.example>\r\n"), send("250 OK\r\n"),
			expect("RCPT TO:<bob@y.org>\r\n"), send("250 OK\r\n"),
			expect("DATA\r\n"), send("354 go\r\n"),
			expect(strings.Replace(welcomeMessage(t, "Welcome"), "To: ann@y.org", "To: bob@y.org", 1)),
			send("250 OK\r\n"))
		if got != "Ok(None)" {
			t.Errorf("sendWelcome = %s, want Ok(None)", got)
		}
	})
	t.Run("refused", func(t *testing.T) {
		got := run(t,
			// {"status":"401","schemes":"Bearer","scope":"https://mail.google.com/"}
			send("334 eyJzdGF0dXMiOiI0MDEiLCJzY2hlbWVzIjoiQmVhcmVyIiwic2NvcGUiOiJodHRwczovL21haWwuZ29vZ2xlLmNvbS8ifQ==\r\n"),
			expect("\r\n"),
			send("535-5.7.8 Username and Password not accepted.\r\n535 5.7.8 https://support.google.com/mail/?p=BadCredentials\r\n"))
		if !strings.HasPrefix(got, "Err(SmtpError.Refused(535") || !strings.Contains(got, "status 401") {
			t.Errorf("sendWelcome = %s, want Refused(535, ...) with status 401", got)
		}
		if strings.Contains(got, "ya29") {
			t.Errorf("the error repeats the token: %s", got)
		}
	})
}

// ADR 0120 §2b: a mail with HTML and an attachment goes as multipart/mixed
// holding multipart/alternative (text, then HTML) and the file in base64,
// its boundaries made from the Message-ID's token, and its non-ASCII name
// written both as RFC 2047 encoded words and as RFC 2231's filename*=.
func TestSmtpSendsHtmlAndAnAttachment(t *testing.T) {
	pdf := make([]byte, 200)
	for i := range pdf {
		pdf[i] = byte(i * 7)
	}
	encoded := base64.StdEncoding.EncodeToString(pdf)
	var lines []string
	for len(encoded) > 76 {
		lines = append(lines, encoded[:76])
		encoded = encoded[76:]
	}
	lines = append(lines, encoded)
	token := smtpToken(t)
	mixed, alternative := "=_m."+token, "=_a."+token
	name := base64.StdEncoding.EncodeToString([]byte("Rechnung März.pdf"))
	message := "From: billing@shop.example\r\nTo: ann@y.org\r\nSubject: Invoice\r\nDate: " + smtpDate +
		"\r\nMessage-ID: <" + token + "@shop.example>\r\nMIME-Version: 1.0\r\n" +
		"Content-Type: multipart/mixed;\r\n boundary=\"" + mixed + "\"\r\n\r\n" +
		"--" + mixed + "\r\nContent-Type: multipart/alternative;\r\n boundary=\"" + alternative + "\"\r\n\r\n" +
		"--" + alternative + "\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: 7bit\r\n\r\n" +
		"Your invoice is attached.\r\n" +
		"--" + alternative + "\r\nContent-Type: text/html; charset=utf-8\r\nContent-Transfer-Encoding: 7bit\r\n\r\n" +
		"<p>Your invoice is <b>attached</b>.</p>\r\n" +
		"--" + alternative + "--\r\n" +
		"--" + mixed + "\r\nContent-Type: application/pdf;\r\n name=\"=?UTF-8?B?" + name + "?=\"\r\n" +
		"Content-Disposition: attachment;\r\n filename*=utf-8''Rechnung%20M%C3%A4rz.pdf\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\n" + strings.Join(lines, "\r\n") + "\r\n" +
		"--" + mixed + "--\r\n.\r\n"
	run := func(t *testing.T, hello string, steps ...runtime.SocketStep) string {
		t.Helper()
		script := &runtime.ScriptedSocket{Peers: []*runtime.SocketPeer{peer("localhost", 1025,
			append([]runtime.SocketStep{
				send("220 localhost ESMTP\r\n"),
				expect("EHLO client.test\r\n"),
				send(hello),
			}, steps...)...)}}
		v, fault := smtpInterp(t, script, "smtp+plain://localhost:1025").Call(context.Background(), "sendInvoice",
			map[string]runtime.Value{"pdf": runtime.Bytes{B: pdf}})
		if fault != nil {
			t.Fatalf("fault: %v", fault)
		}
		if err := script.Done(); err != nil {
			t.Errorf("script: %v", err)
		}
		return fmt.Sprint(v)
	}
	t.Run("sent", func(t *testing.T) {
		got := run(t, "250-localhost\r\n250 SIZE 10000\r\n",
			expect("MAIL FROM:<billing@shop.example>\r\n"), send("250 OK\r\n"),
			expect("RCPT TO:<ann@y.org>\r\n"), send("250 OK\r\n"),
			expect("DATA\r\n"), send("354 go\r\n"),
			expect(message), send("250 OK\r\n"))
		if got != "Ok(None)" {
			t.Errorf("sendInvoice = %s, want Ok(None)", got)
		}
	})
	// SIZE is checked against the message as encoded, attachment and all:
	// the script ends after EHLO, so nothing more may be written.
	t.Run("over the server's SIZE", func(t *testing.T) {
		got := run(t, fmt.Sprintf("250-localhost\r\n250 SIZE %d\r\n", len(message)-1))
		if !strings.Contains(got, "InvalidMail") || !strings.Contains(got, "limit") {
			t.Errorf("sendInvoice = %s, want InvalidMail over the limit", got)
		}
	})
}
