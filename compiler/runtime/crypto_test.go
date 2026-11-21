package runtime

import (
	"context"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/argon2"

	"fiig-lang/compiler/types"
)

// ADR 0075: the prelude crypto group.

var cryptoProgram = []string{
	`rule digest(data: bytes) -> string {
    return crypto.sha256(data).toHex()
}`,
	`rule signWith(key: Secret, data: bytes) -> string {
    return crypto.hmacSha256(key: key, data: data).toHex()
}`,
	`rule signWithBytes(key: bytes, data: bytes) -> string {
    return crypto.hmacSha256(key, data).toHex()
}`,
	// §1's example: GitHub's X-Hub-Signature-256.
	`rule signatureValid(secret: Secret, body: bytes, header: string) -> bool {
    match header.removePrefix("sha256=").fromHex() {
        None -> false
        sent -> crypto.equal(crypto.hmacSha256(key: secret, data: body), sent)
    }
}`,
	`activity hash(password: string) -> string {
    return crypto.hashPassword(password)
}`,
	`rule matches(password: string, stored: string) -> bool {
    return crypto.passwordMatches(password, stored: stored)
}`,
}

func cryptoInterp(t *testing.T, seed uint64) *Interp {
	t.Helper()
	in := checkedInterp(t, cryptoProgram...)
	caps := NewCapabilityRegistry()
	caps.Register("Random", NewSeededRandomProvider(seed))
	in.Capabilities = caps
	return in
}

func callOK(t *testing.T, in *Interp, name string, args map[string]Value) Value {
	t.Helper()
	v, fault := in.Call(context.Background(), name, args)
	if fault != nil {
		t.Fatalf("%s: %v", name, fault)
	}
	return v
}

func TestEveryCryptoFormulaIsImplemented(t *testing.T) {
	declared := map[string]bool{}
	for _, name := range types.CryptoFormulaNames() {
		declared[name] = true
		if cryptoFormulas[name] == nil {
			t.Errorf("crypto.%s is in compiler/types's table but has no body", name)
		}
	}
	for name := range cryptoFormulas {
		if !declared[name] {
			t.Errorf("crypto.%s has a body but isn't in compiler/types's table", name)
		}
	}
}

// §2, against published vectors: FIPS 180-2's "abc", RFC 4231's test case
// 2, and GitHub's documented webhook example.
func TestShaAndHmacVectors(t *testing.T) {
	in := cryptoInterp(t, 1)
	b := func(s string) Value { return Bytes{B: []byte(s)} }
	cases := []struct {
		name string
		args map[string]Value
		want string
	}{
		{"digest", map[string]Value{"data": b("abc")}, "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"},
		{"digest", map[string]Value{"data": b("")}, "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"},
		{"signWith", map[string]Value{"key": NewSecret("K", "Jefe"), "data": b("what do ya want for nothing?")}, "5bdcc146bf60754e6a042426089575c75a003f089d2739839dec58b964ec3843"},
		{"signWithBytes", map[string]Value{"key": b("Jefe"), "data": b("what do ya want for nothing?")}, "5bdcc146bf60754e6a042426089575c75a003f089d2739839dec58b964ec3843"},
		{"signWith", map[string]Value{"key": NewSecret("GITHUB_WEBHOOK_SECRET", "It's a Secret to Everybody"), "data": b("Hello, World!")}, "757107ea0eb2509fc211221cce984b8a37570b6d7586c22c46f4379c8b043e17"},
	}
	for _, c := range cases {
		if got := callOK(t, in, c.name, c.args); got != Str(c.want) {
			t.Errorf("%s(%v) = %v, want %s", c.name, c.args, got, c.want)
		}
	}
}

