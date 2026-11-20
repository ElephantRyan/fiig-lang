// Package testcmd is `fiig test` (ADR 0091): it checks a package with its
// tests/ files, then runs every test, each on an interpreter and a
// capabilities registry of its own, in parallel.
//
// A test starts with nothing it shares with another (§5): a test clock, a
// Random seeded from its name, a new in-memory SQLite database as
// Database, and no network. -caps gives every test real providers instead,
// and -env-file real config values; a test's own fakes win over both.
//
// Usage:
//
//	fiig test [-run pattern] [-caps file.json] [-env-file file.env] [-timeout 30s] [-v] [-json] [-workspace dir]... [dir]
package testcmd

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"fiig-lang/compiler/checker"
	"fiig-lang/compiler/loader"
	"fiig-lang/compiler/lockfile"
	"fiig-lang/compiler/runtime"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
	"fiig-lang/tooling/migrate"
	"fiig-lang/tooling/runprogram"
)

type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

// test is one test found under tests/ (test/ in a module).
type test struct {
	decl *syntax.TestDecl
	file string // relative to the package root
	u    *unit
}

// unit is one checked package whose tests run: the top-level package, or
// one of a module's folders (ADR 0105), and what its tests run on.
type unit struct {
	files    []*syntax.File
	paths    []string
	packages types.Packages
	universe *types.Universe
	imports  map[string]map[string]*types.Package
	groups   map[string]map[string]*types.GroupRef
}

// Result is one test's outcome, as -json prints it.
type Result struct {
	Name       string    `json:"name"`
	File       string    `json:"file"`
	Line       int       `json:"line"`
	Outcome    string    `json:"outcome"` // "pass" or "fail"
	Failures   []Failure `json:"failures,omitempty"`
	DurationMS int64     `json:"durationMs"`
}

// Failure is one way a test failed.
type Failure struct {
	Line    int    `json:"line"`
	Source  string `json:"source,omitempty"` // the failed expect, as written
	Left    string `json:"left,omitempty"`
	Right   string `json:"right,omitempty"`
	Value   string `json:"value,omitempty"` // what an `expect ... is` didn't match
	Message string `json:"message,omitempty"`
}

// Main runs `fiig test`. factories are the provider types a -caps file may
// name beyond tooling/runprogram's own; "sql" also makes each test's
// in-memory database.
func Main(name string, argv []string, factories runtime.FactoryRegistry) {
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	pattern := fs.String("run", "", "run only the tests whose name contains this")
	capsPath := fs.String("caps", "", "a capabilities file giving every test real providers (ADR 0091 §5)")
	envFile := fs.String("env-file", "", "a file of NAME=value lines giving every test real config values")
	timeout := fs.Duration("timeout", 30*time.Second, "each test's deadline")
	verbose := fs.Bool("v", false, "print every test, not only the failures")
	jsonOut := fs.Bool("json", false, "print one JSON object per test")
	var workspaces stringList
	fs.Var(&workspaces, "workspace", "directory to search for the package's dependencies; repeatable")
	fs.Parse(argv)
	if fs.NArg() > 1 {
		fmt.Fprintln(os.Stderr, "usage: "+name+" [-run pattern] [-caps FILE] [-env-file FILE] [-timeout 30s] [-v] [-json] [-workspace DIR]... [dir]")
		os.Exit(2)
	}
	root := "."
	if fs.NArg() == 1 {
		root = fs.Arg(0)
	}
	os.Exit(run(root, *pattern, *capsPath, *envFile, *timeout, *verbose, *jsonOut, workspaces, factories))
}

// Run is `fiig test -workspace ... root` with every other flag left at its
// default, for a host's own tests; it returns the exit code.
func Run(root string, workspaces []string, factories runtime.FactoryRegistry) int {
	return run(root, "", "", "", 30*time.Second, false, false, workspaces, factories)
}

