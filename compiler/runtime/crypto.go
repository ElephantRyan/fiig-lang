package runtime

import (
	"context"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"hash"
	"hash/crc32"
	goruntime "runtime"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"

	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// ADR 0075: the prelude crypto group's bodies, by member, over argument
// values in parameter order.

// Argon2id's parameters for a new hash (ADR 0075 §5): OWASP's 19 MiB, 2
// passes, 1 lane, a 16-byte salt, and a 32-byte hash.
const (
	argonMemoryKiB = 19456
	argonPasses    = 2
	argonLanes     = 1
	argonSaltLen   = 16
	argonHashLen   = 32
)

// The most a stored hash may ask for (§5), so a tampered row can't make
// one check take minutes or gigabytes.
const (
	maxArgonMemoryKiB = 1 << 20 // 1 GiB
	maxArgonPasses    = 10
	maxArgonLanes     = 16
)

// hashSlots bounds the Argon2id runs at once to one per CPU (ADR 0076 §4):
// it is all CPU, so more at once finishes none sooner and only multiplies
// the memory. Shared by the process, so fiig run and tests are bounded too.
var hashSlots = make(chan struct{}, goruntime.GOMAXPROCS(0))

// argonKey is argon2.IDKey in one of hashSlots, waiting for one while the
// call's deadline allows.
func argonKey(ctx context.Context, password string, salt []byte, passes, memory uint32, lanes uint8, keyLen uint32) ([]byte, *Fault) {
	select {
	case hashSlots <- struct{}{}:
	case <-ctx.Done():
		return nil, Faultf("cancelled: %v", ctx.Err())
	}
	defer func() { <-hashSlots }()
	return argon2.IDKey([]byte(password), salt, passes, memory, lanes, keyLen), nil
}

// maxPbkdf2Iterations bounds one PBKDF2 call (ADR 0085 §7).
const maxPbkdf2Iterations = 10_000_000

// maxHkdfLength is the most HKDF-SHA-256 can make: 255 blocks of 32 bytes
// (RFC 5869 §2.3).
const maxHkdfLength = 255 * sha256.Size

type cryptoFunc func(ctx context.Context, in *Interp, args []Value) (Value, *Fault)

// castagnoli is CRC-32C's table (ADR 0113 §2).
var castagnoli = crc32.MakeTable(crc32.Castagnoli)

var cryptoFormulas = map[string]cryptoFunc{
	"crc32c": func(_ context.Context, _ *Interp, args []Value) (Value, *Fault) {
		return Int(crc32.Checksum(args[0].(Bytes).B, castagnoli)), nil
	},
	"sha256": func(_ context.Context, _ *Interp, args []Value) (Value, *Fault) {
		sum := sha256.Sum256(args[0].(Bytes).B)
		return Bytes{B: sum[:]}, nil
	},
	"hmacSha256": hmacWith("hmacSha256", sha256.New),
	// ADR 0075 §6 (amended 2026-11-21): SHA-1 and SHA-512, and their HMACs.
	"sha1": func(_ context.Context, _ *Interp, args []Value) (Value, *Fault) {
		sum := sha1.Sum(args[0].(Bytes).B)
		return Bytes{B: sum[:]}, nil
	},
	"hmacSha1": hmacWith("hmacSha1", sha1.New),
	"sha512": func(_ context.Context, _ *Interp, args []Value) (Value, *Fault) {
		sum := sha512.Sum512(args[0].(Bytes).B)
		return Bytes{B: sum[:]}, nil
	},
	"hmacSha512": hmacWith("hmacSha512", sha512.New),
	// ADR 0146 §4: for a JWT's HS384.
	"hmacSha384": hmacWith("hmacSha384", sha512.New384),
	// ADR 0075 §6 (amended 2026-11-21): RFC 5869 HKDF-SHA-256, extract
	// then expand, `length` bytes out.
	"hkdfSha256": func(_ context.Context, _ *Interp, args []Value) (Value, *Fault) {
		key, f := keyBytes("hkdfSha256", "key", args[0])
		if f != nil {
			return nil, f
		}
		n := int64(args[3].(Int))
		if n < 1 || n > maxHkdfLength {
			return nil, Faultf("crypto.hkdfSha256: a length of %d is outside 1 to %d", n, maxHkdfLength)
		}
		out, err := hkdf.Key(sha256.New, key, args[1].(Bytes).B, string(args[2].(Bytes).B), int(n))
		if err != nil {
			return nil, Faultf("crypto.hkdfSha256: %v", err)
		}
		return Bytes{B: out}, nil
	},
	// ADR 0085 §7: RFC 8018 PBKDF2-HMAC-SHA-256, 32 bytes out.
	"pbkdf2Sha256": func(_ context.Context, _ *Interp, args []Value) (Value, *Fault) {
		password, f := keyBytes("pbkdf2Sha256", "password", args[0])
		if f != nil {
			return nil, f
		}
		n := int64(args[2].(Int))
		if n < 1 || n > maxPbkdf2Iterations {
			return nil, Faultf("crypto.pbkdf2Sha256: %d iterations is outside 1 to %d", n, maxPbkdf2Iterations)
		}
		key, err := pbkdf2.Key(sha256.New, string(password), args[1].(Bytes).B, int(n), sha256.Size)
		if err != nil {
			return nil, Faultf("crypto.pbkdf2Sha256: %v", err)
		}
		return Bytes{B: key}, nil
	},
	// ADR 0113 §6 (amended 2026-11-21): PBKDF2-HMAC-SHA-512, 64 bytes out,
	// for SCRAM-SHA-512.
	"pbkdf2Sha512": func(_ context.Context, _ *Interp, args []Value) (Value, *Fault) {
		password, f := keyBytes("pbkdf2Sha512", "password", args[0])
		if f != nil {
			return nil, f
		}
		n := int64(args[2].(Int))
		if n < 1 || n > maxPbkdf2Iterations {
			return nil, Faultf("crypto.pbkdf2Sha512: %d iterations is outside 1 to %d", n, maxPbkdf2Iterations)
		}
		key, err := pbkdf2.Key(sha512.New, string(password), args[1].(Bytes).B, int(n), sha512.Size)
		if err != nil {
			return nil, Faultf("crypto.pbkdf2Sha512: %v", err)
		}
		return Bytes{B: key}, nil
	},
	// Time depends only on the lengths; different lengths answer false at
	// once, like hmac.Equal.
	"equal": func(_ context.Context, _ *Interp, args []Value) (Value, *Fault) {
		return Bool(subtle.ConstantTimeCompare(args[0].(Bytes).B, args[1].(Bytes).B) == 1), nil
	},
	types.HashPasswordFormula: func(ctx context.Context, in *Interp, args []Value) (Value, *Fault) {
		v, f := in.Capabilities.Call(ctx, "Random", "bytes", []Value{Int(argonSaltLen)})
		if f != nil {
			return nil, f
		}
		salt := v.(Bytes).B
		hash, f := argonKey(ctx, string(args[0].(Str)), salt, argonPasses, argonMemoryKiB, argonLanes, argonHashLen)
		if f != nil {
			return nil, f
		}
		return Str(phcString(argonMemoryKiB, argonPasses, argonLanes, salt, hash)), nil
	},
	"passwordMatches": func(ctx context.Context, _ *Interp, args []Value) (Value, *Fault) {
		stored, err := parsePHC(string(args[1].(Str)))
		if err != nil {
			return nil, Faultf("crypto.passwordMatches: the stored hash %s (ADR 0075 §5)", err)
		}
		got, f := argonKey(ctx, string(args[0].(Str)), stored.salt, stored.passes, stored.memory, stored.lanes, uint32(len(stored.hash)))
		if f != nil {
			return nil, f
		}
		return Bool(subtle.ConstantTimeCompare(got, stored.hash) == 1), nil
	},
}

// keyBytes is a `Secret | bytes` argument's bytes: a Secret's value's
// UTF-8 (§2), or the bytes themselves.
func keyBytes(member, param string, v Value) ([]byte, *Fault) {
	switch k := v.(type) {
	case Secret:
		return []byte(k.value), nil
	case Bytes:
		return k.B, nil
	}
	return nil, Faultf("crypto.%s's %s is a %T, not a Secret or bytes", member, param, v)
}

// hmacWith is the body of an HMAC member over newHash (RFC 2104).
func hmacWith(member string, newHash func() hash.Hash) cryptoFunc {
	return func(_ context.Context, _ *Interp, args []Value) (Value, *Fault) {
		key, f := keyBytes(member, "key", args[0])
		if f != nil {
			return nil, f
		}
		mac := hmac.New(newHash, key)
		mac.Write(args[1].(Bytes).B)
		return Bytes{B: mac.Sum(nil)}, nil
	}
}

// callCrypto evaluates `crypto.member(args)`.
func (in *Interp) callCrypto(ctx context.Context, fa *syntax.FieldAccess, args []syntax.Arg, env *Env) (Value, bool, *Fault) {
	m, ok := types.CryptoFormula(fa.Field)
	body := cryptoFormulas[fa.Field]
	if !ok || body == nil {
		return nil, false, Faultf("the prelude crypto group has no formula %q", fa.Field)
	}
	vals, early, earlyReturn, fault := in.evalArgsPositional(ctx, args, env)
	if fault != nil || earlyReturn {
		return early, earlyReturn, fault
	}
	if vals, fault = placeArgs("crypto."+fa.Field, m.ParamNames(), m.Required(), args, vals); fault != nil {
		return nil, false, fault
	}
	for i, p := range m.Params {
		if !cryptoArgFits(vals[i], p.Type) {
			return nil, false, Faultf("crypto.%s's %s is %s, not a %s", fa.Field, p.Name, vals[i], p.Type)
		}
	}
	v, fault := body(ctx, in, vals)
	return v, false, fault
}

// cryptoArgFits is whether v is a value of a crypto parameter's type, so
// an unchecked program faults rather than panics.
func cryptoArgFits(v Value, t types.Type) bool {
	switch t := t.(type) {
	case types.Union:
		for _, m := range t.Members {
			if cryptoArgFits(v, m) {
				return true
			}
		}
	case types.Opaque:
		_, ok := v.(Secret)
		return ok && t == types.SecretType
	case types.Named:
		// ADR 0146: PublicKey or SignatureAlgorithm, a variant its
		// declaration has. Every field either has is bytes.
		e, ok := v.(EnumValue)
		if !ok || e.TypeName != t.Name {
			return false
		}
		decl, ok := t.Decl.(*syntax.EnumDecl)
		if !ok {
			return false
		}
		variant, ok := decl.Variant(e.Variant)
		if !ok || len(variant.Fields) != len(e.Fields) {
			return false
		}
		for _, f := range e.Fields {
			if _, ok := f.(Bytes); !ok {
				return false
			}
		}
		return true
	case types.Primitive:
		switch t.Name {
		case "bytes":
			_, ok := v.(Bytes)
			return ok
		case "string":
			_, ok := v.(Str)
			return ok
		case "int":
			_, ok := v.(Int)
			return ok
		}
	}
	return false
}

// phcString writes an Argon2id hash in the PHC string format:
// $argon2id$v=19$m=19456,t=2,p=1$<salt>$<hash>, base64 without padding.
func phcString(memory, passes uint32, lanes uint8, salt, hash []byte) string {
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, memory, passes, lanes, b64.EncodeToString(salt), b64.EncodeToString(hash))
}

