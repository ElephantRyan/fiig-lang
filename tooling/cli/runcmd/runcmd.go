// Package runcmd is run-cli, and `fiig run`: is Milestone 4's executable-target entry point: it
// checks every .fiig file under a directory and then actually executes one
// named rule/conversion/activity/gateway/workflow through compiler/runtime, instead of
// only generating a registry or diagnostics the way catalog-cli/check-cli
// do. A program with any checker diagnostic is refused rather than run —
// this tool only ever executes an already-checked program, the same
// guarantee compiler/runtime itself assumes.
//
// There's no capability-contract system yet, so a capability call
// (`Database.queryOne(...)`) needs a stand-in: -caps points at a small
// JSON file of scripted responses (caps.go) — without it, any capability
// call faults immediately, which is the right behavior for a program that
// needs a capability the caller never configured. A real database-backed
// run is libraries/sql's own executable (libraries/sql/cmd/fiig-sql-run),
// which this package deliberately never imports — doing so would mean a
// specific database/sql driver landing in the root module, which stays at
// zero third-party dependencies (ADR 0025).
//
// -workspace resolves root's own fiig.pkg.json dependencies against every
// package found under <dir> (compiler/loader, ADR 0032) and writes the
// resolved build's lockfile at root, the same as check-cli — repeatable,
// so an organization's own internal packages can live in their own
// directory tree (ADR 0037). Without it, a program whose own files `use`
// another package can't be run at all (nothing to resolve the alias
// against); with it, a cross-package call actually executes against the
// real resolved dependency (compiler/runtime's own package-aware
// execution, ADR 0038), not just type-checks.
//
// -replay <id> runs a stream adapter's route on one element instead (ADR
// 0111 open question 1, as amended 2026-11-21; ADR 0047 §6): the element
// is fetched from the route's source by its id, the route's workflow runs
// on it, and what the mapping chose (ack, retry, deadLetter and its
// target, or halt and why) is printed. Nothing is settled: replay is for
// investigating, and an operator settles the element by other means.
// -source names the route when the adapter reads more than one source.
//
// Usage:
//
//	run-cli [-json] [-args '{"id":"c1"}'] [-caps caps.json] [-trace] [-workspace <dir>]... <root> <declName>
//	run-cli -replay <id> [-source <name>] [-json] [-caps caps.json] [-trace] [-workspace <dir>]... <root> <streamAdapter>
package runcmd

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"fiig-lang/compiler/adapter"
	"fiig-lang/compiler/loader"
	"fiig-lang/compiler/lockfile"
	"fiig-lang/compiler/runtime"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
	"fiig-lang/tooling/cli/servecmd"
	"fiig-lang/tooling/migrate"
	"fiig-lang/tooling/runprogram"
)

// stringList collects every occurrence of a repeatable flag, rather
// than keeping only the last one.
type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