func run(root, pattern, capsPath, envFile string, timeout time.Duration, verbose, jsonOut bool, workspaces []string, factories runtime.FactoryRegistry) int {
	var packages types.Packages
	var loaded *loader.Loaded
	if loader.Wanted(root, workspaces) {
		var loadErrs []error
		loaded, loadErrs = loader.Load(root, workspaces)
		if len(loadErrs) > 0 {
			for _, e := range loadErrs {
				fmt.Fprintln(os.Stderr, "error:", e)
			}
			return 2
		}
		packages = loaded.Packages
		if err := lockfile.Write(root, loaded.Lockfile); err != nil {
			fmt.Fprintln(os.Stderr, "error writing lockfile:", err)
			return 2
		}
	}
	// CheckDirFiles, so each diagnostic names its file and the tests run
	// on the trees the check annotated.
	diags, paths, files, err := checker.CheckDirFiles(root, packages)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 2
	}
	// In a module, each folder package is checked with its own tests too
	// (ADR 0105), and its tests run on its own trees.
	units := []*unit{{files: files, paths: paths, packages: packages}}
	if loaded != nil {
		more, checkedPkgs, err := runprogram.CheckModulePackages(root, loaded)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			return 2
		}
		diags = append(diags, more...)
		for _, c := range checkedPkgs {
			units = append(units, &unit{files: c.Files, paths: c.Paths, packages: c.Packages})
		}
	}
	if len(diags) > 0 {
		for _, d := range diags {
			fmt.Fprintln(os.Stderr, d.String())
		}
		fmt.Fprintln(os.Stderr, "the package doesn't check, so no test ran")
		return 2
	}

	var tests []test
	sources := map[string][]string{}
	for _, u := range units {
		found, src, err := findTests(root, u.files, u.paths, pattern)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			return 2
		}
		for _, t := range found {
			t.u = u
			tests = append(tests, t)
		}
		for k, v := range src {
			sources[k] = v
		}
	}
	if len(tests) == 0 {
		if !jsonOut {
			fmt.Println("no tests to run")
		}
		return 0
	}

	env, err := runprogram.NewEnvironment(envFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error: -env-file:", err)
		return 2
	}
	base, problems, err := runprogram.LoadCapabilitiesFileWith(capsPath, factories, env.Lookup)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error: -caps:", err)
		return 2
	}
	if len(problems) > 0 {
		fmt.Fprintln(os.Stderr, "error: -caps:", strings.Join(problems, "; "))
		return 2
	}
	defer base.Close()
	_, sharedDatabase := base.Lookup(types.DatabaseName)

	// Without -env-file no variable is set: a test reads only what it
	// fakes, a default, or what -env-file gives (§5).
	lookup := func(string) (string, bool) { return "", false }
	if envFile != "" {
		lookup = env.Lookup
	}
	config := map[*syntax.ConfigDecl]runtime.Record{}
	for _, u := range units {
		u.universe, _ = types.BuildUniverse(u.files)
		u.imports = runprogram.BuildImports(u.files, u.packages)
		u.groups = runprogram.BuildGroups(u.files, u.packages)
		program := runprogram.Program{Universe: u.universe, Imports: u.imports, Groups: u.groups, Renames: runprogram.Renames(root)}
		var names []string
		for _, t := range tests {
			if t.u == u {
				names = append(names, t.decl.Name)
			}
		}
		if len(names) == 0 {
			continue
		}
		configs := runtime.New(u.universe, runtime.NewCapabilityRegistry())
		if problems := configs.LoadTestConfig(runprogram.ReachableConfig(program, names), lookup); len(problems) > 0 {
			fmt.Fprintln(os.Stderr, "error: config:", strings.Join(problems, "; "))
			return 2
		}
		for k, v := range configs.Config {
			config[k] = v
		}
	}
	top := units[0]

	// ADR 0096 §6: every test's database has the migrations.
	migrations, errs := migrate.Collect(root, packages)
	if len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintln(os.Stderr, "error:", e)
		}
		return 2
	}
	if sharedDatabase {
		shared := runtime.New(top.universe, base) // a driver written in fiig runs on it
		shared.Imports, shared.Groups, shared.Packages = top.imports, top.groups, packages
		if _, problems := migrate.Run(context.Background(), migrations, base, migrate.Apply); len(problems) > 0 {
			fmt.Fprintln(os.Stderr, "error: migrations:", strings.Join(problems, "; "))
			return 1
		}
	}

	var dbSeq atomic.Int64
	runOne := func(t test) Result {
		caps := base.Copy()
		if _, given := caps.Lookup(types.DatabaseName); !given {
			if p, ok := testDatabase(factories, dbSeq.Add(1)); ok {
				caps.Register(types.DatabaseName, p)
				defer closeProvider(p)
				if _, problems := migrate.RunOn(context.Background(), p, migrations, migrate.Apply); len(problems) > 0 {
					return result(t, []runtime.TestFailure{{At: t.decl.Start, Message: "migrations: " + strings.Join(problems, "; ")}}, sources[t.file], 0)
				}
			}
		}
		in := runtime.New(t.u.universe, caps)
		in.Imports, in.Groups, in.Packages, in.Config = t.u.imports, t.u.groups, t.u.packages, config
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		start := time.Now()
		failures := in.RunTest(ctx, t.decl)
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			failures = append(failures, runtime.TestFailure{At: t.decl.Start, Message: fmt.Sprintf("the test ran past its %s deadline (-timeout)", timeout)})
		}
		return result(t, failures, sources[t.file], time.Since(start))
	}

	results := make([]Result, len(tests))
	if sharedDatabase {
		// One real database for every test: one at a time (§6).
		for i, t := range tests {
			results[i] = runOne(t)
		}
	} else {
		var wg sync.WaitGroup
		for i, t := range tests {
			wg.Add(1)
			go func() {
				defer wg.Done()
				results[i] = runOne(t)
			}()
		}
		wg.Wait()
	}
	return report(results, verbose, jsonOut)
}