// ADR 0075 §6 (amended 2026-11-21): SHA-1 and SHA-512 and their HMACs,
// against FIPS 180-2's "abc", RFC 2202's test case 2 (HMAC-SHA-1), and
// RFC 4231's test case 2 (HMAC-SHA-512).
func TestSha1AndSha512Vectors(t *testing.T) {
	in := checkedInterp(t,
		`rule digest1(data: bytes) -> string {
    return crypto.sha1(data).toHex()
}`,
		`rule digest512(data: bytes) -> string {
    return crypto.sha512(data).toHex()
}`,
		`rule sign1(key: Secret, data: bytes) -> string {
    return crypto.hmacSha1(key: key, data: data).toHex()
}`,
		`rule sign512(key: bytes, data: bytes) -> string {
    return crypto.hmacSha512(key, data).toHex()
}`)
	b := func(s string) Value { return Bytes{B: []byte(s)} }
	jefe := "what do ya want for nothing?"
	cases := []struct {
		name string
		args map[string]Value
		want string
	}{
		{"digest1", map[string]Value{"data": b("abc")}, "a9993e364706816aba3e25717850c26c9cd0d89d"},
		{"digest512", map[string]Value{"data": b("abc")}, "ddaf35a193617abacc417349ae20413112e6fa4e89a97ea20a9eeee64b55d39a2192992a274fc1a836ba3c23a3feebbd454d4423643ce80e2a9ac94fa54ca49f"},
		{"sign1", map[string]Value{"key": NewSecret("K", "Jefe"), "data": b(jefe)}, "effcdf6ae5eb2fa2d27416d5f184df9c259a7c79"},
		{"sign512", map[string]Value{"key": b("Jefe"), "data": b(jefe)}, "164b7a7bfcf819e2e395fbe73b56e0a387bd64222e831fd610270cd7ea2505549758bf75c05a994a6d034f65f8f0e6fdcaeab1a34d4a6b4b636e070a38bce737"},
	}
	for _, c := range cases {
		if got := callOK(t, in, c.name, c.args); got != Str(c.want) {
			t.Errorf("%s(%v) = %v, want %s", c.name, c.args, got, c.want)
		}
	}
}

// ADR 0075 §6 (amended 2026-11-21): HKDF-SHA-256 against RFC 5869's test
// case 1, and the length bounds.
func TestHkdfSha256(t *testing.T) {
	in := checkedInterp(t, `rule derive(key: bytes, salt: bytes, info: bytes, length: int) -> string {
    return crypto.hkdfSha256(key, salt: salt, info: info, length: length).toHex()
}`, `rule deriveFromSecret(key: Secret, length: int) -> bytes {
    return crypto.hkdfSha256(key, salt: "".utf8(), info: "".utf8(), length: length)
}`)
	run := func(n int, f func(i int) byte) Value {
		out := make([]byte, n)
		for i := range out {
			out[i] = f(i)
		}
		return Bytes{B: out}
	}
	args := map[string]Value{
		"key":    run(22, func(int) byte { return 0x0b }),
		"salt":   run(13, func(i int) byte { return byte(i) }),
		"info":   run(10, func(i int) byte { return byte(0xf0 + i) }),
		"length": Int(42),
	}
	if got := callOK(t, in, "derive", args); got != Value(Str("3cb25f25faacd57a90434f64d0362f2a2d2d0a90cf1a5a4c5db02d56ecc4c5bf34007208d5b887185865")) {
		t.Errorf("hkdfSha256 = %v", got)
	}
	if got := callOK(t, in, "deriveFromSecret", map[string]Value{"key": NewSecret("K", "k"), "length": Int(255 * 32)}); len(got.(Bytes).B) != 255*32 {
		t.Errorf("the longest output is %d bytes", len(got.(Bytes).B))
	}
	for _, n := range []int64{0, -1, 255*32 + 1} {
		args["length"] = Int(n)
		if _, f := in.Call(context.Background(), "derive", args); f == nil || !strings.Contains(f.Message, "outside 1 to 8160") {
			t.Errorf("length %d: fault = %v", n, f)
		}
	}
}

func TestSignatureCheck(t *testing.T) {
	in := cryptoInterp(t, 1)
	secret := NewSecret("GITHUB_WEBHOOK_SECRET", "It's a Secret to Everybody")
	body := Bytes{B: []byte("Hello, World!")}
	good := "sha256=757107ea0eb2509fc211221cce984b8a37570b6d7586c22c46f4379c8b043e17"
	for header, want := range map[string]bool{
		good:                                 true,
		strings.ToUpper(good[:7]) + good[7:]: false, // the prefix isn't removed, so it isn't hex
		good[:len(good)-1] + "8":             false,
		good[:len(good)-2]:                   false, // a byte short
		"sha256=zz":                          false,
		"":                                   false,
	} {
		got := callOK(t, in, "signatureValid", map[string]Value{"secret": secret, "body": body, "header": Str(header)})
		if got != Bool(want) {
			t.Errorf("signatureValid(%q) = %v, want %v", header, got, want)
		}
	}
}

// §5: a PHC string, a fresh salt each call, checked by passwordMatches;
// and deterministic under the seeded provider.
func TestPasswordHash(t *testing.T) {
	in := cryptoInterp(t, 7)
	stored := string(callOK(t, in, "hash", map[string]Value{"password": Str("correct horse")}).(Str))
	if !strings.HasPrefix(stored, "$argon2id$v=19$m=19456,t=2,p=1$") || strings.Count(stored, "$") != 5 {
		t.Fatalf("hash = %q", stored)
	}
	again := string(callOK(t, in, "hash", map[string]Value{"password": Str("correct horse")}).(Str))
	if again == stored {
		t.Errorf("two hashes of one password share a salt: %q", again)
	}
	for password, want := range map[string]bool{"correct horse": true, "correct horsf": false, "": false, "Correct horse": false} {
		if got := callOK(t, in, "matches", map[string]Value{"password": Str(password), "stored": Str(stored)}); got != Bool(want) {
			t.Errorf("passwordMatches(%q) = %v, want %v", password, got, want)
		}
	}
	seeded := cryptoInterp(t, 7)
	if got := callOK(t, seeded, "hash", map[string]Value{"password": Str("correct horse")}); got != Str(stored) {
		t.Errorf("the same seed hashed to %v, want %s", got, stored)
	}
}