func Main(name string, argv []string, factories runtime.FactoryRegistry) {
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	jsonOut := fs.Bool("json", false, "print the result (or fault) as JSON instead of plain text")
	argsJSON := fs.String("args", "{}", "JSON object of arguments, keyed by parameter name")
	argsFile := fs.String("args-file", "", "a file holding -args's JSON object, or - for stdin, so a secret argument stays out of the command line")
	capsPath := fs.String("caps", "", "path to a JSON file of scripted capability responses")
	migrateFlag := fs.String("migrate", "apply", "apply pending migrations before running, or check: refuse to run while any are pending (ADR 0096 §2)")
	trace := fs.Bool("trace", false, "print the call tree (compiler/runtime.Span) to stderr after running")
	envFile := fs.String("env-file", "", "a file of NAME=value lines that sets environment variables the process doesn't (ADR 0071 §3a)")
	replay := fs.String("replay", "", "the id of one element of a stream adapter's source: run the route's workflow on it and print what its mapping chose, settling nothing (ADR 0111)")
	replaySource := fs.String("source", "", "with -replay, the source of the route to replay, when the adapter reads more than one")
	var workspaces stringList
	fs.Var(&workspaces, "workspace", "directory to search for root's package dependencies (compiler/loader); repeatable. Omitted, only the packages fiig ships are searched (ADR 0074)")
	fs.Parse(argv)
	if fs.NArg() != 2 {
		fmt.Fprintln(os.Stderr, "usage: "+name+" [-json] [-args JSON | -args-file FILE] [-caps FILE] [-env-file FILE] [-migrate apply|check] [-trace] [-workspace DIR]... <root> <declName>")
		fmt.Fprintln(os.Stderr, "       "+name+" -replay ID [-source NAME] [-json] [-caps FILE] [-env-file FILE] [-migrate apply|check] [-trace] [-workspace DIR]... <root> <streamAdapter>")
		os.Exit(2)
	}
	root, name := fs.Arg(0), fs.Arg(1)
	mode, err := migrate.ParseMode(*migrateFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(2)
	}
	env, err := runprogram.NewEnvironment(*envFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error: -env-file:", err)
		os.Exit(1)
	}

	if *replay != "" {
		if *argsJSON != "{}" || *argsFile != "" {
			fmt.Fprintln(os.Stderr, "error: -replay takes its argument from the element; -args and -args-file don't apply")
			os.Exit(2)
		}
		runReplay(servecmd.ReplayOptions{
			Root: root, Adapter: name, ID: *replay, Source: *replaySource,
			CapsPath: *capsPath, Workspaces: workspaces, Factories: factories, Env: env, Migrate: mode,
			Timeout: adapter.DefaultRequestTimeout,
		}, *jsonOut, *trace)
		return
	}
	if *replaySource != "" {
		fmt.Fprintln(os.Stderr, "error: -source names a route to -replay")
		os.Exit(2)
	}

	files, diags, err := runprogram.LoadFiles(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	var packages types.Packages
	var loaded *loader.Loaded
	if loader.Wanted(root, workspaces) {
		var loadErrs []error
		loaded, loadErrs = loader.Load(root, workspaces)
		if len(loadErrs) > 0 {
			for _, e := range loadErrs {
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

	diags = append(diags, runprogram.Check(root, files, packages)...)
	if len(diags) > 0 {
		for _, d := range diags {
			fmt.Fprintln(os.Stderr, d.String())
		}
		fmt.Fprintln(os.Stderr, "refusing to run a program with checker diagnostics")
		os.Exit(1)
	}
	// ADR 0105: `folder.name` runs a declaration of one of the module's
	// folder packages, `ratings.clamp` or `signin.password.check`.
	if dot := strings.LastIndex(name, "."); dot > 0 && loaded != nil {
		pkgPart, declName := name[:dot], name[dot+1:]
		found := false
		_, checked, err := runprogram.CheckModulePackages(root, loaded)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		for _, c := range checked {
			if c.Path == pkgPart || c.Path == loaded.Manifest.Path+"."+pkgPart {
				files, packages, found = nil, c.Packages, true
				for i, f := range c.Files {
					if !syntax.IsTestPath(c.Paths[i]) {
						files = append(files, f)
					}
				}
			}
		}
		if !found {
			fmt.Fprintf(os.Stderr, "no package %q in this module\n", pkgPart)
			os.Exit(1)
		}
		name = declName
	}
	universe, _ := types.BuildUniverse(files) // conflicts already reported by runprogram.Check above

	params, found := runprogram.DeclParams(universe, name)
	if !found {
		fmt.Fprintf(os.Stderr, "no rule/conversion/activity/gateway/workflow named %q\n", name)
		os.Exit(1)
	}

	argsText, err := argsSource(*argsJSON, *argsFile, os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(2)
	}
	rawArgs, err := runprogram.ParseArgs(argsText)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error: -args:", err)
		os.Exit(2)
	}
	imports := runprogram.BuildImports(files, packages)
	entryImports := runprogram.UniversesOf(imports[name])
	args := map[string]runtime.Value{}
	for _, p := range params {
		pt, ok := types.ResolveWithImports(p.Type, universe, entryImports)
		if !ok {
			fmt.Fprintf(os.Stderr, "error: parameter %q has an unresolvable type\n", p.Name)
			os.Exit(1)
		}
		raw, present := rawArgs[p.Name]
		if !present {
			if runtime.IsOptional(pt) {
				args[p.Name] = runtime.None{}
				continue
			}
			fmt.Fprintf(os.Stderr, "error: missing required argument %q\n", p.Name)
			os.Exit(2)
		}
		v, err := runtime.DecodeJSON(raw, pt, universe)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: argument %q: %v\n", p.Name, err)
			os.Exit(2)
		}
		args[p.Name] = v
	}

	caps, problems, err := runprogram.LoadCapabilitiesFileWith(*capsPath, factories, env.Lookup)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error: -caps:", err)
		os.Exit(2)
	}
	if caps == nil {
		caps = runtime.NewCapabilityRegistry()
	}
	runprogram.RegisterDefaults(caps)

	in := runtime.New(universe, caps)
	in.Imports = imports
	in.Groups = runprogram.BuildGroups(files, packages)
	// A driver package the root never imports still runs (ADR 0084 §2).
	in.Packages = packages
	program := runprogram.Program{Universe: universe, Imports: in.Imports, Groups: in.Groups, Renames: runprogram.Renames(root)}
	problems = append(problems, in.LoadConfig(runprogram.ReachableConfig(program, []string{name}), env.Lookup)...)
	if err := runprogram.ConfigReport(problems); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	if err := runprogram.Migrate(context.Background(), root, packages, caps, mode); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	var rec *runtime.Recorder
	if *trace {
		rec = &runtime.Recorder{}
		in.Tracer = rec
	}
	val, fault := in.Call(context.Background(), name, args)
	// A driver's pool says goodbye to the server (ADR 0084 §3).
	caps.Close()
	if rec != nil {
		printTrace(rec.Spans())
	}
	if fault != nil {
		if *jsonOut {
			printJSON(map[string]any{"fault": fault.String()})
		} else {
			fmt.Fprintln(os.Stderr, "fault:", fault)
		}
		os.Exit(1)
	}
	if *jsonOut {
		printJSON(runtime.EncodeJSON(val))
	} else {
		fmt.Println(val.String())
	}
}

// runReplay is -replay: it prints the disposition, and says nothing was
// settled. A halt is a disposition like the others, so it exits 0; an
// element the source can't give is 1.
func runReplay(o servecmd.ReplayOptions, jsonOut, trace bool) {
	var rec *runtime.Recorder
	if trace {
		rec = &runtime.Recorder{}
		o.Tracer = rec
	}
	replayed, err := servecmd.Replay(context.Background(), o)
	if rec != nil {
		printTrace(rec.Spans())
	}
	if err != nil {
		if jsonOut {
			printJSON(map[string]any{"error": err.Error()})
		} else {
			fmt.Fprintln(os.Stderr, "error:", err)
		}
		os.Exit(1)
	}
	if jsonOut {
		printJSON(replayed.JSON())
		return
	}
	fmt.Println(replayed.Describe())
	fmt.Println("not settled: replay only investigates; settle the element by other means")
}

// printTrace renders a Recorder's spans as an indented call tree to
// stderr — spans arrive in completion order (a child always before its
// parent), so this groups them by ParentID first and walks down from the
// roots, rather than assuming they're already in call order.
func printTrace(spans []runtime.Span) {
	children := map[int64][]runtime.Span{}
	var roots []runtime.Span
	for _, s := range spans {
		if s.ParentID == 0 {
			roots = append(roots, s)
		} else {
			children[s.ParentID] = append(children[s.ParentID], s)
		}
	}
	fmt.Fprintln(os.Stderr, "--- trace ---")
	var walk func(s runtime.Span, depth int)
	walk = func(s runtime.Span, depth int) {
		detail := ""
		if s.Detail != "" {
			detail = ": " + s.Detail
		}
		fmt.Fprintf(os.Stderr, "%s%s [%s] %s (%s)%s\n",
			strings.Repeat("  ", depth), s.Name, s.Kind, s.Outcome, s.Duration(), detail)
		for _, c := range children[s.ID] {
			walk(c, depth+1)
		}
	}
	for _, r := range roots {
		walk(r, 0)
	}
}

func printJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// argsSource is the arguments' JSON: -args's text, or the contents of
// -args-file, where - is stdin. Both at once is an error.
func argsSource(argsJSON, argsFile string, stdin io.Reader) (string, error) {
	if argsFile == "" {
		return argsJSON, nil
	}
	if argsJSON != "{}" {
		return "", fmt.Errorf("-args and -args-file both given; use one")
	}
	var data []byte
	var err error
	if argsFile == "-" {
		data, err = io.ReadAll(stdin)
	} else {
		data, err = os.ReadFile(argsFile)
	}
	if err != nil {
		return "", fmt.Errorf("-args-file: %w", err)
	}
	return string(data), nil
}
