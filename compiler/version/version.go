// Package version is the toolchain's version, stamped into release builds
// with -ldflags "-X fiig-lang/compiler/version.Version=v0.1.0-alpha.1". A
// build without it is "dev".
package version

// Version is the toolchain release this binary was built as.
var Version = "dev"