// §5: a hash made at other parameters still checks, so they can change.
func TestPasswordMatchesReadsItsParameters(t *testing.T) {
	in := cryptoInterp(t, 1)
	salt := []byte("0123456789abcdef")
	stored := phcString(4096, 3, 2, salt, argon2.IDKey([]byte("hunter2"), salt, 3, 4096, 2, 24))
	if got := callOK(t, in, "matches", map[string]Value{"password": Str("hunter2"), "stored": Str(stored)}); got != Bool(true) {
		t.Errorf("passwordMatches at m=4096,t=3,p=2 = %v", got)
	}
}

// §5: a stored hash fiig can't read, or one asking for too much, faults.
func TestPasswordMatchesRefusals(t *testing.T) {
	in := cryptoInterp(t, 1)
	salt := "MDEyMzQ1Njc4OWFiY2RlZg"
	hash := "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	cases := map[string]string{
		"plain text":         "isn't a PHC string",
		"$2b$10$abcdefghijk": "isn't a PHC string",
		"$argon2i$v=19$m=19456,t=2,p=1$" + salt + "$" + hash:        `is "argon2i", not argon2id`,
		"$argon2id$v=16$m=19456,t=2,p=1$" + salt + "$" + hash:       "has version",
		"$argon2id$v=19$m=19456,t=2$" + salt + "$" + hash:           "all of m, t, and p",
		"$argon2id$v=19$m=19456,t=2,p=1,x=3$" + salt + "$" + hash:   `unknown parameter "x"`,
		"$argon2id$v=19$m=19456,t=2,t=2,p=1$" + salt + "$" + hash:   "malformed parameter list",
		"$argon2id$v=19$m=2000000,t=2,p=1$" + salt + "$" + hash:     "2000000 KiB",
		"$argon2id$v=19$m=19456,t=11,p=1$" + salt + "$" + hash:      "11 passes",
		"$argon2id$v=19$m=19456,t=2,p=17$" + salt + "$" + hash:      "17 lanes",
		"$argon2id$v=19$m=19456,t=2,p=1$" + "!!" + "$" + hash:       "salt",
		"$argon2id$v=19$m=19456,t=2,p=1$" + salt + "$" + hash + "=": "hash",
	}
	for stored, fragment := range cases {
		_, fault := in.Call(context.Background(), "matches", map[string]Value{"password": Str("pw"), "stored": Str(stored)})
		if fault == nil || !strings.Contains(fault.Error(), fragment) || !strings.Contains(fault.Error(), "the stored hash") {
			t.Errorf("passwordMatches(%q): fault = %v, want one containing %q", stored, fault, fragment)
		}
	}
}

