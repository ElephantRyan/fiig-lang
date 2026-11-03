// Command run-cli is `fiig run`'s logic, tooling/cli/runcmd, as its own
// binary; see that package for usage.
package main

import (
	"os"

	"fiig-lang/tooling/cli/runcmd"
)

func main() { runcmd.Main("run-cli", os.Args[1:], nil) }
