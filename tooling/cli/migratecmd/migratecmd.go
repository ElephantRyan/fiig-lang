// Package migratecmd is `fiig migrate` (ADR 0096 §2): apply a program's
// pending migrations and exit, for a deployment that runs them as their
// own step and starts the program with -migrate=check.
package migratecmd

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"fiig-lang/compiler/loader"
	"fiig-lang/compiler/lockfile"
	"fiig-lang/compiler/runtime"
	"fiig-lang/compiler/types"
	"fiig-lang/tooling/migrate"
	"fiig-lang/tooling/runprogram"
)

type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

func Main(name string, argv []string, factories runtime.FactoryRegistry) {
	os.Exit(Run(name, argv, factories))
}

// Run is Main without the exit, for tests.
func Run(name string, argv []string, factories runtime.FactoryRegistry) int {
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	capsPath := fs.String("caps", "", "path to the capabilities file that binds each package's Database")
	envFile := fs.String("env-file", "", "a file of NAME=value lines that sets environment variables the process doesn't (ADR 0071 §3a)")
	check := fs.Bool("check", false, "list pending migrations and apply none; exits 1 if any are pending")
	var workspaces stringList
	fs.Var(&workspaces, "workspace", "directory to search for root's package dependencies (compiler/loader); repeatable")
	fs.Parse(argv)
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: "+name+" [-caps FILE] [-env-file FILE] [-check] [-workspace DIR]... <root>")
		return 2
	}
	root := fs.Arg(0)
	env, err := runprogram.NewEnvironment(*envFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error: -env-file:", err)
		return 1
	}
	files, diags, err := runprogram.LoadFiles(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	var packages types.Packages
	if loader.Wanted(root, workspaces) {
		loaded, errs := loader.Load(root, workspaces)
		if len(errs) > 0 {
			for _, e := range errs {
				fmt.Fprintln(os.Stderr, "error:", e)
			}
			return 1
		}
		packages = loaded.Packages
		if err := lockfile.Write(root, loaded.Lockfile); err != nil {
			fmt.Fprintln(os.Stderr, "error writing lockfile:", err)
			return 1
		}
	}
	// A program that doesn't check never starts, so its schema isn't moved
	// ahead of it.
	if diags = append(diags, runprogram.Check(root, files, packages)...); len(diags) > 0 {
		for _, d := range diags {
			fmt.Fprintln(os.Stderr, d.String())
		}
		fmt.Fprintln(os.Stderr, "refusing to migrate a program with checker diagnostics")
		return 1
	}
	caps, problems, err := runprogram.LoadCapabilitiesFileWith(*capsPath, factories, env.Lookup)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error: -caps:", err)
		return 2
	}
	if err := runprogram.ConfigReport(problems); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	if caps == nil {
		caps = runtime.NewCapabilityRegistry()
	}
	runprogram.RegisterDefaults(caps)
	universe, _ := types.BuildUniverse(files)
	in := runtime.New(universe, caps)
	in.Imports = runprogram.BuildImports(files, packages)
	in.Groups = runprogram.BuildGroups(files, packages)
	in.Packages = packages // a driver written in fiig runs on this interpreter (ADR 0084)
	defer caps.Close()

	pkgs, errs := migrate.Collect(root, packages)
	if len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintln(os.Stderr, "error:", e)
		}
		return 1
	}
	mode := migrate.Apply
	if *check {
		mode = migrate.Check
	}
	applied, problems := migrate.Run(context.Background(), pkgs, caps, mode)
	for _, a := range applied {
		fmt.Println("applied", a)
	}
	if len(problems) > 0 {
		for _, p := range problems {
			fmt.Fprintln(os.Stderr, "error:", p)
		}
		return 1
	}
	if len(applied) == 0 {
		fmt.Println("up to date")
	}
	return 0
}