// ADR 0076 §4: with every hash slot taken, a hash waits for one, and stops
// at the call's deadline rather than running.
func TestPasswordHashesWaitForASlot(t *testing.T) {
	in := cryptoInterp(t, 1)
	for i := 0; i < cap(hashSlots); i++ {
		hashSlots <- struct{}{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, fault := in.Call(ctx, "hash", map[string]Value{"password": Str("pw")})
	if fault == nil || !strings.Contains(fault.String(), "cancelled") {
		t.Errorf("with every slot taken: got fault %v, want cancelled at the deadline", fault)
	}
	done := make(chan Value)
	go func() { done <- callOK(t, in, "hash", map[string]Value{"password": Str("pw")}) }()
	<-hashSlots // one frees, and the waiting hash takes it
	if v := <-done; !strings.HasPrefix(string(v.(Str)), "$argon2id$") {
		t.Errorf("got %v", v)
	}
	for i := 1; i < cap(hashSlots); i++ {
		<-hashSlots
	}
}

// ADR 0113 §2: CRC-32C against known answers: the "123456789" check value
// every CRC catalogue gives for CRC-32C, and RFC 3720 Appendix B.4's four
// 32-byte iSCSI vectors (which it writes low byte first).
func TestCrc32cVectors(t *testing.T) {
	in := checkedInterp(t, `rule checksum(data: bytes) -> int {
    return crypto.crc32c(data)
}`)
	run := func(n int, f func(i int) byte) []byte {
		out := make([]byte, n)
		for i := range out {
			out[i] = f(i)
		}
		return out
	}
	cases := []struct {
		name string
		data []byte
		want int64
	}{
		{"empty", nil, 0},
		{"check value", []byte("123456789"), 0xE3069283},
		{"32 zeros", run(32, func(int) byte { return 0 }), 0x8A9136AA},
		{"32 ones", run(32, func(int) byte { return 0xFF }), 0x62A8AB43},
		{"ascending", run(32, func(i int) byte { return byte(i) }), 0x46DD794E},
		{"descending", run(32, func(i int) byte { return byte(31 - i) }), 0x113FDB5C},
	}
	for _, c := range cases {
		if got := callOK(t, in, "checksum", map[string]Value{"data": Bytes{B: c.data}}); got != Value(Int(c.want)) {
			t.Errorf("%s: crc32c = %v, want %d (0x%08X)", c.name, got, c.want, c.want)
		}
	}
}

// ADR 0113 §6 (amended 2026-11-21): PBKDF2-HMAC-SHA-512 for SCRAM-SHA-512,
// against the widely published vectors for P "password", S "salt", c 1,
// and the long password and salt at c 4096 (RFC 6070's inputs, 64 bytes
// out), which Python's hashlib.pbkdf2_hmac gives too.
func TestPbkdf2Sha512(t *testing.T) {
	in := checkedInterp(t,
		`rule derive(password: bytes, salt: bytes, iterations: int) -> string {
    return crypto.pbkdf2Sha512(password, salt, iterations).toHex()
}`)
	cases := []struct {
		password, salt string
		iterations     int
		want           string
	}{
		{"password", "salt", 1, "867f70cf1ade02cff3752599a3a53dc4af34c7a669815ae5d513554e1c8cf252c02d470a285a0501bad999bfe943c08f050235d7d68b1da55e63f73b60a57fce"},
		{"passwordPASSWORDpassword", "saltSALTsaltSALTsaltSALTsaltSALTsalt", 4096, "8c0511f4c6e597c6ac6315d8f0362e225f3c501495ba23b868c005174dc4ee71115b59f9e60cd9532fa33e0f75aefe30225c583a186cd82bd4daea9724a3d3b8"},
	}
	for _, c := range cases {
		got := callOK(t, in, "derive", map[string]Value{"password": Bytes{B: []byte(c.password)}, "salt": Bytes{B: []byte(c.salt)}, "iterations": Int(c.iterations)})
		if got != Value(Str(c.want)) {
			t.Errorf("pbkdf2Sha512(%q, %q, %d) = %v", c.password, c.salt, c.iterations, got)
		}
	}
	if _, f := in.Call(context.Background(), "derive", map[string]Value{"password": Bytes{}, "salt": Bytes{}, "iterations": Int(0)}); f == nil || !strings.Contains(f.Message, "outside 1 to") {
		t.Errorf("0 iterations: fault = %v", f)
	}
}

// ADR 0085 §7: PBKDF2-HMAC-SHA-256 against RFC 7914 §11's first vector
// (P "passwd", S "salt", c 1; its first 32 bytes), and xor.
func TestPbkdf2AndXor(t *testing.T) {
	in := checkedInterp(t,
		`rule derive(password: bytes, salt: bytes, iterations: int) -> string {
    return crypto.pbkdf2Sha256(password, salt, iterations).toHex()
}`,
		`rule flip(a: bytes, b: bytes) -> bytes {
    return a.xor(b)
}`)
	got := callOK(t, in, "derive", map[string]Value{"password": Bytes{B: []byte("passwd")}, "salt": Bytes{B: []byte("salt")}, "iterations": Int(1)})
	if got != Value(Str("55ac046e56e3089fec1691c22544b605f94185216dde0465e68b9d57c20dacbc")) {
		t.Errorf("pbkdf2Sha256 = %v", got)
	}
	if _, f := in.Call(context.Background(), "derive", map[string]Value{"password": Bytes{}, "salt": Bytes{}, "iterations": Int(0)}); f == nil || !strings.Contains(f.Message, "outside 1 to") {
		t.Errorf("0 iterations: fault = %v", f)
	}
	if got := callOK(t, in, "flip", map[string]Value{"a": Bytes{B: []byte{0xf0, 0x0f}}, "b": Bytes{B: []byte{0xff, 0xff}}}); !Equal(got, Bytes{B: []byte{0x0f, 0xf0}}) {
		t.Errorf("xor = %v", got)
	}
	if _, f := in.Call(context.Background(), "flip", map[string]Value{"a": Bytes{B: []byte{1}}, "b": Bytes{B: []byte{1, 2}}}); f == nil || !strings.Contains(f.Message, "same length") {
		t.Errorf("xor of different lengths: fault = %v", f)
	}
}
