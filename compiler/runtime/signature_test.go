package runtime

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"math/big"
	"strings"
	"testing"

	"fiig-lang/compiler/types"
)

// ADR 0146 §1-§3: public-key signatures, checked through fiig rules
// against keys and signatures Go's standard library makes.

var signatureProgram = []string{
	`rule verifies(key: PublicKey, algorithm: SignatureAlgorithm, data: bytes, signature: bytes) -> bool {
    return crypto.verify(key, algorithm, data, signature);
}`,
	`rule checked(key: PublicKey) -> string {
    return match (crypto.checkPublicKey(key)) {
        Ok(_) -> "ok",
        Err(KeyError.Malformed(reason)) -> "malformed: ${reason}",
        Err(KeyError.Unsupported(reason)) -> "unsupported: ${reason}"
    }
}`,
	`rule fromPem(pem: string) -> Result<PublicKey, KeyError> {
    return crypto.publicKeyFromPem(pem);
}`,
	`rule signed(key: Secret, algorithm: SignatureAlgorithm, data: bytes) -> bytes {
    return crypto.sign(key, algorithm, data);
}`,
}

func algorithm(name string) EnumValue {
	return EnumValue{TypeName: "SignatureAlgorithm", Variant: name, Decl: types.SignatureAlgorithmDecl}
}

func pubKey(t *testing.T, key crypto.PublicKey) EnumValue {
	t.Helper()
	v, err := publicKeyValue(key)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func privatePem(t *testing.T, key crypto.PrivateKey) Secret {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return NewSecret("SIGNING_KEY", string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})))
}

type signingKeys struct {
	rsa     *rsa.PrivateKey
	p256    *ecdsa.PrivateKey
	p384    *ecdsa.PrivateKey
	ed25519 ed25519.PrivateKey
}

func newSigningKeys(t *testing.T) signingKeys {
	t.Helper()
	r, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p256, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	p384, _ := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	_, ed, _ := ed25519.GenerateKey(rand.Reader)
	return signingKeys{rsa: r, p256: p256, p384: p384, ed25519: ed}
}

func (k signingKeys) forAlgorithm(name string) crypto.PrivateKey {
	switch {
	case name == "Ed25519":
		return k.ed25519
	case name == "EcdsaP256Sha256":
		return k.p256
	case name == "EcdsaP384Sha384":
		return k.p384
	}
	return k.rsa
}

func publicOf(key crypto.PrivateKey) crypto.PublicKey {
	return key.(crypto.Signer).Public()
}

// Everything sign makes, verify accepts, for every algorithm sign signs;
// a changed byte, another key, or another algorithm of the same key type
// is false.
func TestSignAndVerify(t *testing.T) {
	in := checkedInterp(t, signatureProgram...)
	keys := newSigningKeys(t)
	data := Bytes{B: []byte(`{"sub":"1234567890"}`)}
	for _, name := range []string{"Ed25519", "EcdsaP256Sha256", "EcdsaP384Sha384", "RsaPkcs1Sha256", "RsaPkcs1Sha384", "RsaPkcs1Sha512"} {
		private := keys.forAlgorithm(name)
		sig := callOK(t, in, "signed", map[string]Value{"key": privatePem(t, private), "algorithm": algorithm(name), "data": data}).(Bytes)
		public := pubKey(t, publicOf(private))
		verify := func(key EnumValue, alg string, d, s []byte) Value {
			return callOK(t, in, "verifies", map[string]Value{"key": key, "algorithm": algorithm(alg), "data": Bytes{B: d}, "signature": Bytes{B: s}})
		}
		if got := verify(public, name, data.B, sig.B); got != Bool(true) {
			t.Errorf("%s: its own signature = %v", name, got)
		}
		changed := append([]byte(nil), data.B...)
		changed[0] ^= 1
		if got := verify(public, name, changed, sig.B); got != Bool(false) {
			t.Errorf("%s: changed data = %v", name, got)
		}
		if got := verify(public, name, data.B, sig.B[:len(sig.B)-1]); got != Bool(false) {
			t.Errorf("%s: a short signature = %v", name, got)
		}
		other := pubKey(t, publicOf(newSigningKeys(t).forAlgorithm(name)))
		if got := verify(other, name, data.B, sig.B); got != Bool(false) {
			t.Errorf("%s: another key = %v", name, got)
		}
		// The algorithm decides; the key can't stand for another kind.
		for _, wrong := range []string{"Ed25519", "EcdsaP256Sha256", "RsaPkcs1Sha256", "RsaPssSha256"} {
			if wrong != name && verify(public, wrong, data.B, sig.B) != Bool(false) {
				t.Errorf("%s's signature verified as %s", name, wrong)
			}
		}
	}
}

