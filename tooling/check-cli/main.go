// Command check-cli is `fiig check`'s logic, tooling/cli/checkcmd, as its own
// binary; see that package for usage.
package main

import (
	"os"

	"fiig-lang/tooling/cli/checkcmd"
)

func main() { checkcmd.Main("check-cli", os.Args[1:]) }
