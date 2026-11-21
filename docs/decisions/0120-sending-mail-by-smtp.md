# ADR 0120: Sending Mail by SMTP, Written in fiig

Status: Accepted (2026-11-20; the owner approved it in review). Implemented 2026-11-20.
Date: 2026-11-20
Related: ADR 0042 (the socket floor; Milestone 7's "no domain capability is
backed by Go"), ADR 0043 (capability groups), ADR 0058 (binary patterns),
ADR 0060 (Socket, including `startTls`), ADR 0071 (config), ADR 0074
(`fiig.http`, the model this follows), ADR 0075 (hashing), ADR 0086
(trusted certificates), ADR 0109 (connections kept between calls).

## Context

`EmailSender` appears in the corpus and in ADR 0042's list of domain
capabilities, as the example of what a program can't do without Go. It was
only ever a name: no provider existed, and a program that calls
`EmailSender.send` has nothing to run it. Milestone 7 says no domain
capability is backed by Go. SMTP is a small text protocol over a socket, so
a package written in fiig can send mail, as `fiig.http` makes requests and
`fiig.postgres` talks to a database.

How other languages shape it:

| Library | A mail is | Connection | TLS |
| --- | --- | --- | --- |
| Python `smtplib` | `sendmail(from, to, text)` or `send_message(msg)` | one `SMTP` object, reused | `starttls()` and `SMTP_SSL`, both explicit |
| Go `net/smtp` | `SendMail(addr, auth, from, to, bytes)` | one per call | STARTTLS if the server offers it, silently |
| Jakarta Mail | a `MimeMessage` built by the caller | a `Transport` | `mail.smtp.starttls.enable`, off unless set |
| Nodemailer | an object: from, to, subject, text, html | a pooled transport | `secure: true` for 465, STARTTLS "when available" |

Two lessons. Go's and Nodemailer's "STARTTLS if offered" can be defeated by
a network attacker who removes the offer, after which the client sends the
mail, and a password, in the clear (STRIPTLS). And a library that makes the
caller build the message hands them header injection: an address or subject
with a line break in it writes a header, or a protocol command, of the
caller's choosing.

## Decision

### 1. One gateway: `Smtp.send`

```fiig
capability Smtp {
    gateway send(mail: Mail) -> Result<None, SmtpError>
        requires Socket
}
```

```fiig
match Smtp.send(Mail(from: "shop@example.com", to: [email], subject: "Your receipt", body: text)) {
    Ok(_) -> ...
    Err(SmtpError.Refused(code, message)) -> ...
}
```

```fiig
model Mail { from: string, to: List<string>, subject: string, body: string }

enum SmtpError {
    Socket(failure: SocketError)         // connecting, writing, or reading failed
    Refused(code: int, message: string)  // the server's reply: 4xx try later, 5xx no
    Protocol(reason: string)             // the server did something SMTP doesn't, or can't do what the URL needs
    InvalidUrl(reason: string)           // SMTP_URL can't be read
    InvalidMail(reason: string)          // the mail can't be sent as written
}
```

A mail is plain text, in UTF-8. The message's headers, its encoding and its
dates are the package's job, not the caller's (§2).

### 2. What is written, and what is refused

**Where the server is: `SMTP_URL`.** A `Secret`, since it usually carries
the password. The scheme says how the connection is secured:

| URL | Port | Security |
| --- | --- | --- |
| `smtp://[user[:password]@]host[:port]` | 587 | starts plain and **must** upgrade with STARTTLS |
| `smtps://...` | 465 | TLS from the first byte |
| `smtp+plain://...` | 25 | never TLS: for a server on the same machine |

`smtp://` never sends a mail or a password in the clear. If the server's
EHLO reply doesn't offer STARTTLS the send ends with `Err(Protocol(...))`
before MAIL FROM or AUTH, so a network attacker who strips the offer gets
an error, not a mail. Writing `smtp+plain://` is a decision someone makes
in a line of configuration, as `{"ScheduleLock": {"type": "local"}}` is
(ADR 0112 §4). The server's certificate is checked against the trust the
capabilities file gives `Socket` (ADR 0086); one that isn't trusted fails
the handshake, with nothing sent. A user and password in the URL are
percent-decoded (`%40` for `@`), and a reason the URL can't be read never
repeats it. `SMTP_HELO_NAME` (default `localhost`) is what EHLO says, and
`SMTP_TIMEOUT_MS` (default 30000) bounds one send, connecting included.

**Addresses and subjects are checked before anything is sent.** An address is
ASCII with exactly one `@`, something on each side, and none of space,
control characters, `< > ( ) , : ; " [ ] \`. A subject has no line break.
There are at most 100 recipients. Anything else is `Err(InvalidMail)`, and
the reason names the recipient by position and never repeats it, since it
may be an attempt to write a command: `"b@y.org>\r\nDATA"` fails, and
`RCPT TO:<...>` never carries it.

**The message.** Headers `From`, `To` (every recipient, so there's no hidden
one), `Subject`, `Date` (UTC, from `fiig.time`'s `formatRfc5322`, ADR 0123),
`Message-ID` (a random token at the sender's domain), `MIME-Version`,
`Content-Type: text/plain; charset=utf-8`, and `Content-Transfer-Encoding`.

- A body of plain ASCII goes as `7bit`. Anything else, or a line over the
  998 bytes SMTP carries, goes as `base64`, wrapped at 76.
- Line endings are CRLF whatever the caller wrote (`\n`, `\r`, `\r\n`), and a
  body line that starts with a dot gets another (RFC 5321 §4.5.2), so
  the end of data can't be forged.
- A subject that isn't printable ASCII is written as RFC 2047 encoded words
  of at most 42 bytes, cut only between characters and folded.
- Addresses are ASCII: no SMTPUTF8 (open question 3).

**A refused recipient ends the send.** A mail goes to every address it is
addressed to or to none. It doesn't report a partial success the caller
must then untangle. The `Refused` message says which recipient.

### 3. The dialogue is a state machine of conversions

A gateway can't call another gateway, and every Socket call must be in one,
so the whole conversation (greeting, EHLO, STARTTLS, EHLO again, AUTH, MAIL
FROM, RCPT TO each, DATA, the body, QUIT) is one gateway with one loop. What
to send next and what a reply means are not in the gateway, they're
conversions over a `Dialogue` model:

- `nextStep(dialogue)` is one of `Listen`, `Send(bytes)`, `SendAndClose(bytes)`,
  `Upgrade`.
- `advance(dialogue, reply)` is the next dialogue, or the `SmtpError` that
  ends the send. A reply that isn't the code the phase expects is the
  server refusing, whatever its text.
- `readSmtpReply(bytes)` reads one reply, several lines for EHLO's, and asks
  for more bytes until its last line has arrived.

The gateway only writes bytes, reads replies, performs the TLS handshake on
`Upgrade`, and closes. So every decision is tested by a pure call with no
socket (53 tests), and the gateway itself by a scripted server that checks
each byte (§ Implementation notes).

**After STARTTLS the conversation starts again** with EHLO, and what the
server offered before is forgotten (RFC 3207 §4.2). **Bytes that arrive
between the server's agreement to STARTTLS and the handshake** were sent in
the clear, perhaps by someone other than the server (CVE-2011-0411), so the
send ends with `Err(Protocol(...))`.

**Login is AUTH PLAIN only**, with the user and password the URL carries,
after TLS (or on `smtp+plain://`, where writing it is the decision). A
server that offers a login mechanism but not PLAIN is `Err(Protocol(...))`
(open question 2). **One mail is one connection**: no connection is kept for
the next (open question 1). The QUIT after the mail is accepted is written
without waiting, and a server that has hung up already isn't a failure.

*(Amended 2026-11-21: AUTH LOGIN is the second mechanism. A server whose
EHLO offers PLAIN is logged in to with PLAIN, as before; one that offers
LOGIN and not PLAIN (older Exchange) gets `AUTH LOGIN`, then the user and
then the password, each in base64 on its own line, answering the server's
two 334 prompts; any other reply ends the send as `Refused`. A server that
offers neither is still `Err(Protocol(...))`. The prompts' text isn't
checked: it is `Username:` and `Password:` in base64 by convention only,
and the order is fixed. XOAUTH2 and CRAM-MD5 remain out.)*

### 4. Scope

In: one plain-text mail to one or more recipients, over STARTTLS, implicit
TLS, or plain, with AUTH PLAIN or none; SIZE respected when the server
offers it (a message over its limit is `Err(InvalidMail)` before it is sent).

Out: HTML and attachments (multipart), Cc, Bcc, Reply-To and display names,
AUTH LOGIN, CRAM-MD5 and XOAUTH2, SMTPUTF8 and internationalised addresses,
delivery status notifications, connection reuse, and pipelining.

## Alternatives considered

- **`EmailSender` as an interface providers fulfil** (ADR 0043). A program
  could swap Smtp for a provider's HTTP API. It's still possible: nothing
  here prevents declaring the interface later and having `Smtp` provide it.
  But the contract (what a mail is, what an error is) should come from a
  real use, and SMTP is the one every provider also speaks.
- **STARTTLS when offered** (Go, Nodemailer). Defeated by stripping the
  offer. `smtp://` requires it, and `smtp+plain://` says so when it doesn't.
- **A separate Host, Port, User and Password setting.** A hosted service
  hands out a URL (and a transactional provider's "SMTP credentials" are
  `smtp://apikey:secret@host:587`), so separate settings would make every
  deployment take it apart. The same reasoning as ADR 0110's `REDIS_URL`.
- **Let the caller supply headers.** The most flexible and the most
  dangerous: header injection is the commonest SMTP bug. Fields are named,
  and the package writes the headers.
- **Send to the recipients that accept it.** A partial send leaves the
  caller to find out who got the mail. All or none.

## Consequences

- New built-in package `fiig.smtp`, embedded beside `fiig.http` and
  `fiig.redis`.
- A program that sends mail `requires Smtp` and binds `Socket`; the
  capabilities file's `trust` (ADR 0086) is how a private CA is trusted.
- Milestone 7's criterion has no Go-backed domain capability left for mail.

## Open questions

1. **Connection reuse.** One connection per mail costs a TCP, TLS, EHLO and
   AUTH round trip each. ADR 0109's `reuse` and `keep` could keep one with
   a `session` naming the user (RSET between mails), which a program that
   sends in bulk wants.
2. **More login mechanisms.** ~~AUTH LOGIN (older Exchange),~~ Done
   2026-11-21 (§3's amendment). XOAUTH2 (Gmail, Microsoft 365), and
   CRAM-MD5 remain. XOAUTH2 needs an access token, not a password, which is
   a different configuration.
3. **SMTPUTF8 and internationalised addresses** (RFC 6531), and IDN domains.
4. **Multipart: HTML and attachments**, and Cc, Bcc, Reply-To and display
   names. A `Mail` model grows fields, or a second `send` takes a richer
   one.
5. ~~**A time type.** `Date` needed a calendar written in fiig
   (`rfc5322Date`); an S3 library needs another for SigV4 (`20261120T135958Z`).
   A `time` type (known-limitations) would end the duplication.~~ Done by ADR
   0123, as the library `fiig.time` over `int` milliseconds.

## Implementation notes (2026-11-20)

- `compiler/stdlib/smtp/src`: `Smtp` (the gateway), `Mail`, `SmtpError`,
  `SmtpServer` (config), `SmtpSecurity`, `SmtpTarget`, `ParseSmtpUrl`,
  `SmtpReply`, `SmtpReplyRead`, `ReadSmtpReply`, `SmtpPhase`, `Dialogue`,
  `SmtpStep`, `StartDialogue`, `NextStep`, `Advance`, `AfterUpgrade`,
  `CheckMail`, `MailData`, `MessageIdFor`, and `Rfc5322Date`.
- **Tests:** 53 fiig tests (`compiler/stdlib/smtp/test`): the date against
  known instants including leap days and the last second of a year, the
  message's bytes (dot-stuffing, line endings, base64 and encoded-word
  values worked out by hand), every refused address, the URL forms, every
  partial reply, and the dialogue from greeting to QUIT with each refusal. A
  package whose path starts `fiig.` can't run `fiig test` in place, so they
  run on a copy under another path. Eight Go tests (`tooling/runprogram/
  smtp_test.go`) run the gateway against scripted servers byte for byte,
  with a manual clock and a seeded random source: the whole STARTTLS and login
  submission, a plain local server, `smtp://` refusing a server that doesn't
  offer STARTTLS (and the password absent from the error), bytes injected
  before the handshake, a refused recipient, a server that hangs up, a mail
  refused before any connection, and an unreadable URL.
- **Dogfooded against Mailpit** (a real SMTP server, in Docker): a plain
  send decoded to the same subject and body; a subject with `ü ß ö — ✓`
  (folded across three encoded words) and a body with an emoji, a line
  starting `.` and another starting `..` all arrived exactly as written; the
  `Date` matched the server's own receive time; STARTTLS and AUTH PLAIN
  succeeded with the self-signed certificate trusted through `trust`;
  `smtp://` against a server with no STARTTLS refused with nothing sent; and
  against the TLS server with the certificate not trusted, the handshake
  failed (`x509: certificate signed by unknown authority`) with nothing
  sent. Not tried: `smtps://`, and a server that refuses a login.
- **Language friction.** (1) `command` is a keyword, so a helper named for
  what it writes had to be `crlfLine`. (2) A gateway can't call a gateway, so
  the dialogue is one loop (§3); that turned out well, since every decision
  is a testable conversion. (3) No time type: `rfc5322Date` was Hinnant's
  civil-from-days in fiig, and moved to `fiig.time` (ADR 0123). (4) The percent-decoding in `ParseSmtpUrl` is
  copied from `fiig.redis`'s; a shared helper has no home, because a built-in
  package can't import a private one. (5) `Some(x)` isn't a pattern: a
  `string | None` is matched with a bare name, which a test helper does.