// ECDSA signs deterministically (RFC 6979), so sign is pure: the same key
// and data give the same signature.
func TestSignIsDeterministic(t *testing.T) {
	in := checkedInterp(t, signatureProgram...)
	keys := newSigningKeys(t)
	for _, name := range []string{"EcdsaP256Sha256", "RsaPkcs1Sha256", "Ed25519"} {
		args := map[string]Value{"key": privatePem(t, keys.forAlgorithm(name)), "algorithm": algorithm(name), "data": Bytes{B: []byte("x")}}
		a := callOK(t, in, "signed", args).(Bytes)
		b := callOK(t, in, "signed", args).(Bytes)
		if !bytes.Equal(a.B, b.B) {
			t.Errorf("%s signed twice differently", name)
		}
	}
}

// PSS verifies, with a salt as long as the hash (RFC 7518 §3.5).
func TestVerifyPss(t *testing.T) {
	in := checkedInterp(t, signatureProgram...)
	keys := newSigningKeys(t)
	data := []byte("pss")
	for name, h := range map[string]crypto.Hash{"RsaPssSha256": crypto.SHA256, "RsaPssSha384": crypto.SHA384, "RsaPssSha512": crypto.SHA512} {
		sig, err := rsa.SignPSS(rand.Reader, keys.rsa, h, digest(h, data), &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash})
		if err != nil {
			t.Fatal(err)
		}
		got := callOK(t, in, "verifies", map[string]Value{"key": pubKey(t, &keys.rsa.PublicKey), "algorithm": algorithm(name), "data": Bytes{B: data}, "signature": Bytes{B: sig}})
		if got != Bool(true) {
			t.Errorf("%s = %v", name, got)
		}
	}
}

// CVE-2022-21449: r = s = 0 is no signature, for any message.
func TestVerifyRefusesAZeroEcdsaSignature(t *testing.T) {
	in := checkedInterp(t, signatureProgram...)
	keys := newSigningKeys(t)
	for name, size := range map[string]int{"EcdsaP256Sha256": 32, "EcdsaP384Sha384": 48} {
		got := callOK(t, in, "verifies", map[string]Value{
			"key": pubKey(t, publicOf(keys.forAlgorithm(name))), "algorithm": algorithm(name),
			"data": Bytes{B: []byte("anything")}, "signature": Bytes{B: make([]byte, 2*size)},
		})
		if got != Bool(false) {
			t.Errorf("%s: r = s = 0 verified", name)
		}
	}
}

// A key verify can't use is false there, and checkPublicKey says why.
func TestCheckPublicKey(t *testing.T) {
	in := checkedInterp(t, signatureProgram...)
	small, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	keys := newSigningKeys(t)
	good := pubKey(t, publicOf(keys.p256))
	offCurve := pubKey(t, publicOf(keys.p256))
	offCurve.Fields = []Value{offCurve.Fields[0], Bytes{B: bytes.Repeat([]byte{1}, 32)}}
	evenExponent := pubKey(t, &keys.rsa.PublicKey)
	evenExponent.Fields = []Value{evenExponent.Fields[0], Bytes{B: big.NewInt(65536).Bytes()}}
	for _, c := range []struct {
		key  EnumValue
		want string
	}{
		{good, "ok"},
		{pubKey(t, &keys.rsa.PublicKey), "ok"},
		{pubKey(t, publicOf(keys.ed25519)), "ok"},
		{pubKey(t, &small.PublicKey), "unsupported: an RSA modulus of 1024 bits, under 2048"},
		{offCurve, "malformed: a point that isn't on P-256"},
		{evenExponent, "malformed: an RSA exponent that isn't odd and between 3 and 2^31"},
		{EnumValue{TypeName: "PublicKey", Variant: "Ed25519", Fields: []Value{Bytes{B: []byte{1, 2}}}, Decl: types.PublicKeyDecl}, "malformed: an Ed25519 key of 2 bytes, not 32"},
	} {
		if got := callOK(t, in, "checked", map[string]Value{"key": c.key}); got != Str(c.want) {
			t.Errorf("checked = %v, want %q", got, c.want)
		}
	}
	sig := ecdsaSign(t, keys.p256, []byte("x"))
	if got := callOK(t, in, "verifies", map[string]Value{"key": offCurve, "algorithm": algorithm("EcdsaP256Sha256"), "data": Bytes{B: []byte("x")}, "signature": Bytes{B: sig}}); got != Bool(false) {
		t.Errorf("a key off the curve verified: %v", got)
	}
}