type phcHash struct {
	memory, passes uint32
	lanes          uint8
	salt, hash     []byte
}

// parsePHC reads what phcString writes, with any parameters up to the
// ceilings above. The error completes "the stored hash ...", and never
// quotes the hash.
func parsePHC(s string) (phcHash, error) {
	var h phcHash
	parts := strings.Split(s, "$")
	if len(parts) != 6 || parts[0] != "" {
		return h, fmt.Errorf("isn't a PHC string ($argon2id$v=19$m=...,t=...,p=...$salt$hash)")
	}
	if parts[1] != "argon2id" {
		return h, fmt.Errorf("is %q, not argon2id", parts[1])
	}
	if parts[2] != "v="+strconv.Itoa(argon2.Version) {
		return h, fmt.Errorf("has version %q; only v=%d is read", parts[2], argon2.Version)
	}
	seen := map[string]bool{}
	for _, kv := range strings.Split(parts[3], ",") {
		name, value, ok := strings.Cut(kv, "=")
		n, err := strconv.ParseUint(value, 10, 32)
		if !ok || err != nil || seen[name] {
			return h, fmt.Errorf("has a malformed parameter list %q", parts[3])
		}
		seen[name] = true
		switch name {
		case "m":
			h.memory = uint32(n)
		case "t":
			h.passes = uint32(n)
		case "p":
			if n > 255 {
				return h, fmt.Errorf("asks for %d lanes; the most is %d", n, maxArgonLanes)
			}
			h.lanes = uint8(n)
		default:
			return h, fmt.Errorf("has an unknown parameter %q", name)
		}
	}
	switch {
	case !seen["m"] || !seen["t"] || !seen["p"]:
		return h, fmt.Errorf("doesn't give all of m, t, and p")
	case h.passes < 1 || h.passes > maxArgonPasses:
		return h, fmt.Errorf("asks for %d passes; it must be 1 to %d", h.passes, maxArgonPasses)
	case h.lanes < 1 || h.lanes > maxArgonLanes:
		return h, fmt.Errorf("asks for %d lanes; it must be 1 to %d", h.lanes, maxArgonLanes)
	case h.memory < 8*uint32(h.lanes) || h.memory > maxArgonMemoryKiB:
		return h, fmt.Errorf("asks for %d KiB of memory; it must be %d to %d", h.memory, 8*uint32(h.lanes), maxArgonMemoryKiB)
	}
	var err error
	if h.salt, err = base64.RawStdEncoding.DecodeString(parts[4]); err != nil || len(h.salt) < 8 || len(h.salt) > 64 {
		return h, fmt.Errorf("has a salt that isn't 8 to 64 bytes of unpadded base64")
	}
	if h.hash, err = base64.RawStdEncoding.DecodeString(parts[5]); err != nil || len(h.hash) < 16 || len(h.hash) > 64 {
		return h, fmt.Errorf("has a hash that isn't 16 to 64 bytes of unpadded base64")
	}
	return h, nil
}
