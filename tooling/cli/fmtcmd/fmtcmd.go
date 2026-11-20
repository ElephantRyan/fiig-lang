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
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"fiig-lang/compiler/format"
	"fiig-lang/compiler/parser"
	"fiig-lang/tooling/relayout"
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
	relayoutFlag := fs.Bool("layout", false, "move the module in the one directory given to src/ and test/, with files named after their declarations (ADRs 0102, 0104, 0105)")
	dryRun := fs.Bool("n", false, "with -layout, print what would move and change nothing")
	into := fs.String("into", "", "with -layout, the folder package for files that would sit directly in src/; an application needs one (ADR 0108)")
	var absorb absorbList
	fs.Var(&absorb, "absorb", "with -layout, DIR=folder: fold the module in DIR into this one as src/folder/, rewriting imports of it; repeatable")
	fs.Parse(args)
	if *relayoutFlag {
		os.Exit(runLayout(fs.Args(), absorb, *into, *dryRun))
	}

	if fs.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: "+name+" [-w] [-json] <file.fiig or directory>...")
		os.Exit(2)
	}

	exit := 0
	var allErrors []fileErrors
	paths, err := expandDirs(fs.Args())
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	for _, path := range paths {
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

type absorbList []relayout.Absorb

func (a *absorbList) String() string { return fmt.Sprint(*a) }
func (a *absorbList) Set(v string) error {
	dir, folder, ok := strings.Cut(v, "=")
	if !ok || dir == "" || folder == "" {
		return fmt.Errorf("-absorb takes DIR=folder")
	}
	*a = append(*a, relayout.Absorb{Dir: dir, Folder: folder})
	return nil
}

// runLayout is `fiig fmt -layout`.
func runLayout(args []string, absorb absorbList, into string, dryRun bool) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: fiig fmt -layout [-n] [-into folder] [-absorb DIR=folder]... <module>")
		return 2
	}
	plan, err := relayout.Relayout(args[0], absorb, into)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	for _, m := range plan.Moves {
		if len(m.From) == 1 {
			rel, _ := filepath.Rel(args[0], m.From[0])
			if filepath.ToSlash(rel) == m.To {
				continue
			}
			fmt.Printf("%s -> %s\n", filepath.ToSlash(rel), m.To)
		} else {
			fmt.Printf("%d test files -> %s\n", len(m.From), m.To)
		}
	}
	for _, d := range plan.Remove {
		fmt.Printf("absorbed, removed: %s\n", d)
	}
	for _, sc := range plan.Scenarios {
		fmt.Printf("to place: %s checks more than one file, or none; it is in test/scenarios/ until you move it\n", sc)
	}
	for _, n := range plan.Notes {
		fmt.Printf("by hand: %s\n", n)
	}
	if dryRun {
		return 0
	}
	if err := plan.Apply(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	return 0
}

// expandDirs is args with each directory replaced by the .fiig files under
// it, in path order, so `fiig fmt -w .` formats a module (abs-recommendations
// G48). Hidden directories are skipped, as the layout walk skips them.
func expandDirs(args []string) ([]string, error) {
	var out []string
	for _, arg := range args {
		info, err := os.Stat(arg)
		if err != nil || !info.IsDir() {
			out = append(out, arg)
			continue
		}
		err = filepath.WalkDir(arg, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if path != arg && strings.HasPrefix(d.Name(), ".") {
					return filepath.SkipDir
				}
				return nil
			}
			if filepath.Ext(path) == ".fiig" {
				out = append(out, path)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}
