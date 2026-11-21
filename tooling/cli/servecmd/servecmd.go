// Package servecmd is `fiig serve`: it checks a fiig program and serves its `adapter
// http` declarations on one listener: the deployable form of a fiig application. It is
// `fiig run`'s counterpart for a long-running service, with the same
// capability wiring (ADR 0027): -caps names a JSON file whose "sql"-typed
// capability opens a real database (when the binary registers the sql
// factory, as `fiig` does), whose "socket"-typed capability is the real
// network, and whose strings may name environment variables as `${NAME}`,
// so a container passes its paths and secrets in the environment:
//
//	{"Database": {"type": "sql", "driver": "sqlite", "dsn": "file:${DB_PATH}"}}
//
// A program with checker diagnostics is refused, as fiig-sql-run refuses
// one. SIGINT or SIGTERM stops accepting connections and lets in-flight
// requests finish for up to -shutdown.
//
// What clients send is bounded (ADR 0076): a body of MAX_BODY_BYTES
// (-max-body, 2 MiB), MAX_REQUESTS at once (-max-requests, 256) with as
// many waiting up to 5 s, and REQUEST_TIMEOUT per request
// (-request-timeout, 30 s).
//
// A modular monolith serves several adapters at once (ADR 0079 §6): a
// bare name is an adapter in root, and a dotted one is a dependency's,
// its package path then its name. They share the listener, the limits,
// and the CORS policy, and two routes that would serve the same requests
// stop it before it starts. The caps file's "packages" section binds a
// capability for one package's workflows (§6a).
//
// Usage:
//
//	fiig serve [-addr :8080] [-caps caps.json] [-max-body N] [-max-requests N] [-request-timeout D] [-workspace <dir>]... <root> <adapterName>...
package servecmd

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"fiig-lang/compiler/adapter"
	"fiig-lang/compiler/loader"
	"fiig-lang/compiler/runtime"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
	"fiig-lang/compiler/version"
	"fiig-lang/tooling/migrate"
	"fiig-lang/tooling/runprogram"
)

type stringList []string

func (s *stringList) String() string     { return fmt.Sprint(*s) }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

