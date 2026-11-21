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
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"fiig-lang/compiler/checker"
	"fiig-lang/compiler/loader"
	"fiig-lang/compiler/lockfile"
	"fiig-lang/compiler/migration"
	"fiig-lang/compiler/runtime"
	"fiig-lang/compiler/types"
	"fiig-lang/tooling/runprogram"
	"fiig-lang/tooling/schemacheck"
)

// stringList collects every occurrence of a repeatable flag, rather
// than keeping only the last one.
type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

// Main runs the command. factories are the capability factories this binary
// has, for -schema (the "sql" one); the separate check-cli passes none.
func Main(name string, args []string, factories ...runtime.FactoryRegistry) {
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	asJSON := fs.Bool("json", false, "print diagnostics as a JSON array instead of one line each")
	schema := fs.Bool("schema", false, "also apply the migrations to an in-memory SQLite database and prepare every literal Database query against it, so a table or column the schema lacks is found now (ADR 0131)")
	var workspaces stringList
	fs.Var(&workspaces, "workspace", "directory to search for root's package dependencies (compiler/loader); repeatable. Omitted, only the packages fiig ships are searched (ADR 0074)")
	fs.Parse(args)
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: "+name+" [-json] [-schema] [-workspace <dir>]... <root>")
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

	diags, paths, files, err := checker.CheckDirFiles(root, packages)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	if loaded != nil {
		// ADR 0105: each folder package's tests (its sources the loader
		// already checked).
		more, checked, err := runprogram.CheckModulePackages(root, loaded)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		diags = append(diags, more...)
		for _, c := range checked {
			files = append(files, c.Files...)
			paths = append(paths, c.Paths...)
		}
	}
	if *schema && len(diags) == 0 {
		// ADR 0131: only a program that checks clean has queries worth
		// preparing; the rest would be noise on top of the errors.
		var sql runtime.FactoryRegistry
		if len(factories) > 0 {
			sql = factories[0]
		}
		res, err := schemacheck.Check(context.Background(), root, packages, schemacheck.Queries(files, paths), sql)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error: -schema:", err)
			os.Exit(1)
		}
		diags = append(diags, res.Diagnostics...)
		if res.Note != "" {
			fmt.Fprintln(os.Stderr, "note:", res.Note)
		}
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
