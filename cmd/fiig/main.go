// Command fiig is the fiig toolchain in one binary: every tool, with
// libraries/sql's provider and a pure-Go SQLite driver built in, so `run`
// and `serve` reach a real database with no other install.
//
// Usage:
//
//	fiig check   [-json] [-workspace DIR]... <root>
//	fiig run     [-json] [-args JSON] [-caps FILE] [-trace] [-workspace DIR]... <root> <declName>
//	fiig serve   [-addr ADDR] [-caps FILE] [-workspace DIR]... <root> <adapterName>
//	fiig fmt     [-w] [-json] <file.fiig>...
//	fiig catalog generate|find|explain|diff ...
//	fiig version
//
// The separate tools (check-cli, run-cli, fmt-cli, catalog-cli,
// fiig-sql-run, fiig-serve) are the same code, kept for the repository's
// own use.
package main

import (
	"fmt"
	"os"

	_ "modernc.org/sqlite" // pure-Go driver, registered under "sqlite": no cgo, so the binary is static

	"fiig-lang/compiler/runtime"
	"fiig-lang/compiler/version"
	fiigsql "fiig-lang/libraries/sql"
	"fiig-lang/tooling/cli/catalogcmd"
	"fiig-lang/tooling/cli/checkcmd"
	"fiig-lang/tooling/cli/fmtcmd"
	"fiig-lang/tooling/cli/runcmd"
	"fiig-lang/tooling/cli/servecmd"
)

const usage = `usage: fiig <command> [arguments]

commands:
  check     type-check a program and report every diagnostic
  run       run one declaration of a program
  serve     serve one of a program's http adapters
  fmt       format .fiig files
  catalog   generate, search, and compare a program's registry
  version   print the toolchain version

Run "fiig <command> -h" for a command's flags.`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	sql := runtime.FactoryRegistry{"sql": fiigsql.Factory}
	switch cmd {
	case "check":
		checkcmd.Main("fiig check", args)
	case "run":
		runcmd.Main("fiig run", args, sql)
	case "serve":
		servecmd.Main("fiig serve", args, sql)
	case "fmt":
		fmtcmd.Main("fiig fmt", args)
	case "catalog":
		catalogcmd.Main("fiig catalog", args)
	case "version", "-version", "--version":
		fmt.Println("fiig", version.Version)
	case "help", "-h", "--help":
		fmt.Println(usage)
	default:
		fmt.Fprintf(os.Stderr, "fiig: unknown command %q\n\n%s\n", cmd, usage)
		os.Exit(2)
	}
}
