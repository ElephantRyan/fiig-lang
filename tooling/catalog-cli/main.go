// Command catalog-cli is `fiig catalog`'s logic, tooling/cli/catalogcmd, as its own
// binary; see that package for usage.
package main

import (
	"os"

	"fiig-lang/tooling/cli/catalogcmd"
)

func main() { catalogcmd.Main("catalog-cli", os.Args[1:]) }