// Main serves; factories are the provider types the host adds to the
// built-in ones, such as libraries/sql's "sql".
func Main(name string, args []string, factories runtime.FactoryRegistry) {
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	addrFlag := fs.String("addr", "", "address to listen on (default $LISTEN_ADDR, else :8080)")
	capsPath := fs.String("caps", "", "path to a JSON file of capability configuration (ADR 0027); `${NAME}` in its strings reads the environment")
	envFile := fs.String("env-file", "", "a file of NAME=value lines that sets environment variables the process doesn't (ADR 0071 §3a), for development")
	shutdown := fs.Duration("shutdown", 10*time.Second, "how long in-flight requests may run after SIGINT/SIGTERM")
	var workspaces stringList
	fs.Var(&workspaces, "workspace", "directory to search for root's package dependencies (compiler/loader); repeatable")
	var origins stringList
	fs.Var(&origins, "allow-origin", "an origin a browser front end may call from, as scheme://host[:port], or * for any (ADR 0070); repeatable; default $ALLOW_ORIGINS, comma-separated; none means CORS off")
	maxBody := fs.Int64("max-body", 0, "the most bytes a request body may have, else 413 (ADR 0076 §1); default $MAX_BODY_BYTES, else 2097152")
	maxRequests := fs.Int("max-requests", 0, "how many requests may run at once, with as many waiting up to 5 s, else 503 (ADR 0076 §2); default $MAX_REQUESTS, else 256")
	migrateFlag := fs.String("migrate", "apply", "apply pending migrations before serving, or check: refuse to start while any are pending (ADR 0096 §2)")
	requestTimeout := fs.Duration("request-timeout", 0, "how long a request may run, else 503 (ADR 0076 §3); default $REQUEST_TIMEOUT, else 30s")
	checkOnly := fs.Bool("check", false, "run only the startup routes (ADR 0124), then exit 0 if the program could start and 1 if not; nothing listens, no migration is applied, and `once` is ignored")
	fs.Parse(args)
	if fs.NArg() < 2 {
		fmt.Fprintln(os.Stderr, "usage: "+name+" [-addr ADDR] [-caps FILE] [-env-file FILE] [-allow-origin ORIGIN]... [-max-body N] [-max-requests N] [-request-timeout D] [-workspace DIR]... <root> <adapterName>...")
		os.Exit(2)
	}
	env, err := runprogram.NewEnvironment(*envFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error: -env-file:", err)
		os.Exit(1)
	}
	addr := *addrFlag
	if addr == "" {
		addr = ":8080"
		if v, ok := env.Lookup("LISTEN_ADDR"); ok && v != "" {
			addr = v
		}
	}
	var originProblems []string
	if len(origins) == 0 {
		if v, ok := env.Lookup("ALLOW_ORIGINS"); ok {
			origins = strings.Split(v, ",")
		}
	}
	cors, err := adapter.ParseOrigins(origins)
	if err != nil {
		originProblems = append(originProblems, "ALLOW_ORIGINS / -allow-origin: "+err.Error())
	}
	limits, limitProblems := readLimits(env, *maxBody, *maxRequests, *requestTimeout)
	originProblems = append(originProblems, limitProblems...)
	mode, err := migrate.ParseMode(*migrateFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(2)
	}
	if *checkOnly {
		mode = migrate.Skip
	}
	handler, loops, bindings, caps, err := load(fs.Arg(0), fs.Args()[1:], *capsPath, workspaces, factories, cors, limits, env, originProblems, mode)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	readTimeout, writeTimeout := limits.ServerTimeouts()
	srv := &http.Server{
		Addr: addr, Handler: handler, ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout: readTimeout, WriteTimeout: writeTimeout, MaxHeaderBytes: adapter.MaxHeaderBytes,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// ADR 0124 §2: the startup checks run first, all at once, and nothing
	// listens, claims, or reads until none of them refuses.
	var background []loop
	for _, l := range loops {
		if !l.startup {
			background = append(background, l)
			continue
		}
		l.setLog(log.Printf)
		if *checkOnly {
			l.ignoreOnce()
		}
		log.Printf("fiig: %s", l.describe)
		if err := l.run(ctx); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			caps.Close()
			os.Exit(1)
		}
	}
	if *checkOnly {
		// ADR 0124 §6: the program could start, which is all -check says.
		caps.Close()
		log.Print("fiig: the startup checks passed, so the program could start")
		return
	}
	loops = background
	// ADR 0111 §4-§5, ADR 0112 §2: each stream and schedule route runs
	// until shutdown; one that halts stops the whole process, so the halt
	// is seen (and a stream's element is delivered again to whatever runs
	// the adapter next).
	halted := make(chan error, len(loops))
	running := make(chan struct{}, len(loops))
	for _, l := range loops {
		l.setLog(log.Printf)
		go func() {
			defer func() { running <- struct{}{} }()
			if err := l.run(ctx); err != nil {
				halted <- err
				stop()
			}
		}()
	}
	go func() {
		<-ctx.Done()
		done, cancel := context.WithTimeout(context.Background(), *shutdown)
		defer cancel()
		srv.Shutdown(done)
	}()
	log.Printf("fiig %s: serving %s on %s", version.Version, strings.Join(fs.Args()[1:], ", "), addr)
	for _, b := range bindings {
		log.Printf("fiig: %s", b)
	}
	if cors != nil {
		log.Printf("fiig: allowing cross-origin calls from %s", strings.Join(cors.Origins(), ", "))
	}
	for _, l := range loops {
		log.Printf("fiig: %s", l.describe)
	}
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	// Each stream route finishes the element in hand (ADR 0111 §5), and
	// each schedule route its run (ADR 0112 §3).
	for range loops {
		<-running
	}
	// Requests have drained; a driver's pool closes its connections (ADR
	// 0084 §3).
	caps.Close()
	select {
	case err := <-halted:
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	default:
	}
	log.Print("fiig: stopped")
}

