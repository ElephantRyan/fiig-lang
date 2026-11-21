package types

import (
	"sort"

	"fiig-lang/compiler/syntax"
)

// ADR 0116: the prelude compress group, the codecs Kafka record batches
// use, written in Go because a codec in fiig would be too slow (ADR 0115).

// CompressGroup is the prelude compress group's name. Like crypto, it is
// visible in every file without a `use`, so no declaration, import alias,
// parameter, or binding may take it.
const CompressGroup = "compress"

// DecompressErrorDecl is the prelude's `DecompressError` enum (§3).
var DecompressErrorDecl = preludeDecls["DecompressError"].(*syntax.EnumDecl)

// DecompressErrorType is `DecompressError` as a type.
var DecompressErrorType = Named{Name: "DecompressError", Decl: DecompressErrorDecl}

func decompressed() Type {
	return Generic{Name: "Result", Args: []Type{bytesType, DecompressErrorType}}
}

// compressFormulas is the compress group's table: each codec compresses
// and decompresses, and a decompress names the most it may produce (§3).
// The checker types a `compress.member(...)` call against it and the
// runtime dispatches through it; TestEveryCompressFormulaIsImplemented
// keeps the two in step.
var compressFormulas = map[string]Method{
	"gzip":     {[]Param{p("data", bytesType)}, bytesType},
	"gunzip":   {[]Param{p("data", bytesType), p("maxSize", intType)}, decompressed()},
	"snappy":   {[]Param{p("data", bytesType)}, bytesType},
	"unsnappy": {[]Param{p("data", bytesType), p("maxSize", intType)}, decompressed()},
	"lz4":      {[]Param{p("data", bytesType)}, bytesType},
	"unlz4":    {[]Param{p("data", bytesType), p("maxSize", intType)}, decompressed()},
	"zstd":     {[]Param{p("data", bytesType)}, bytesType},
	"unzstd":   {[]Param{p("data", bytesType), p("maxSize", intType)}, decompressed()},
}

// CompressFormula is the compress group's member by name.
func CompressFormula(name string) (Method, bool) {
	m, ok := compressFormulas[name]
	return m, ok
}

// CompressFormulaNames lists the compress group's members, sorted.
func CompressFormulaNames() []string {
	names := make([]string, 0, len(compressFormulas))
	for name := range compressFormulas {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
