package types

import "fiig-lang/compiler/syntax"

// ADR 0073: the prelude json group, and the two prelude enums it works in.

// JsonGroup is the prelude json group's name. Like math, it is visible in
// every file without a `use`, so no declaration, import alias, parameter,
// or binding may take it.
const JsonGroup = "json"

// JsonValueDecl is the prelude's `JsonValue` enum (ADR 0062 §1), the
// untyped JSON tree.
var JsonValueDecl = preludeDecls["JsonValue"].(*syntax.EnumDecl)

// JsonValueType is `JsonValue` as a type.
var JsonValueType = Named{Name: "JsonValue", Decl: JsonValueDecl}

// JsonErrorDecl is the prelude's `JsonError` enum (ADR 0073 §5).
var JsonErrorDecl = preludeDecls["JsonError"].(*syntax.EnumDecl)

// JsonErrorType is `JsonError` as a type.
var JsonErrorType = Named{Name: "JsonError", Decl: JsonErrorDecl}

// IsPreludeGroup reports whether name is a prelude group, math, json,
// crypto, or compress: the names every file sees without a `use`.
func IsPreludeGroup(name string) bool {
	return name == MathGroup || name == JsonGroup || name == CryptoGroup || name == CompressGroup
}
