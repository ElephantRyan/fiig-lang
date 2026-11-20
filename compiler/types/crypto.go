package types

import "sort"

// ADR 0075: the prelude crypto group.

// CryptoGroup is the prelude crypto group's name. Like math and json, it
// is visible in every file without a `use`, so no declaration, import
// alias, parameter, or binding may take it.
const CryptoGroup = "crypto"

// HashPasswordFormula is the one crypto member that isn't pure: its salt
// comes from the program's Random provider, so it is a local effect like
// Random (ADR 0075 §5).
const HashPasswordFormula = "hashPassword"

// cryptoFormulas is the crypto group's table. The checker types a
// `crypto.member(...)` call against it and the runtime dispatches through
// it; TestEveryCryptoFormulaIsImplemented keeps the two in step.
var cryptoFormulas = map[string]Method{
	"sha256":     {[]Param{p("data", bytesType)}, bytesType},
	"hmacSha256": {[]Param{p("key", Union{Members: []Type{SecretType, bytesType}}), p("data", bytesType)}, bytesType},
	"equal":      {[]Param{p("a", bytesType), p("b", bytesType)}, boolType},
	// ADR 0085 §7: for protocols that specify PBKDF2, such as SCRAM.
	"pbkdf2Sha256": {[]Param{p("password", Union{Members: []Type{SecretType, bytesType}}), p("salt", bytesType), p("iterations", intType)}, bytesType},
	// ADR 0113 §2: CRC-32C (Castagnoli), the checksum Kafka record batches
	// carry. A checksum for catching corruption, not a cryptographic hash:
	// anyone can make data with any CRC.
	"crc32c": {[]Param{p("data", bytesType)}, intType},

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
