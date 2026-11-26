package types

import (
	"sort"

	"fiig-lang/compiler/syntax"
)

// ADR 0075: the prelude crypto group.

// CryptoGroup is the prelude crypto group's name. Like math and json, it
// is visible in every file without a `use`, so no declaration, import
// alias, parameter, or binding may take it.
const CryptoGroup = "crypto"

// HashPasswordFormula is the one crypto member that isn't pure: its salt
// comes from the program's Random provider, so it is a local effect like
// Random (ADR 0075 §5).
const HashPasswordFormula = "hashPassword"

// ADR 0146 §1-§2: the prelude types public-key signatures work in.
var (
	SignatureAlgorithmDecl = preludeDecls["SignatureAlgorithm"].(*syntax.EnumDecl)
	PublicKeyDecl          = preludeDecls["PublicKey"].(*syntax.EnumDecl)
	KeyErrorDecl           = preludeDecls["KeyError"].(*syntax.EnumDecl)

	SignatureAlgorithmType = Named{Name: "SignatureAlgorithm", Decl: SignatureAlgorithmDecl}
	PublicKeyType          = Named{Name: "PublicKey", Decl: PublicKeyDecl}
	KeyErrorType           = Named{Name: "KeyError", Decl: KeyErrorDecl}
)

// cryptoFormulas is the crypto group's table. The checker types a
// `crypto.member(...)` call against it and the runtime dispatches through
// it; TestEveryCryptoFormulaIsImplemented keeps the two in step.
var cryptoFormulas = map[string]Method{
	"sha256":     {[]Param{p("data", bytesType)}, bytesType},
	"hmacSha256": {[]Param{p("key", Union{Members: []Type{SecretType, bytesType}}), p("data", bytesType)}, bytesType},
	"equal":      {[]Param{p("a", bytesType), p("b", bytesType)}, boolType},
	// ADR 0075 §6 (amended 2026-11-21): SHA-1 is for checking signatures
	// a third party already makes with it, such as Twilio's, not for new
	// designs.
	"sha1":       {[]Param{p("data", bytesType)}, bytesType},
	"hmacSha1":   {[]Param{p("key", Union{Members: []Type{SecretType, bytesType}}), p("data", bytesType)}, bytesType},
	"sha512":     {[]Param{p("data", bytesType)}, bytesType},
	"hmacSha512": {[]Param{p("key", Union{Members: []Type{SecretType, bytesType}}), p("data", bytesType)}, bytesType},
	// ADR 0146 §4: HMAC-SHA-384, for a JWT's HS384.
	"hmacSha384": {[]Param{p("key", Union{Members: []Type{SecretType, bytesType}}), p("data", bytesType)}, bytesType},
	// ADR 0075 §6 (amended 2026-11-21): RFC 5869 HKDF-SHA-256, `length`
	// bytes out.
	"hkdfSha256": {[]Param{p("key", Union{Members: []Type{SecretType, bytesType}}), p("salt", bytesType), p("info", bytesType), p("length", intType)}, bytesType},
	// ADR 0085 §7: for protocols that specify PBKDF2, such as SCRAM.
	"pbkdf2Sha256": {[]Param{p("password", Union{Members: []Type{SecretType, bytesType}}), p("salt", bytesType), p("iterations", intType)}, bytesType},
	// ADR 0113 §6 (amended 2026-11-21): the same with HMAC-SHA-512 and 64
	// bytes out, for SCRAM-SHA-512.
	"pbkdf2Sha512": {[]Param{p("password", Union{Members: []Type{SecretType, bytesType}}), p("salt", bytesType), p("iterations", intType)}, bytesType},
	// ADR 0113 §2: CRC-32C (Castagnoli), the checksum Kafka record batches
	// carry. A checksum for catching corruption, not a cryptographic hash:
	// anyone can make data with any CRC.
	"crc32c": {[]Param{p("data", bytesType)}, intType},

	// ADR 0146 §1-§3: public-key signatures. The algorithm is always an
	// argument, and a public key and a Secret can't stand in for each
	// other, so the JWT algorithm confusion can't be written.
	"verify":           {[]Param{p("key", PublicKeyType), p("algorithm", SignatureAlgorithmType), p("data", bytesType), p("signature", bytesType)}, boolType},
	"checkPublicKey":   {[]Param{p("key", PublicKeyType)}, Generic{Name: "Result", Args: []Type{NoneType{}, KeyErrorType}}},
	"publicKeyFromPem": {[]Param{p("pem", stringType)}, Generic{Name: "Result", Args: []Type{PublicKeyType, KeyErrorType}}},
	"sign":             {[]Param{p("key", SecretType), p("algorithm", SignatureAlgorithmType), p("data", bytesType)}, bytesType},

	HashPasswordFormula: {[]Param{p("password", stringType)}, stringType},
	"passwordMatches":   {[]Param{p("password", stringType), p("stored", stringType)}, boolType},
}

// CryptoFormula is the crypto group's member by name.
func CryptoFormula(name string) (Method, bool) {
	m, ok := cryptoFormulas[name]
	return m, ok
}

// CryptoFormulaNames lists the crypto group's members, sorted.
func CryptoFormulaNames() []string {
	names := make([]string, 0, len(cryptoFormulas))
	for name := range cryptoFormulas {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
