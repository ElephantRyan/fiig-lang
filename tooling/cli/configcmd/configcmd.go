// Package configcmd is `fiig config`: it lists every environment variable
// a program reads (ADR 0071 §5), from its own config declarations, its
// packages', and the caps file, so a deployment's checklist and its
// .env.example are derived rather than written by hand. Given an entry, a
// declaration or an http adapter, it lists only what that entry reaches,
// which is what `fiig run` or `fiig serve` of it demands; an http adapter
// adds fiig serve's own LISTEN_ADDR, ALLOW_ORIGINS, and ADR 0076's limits.
// An entry is named as `fiig serve` names one (ADR 0079 §6): bare for the
// root, or a folder package's path (`ratings.ratingsApi`), and several may
// be given, so one report covers every adapter a process serves. With none,
// every package of the module is listed (abs-recommendations G44).
//
// Usage:
//
//	fiig config [-caps FILE] [-workspace DIR]... <root> [entry...]
package configcmd

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"fiig-lang/compiler/loader"
	"fiig-lang/compiler/runtime"
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
	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: "+name+" [-caps FILE] [-workspace DIR]... <root> [entry...]")
		os.Exit(2)
	}
	root := fs.Arg(0)
	files, diags, err := runprogram.LoadFiles(root)
	if err != nil {
		fail(err)
	}
	var packages types.Packages
	var modulePath string
	var moduleOwn []*types.Package
	if loader.Wanted(root, workspaces) {
		loaded, errs := loader.Load(root, workspaces)
		if len(errs) > 0 {
			for _, e := range errs {
				fmt.Fprintln(os.Stderr, "error:", e)
			}
			os.Exit(1)
		}
		packages = loaded.Packages
		if len(loaded.Module) > 0 {
			modulePath = loaded.Manifest.Path
			for _, m := range loaded.Module {
				moduleOwn = append(moduleOwn, m.Package)
			}
		}
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
	// With no entry, the module's folder packages count too: an application's
	// own code is in them, and the top-level package holds none (ADR 0108).
	if fs.NArg() == 1 && len(moduleOwn) > 0 {
		var all []runprogram.Entry
		for _, pkg := range moduleOwn {
			var names []string
			for name := range pkg.Universe.Decls {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				all = append(all, runprogram.Entry{Package: pkg, Name: name})
			}
		}
		sources = append(sources, runprogram.ReachableConfigFrom(program, all)...)
	}
	serving := true
	// With entries named, only the packages they reach have bindings to
	// report (abs-recommendations G51); with none, every package does.
	var reached map[string]bool
	if fs.NArg() >= 2 {
		serving = false
		var entries []runprogram.Entry
		seen := map[string]bool{}
		for _, name := range fs.Args()[1:] {
			if seen[name] {
				fail(fmt.Errorf("entry %s is named twice", name))
			}
			seen[name] = true
			pkg, bare, d, ok := resolveEntry(name, universe, packages, modulePath)
			if !ok {
				fail(fmt.Errorf("no declaration named %q", name))
			}
			ad, isAdapter := d.(*syntax.AdapterDecl)
			serving = serving || isAdapter && ad.Kind != syntax.AdapterCLI
			entries = append(entries, runprogram.Entry{Package: pkg, Name: bare})
			// A stream adapter's source and a schedule adapter's lock read their
			// own config (ADR 0111 §2, ADR 0112 §4), and the capabilities file,
			// not the program, says which, so every package that provides one
			// is listed.
			iface := ""
			if isAdapter {
				iface = map[syntax.AdapterKind]string{syntax.AdapterStream: runtime.StreamSourceInterface, syntax.AdapterSchedule: runtime.ScheduleLockInterface}[ad.Kind]
			}
			if iface != "" {
				for _, other := range packages {
					if group := other.Provides[iface]; group != "" {
						entries = append(entries, runprogram.Entry{Package: other, Name: group})
					}
				}
			}
		}
		sources = runprogram.ReachableConfigFrom(program, entries)
		reached = runprogram.ReachedPackages(program, entries, packages)
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
	listed, listedOwn := packages, ownPaths
	if reached != nil {
		listed, listedOwn = types.Packages{}, nil
		for path, pkg := range packages {
			if reached[path] {
				listed[path] = pkg
			}
		}
		for _, path := range ownPaths {
			if reached[path] {
				listedOwn = append(listedOwn, path)
			}
		}
	}
	bindings := runprogram.Bindings(listed, func(path, capability string) bool { return own[path][capability] }, listedOwn)
	if len(bindings) > 0 {
		fmt.Println()
		fmt.Println("capability bindings:")
		for _, b := range bindings {
			fmt.Println("  " + b)
		}
	}
}

// resolveEntry finds the declaration an entry names: bare in the root's
// own files, or a package's path and then the name, the path in full or
// relative to the module (`ratings.ratingsApi`), as fiig serve reads it.
// pkg is nil for the root.
func resolveEntry(name string, root *types.Universe, packages types.Packages, modulePath string) (pkg *types.Package, bare string, d syntax.Decl, ok bool) {
	i := strings.LastIndex(name, ".")
	if i < 0 {
		d, ok = root.Lookup(name)
		return nil, name, d, ok
	}
	path, bare := name[:i], name[i+1:]
	pkg, found := packages[path]
	if !found && modulePath != "" {
		pkg, found = packages[modulePath+"."+path]
	}
	if !found {
		return nil, "", nil, false
	}
	d, ok = pkg.Universe.Lookup(bare)
	return pkg, bare, d, ok
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
