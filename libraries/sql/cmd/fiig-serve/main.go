// Command fiig-serve is `fiig serve` (tooling/cli/servecmd) with
// libraries/sql's provider and a pure-Go SQLite driver; see that package
// for usage.
package main

import (
	"os"

	_ "modernc.org/sqlite" // pure-Go driver, registered under "sqlite": no cgo, so the binary is static

	"fiig-lang/compiler/runtime"
	fiigsql "fiig-lang/libraries/sql"
	"fiig-lang/tooling/cli/servecmd"
)

func main() {
	servecmd.Main("fiig-serve", os.Args[1:], runtime.FactoryRegistry{"sql": fiigsql.Factory})
}
