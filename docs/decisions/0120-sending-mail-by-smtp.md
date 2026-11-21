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

### 2a. Cc, Bcc, Reply-To and display names

*(Amended 2026-11-21: the first half of open question 4 decided.)* `Mail`
grows four fields, each of which may be left out, so every `Mail` written
before still means what it did:

```fiig
model Mail {
    from: string
    to: List<string>
    subject: string
    body: string
    cc: List<string> | None
    bcc: List<string> | None
    replyTo: string | None
    names: Map<string, string> | None   // display names, by address
}

Smtp.send(Mail(from: "reports@shop.example", to: ["ann@y.org"], cc: ["bob@y.org"],
    bcc: ["audit@shop.example"], subject: "Weekly report", body: text,
    names: ["reports@shop.example": "Shop Reports", "ann@y.org": "Ann Smith"]))
```

- **Every recipient is sent to; Bcc is written nowhere.** RCPT TO names
  `to`, then `cc`, then `bcc`, each address once however many lists hold it
  (`recipientsOf`). To and Cc are written as headers, Bcc never, so a Bcc
  recipient's address reaches no one else. "All or none" holds across the
  three: a refused Bcc recipient ends the send like any other. The 100
  limit counts all three, and a mail needs one recipient in any of them; a
  mail to Bcc alone has no To header (RFC 5322 §3.6.3 allows that).
- **Reply-To is one address**, checked as the others are. A list is rare
  enough in practice to wait for a use.
- **Display names are a map from address to name, not an `Address`
  model.** Addresses stay plain strings everywhere (checking, RCPT TO,
  deduplication), and a name is presentation only, so `to: [email]` keeps
  working and a program that has no names writes nothing new. An `Address
  { email, name }` (Go's `mail.Address`, Jakarta's `InternetAddress`) would
  have made every `to` a list of models for the common case of no names;
  `"Ann <ann@y.org>"` strings (Python, Nodemailer) would have made the
  package parse what the caller wrote, the injection surface §2 avoids. A
  name given for an address the mail doesn't have is `Err(InvalidMail)`,
  which catches a typo in either; one for a Bcc address is allowed and
  never written.