func ecdsaSign(t *testing.T, key *ecdsa.PrivateKey, data []byte) []byte {
	t.Helper()
	der, err := key.Sign(rand.Reader, digest(crypto.SHA256, data), crypto.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	rs, err := ecdsaRS(der, 32)
	if err != nil {
		t.Fatal(err)
	}
	return rs
}

// §2: SPKI and PKCS #1 public keys, read as the same PublicKey, which
// verifies what the private key signed.
func TestPublicKeyFromPem(t *testing.T) {
	in := checkedInterp(t, signatureProgram...)
	keys := newSigningKeys(t)
	spki := func(key crypto.PublicKey) string {
		der, err := x509.MarshalPKIXPublicKey(key)
		if err != nil {
			t.Fatal(err)
		}
		return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
	}
	pkcs1 := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PUBLIC KEY", Bytes: x509.MarshalPKCS1PublicKey(&keys.rsa.PublicKey)}))
	for text, want := range map[string]EnumValue{
		spki(&keys.rsa.PublicKey):    pubKey(t, &keys.rsa.PublicKey),
		pkcs1:                        pubKey(t, &keys.rsa.PublicKey),
		spki(publicOf(keys.p384)):    pubKey(t, publicOf(keys.p384)),
		spki(publicOf(keys.ed25519)): pubKey(t, publicOf(keys.ed25519)),
	} {
		r := callOK(t, in, "fromPem", map[string]Value{"pem": Str(text)}).(Result)
		if !r.Ok || !Equal(r.Value, want) {
			t.Errorf("fromPem = %v, want %v", r, want)
		}
	}
	for text, want := range map[string]string{
		"not a key": "Malformed(no PEM block)",
		string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte{1}})): `a "CERTIFICATE" PEM block`,
		string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: []byte{1}})):  "Malformed(the key doesn't parse",
	} {
		r := callOK(t, in, "fromPem", map[string]Value{"pem": Str(text)}).(Result)
		if r.Ok || !strings.Contains(r.Value.String(), want) {
			t.Errorf("fromPem(%q) = %v, want %q", text, r, want)
		}
	}
}

// §3: a key that can't sign is a fault that names the problem, never the
// key; PSS verifies but doesn't sign.
func TestSignFaults(t *testing.T) {
	in := checkedInterp(t, signatureProgram...)
	keys := newSigningKeys(t)
	key := privatePem(t, keys.p256)
	for _, c := range []struct {
		key       Secret
		algorithm string
		want      string
	}{
		{key, "RsaPkcs1Sha256", "a key on P-256 can't sign RsaPkcs1Sha256"},
		{key, "EcdsaP384Sha384", "a key on P-256 can't sign EcdsaP384Sha384"},
		{privatePem(t, keys.rsa), "RsaPssSha256", "RsaPssSha256 verifies but doesn't sign"},
		{NewSecret("SIGNING_KEY", "not pem"), "Ed25519", "the key holds no PEM block"},
	} {
		_, f := in.Call(t.Context(), "signed", map[string]Value{"key": c.key, "algorithm": algorithm(c.algorithm), "data": Bytes{B: []byte("x")}})
		if f == nil || !strings.Contains(f.Message, c.want) {
			t.Errorf("sign with %s: fault = %v, want %q", c.algorithm, f, c.want)
		}
		if f != nil && strings.Contains(f.Message, "PRIVATE KEY") && strings.Contains(f.Message, "MII") {
			t.Errorf("a fault quoted the key: %s", f.Message)
		}
	}
}

// ADR 0146 §4: HMAC-SHA-384, for HS384, against RFC 4231's test case 2.
func TestHmacSha384Vector(t *testing.T) {
	in := checkedInterp(t, `rule mac(key: bytes, data: bytes) -> string {
    return crypto.hmacSha384(key, data).toHex();
}`)
	got := callOK(t, in, "mac", map[string]Value{"key": Bytes{B: []byte("Jefe")}, "data": Bytes{B: []byte("what do ya want for nothing?")}})
	want := "af45d2e376484031617f78d2b58a6b1b9c7ef464f5a01b47e42ec3736322445e8e2240ca5e69e2c78b3239ecfab21649"
	if got != Str(want) {
		t.Fatalf("hmacSha384 = %v, want %s", got, want)
	}
}
