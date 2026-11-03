// Package fmtcmd is fmt-cli, and `fiig fmt`: is the Milestone 1 prototype of the canonical formatter's
// CLI. By default it prints the formatted source to stdout; -w rewrites the
// file in place (a no-op if it's already canonical, since the formatter is
// idempotent).
//
// Usage:
//
//	fmt-cli [-w] [-json] <file.fiig>...
package fmtcmd

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"fiig-lang/compiler/format"
	"fiig-lang/compiler/parser"
)

// fileErrors pairs one file's parse errors with its path, for -json output
// — parser.Error itself has no file field, since it's reported mid-parse
// with no path in scope yet.
type fileErrors struct {
	File   string         `json:"file"`
	Errors []parser.Error `json:"errors"`
}

func Main(name string, args []string) {
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	write := fs.Bool("w", false, "write the formatted result back to each file instead of printing it")
	asJSON := fs.Bool("json", false, "print format errors as a JSON array instead of one line each")
	fs.Parse(args)

	if fs.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: "+name+" [-w] [-json] <file.fiig>...")
		os.Exit(2)
	}

	exit := 0
	var allErrors []fileErrors
	for _, path := range fs.Args() {
		src, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			exit = 1
			continue
		}
		out, errs := format.Format(string(src))
		if len(errs) > 0 {
			exit = 1
			if *asJSON {
				allErrors = append(allErrors, fileErrors{File: path, Errors: errs})
			} else {
				for _, e := range errs {
					fmt.Fprintf(os.Stderr, "%s: %s: %s\n", path, e.At, e.Msg)
				}
			}
			continue
		}
		if *write {
			if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
				fmt.Fprintln(os.Stderr, "error:", err)
				exit = 1
			}
			continue
		}
		fmt.Print(out)
	}
	if *asJSON {
		if allErrors == nil {
			allErrors = []fileErrors{} // encode as [], not JSON null
		}
		enc := json.NewEncoder(os.Stderr)
		enc.SetIndent("", "  ")
		if err := enc.Encode(allErrors); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			exit = 1
		}
	}
	os.Exit(exit)
}
