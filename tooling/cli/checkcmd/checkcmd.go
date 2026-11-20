// Package checkcmd is check-cli, and `fiig check`: runs the full Milestone 2 checker (compiler/checker —
// bug-classes.md categories A-H, per its package doc) over every .fiig
// file under a directory.
//
// Usage:
//
//	check-cli [-json] [-workspace <dir>]... <root>
//
// -workspace resolves root's own fiig.pkg.json dependencies against
// every package found under <dir> (compiler/loader, ADR 0032) and writes
// the resolved build's lockfile at root; repeatable, so an
// organization's own internal packages (e.g. a client library for an
// internal API) can live in their own directory tree, searched alongside
// the main one rather than forced into it. Omitted entirely, root is
// checked alone with no package context, same as before Milestone 6's
// loader existed.
//
// Exit code 1 if any diagnostic was reported, 0 otherwise.
package checkcmd

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"fiig-lang/compiler/checker"
	"fiig-lang/compiler/loader"
	"fiig-lang/compiler/lockfile"
	"fiig-lang/compiler/migration"
	"fiig-lang/compiler/types"
	"fiig-lang/tooling/runprogram"
)

// stringList collects every occurrence of a repeatable flag, rather
// than keeping only the last one.
type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

func Main(name string, args []string) {
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	asJSON := fs.Bool("json", false, "print diagnostics as a JSON array instead of one line each")
	var workspaces stringList
	fs.Var(&workspaces, "workspace", "directory to search for root's package dependencies (compiler/loader); repeatable. Omitted, only the packages fiig ships are searched (ADR 0074)")
	fs.Parse(args)
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: "+name+" [-json] [-workspace <dir>]... <root>")
		os.Exit(2)
	}
	root := fs.Arg(0)

	var packages types.Packages
	var loaded *loader.Loaded
	if loader.Wanted(root, workspaces) {
		var errs []error
		loaded, errs = loader.Load(root, workspaces)
		if len(errs) > 0 {
			for _, e := range errs {
				fmt.Fprintln(os.Stderr, "error:", e)
			}
			os.Exit(1)
		}
		packages = loaded.Packages
		if err := lockfile.Write(root, loaded.Lockfile); err != nil {
			fmt.Fprintln(os.Stderr, "error writing lockfile:", err)
			os.Exit(1)
		}
	}

	diags, err := checker.CheckDir(root, packages)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	if loaded != nil {
		// ADR 0105: each folder package's tests (its sources the loader
		// already checked).
		more, _, err := runprogram.CheckModulePackages(root, loaded)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		diags = append(diags, more...)
	}
	// ADR 0096 §1: a migration file that doesn't fit is reported here too,
	// not first at startup. A dependency's were reported by its load.
	if _, errs := migration.Read(os.DirFS(root)); len(errs) > 0 {
		for _, e := range errs {
			// Each error starts with the file it's about: "migrations/x.sql: ...".
			file, msg, _ := strings.Cut(e.Error(), ": ")
			diags = append(diags, checker.Diagnostic{File: file, Msg: msg})
		}
	}
	if *asJSON {
		if diags == nil {
			diags = []checker.Diagnostic{} // encode as [], not JSON null
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(diags); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	} else {
		for _, d := range diags {
			fmt.Println(d.String())
		}
	}
	if len(diags) > 0 {
		os.Exit(1)
	}
}
