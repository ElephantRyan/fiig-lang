// Package configcmd is `fiig config`: it lists every environment variable
// a program reads (ADR 0071 §5), from its own config declarations, its
// packages', and the caps file, so a deployment's checklist and its
// .env.example are derived rather than written by hand. Given an entry, a
// declaration or an http adapter, it lists only what that entry reaches,
// which is what `fiig run` or `fiig serve` of it demands; an http adapter
// adds fiig serve's own LISTEN_ADDR, ALLOW_ORIGINS, and ADR 0076's limits.
//
// Usage:
//
//	fiig config [-caps FILE] [-workspace DIR]... <root> [entry]
package configcmd

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"fiig-lang/compiler/loader"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
	"fiig-lang/tooling/runprogram"
)

type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

func Main(name string, args []string) {
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	capsPath := fs.String("caps", "", "the capabilities file whose ${NAME}s to list too (ADR 0027)")
	var workspaces stringList
	fs.Var(&workspaces, "workspace", "directory to search for root's package dependencies (compiler/loader); repeatable")
	fs.Parse(args)
	if fs.NArg() < 1 || fs.NArg() > 2 {
		fmt.Fprintln(os.Stderr, "usage: "+name+" [-caps FILE] [-workspace DIR]... <root> [entry]")
		os.Exit(2)
	}
	root := fs.Arg(0)
	files, diags, err := runprogram.LoadFiles(root)
	if err != nil {
		fail(err)
	}
	var packages types.Packages
	if loader.Wanted(root, workspaces) {
		loaded, errs := loader.Load(root, workspaces)
		if len(errs) > 0 {
			for _, e := range errs {
				fmt.Fprintln(os.Stderr, "error:", e)
			}
			os.Exit(1)
		}
		packages = loaded.Packages
	}
	diags = append(diags, runprogram.Check(root, files, packages)...)
	if len(diags) > 0 {
		for _, d := range diags {
			fmt.Fprintln(os.Stderr, d.String())
		}
		fail(fmt.Errorf("the program has checker diagnostics"))
	}
	universe, _ := types.BuildUniverse(files)
	program := runprogram.Program{
		Universe: universe,
		Imports:  runprogram.BuildImports(files, packages),
		Groups:   runprogram.BuildGroups(files, packages),
		Renames:  runprogram.Renames(root),
	}
	sources := runprogram.AllConfig(program)
	serving := true
	if fs.NArg() == 2 {
		entry := fs.Arg(1)
		d, ok := universe.Lookup(entry)
		if !ok {
			fail(fmt.Errorf("no declaration named %q", entry))
		}
		ad, isAdapter := d.(*syntax.AdapterDecl)
		serving = isAdapter && ad.Kind == syntax.AdapterHTTP
		sources = runprogram.ReachableConfig(program, []string{entry})
	}
	vars, err := runprogram.Variables(sources, *capsPath)
	if err != nil {
		fail(err)
	}
	if serving {
		vars = append(vars,
			runprogram.Variable{Name: "ALLOW_ORIGINS", Type: "origins", Requirement: "optional", Where: "fiig serve: browser origins allowed to call it (ADR 0070)"},
			runprogram.Variable{Name: "LISTEN_ADDR", Type: "address", Requirement: "= :8080", Where: "fiig serve"},
			runprogram.Variable{Name: "MAX_BODY_BYTES", Type: "int", Requirement: "= 2097152", Where: "fiig serve: the most bytes a request body may have (ADR 0076)"},
			runprogram.Variable{Name: "MAX_REQUESTS", Type: "int", Requirement: "= 256", Where: "fiig serve: requests run at once, as many more waiting (ADR 0076)"},
			runprogram.Variable{Name: "REQUEST_TIMEOUT", Type: "duration", Requirement: "= 30s", Where: "fiig serve: how long a request may run (ADR 0076)"},
		)
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	for _, v := range vars {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", v.Name, v.Type, v.Requirement, v.Where)
	}
	w.Flush()

	// Where each domain's capabilities come from (ADR 0079 §6a), so one
	// sharing the application's database shows before it's deployed.
	own, err := runprogram.CapsPackages(*capsPath)
	if err != nil {
		fail(fmt.Errorf("-caps: %w", err))
	}
	var ownPaths []string
	for path := range own {
		ownPaths = append(ownPaths, path)
	}
	if problems := runprogram.UnknownPackages(ownPaths, packages); len(problems) > 0 {
		fail(errors.New(strings.Join(problems, "; ")))
	}
	bindings := runprogram.Bindings(packages, func(path, capability string) bool { return own[path][capability] }, ownPaths)
	if len(bindings) > 0 {
		fmt.Println()
		fmt.Println("capability bindings:")
		for _, b := range bindings {
			fmt.Println("  " + b)
		}
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