// findTests is every test under tests/ whose name contains pattern, in
// file order, with each test file's lines for the report.
func findTests(root string, files []*syntax.File, paths []string, pattern string) ([]test, map[string][]string, error) {
	var tests []test
	sources := map[string][]string{}
	for i, f := range files {
		if !f.Tests || len(f.Decls) == 0 {
			continue
		}
		for _, d := range f.Decls {
			// A test file may hold many tests (ADR 0102 §2).
			t, ok := d.(*syntax.TestDecl)
			if !ok || !strings.Contains(t.Name, pattern) {
				continue
			}
			if _, read := sources[paths[i]]; !read {
				src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(paths[i])))
				if err != nil {
					return nil, nil, err
				}
				sources[paths[i]] = strings.Split(strings.ReplaceAll(string(src), "\r\n", "\n"), "\n")
			}
			tests = append(tests, test{decl: t, file: paths[i]})
		}
	}
	return tests, sources, nil
}

// testDatabase is a new, empty in-memory SQLite database (§5), through
// the "sql" factory; false when the toolchain has none.
func testDatabase(factories runtime.FactoryRegistry, n int64) (runtime.CapabilityProvider, bool) {
	f, ok := factories["sql"]
	if !ok {
		return nil, false
	}
	p, err := f(map[string]any{"type": "sql", "driver": "sqlite", "dsn": fmt.Sprintf("file:fiig-test-%d-%d?mode=memory&cache=shared", os.Getpid(), n)})
	if err != nil {
		return nil, false
	}
	return p, true
}

func closeProvider(p runtime.CapabilityProvider) {
	if c, ok := p.(interface{ Close() }); ok {
		c.Close()
	}
}

func result(t test, failures []runtime.TestFailure, lines []string, d time.Duration) Result {
	r := Result{Name: t.decl.Name, File: t.file, Line: t.decl.Start.Line, Outcome: "pass", DurationMS: d.Milliseconds()}
	for _, f := range failures {
		out := Failure{Line: f.At.Line, Left: f.Left, Right: f.Right, Value: f.Value, Message: f.Message}
		if f.Expect && f.At.Line >= 1 && f.At.Line <= len(lines) {
			out.Source = strings.TrimSpace(lines[f.At.Line-1])
		}
		r.Failures = append(r.Failures, out)
	}
	if len(r.Failures) > 0 {
		r.Outcome = "fail"
	}
	return r
}

// report prints the results and returns the exit code: 0 when every test
// passed, 1 when any failed.
func report(results []Result, verbose, jsonOut bool) int {
	failed := 0
	var total time.Duration
	for _, r := range results {
		total += time.Duration(r.DurationMS) * time.Millisecond
		if r.Outcome == "fail" {
			failed++
		}
		if jsonOut {
			b, _ := json.Marshal(r)
			fmt.Println(string(b))
			continue
		}
		if r.Outcome == "pass" {
			if verbose {
				fmt.Printf("ok   %s (%s:%d)\n", r.Name, r.File, r.Line)
			}
			continue
		}
		fmt.Printf("FAIL %s (%s:%d)\n", r.Name, r.File, r.Line)
		for _, f := range r.Failures {
			if f.Source != "" {
				fmt.Printf("    %s:%d: %s\n", r.File, f.Line, f.Source)
				if f.Left != "" || f.Right != "" {
					fmt.Printf("      left:  %s\n      right: %s\n", f.Left, f.Right)
				}
				if f.Value != "" {
					fmt.Printf("      value: %s\n", f.Value)
				}
				continue
			}
			fmt.Printf("    %s:%d: %s\n", r.File, f.Line, f.Message)
		}
	}
	if !jsonOut {
		fmt.Printf("%d passed, %d failed\n", len(results)-failed, failed)
	}
	if failed > 0 {
		return 1
	}
	return 0
}
