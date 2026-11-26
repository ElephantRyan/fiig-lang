package runtime

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"

	"golang.org/x/crypto/cryptobyte"
	"golang.org/x/crypto/cryptobyte/asn1"

	"fiig-lang/compiler/types"
)

// ADR 0146 §1-§3: public-key signatures in the prelude crypto group, all
// of it Go's standard library. A PublicKey is a plain value, checked every
// time verify uses it; a private key is PEM in a Secret, read every time
// sign uses it.

const (
	// minRsaBits is the smallest RSA modulus verify accepts (§1).
	minRsaBits = 2048
	// maxRsaBits bounds the work one verify can be made to do.
	maxRsaBits = 8192
	// maxRsaExponent is the largest public exponent, as crypto/rsa limits it.
	maxRsaExponent = 1<<31 - 1
)

// signatureScheme is what one SignatureAlgorithm variant means.
type signatureScheme struct {
	key  string // the PublicKey variant it takes
	hash crypto.Hash
	pss  bool
}

var signatureSchemes = map[string]signatureScheme{
	"Ed25519":         {key: "Ed25519"},
	"EcdsaP256Sha256": {key: "EcP256", hash: crypto.SHA256},
	"EcdsaP384Sha384": {key: "EcP384", hash: crypto.SHA384},
	"RsaPkcs1Sha256":  {key: "Rsa", hash: crypto.SHA256},
	"RsaPkcs1Sha384":  {key: "Rsa", hash: crypto.SHA384},
	"RsaPkcs1Sha512":  {key: "Rsa", hash: crypto.SHA512},
	"RsaPssSha256":    {key: "Rsa", hash: crypto.SHA256, pss: true},
	"RsaPssSha384":    {key: "Rsa", hash: crypto.SHA384, pss: true},
	"RsaPssSha512":    {key: "Rsa", hash: crypto.SHA512, pss: true},
}

func init() {
	cryptoFormulas["verify"] = func(_ context.Context, _ *Interp, args []Value) (Value, *Fault) {
		key, err := publicKeyOf(args[0].(EnumValue))
		if err != nil {
			return Bool(false), nil
		}
		return Bool(verifySignature(key, args[1].(EnumValue).Variant, args[2].(Bytes).B, args[3].(Bytes).B)), nil
	}
	cryptoFormulas["checkPublicKey"] = func(_ context.Context, _ *Interp, args []Value) (Value, *Fault) {
		if _, err := publicKeyOf(args[0].(EnumValue)); err != nil {
			return keyError(err), nil
		}
		return Result{Ok: true, Value: None{}}, nil
	}
	cryptoFormulas["publicKeyFromPem"] = func(_ context.Context, _ *Interp, args []Value) (Value, *Fault) {
		v, err := publicKeyFromPem(string(args[0].(Str)))
		if err != nil {
			return keyError(err), nil
		}
		return Result{Ok: true, Value: v}, nil
	}
	cryptoFormulas["sign"] = func(_ context.Context, _ *Interp, args []Value) (Value, *Fault) {
		sig, err := signWith(args[0].(Secret).value, args[1].(EnumValue).Variant, args[2].(Bytes).B)
		if err != nil {
			// The key is the program's own configuration (§3): a fault, which
			// names the problem and never the key.
			return nil, Faultf("crypto.sign: %v (ADR 0146 §3)", err)
		}
		return Bytes{B: sig}, nil
	}
}

// keyProblem is why a public key can't be used: Malformed or Unsupported.
type keyProblem struct {
	unsupported bool
	reason      string
}

func (k *keyProblem) Error() string { return k.reason }

func malformed(format string, a ...any) error {
	return &keyProblem{reason: fmt.Sprintf(format, a...)}
}

func unsupported(format string, a ...any) error {
	return &keyProblem{unsupported: true, reason: fmt.Sprintf(format, a...)}
}

func keyError(err error) Value {
	variant, reason := "Malformed", err.Error()
	var k *keyProblem
	if errors.As(err, &k) && k.unsupported {
		variant = "Unsupported"
	}
	return Result{Ok: false, Value: EnumValue{TypeName: "KeyError", Variant: variant, Fields: []Value{Str(reason)}, Decl: types.KeyErrorDecl}}
}

