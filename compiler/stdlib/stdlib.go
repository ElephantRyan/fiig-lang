// Package stdlib holds the fiig packages the toolchain ships (ADR 0074 §1).
// They are embedded so a release stays one file, and the loader searches
// FS as one more workspace root, so a program that depends on one needs no
// -workspace flag to find it.
package stdlib

import "embed"

// FS holds one directory per package, each with its own fiig.pkg.json.
//
//go:embed http postgres redis s3 schedule smtp sql stream
var FS embed.FS
