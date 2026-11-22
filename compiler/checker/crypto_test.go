package checker

import "testing"

// ADR 0075: the prelude crypto group.

func TestCryptoChecksClean(t *testing.T) {
	diags := checkSrc(t, `rule valid(secret: Secret, body: bytes, header: string) -> bool {
    return match (header.removePrefix("sha256=").fromHex()) {
        None -> false,
        sent -> crypto.equal(crypto.hmacSha256(key: secret, data: body), sent),
    }
}`, `conversion keyDigest(key: string) -> string {
    return crypto.sha256(key.utf8()).toHex();
}`, `formulas signing {
    formula sign(key: bytes, data: bytes) -> bytes {
        return crypto.hmacSha256(key, data);
    }
}`, `rule twilioValid(token: Secret, signed: bytes, sent: bytes) -> bool {
    return crypto.equal(crypto.hmacSha1(key: token, data: signed), sent);
}`, `conversion digests(data: bytes) -> bytes {
    return crypto.sha512(crypto.sha1(data));
}`, `formulas keys {
    formula sign512(key: bytes, data: bytes) -> bytes {
        return crypto.hmacSha512(key, data);
    }
    formula derive(key: Secret, salt: bytes) -> bytes {
        return crypto.hkdfSha256(key, salt: salt, info: "session".utf8(), length: 32);
    }
}`, `activity register(password: string) -> string {
    return crypto.hashPassword(password);
}`, `rule check(password: string, stored: string) -> bool {
    return crypto.passwordMatches(password: password, stored: stored);
}`)
	if len(diags) > 0 {
		t.Fatalf("diags = %v", diags)
	}
}

func TestCryptoRefusals(t *testing.T) {
	cases := []struct {
		name, src, rule, fragment string
	}{
		{"string data", `rule r(s: string) -> bytes {
    return crypto.sha256(s);
}`, "A1", "expected bytes"},
		{"string key", `rule r(k: string, d: bytes) -> bytes {
    return crypto.hmacSha256(key: k, data: d);
}`, "A1", "expected Secret | bytes"},
		{"string sha1 key", `rule r(k: string, d: bytes) -> bytes {
    return crypto.hmacSha1(key: k, data: d);
}`, "A1", "expected Secret | bytes"},
		{"string hkdf info", `rule r(k: bytes, s: bytes) -> bytes {
    return crypto.hkdfSha256(k, salt: s, info: "x", length: 32);
}`, "A1", "expected bytes"},
		{"hkdf without length", `rule r(k: bytes, s: bytes) -> bytes {
    return crypto.hkdfSha256(k, s, s);
}`, "", "crypto.hkdfSha256 takes 4 argument(s), got 3"},
		{"bytes password", `rule r(p: bytes, s: string) -> bool {
    return crypto.passwordMatches(p, s);
}`, "A1", "expected string"},
		{"unknown", `rule r(d: bytes) -> bytes {
    return crypto.md5(d);
}`, "", `no formula "md5"`},
		{"arity", `rule r(d: bytes) -> bytes {
    return crypto.sha256(d, d);
}`, "", "crypto.sha256 takes 1 argument(s), got 2"},
		{"bad name", `rule r(d: bytes) -> bytes {
    return crypto.sha256(input: d);
}`, "", `crypto.sha256 has no parameter "input"`},
		{"not called", `rule r(d: bytes) -> bool {
    f = crypto.sha256;
    return true;
}`, "", "call it, e.g. crypto.sha256(...)"},
		{"hash in a rule", `rule r(p: string) -> string {
    return crypto.hashPassword(p);
}`, "B10", "a rule may not call crypto.hashPassword"},
		{"hash in a conversion", `conversion r(p: string) -> string {
    return crypto.hashPassword(p);
}`, "B10", "a conversion may not call crypto.hashPassword"},
		{"hash in a workflow", `workflow r(p: string) -> string {
    return crypto.hashPassword(p);
}`, "B10", "a workflow may not call crypto.hashPassword"},
		{"local named crypto", `rule r(crypto: int) -> int {
    return crypto;
}`, "G4", "prelude crypto group"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			wantDiag(t, checkSrc(t, c.src), c.rule, c.fragment)
		})
	}
	wantDiag(t, checkSrc(t, `rule crypto(a: int) -> int {
    return a;
}`), "K2", "the prelude crypto group")
}