// publicKeyOf is v as a Go public key, with verify's checks (§1).
func publicKeyOf(v EnumValue) (crypto.PublicKey, error) {
	field := func(i int) []byte { return v.Fields[i].(Bytes).B }
	switch v.Variant {
	case "Rsa":
		n := new(big.Int).SetBytes(field(0))
		bits := n.BitLen()
		if bits < minRsaBits {
			return nil, unsupported("an RSA modulus of %d bits, under %d", bits, minRsaBits)
		}
		if bits > maxRsaBits {
			return nil, unsupported("an RSA modulus of %d bits, over %d", bits, maxRsaBits)
		}
		if n.Bit(0) == 0 {
			return nil, malformed("an RSA modulus that is even")
		}
		e := new(big.Int).SetBytes(field(1))
		if e.Bit(0) == 0 || e.Cmp(big.NewInt(3)) < 0 || e.Cmp(big.NewInt(maxRsaExponent)) > 0 {
			return nil, malformed("an RSA exponent that isn't odd and between 3 and 2^31")
		}
		return &rsa.PublicKey{N: n, E: int(e.Int64())}, nil
	case "EcP256":
		return ecKey(elliptic.P256(), 32, field(0), field(1))
	case "EcP384":
		return ecKey(elliptic.P384(), 48, field(0), field(1))
	case "Ed25519":
		k := field(0)
		if len(k) != ed25519.PublicKeySize {
			return nil, malformed("an Ed25519 key of %d bytes, not %d", len(k), ed25519.PublicKeySize)
		}
		return ed25519.PublicKey(k), nil
	}
	return nil, unsupported("a %s key", v.Variant)
}

// ecKey is the point (x, y) on curve, each coordinate at most size bytes
// (a shorter one is a JWK that dropped leading zeros, and is padded).
func ecKey(curve elliptic.Curve, size int, x, y []byte) (crypto.PublicKey, error) {
	if len(x) > size || len(y) > size {
		return nil, malformed("a %s coordinate longer than %d bytes", curve.Params().Name, size)
	}
	point := make([]byte, 1+2*size)
	point[0] = 4
	copy(point[1+size-len(x):1+size], x)
	copy(point[1+2*size-len(y):], y)
	k, err := ecdsa.ParseUncompressedPublicKey(curve, point)
	if err != nil {
		return nil, malformed("a point that isn't on %s", curve.Params().Name)
	}
	return k, nil
}

// digest is data hashed by h.
func digest(h crypto.Hash, data []byte) []byte {
	switch h {
	case crypto.SHA256:
		sum := sha256.Sum256(data)
		return sum[:]
	case crypto.SHA384:
		sum := sha512.Sum384(data)
		return sum[:]
	}
	sum := sha512.Sum512(data)
	return sum[:]
}

// verifySignature is whether signature is key's over data under the
// algorithm. A key of the wrong kind for it is false (§1).
func verifySignature(key crypto.PublicKey, algorithm string, data, signature []byte) bool {
	scheme, ok := signatureSchemes[algorithm]
	if !ok {
		return false
	}
	switch k := key.(type) {
	case ed25519.PublicKey:
		return scheme.key == "Ed25519" && len(signature) == ed25519.SignatureSize && ed25519.Verify(k, data, signature)
	case *ecdsa.PublicKey:
		size := (k.Curve.Params().BitSize + 7) / 8
		want := map[string]string{"P-256": "EcP256", "P-384": "EcP384"}[k.Curve.Params().Name]
		if scheme.key != want || len(signature) != 2*size {
			return false
		}
		// JOSE's r || s (RFC 7518 §3.4), as the DER crypto/ecdsa reads.
		// VerifyASN1 refuses r or s of zero or not below the order, the
		// check CVE-2022-21449 was missing.
		der, err := ecdsaDER(signature[:size], signature[size:])
		return err == nil && ecdsa.VerifyASN1(k, digest(scheme.hash, data), der)
	case *rsa.PublicKey:
		if scheme.key != "Rsa" {
			return false
		}
		d := digest(scheme.hash, data)
		if scheme.pss {
			// RFC 7518 §3.5: the salt is as long as the hash.
			return rsa.VerifyPSS(k, scheme.hash, d, signature, &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash}) == nil
		}
		return rsa.VerifyPKCS1v15(k, scheme.hash, d, signature) == nil
	}
	return false
}

func ecdsaDER(r, s []byte) ([]byte, error) {
	var b cryptobyte.Builder
	b.AddASN1(asn1.SEQUENCE, func(b *cryptobyte.Builder) {
		b.AddASN1BigInt(new(big.Int).SetBytes(r))
		b.AddASN1BigInt(new(big.Int).SetBytes(s))
	})
	return b.Bytes()
}

// publicKeyFromPem reads one SPKI or PKCS #1 public key (§2) and returns
// it as a PublicKey that has passed checkPublicKey.
func publicKeyFromPem(text string) (Value, error) {
	block, _ := pem.Decode([]byte(text))
	if block == nil {
		return nil, malformed("no PEM block")
	}
	var key any
	var err error
	switch block.Type {
	case "PUBLIC KEY":
		key, err = x509.ParsePKIXPublicKey(block.Bytes)
	case "RSA PUBLIC KEY":
		key, err = x509.ParsePKCS1PublicKey(block.Bytes)
	default:
		return nil, malformed("a %q PEM block, not a PUBLIC KEY or RSA PUBLIC KEY", block.Type)
	}
	if err != nil {
		return nil, malformed("the key doesn't parse: %v", err)
	}
	v, err := publicKeyValue(key)
	if err != nil {
		return nil, err
	}
	if _, err := publicKeyOf(v); err != nil {
		return nil, err
	}
	return v, nil
}