// load checks root and builds the handler for the http adapters named
// names, with every configuration problem (ADR 0071 §3's one report)
// found first. It returns ADR 0079 §6a's listing of each domain's
// capability bindings, for the startup log.
func load(root string, names []string, capsPath string, workspaces []string, factories runtime.FactoryRegistry, cors *adapter.CORS, limits adapter.Limits, env *runprogram.Environment, problems []string, mode migrate.Mode) (http.Handler, []loop, []string, *runtime.CapabilityRegistry, error) {
	p, err := check(root, names, workspaces, "serve")
	if err != nil {
		return nil, nil, nil, nil, err
	}
	files, packages, universe, served := p.files, p.packages, p.universe, p.served
	var streamed, scheduled, startups, httpServed []adapter.Served
	for _, s := range served {
		switch s.Decl.Kind {
		case syntax.AdapterStartup:
			startups = append(startups, s)
		case syntax.AdapterStream:
			streamed = append(streamed, s)
		case syntax.AdapterSchedule:
			scheduled = append(scheduled, s)
		default:
			httpServed = append(httpServed, s)
		}
	}

	caps, capProblems, err := runprogram.LoadCapabilitiesFileWith(capsPath, factories, env.Lookup)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("-caps: %w", err)
	}
	problems = append(capProblems, problems...)
	if caps == nil {
		caps = runtime.NewCapabilityRegistry()
	}
	problems = append(problems, runprogram.UnknownPackages(caps.Packages(), packages)...)
	if err := runprogram.RefuseSeededRandom(caps); err != nil {
		return nil, nil, nil, nil, err
	}
	runprogram.RegisterDefaults(caps)
	in := runtime.New(universe, caps)
	in.Imports = runprogram.BuildImports(files, packages)
	in.Groups = runprogram.BuildGroups(files, packages)
	in.Packages = packages
	program := runprogram.Program{Universe: universe, Imports: in.Imports, Groups: in.Groups, Renames: runprogram.Renames(root)}
	entries := make([]runprogram.Entry, len(served))
	for i, s := range served {
		entries[i] = runprogram.Entry{Package: s.Package, Name: s.Decl.Name}
	}
	streams, err := adapter.StreamRunners(streamed, universe, in, consumerName(), limits.RequestTimeout)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	jobs, err := adapter.ScheduleRunners(scheduled, universe, in, consumerName())
	if err != nil {
		return nil, nil, nil, nil, err
	}
	// A source or lock reads its own config (ADR 0111 §2, ADR 0112 §4),
	// which no workflow reaches, so its gateways are entries too.
	checks, err := adapter.StartupRunners(startups, universe, in, consumerName())
	if err != nil {
		return nil, nil, nil, nil, err
	}
	var loops []loop
	for _, r := range checks {
		if p, isFiig := r.Lock().(*runtime.LockProvider); isFiig {
			pkg, group, _ := p.Resolve()
			entries = append(entries, runprogram.Entry{Package: pkg, Name: group})
		}
	}
	if len(checks) > 0 {
		loops = append(loops, loop{
			startup: true,
			run:     func(ctx context.Context) error { return adapter.RunStartup(ctx, checks) },
			setLog: func(f func(string, ...any)) {
				for _, r := range checks {
					r.Logf = f
				}
			},
			ignoreOnce: func() {
				for _, r := range checks {
					r.IgnoreOnce = true
				}
			},
			describe: fmt.Sprintf("checking %d startup route(s) before serving", len(checks)),
		})
	}
	for _, r := range streams {
		pkg, group, _ := r.Source().Resolve()
		entries = append(entries, runprogram.Entry{Package: pkg, Name: group})
		loops = append(loops, loop{run: r.Run, setLog: func(f func(string, ...any)) { r.Logf = f }, describe: fmt.Sprintf("reading stream %s from %s", r.Name(), r.Source().Path())})
	}
	for _, r := range jobs {
		if p, isFiig := r.Lock().(*runtime.LockProvider); isFiig {
			pkg, group, _ := p.Resolve()
			entries = append(entries, runprogram.Entry{Package: pkg, Name: group})
		}
		loops = append(loops, loop{run: r.Run, setLog: func(f func(string, ...any)) { r.Logf = f }, describe: fmt.Sprintf("running %s, claimed with %s", r.Name(), r.Lock().Describe())})
	}
	problems = append(problems, in.LoadConfig(runprogram.ReachableConfigFrom(program, entries), env.Lookup)...)
	if err := runprogram.ConfigReport(problems); err != nil {
		return nil, nil, nil, nil, err
	}
	if err := runprogram.Migrate(context.Background(), root, packages, caps, mode); err != nil {
		return nil, nil, nil, nil, err
	}
	h, err := adapter.HTTPHandlerFor(httpServed, universe, in, limits)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	h, err = adapter.WithCORSFor(h, httpServed, cors)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	return h, loops, runprogram.Bindings(packages, caps.BoundFor, caps.Packages()), caps, nil
}

// loop is a stream or schedule route: something fiig serve runs beside its
// listener until shutdown (ADRs 0111, 0112).
type loop struct {
	run      func(context.Context) error
	setLog   func(func(string, ...any))
	describe string
	// startup marks the one loop that checks the program before anything
	// else starts, and ends when it has (ADR 0124 §2).
	startup bool
	// ignoreOnce, on the startup loop, makes `-check` run a `once` route
	// without claiming it.
	ignoreOnce func()
}

// checked is a program loaded and checked, with the adapters it was asked
// to serve or describe.
type checked struct {
	files    []*syntax.File
	packages types.Packages
	universe *types.Universe
	served   []adapter.Served
}

