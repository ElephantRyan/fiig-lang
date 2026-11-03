package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintf(os.Stdout, "ok %d\n", len(os.Args[1:]))
}