- **A name is written the safest way it can be.** Plain words (letters,
  digits, spaces and the symbols RFC 5322's atoms allow) go as they are:
  `Shop Reports <reports@shop.example>`. One with a character that means
  something in an address header (`, . : ; @ < > ( ) [ ] \ "`), or `=?`,
  which a reader would take for an encoded word, is quoted, with `\` and
  `"` escaped: `"Smith, Ann" <ann@y.org>`. One that isn't printable ASCII is
  RFC 2047 encoded words, as the subject is. A name with a control
  character (a line break among them) or over 200 bytes is
  `Err(InvalidMail)`, and the reason never repeats it.
- **An address header is folded** before an address that would take its
  line past 78 characters, so 100 recipients can't make a line SMTP won't
  carry.

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

### 3a. Connections are kept between mails

*(Amended 2026-11-21: open question 1 decided. "One mail is one
connection" above no longer holds.)* A mail the server accepts leaves its
connection with `Socket.keep` (ADR 0109), and the next `send` takes it with
`Socket.reuse`, so a program sending in bulk pays the TCP, TLS, EHLO and
AUTH round trips once.

- **The session names the login**: `user=<name>`, or `no login`
  (`sessionFor`). With the key's host, port and TLS, a connection logged in
  as one user never sends as another. smtp:// is kept as TLS, since it
  upgraded before anything was sent.
- **A kept connection starts with RSET** (`resumeDialogue`), which clears
  whatever the last mail left, then MAIL FROM. What EHLO offered isn't asked
  again, so SIZE isn't checked before a mail on a kept connection; the
  server refuses one over its limit itself (552).
- **No QUIT.** After the 250 for the message the connection is kept, not
  closed, so §3's `SendAndClose(bytes)` step gave way to `Finish`; one the pool later drops (idle limit, the server's own timeout)
  is closed without QUIT, which servers treat as the client going away.
  A connection with bytes left unread after the 250 isn't kept.
- **Any error closes the connection**, a refusal included: what the server
  thinks of the dialogue after one is better not guessed.
- **A dead kept connection is replaced, once.** A server that closed an idle
  connection shows it as a socket failure or a 421 on RSET (or MAIL FROM,
  RCPT TO or DATA). The mail then goes again from the start on a new
  connection (`resendOnNewConnection`). Never once the body has been
  written: the server may have accepted the mail even if its 250 never
  arrived, and sending it again could deliver it twice. A new connection
  is never retried, so a send makes at most two.

### 3b. XOAUTH2

*(Amended 2026-11-21: open question 2's XOAUTH2 decided.)* Gmail and
Microsoft 365 turn passwords off for SMTP and take an OAuth 2.0 access token
instead, by the XOAUTH2 mechanism both document.

- **The token is a setting, `SMTP_OAUTH_TOKEN`**, a `Secret`; the user it
  logs in as is SMTP_URL's, percent-encoded as any user is:
  `smtps://ann%40gmail.com@smtp.gmail.com`. The URL then needs no password.
  A token with a space, a control character or non-ASCII is refused before
  anything is sent (it would break the framing below), and no reason ever
  repeats it.
- **One step.** `AUTH XOAUTH2` carries `user=<user>\x01auth=Bearer
  <token>\x01\x01` in base64 as its initial response. 235 logs in. A server
  that refuses the token answers 334 with a base64 JSON object
  (`{"status":"401","schemes":"Bearer","scope":"https://mail.google.com/"}`);
  the client answers it with an empty line, as the mechanism requires, and
  the server's next reply (535) ends the send as `Refused`, its message
  saying `the XOAUTH2 token (status 401): ` before the server's text. A
  challenge that isn't base64 JSON leaves the status out, nothing more:
  what's read from it is decoration on a refusal that is coming anyway.
- **Which mechanism: `SMTP_AUTH`**, `auto` (the default), `plain`, `login`
  or `xoauth2`. `auto` prefers XOAUTH2 when a token is set and EHLO offers
  it, then PLAIN, then LOGIN, as before; a token without XOAUTH2 offered
  falls back to the URL's password, or with none ends the send
  `Err(Protocol(...))`. The other three take that mechanism or end the send
  the same way, for a server whose offers include one that doesn't work for
  this account. `xoauth2` without a token, or any of them with no user in
  the URL, is refused before connecting.
- **The kept-connection session is unchanged**, `user=<name>`: the
  mechanism is how a connection proves who it is, not who it is.

**Refreshing the token is not fiig.smtp's.** An access token lasts about an
hour. Considered: a refresh inside `send` (a POST of `refresh_token`,
`client_id` and `client_secret` to the provider's token endpoint through
`fiig.http`, the token cached until it expires). Not done, because:

- **It would change every program that sends mail.** `send` would
  `require HttpClient` as well as `Socket`, so every manifest naming fiig.smtp
  would have to allow `HttpClient` (ADR 0035), the ones that log in with a
  password included.
- **A cache has nowhere to live.** Nothing survives between calls but a kept
  connection, and its session is a key the pool matches, not a store; a
  token in it would be a secret in a pool key, and gone with the connection.
  Without one, every new connection would ask the token endpoint first.
- **The grants differ by provider**, and none is SMTP's: a Google service
  account signs an RS256 JWT (fiig has no RSA signing), a Microsoft 365
  daemon uses client credentials with a scope, a user's app a refresh token.
  Choosing among them is an OAuth package's job (none exists yet), not a mail
  package's.
- **A login outlives its token.** SMTP authentication lasts the session
  (RFC 4954 §4), so a kept connection goes on sending after the token
  expires; only a new connection needs a fresh one.

So the deployment supplies a fresh token: config is read once, at start
(ADR 0071 §8), so a long-running program is restarted with a new
`SMTP_OAUTH_TOKEN` before the old one expires, by whatever rotates its
secrets. A short-lived one (a job, a CLI run) fetches one as it starts. A
token that has expired shows as the 535 refusal above, never as a hang.
Config reloading, or an OAuth package that a program calls itself, would
lift this; either is its own decision.

### 4. Scope

In: one plain-text mail to one or more recipients, over STARTTLS, implicit
TLS, or plain, with AUTH PLAIN or none; SIZE respected when the server
offers it (a message over its limit is `Err(InvalidMail)` before it is sent).

Out: HTML and attachments (multipart), ~~Cc, Bcc, Reply-To and display names,
AUTH LOGIN,~~ CRAM-MD5 ~~and XOAUTH2~~, SMTPUTF8 and internationalised addresses,
delivery status notifications, ~~connection reuse,~~ and pipelining.
*(Amended 2026-11-21: AUTH LOGIN (§3), connection reuse (§3a), and Cc,
Bcc, Reply-To and display names (§2a) moved in.)* *(Amended 2026-11-21:
XOAUTH2 (§3b) moved in; refreshing its token stays out.)*

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

1. ~~**Connection reuse.** One connection per mail costs a TCP, TLS, EHLO and
   AUTH round trip each. ADR 0109's `reuse` and `keep` could keep one with
   a `session` naming the user (RSET between mails), which a program that
   sends in bulk wants.~~ Done 2026-11-21 (§3a).
2. **More login mechanisms.** ~~AUTH LOGIN (older Exchange),~~ Done
   2026-11-21 (§3's amendment). ~~XOAUTH2 (Gmail, Microsoft 365)~~ Done
   2026-11-21 (§3b), with the token a setting the deployment refreshes.
   CRAM-MD5 remains.
3. **SMTPUTF8 and internationalised addresses** (RFC 6531), and IDN domains.
4. **Multipart: HTML and attachments**, ~~and Cc, Bcc, Reply-To and display
   names. A `Mail` model grows fields, or a second `send` takes a richer
   one.~~ Cc, Bcc, Reply-To and display names done 2026-11-21 (§2a), as
   optional `Mail` fields; multipart remains.
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
