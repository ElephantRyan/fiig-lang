// Command fmt-cli is `fiig fmt`'s logic, tooling/cli/fmtcmd, as its own
// binary; see that package for usage.
package main

import (
	"os"

	"fiig-lang/tooling/cli/fmtcmd"
)

func main() { fmtcmd.Main("fmt-cli", os.Args[1:]) }
