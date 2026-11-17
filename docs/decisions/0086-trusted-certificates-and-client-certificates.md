# ADR 0086: Trusted Certificate Authorities and Client Certificates

Status: Accepted and implemented (2026-11-17; see Implementation notes)
Date: 2026-11-17
Related: ADR 0027 (declarative capability configuration), ADR 0060 (`Socket`
and TLS; its open question 2), ADR 0071 (config read and reported before
start), ADR 0074 (`fiig.http`), ADR 0084 §3 (a pooled connection's
lifetime), ADR 0085 (`fiig.postgres`; its open question 2).

## Context

Every TLS connection a fiig program makes goes through `Socket.connectTls`
or `Socket.startTls`, which verify the server's certificate against the
operating system's roots and present no certificate of their own. Two
common deployments can't connect at all:

- **A server signed by a CA the system doesn't trust.** Amazon RDS and
  Google Cloud SQL sign with their own CAs, and most companies run an
  internal CA for internal services. `verify-full` refuses them, and fiig
  offers no way to turn verification off (ADR 0060, ADR 0085 §5).
- **A server that requires the client's certificate** (mutual TLS): Cloud
  SQL's server-enforced mode, service meshes, and many internal APIs.

Both are configuration of where a program runs, not of what it does, and
ADR 0060 open question 2 already placed them in the capabilities file
rather than in fiig source, so no package can change what a program
trusts.

### How others configure it

| | Extra CAs | Client certificate | Where |
|---|---|---|---|
| libpq | `sslrootcert`, replaces the system roots | `sslcert` + `sslkey` | the connection string |
| Java | a truststore, replacing by default | a keystore | JVM flags or per pool |
| Go `crypto/tls` | `RootCAs`, replaces | `Certificates`, sent to any server that asks | per `tls.Config` |
| Node.js | `ca` replaces; `NODE_EXTRA_CA_CERTS` adds | `cert` + `key` | per client, or the environment |
| curl | `--cacert` replaces; `--capath` adds | `--cert` + `--key` | per request |
| Kubernetes, cert-manager, Vault agent | files mounted into the container and rotated in place | | volumes |

## Decision

### 1. `Socket`'s entry in the capabilities file

```json
{"Socket": {"type": "socket",
  "trust": ["/etc/ssl/corp-root.pem", "${RDS_CA_BUNDLE}"],
  "systemRoots": true,
  "clientCertificates": [
    {"hosts": ["db.internal", "*.mesh.internal"],
     "certificate": "/var/run/secrets/db/tls.crt",
     "key": "/var/run/secrets/db/tls.key"}
  ]}}
```

Every field is optional; with none, `Socket` behaves as today. `Socket` is
already a default the file need not name (ADR 0085's implementation
notes), so a file names it only to configure it. A per-package binding
(ADR 0079 §6a) may configure it per domain like any capability.

### 2. `trust` adds to the system roots

Each `trust` entry is a PEM file of one or more CA certificates, added to
the operating system's roots (the `NODE_EXTRA_CA_CERTS` model). Adding a
company CA must not break a call to a public API. `"systemRoots": false`
trusts only the listed CAs, for pinning a connection to one provider's
bundle; with it, `trust` must not be empty.

### 3. Files, named by path

Certificates and keys are files, as Kubernetes secrets, cert-manager, and
Vault agents deliver them. A path may name an environment variable
(`${RDS_CA_BUNDLE}`, ADR 0071 §6). PEM text in an environment variable is
not accepted: multi-line values are mangled by many deployment tools, and
a file is what every certificate source above already produces.

### 4. A client certificate is presented only to its hosts

Each `clientCertificates` entry lists the hosts it is for: exact names,
or `*.domain` for one label below `domain`, compared with the host the
program connected to (the name `startTls` verifies). When a server asks
for a certificate, the first entry matching the connection's host is
presented; when none matches, none is. Go's default, the one configured
certificate to any server that asks, would hand a database's identity to
a third-party API that happened to request one.