// check loads root with its dependencies, refuses it with checker
// diagnostics (verb says what was refused), and finds the adapters
// names names.
func check(root string, names, workspaces []string, verb string) (*checked, error) {
	files, diags, err := runprogram.LoadFiles(root)
	if err != nil {
		return nil, err
	}
	var packages types.Packages
	modulePath := ""
	if loader.Wanted(root, workspaces) {
		loaded, loadErrs := loader.Load(root, workspaces)
		if len(loadErrs) > 0 {
			return nil, errors.Join(loadErrs...)
		}
		packages = loaded.Packages
		if len(loaded.Module) > 0 {
			modulePath = loaded.Manifest.Path
		}
	}
	diags = append(diags, runprogram.Check(root, files, packages)...)
	if len(diags) > 0 {
		for _, d := range diags {
			fmt.Fprintln(os.Stderr, d.String())
		}
		return nil, fmt.Errorf("refusing to %s a program with checker diagnostics", verb)
	}
	universe, _ := types.BuildUniverse(files) // conflicts already reported by runprogram.Check above
	served, err := findAdapters(names, files, packages, modulePath)
	if err != nil {
		return nil, err
	}
	return &checked{files: files, packages: packages, universe: universe, served: served}, nil
}

// findAdapters resolves each name `fiig serve` was given (ADR 0079 §6): a
// bare name in root's own files, a dotted one as a dependency's package
// path and then its adapter's name.
func findAdapters(names []string, files []*syntax.File, packages types.Packages, modulePath string) ([]adapter.Served, error) {
	var out []adapter.Served
	seen := map[string]bool{}
	for _, name := range names {
		if seen[name] {
			return nil, fmt.Errorf("adapter %s is named twice", name)
		}
		seen[name] = true
		if path, adapterName, dotted := cutLast(name); dotted {
			pkg, ok := packages[path]
			if !ok && modulePath != "" {
				// ADR 0105: a folder package named relative to its module,
				// `ratings.api` for t.app.ratings's adapter api.
				pkg, ok = packages[modulePath+"."+path]
			}
			if !ok {
				return nil, fmt.Errorf("adapter %s: the application doesn't depend on a package %s", name, path)
			}
			d, _ := pkg.Universe.Lookup(adapterName)
			ad, ok := d.(*syntax.AdapterDecl)
			if !ok {
				return nil, fmt.Errorf("package %s has no adapter named %q", path, adapterName)
			}
			out = append(out, adapter.Served{Decl: ad, Package: pkg})
			continue
		}
		var decl *syntax.AdapterDecl
		for _, f := range files {
			for _, d := range f.Decls {
				if ad, ok := d.(*syntax.AdapterDecl); ok && ad.Name == name {
					decl = ad
				}
			}
		}
		if decl == nil {
			return nil, fmt.Errorf("no adapter named %q", name)
		}
		out = append(out, adapter.Served{Decl: decl})
	}
	return out, nil
}

// consumerName is this process among a stream group's consumers (ADR
// 0111 §5): the host name, which a pod or a machine keeps across restarts,
// so a group doesn't gain a dead consumer every time a process restarts.
// It also holds this process's schedule claims (ADR 0112 §4).
func consumerName() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "fiig"
	}
	return host
}

// cutLast splits a dotted name at its last dot.
func cutLast(name string) (string, string, bool) {
	i := strings.LastIndex(name, ".")
	if i < 0 {
		return "", name, false
	}
	return name[:i], name[i+1:], true
}

// readLimits is ADR 0076's limits: each flag, else its variable, else the
// default. A bad value is a problem for ADR 0071 §3's one report.
func readLimits(env *runprogram.Environment, maxBody int64, maxRequests int, requestTimeout time.Duration) (adapter.Limits, []string) {
	l := adapter.DefaultLimits()
	var problems []string
	positive := func(name string, flag int64, set func(int64)) {
		if flag != 0 {
			if flag < 0 {
				problems = append(problems, fmt.Sprintf("%s / -%s: %d isn't a positive whole number", name, flagName[name], flag))
				return
			}
			set(flag)
			return
		}
		v, ok := env.Lookup(name)
		if !ok || v == "" {
			return
		}
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 {
			problems = append(problems, fmt.Sprintf("%s: %q isn't a positive whole number", name, v))
			return
		}
		set(n)
	}
	positive("MAX_BODY_BYTES", maxBody, func(n int64) { l.MaxBodyBytes = n })
	positive("MAX_REQUESTS", int64(maxRequests), func(n int64) { l.MaxRequests = int(n) })
	switch {
	case requestTimeout < 0:
		problems = append(problems, fmt.Sprintf("REQUEST_TIMEOUT / -request-timeout: %s isn't a positive duration", requestTimeout))
	case requestTimeout > 0:
		l.RequestTimeout = requestTimeout
	default:
		if v, ok := env.Lookup("REQUEST_TIMEOUT"); ok && v != "" {
			d, err := time.ParseDuration(v)
			if err != nil || d <= 0 {
				problems = append(problems, fmt.Sprintf("REQUEST_TIMEOUT: %q isn't a positive duration, such as 30s or 2m", v))
			} else {
				l.RequestTimeout = d
			}
		}
	}
	return l, problems
}

var flagName = map[string]string{"MAX_BODY_BYTES": "max-body", "MAX_REQUESTS": "max-requests"}
