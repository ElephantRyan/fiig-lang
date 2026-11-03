// Command fiig-sql-run is `fiig run` (tooling/cli/runcmd) with
// libraries/sql's provider and a pure-Go SQLite driver, so a "sql"-typed
// capability in -caps opens a real database (ADR 0025, ADR 0027):
//
//	{"Database": {"type": "sql", "driver": "sqlite", "dsn": "file:app.db"}}
//
// Every flag is run-cli's; see runcmd.
package main

import (
	"os"

	_ "modernc.org/sqlite" // pure-Go driver, registered under "sqlite": no cgo, no separate install step

	"fiig-lang/compiler/runtime"
	fiigsql "fiig-lang/libraries/sql"
	"fiig-lang/tooling/cli/runcmd"
)

func main() {
	runcmd.Main("fiig-sql-run", os.Args[1:], runtime.FactoryRegistry{"sql": fiigsql.Factory})
}