The key is PEM, PKCS #8, PKCS #1 (RSA), or SEC 1 (EC), unencrypted, as
Go's `tls.X509KeyPair` reads; a key that doesn't match its certificate is
refused (§6).

### 5. Rotation without a restart

Before each TLS handshake, `Socket` checks the modification time of each
file it uses and rereads any that changed. cert-manager renews well before
expiry (by default when two thirds of a certificate's life has passed),
and a pooled connection lives at most 30 minutes (ADR 0084 §3), so
long-running services move to renewed certificates without a restart. A
reread file that no longer parses keeps the last good contents, and the
failure is logged once per change; startup is the only point where a bad
file stops the program.

### 6. Problems are reported before the program starts

`fiig serve` and `fiig run` read every file when they load the
capabilities file: a missing file, PEM with no certificate, a key that
doesn't match its certificate, `systemRoots: false` with no `trust`, or a
host pattern other than a name or `*.domain` is a startup error, listed
with every other configuration problem in ADR 0071's one report.

### 7. What stays fixed

- Host name verification is always on. No setting skips verification,
  globally or per host.
- TLS 1.2 is the minimum; the cipher suites are Go's defaults.
- `fiig.http` and `fiig.postgres` change nothing: both already call
  `connectTls` or `startTls`.

## Alternatives considered

- **`trust` replaces the system roots**, as libpq, Go, and Java do by
  default. Safer against a compromised public CA for one connection, but
  the common enterprise case is "also trust our CA", and replacing breaks
  every public call in the same process. `systemRoots: false` keeps the
  replacing behavior for those who want it.
- **CA and certificate settings per driver** (`sslrootcert` in
  `fiig.postgres`'s settings). Every protocol package would carry its own
  copy, and `fiig.http` would need another; `Socket` is where TLS lives.
- **An option to skip verification for development.** Every library that
  has one finds it in production. A development server's CA goes in
  `trust` instead.

## Consequences

- `compiler/runtime`'s `SocketProvider` gains a TLS configuration: roots,
  host-scoped client certificates, and a file watcher; `runprogram`'s
  `"socket"` factory reads §1's fields.
- `docs/deploying.md` documents the fields with an RDS example and a
  Kubernetes secret mount.
- `known-limitations.md` drops `fiig.postgres`'s CA limitation.

## Open questions

1. **Revocation** (CRL, OCSP stapling). Go verifies neither by default;
   short-lived certificates are the common answer.
2. **Pinning a server certificate or public key** rather than a CA.
3. **Keys held in an HSM or a cloud KMS** (PKCS #11), which never exist
   as files.
4. **Encrypted private keys** and a passphrase from config.

## Implementation notes (2026-11-17)

- **Runtime** (`compiler/runtime/tlsconfig.go`): `TLSSettings` holds the
  files' last good contents by modification time, the root pool, and the
  client certificates; `NetBackend.TLS` carries it into every handshake,
  where a `GetClientCertificate` callback picks the entry whose hosts match
  the verified server name, and an empty certificate otherwise.
  `runprogram`'s `"socket"` factory reads §1's fields; with none, it
  builds the same provider as before.
- **One report:** a provider factory's error now becomes startup problems
  (one per line, each naming its capability) instead of stopping at the
  first, so every certificate problem joins ADR 0071's report.
- **Tests** (`tooling/runprogram/certificates_test.go`), with a throwaway
  CA and real TLS servers: an unknown CA refused and then trusted through
  `trust`, host verification still on; a client certificate presented to
  its host and not to another server that asks; a renewed CA file picked
  up without a restart, and a broken renewal keeping the last good one;
  and the startup problems.
- **Dogfooded** on the Postgres 17 container with SSL on and `hostssl ...
  clientcert=verify-full`, a throwaway CA, and `fiig.postgres` under
  `verify-full`: refused without `trust`; with `trust` but no client
  certificate, Postgres refused (`28000 connection requires a valid
  client certificate`); with both, connected; with the client certificate
  scoped to another host, not sent and refused; a missing file reported at
  startup.
- **`fiig.http` and `fiig.postgres` are unchanged**, as §7 said.