// publicKeyValue is a Go public key as a PublicKey value.
func publicKeyValue(key any) (EnumValue, error) {
	enum := func(variant string, fields ...[]byte) EnumValue {
		vs := make([]Value, len(fields))
		for i, f := range fields {
			vs[i] = Bytes{B: f}
		}
		return EnumValue{TypeName: "PublicKey", Variant: variant, Fields: vs, Decl: types.PublicKeyDecl}
	}
	switch k := key.(type) {
	case *rsa.PublicKey:
		return enum("Rsa", k.N.Bytes(), big.NewInt(int64(k.E)).Bytes()), nil
	case *ecdsa.PublicKey:
		point, err := k.Bytes()
		if err != nil {
			return EnumValue{}, malformed("the key doesn't parse: %v", err)
		}
		size := (len(point) - 1) / 2
		switch k.Curve.Params().Name {
		case "P-256":
			return enum("EcP256", point[1:1+size], point[1+size:]), nil
		case "P-384":
			return enum("EcP384", point[1:1+size], point[1+size:]), nil
		}
		return EnumValue{}, unsupported("a key on %s", k.Curve.Params().Name)
	case ed25519.PublicKey:
		return enum("Ed25519", []byte(k)), nil
	}
	return EnumValue{}, unsupported("a %T key", key)
}

// signWith signs data with the PEM private key in text (§3). Only the
// deterministic algorithms sign, so sign is pure.
func signWith(text, algorithm string, data []byte) ([]byte, error) {
	scheme, ok := signatureSchemes[algorithm]
	if !ok {
		return nil, fmt.Errorf("no algorithm %s", algorithm)
	}
	if scheme.pss {
		return nil, fmt.Errorf("%s verifies but doesn't sign: PSS needs a random salt", algorithm)
	}
	block, _ := pem.Decode([]byte(text))
	if block == nil {
		return nil, errors.New("the key holds no PEM block")
	}
	var key any
	var err error
	switch block.Type {
	case "PRIVATE KEY":
		key, err = x509.ParsePKCS8PrivateKey(block.Bytes)
	case "RSA PRIVATE KEY":
		key, err = x509.ParsePKCS1PrivateKey(block.Bytes)
	case "EC PRIVATE KEY":
		key, err = x509.ParseECPrivateKey(block.Bytes)
	default:
		return nil, fmt.Errorf("the key is a %q PEM block, not a PRIVATE KEY, RSA PRIVATE KEY or EC PRIVATE KEY", block.Type)
	}
	if err != nil {
		return nil, errors.New("the key doesn't parse as its PEM block says")
	}
	switch k := key.(type) {
	case ed25519.PrivateKey:
		if scheme.key != "Ed25519" {
			return nil, fmt.Errorf("an Ed25519 key can't sign %s", algorithm)
		}
		return ed25519.Sign(k, data), nil
	case *ecdsa.PrivateKey:
		want := map[string]string{"P-256": "EcP256", "P-384": "EcP384"}[k.Curve.Params().Name]
		if scheme.key != want {
			return nil, fmt.Errorf("a key on %s can't sign %s", k.Curve.Params().Name, algorithm)
		}
		// A nil random source signs deterministically, by RFC 6979.
		der, err := k.Sign(nil, digest(scheme.hash, data), scheme.hash)
		if err != nil {
			return nil, err
		}
		return ecdsaRS(der, (k.Curve.Params().BitSize+7)/8)
	case *rsa.PrivateKey:
		if scheme.key != "Rsa" {
			return nil, fmt.Errorf("an RSA key can't sign %s", algorithm)
		}
		if k.N.BitLen() < minRsaBits {
			return nil, fmt.Errorf("an RSA key of %d bits, under %d", k.N.BitLen(), minRsaBits)
		}
		return rsa.SignPKCS1v15(nil, k, scheme.hash, digest(scheme.hash, data))
	}
	return nil, fmt.Errorf("a %T key", key)
}

// ecdsaRS is a DER ECDSA signature as JOSE's r || s, each size bytes.
func ecdsaRS(der []byte, size int) ([]byte, error) {
	var r, s big.Int
	in := cryptobyte.String(der)
	var seq cryptobyte.String
	if !in.ReadASN1(&seq, asn1.SEQUENCE) || !seq.ReadASN1Integer(&r) || !seq.ReadASN1Integer(&s) {
		return nil, errors.New("crypto/ecdsa gave a signature that isn't DER")
	}
	out := make([]byte, 2*size)
	r.FillBytes(out[:size])
	s.FillBytes(out[size:])
	return out, nil
}
